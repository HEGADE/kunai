package codex

// A minimal, kunai-native Codex proxy: it accepts the Anthropic Messages API that
// the `claude` CLI speaks (POST /v1/messages), translates the request to Codex's
// /responses format, calls ChatGPT's backend with the account's OAuth token, and
// stream-translates the reply back to Anthropic SSE. This is the piece that lets
// kunai drop the 40MB CLIProxyAPI sidecar for a Codex provider: the translation is
// ported from CLIProxyAPI (see translate_*.go, proven against its golden tests);
// this file is the thin executor around it.
//
// Scope, stated honestly: single-turn and ordinary multi-turn tool use go through
// the same translator the sidecar uses. What is NOT yet reproduced here is the
// sidecar's reasoning-replay/signature CACHE (codex_executor.go's replay machinery),
// which some multi-turn reasoning sequences need to avoid an upstream "invalid
// signature" rejection. That path needs a live token to exercise and is flagged in
// the package README. Everything below is offline-tested (proxy_test.go) with a
// mock upstream.

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	defaultCodexBaseURL = "https://chatgpt.com/backend-api/codex"
	codexUserAgent      = "codex-tui/0.135.0 (Mac OS 26.5.0; arm64) iTerm.app/3.6.10 (codex-tui; 0.135.0)"
	codexOriginator     = "codex-tui"
)

// Proxy serves the Anthropic Messages API and forwards to Codex. Construct with
// NewProxy. It is safe for concurrent use.
type Proxy struct {
	tokens  *tokenManager
	baseURL string
	client  *http.Client
}

// NewProxy builds a proxy that authenticates with the Codex token at tokenPath.
// owns indicates kunai may write a refreshed token back (true for the sidecar auth
// dir kunai wrote, false for a shared ~/.codex/auth.json).
func NewProxy(tokenPath string, owns bool) *Proxy {
	return &Proxy{
		tokens:  newTokenManager(tokenPath, owns),
		baseURL: defaultCodexBaseURL,
		client:  &http.Client{Timeout: 10 * time.Minute}, // a turn can be long
	}
}

// Handler returns the HTTP mux for the proxy (mountable under any prefix).
func (p *Proxy) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/messages", p.handleMessages)
	mux.HandleFunc("/v1/models", p.handleModels)
	return mux
}

// handleModels answers a minimal model list so a client health check passes; the
// real model is chosen by kunai's provider mapping, not discovered here.
func (p *Proxy) handleModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"data":[],"object":"list"}`)
}

func (p *Proxy) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	inbound, err := io.ReadAll(r.Body)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "read request: "+err.Error())
		return
	}
	model := gjson.GetBytes(inbound, "model").String()
	wantStream := gjson.GetBytes(inbound, "stream").Bool()

	upstreamBody := p.buildCodexRequest(model, inbound)

	access, account, err := p.tokens.creds(r.Context())
	if err != nil {
		writeAnthropicError(w, http.StatusUnauthorized, "codex auth: "+err.Error())
		return
	}

	upURL := strings.TrimSuffix(p.baseURL, "/") + "/responses"
	upReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, upURL, bytes.NewReader(upstreamBody))
	if err != nil {
		writeAnthropicError(w, http.StatusInternalServerError, err.Error())
		return
	}
	applyCodexHeaders(upReq, access, account)

	upResp, err := p.client.Do(upReq)
	if err != nil {
		writeAnthropicError(w, http.StatusBadGateway, "codex upstream: "+err.Error())
		return
	}
	defer upResp.Body.Close()

	if upResp.StatusCode < 200 || upResp.StatusCode >= 300 {
		b, _ := io.ReadAll(upResp.Body)
		writeAnthropicError(w, upResp.StatusCode, codexErrorMessage(b))
		return
	}

	if wantStream {
		p.streamBack(r.Context(), w, model, inbound, upResp.Body)
		return
	}
	p.bufferBack(r.Context(), w, model, inbound, upResp.Body)
}

// buildCodexRequest translates the Anthropic request and applies the same body
// massaging the reference executor does before calling /responses.
func (p *Proxy) buildCodexRequest(model string, inbound []byte) []byte {
	baseModel := ParseSuffix(model).ModelName
	body := ConvertClaudeRequestToCodex(baseModel, inbound, false)
	body, _ = sjson.SetBytes(body, "model", baseModel)
	body, _ = sjson.SetBytes(body, "stream", true)
	body, _ = sjson.DeleteBytes(body, "previous_response_id")
	body, _ = sjson.DeleteBytes(body, "prompt_cache_retention")
	body, _ = sjson.DeleteBytes(body, "safety_identifier")
	body, _ = sjson.DeleteBytes(body, "stream_options")
	if !gjson.GetBytes(body, "instructions").Exists() {
		body, _ = sjson.SetBytes(body, "instructions", "")
	}
	return body
}

// streamBack pumps the upstream Codex SSE through the streaming translator and
// writes Anthropic SSE to the client as events arrive.
func (p *Proxy) streamBack(ctx context.Context, w http.ResponseWriter, model string, original []byte, body io.Reader) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)

	var param any
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // a codex event can be large (reasoning)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		for _, out := range ConvertCodexResponseToClaude(ctx, model, original, nil, append([]byte(nil), line...), &param) {
			_, _ = w.Write(out)
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// bufferBack collects the whole upstream stream and returns a single Anthropic
// Messages JSON (for a non-streaming client request).
func (p *Proxy) bufferBack(ctx context.Context, w http.ResponseWriter, model string, original []byte, body io.Reader) {
	raw, _ := io.ReadAll(body)
	// The non-stream translator wants the terminal completed event's data.
	var completed []byte
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(line[5:])
		t := gjson.GetBytes(data, "type").String()
		if t == "response.completed" || t == "response.incomplete" {
			completed = data
		}
	}
	if completed == nil {
		writeAnthropicError(w, http.StatusBadGateway, "codex upstream: no completed event")
		return
	}
	var param any
	out := ConvertCodexResponseToClaudeNonStream(ctx, model, original, nil, completed, &param)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}

// applyCodexHeaders sets the headers Codex's /responses endpoint expects, taken
// from the reference executor's applyCodexHeadersFromSources.
func applyCodexHeaders(r *http.Request, token, account string) {
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("User-Agent", codexUserAgent)
	r.Header.Set("Originator", codexOriginator)
	r.Header.Set("Accept", "text/event-stream")
	r.Header.Set("Connection", "Keep-Alive")
	if account != "" {
		r.Header.Set("Chatgpt-Account-Id", account)
	}
	// The reference sets a Session_id when the UA advertises Mac OS (ours does).
	if strings.Contains(codexUserAgent, "Mac OS") {
		r.Header.Set("Session_id", uuid.NewString())
	}
}

func codexErrorMessage(b []byte) string {
	if m := gjson.GetBytes(b, "error.message").String(); m != "" {
		return m
	}
	if m := gjson.GetBytes(b, "message").String(); m != "" {
		return m
	}
	if s := strings.TrimSpace(string(b)); s != "" {
		return s
	}
	return "codex upstream error"
}

func writeAnthropicError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	out := []byte(`{"type":"error","error":{"type":"api_error","message":""}}`)
	out, _ = sjson.SetBytes(out, "error.message", msg)
	_, _ = w.Write(out)
}
