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
	"bytes"
	"context"
	"io"
	"log"
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

	// Stop an over-window request before it is sent: a Codex model has a smaller
	// context window than the Claude window the CLI packs to, so a too-large turn
	// would otherwise be dropped mid-stream ("stream disconnected"). Returning
	// prompt-too-long lets the CLI compact or surface it instead.
	baseModel := codexModelOrFallback(ParseSuffix(model).ModelName)
	if tooLong, status, errType, msg := GuardContextWindow(baseModel, inbound); tooLong {
		log.Printf("codex: request over window for %s: %s", baseModel, msg)
		WriteAnthropicError(w, status, errType, msg)
		return
	}

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
		log.Printf("codex: upstream HTTP %d body=%.200s", upResp.StatusCode, string(b))
		status, errType, msg := ClassifyUpstreamError(upResp.StatusCode, b)
		WriteAnthropicError(w, status, errType, msg)
		return
	}

	if wantStream {
		StreamTranslate(r.Context(), w, "codex", model, inbound, upResp.Body)
		return
	}
	p.bufferBack(r.Context(), w, model, inbound, upResp.Body)
}

// buildCodexRequest translates the Anthropic request and applies the same body
// massaging the reference executor does before calling /responses.
func (p *Proxy) buildCodexRequest(model string, inbound []byte) []byte {
	baseModel := codexModelOrFallback(ParseSuffix(model).ModelName)
	body := ConvertClaudeRequestToCodex(baseModel, inbound, false)
	body, _ = sjson.SetBytes(body, "model", baseModel)
	body, _ = sjson.SetBytes(body, "stream", true)
	body, _ = sjson.DeleteBytes(body, "previous_response_id")
	body, _ = sjson.DeleteBytes(body, "prompt_cache_retention")
	body, _ = sjson.DeleteBytes(body, "safety_identifier")
	body, _ = sjson.DeleteBytes(body, "stream_options")
	body = dropOrphanToolChoice(body)
	if !gjson.GetBytes(body, "instructions").Exists() {
		body, _ = sjson.SetBytes(body, "instructions", "")
	}
	return body
}

// fallbackCodexModel is used when the request names a non-Codex model (e.g. a
// resolved Claude id like claude-opus-4-8 that a switched session carries), which
// Codex 404s on. Coercing to a real model keeps the session working.
const fallbackCodexModel = "gpt-5.5"

func codexModelOrFallback(model string) string {
	m := strings.ToLower(model)
	for _, pfx := range []string{"gpt", "codex", "o1", "o3", "o4", "chatgpt"} {
		if strings.HasPrefix(m, pfx) {
			return model
		}
	}
	return fallbackCodexModel
}

// InjectContextEstimate fills a message_start event's usage.input_tokens with an
// estimate of the context size, because the /responses upstreams only report the real
// input tokens at the END of the stream (response.completed) while Anthropic carries
// input_tokens in message_start at the START -- so without this the CLI reads a
// context of 0 and the meter never moves. The estimate is bytes/4.8 of the original
// request (measured against real Codex usage; JSON overhead puts it a little above the
// classic 4 chars/token). Approximate, but a context meter is a gauge, and an
// approximate meter beats a dead one. Applied to the event bytes as they stream.
func InjectContextEstimate(event, original []byte) []byte {
	if !bytes.Contains(event, []byte(`"type":"message_start"`)) {
		return event
	}
	est := int64(len(original)) * 10 / 48
	if est <= 0 {
		return event
	}
	// event is "event: message_start\ndata: {json}\n\n"; rewrite the json's usage.
	i := bytes.Index(event, []byte("data:"))
	if i < 0 {
		return event
	}
	head := event[:i]
	rest := event[i+len("data:"):]
	// rest starts with " {json}\n\n"
	trimmed := bytes.TrimLeft(rest, " ")
	end := bytes.IndexByte(trimmed, '\n')
	if end < 0 {
		end = len(trimmed)
	}
	obj := trimmed[:end]
	tail := trimmed[end:]
	obj, err := sjson.SetBytes(obj, "message.usage.input_tokens", est)
	if err != nil {
		return event
	}
	out := append([]byte{}, head...)
	out = append(out, "data: "...)
	out = append(out, obj...)
	out = append(out, tail...)
	return out
}

// dropOrphanToolChoice removes a tool_choice with no tools to choose from, which the
// /responses upstreams reject ("A tool_choice was set but no tools were specified").
func dropOrphanToolChoice(body []byte) []byte {
	if !gjson.GetBytes(body, "tool_choice").Exists() {
		return body
	}
	tools := gjson.GetBytes(body, "tools")
	if !tools.Exists() || !tools.IsArray() || len(tools.Array()) == 0 {
		body, _ = sjson.DeleteBytes(body, "tool_choice")
	}
	return body
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

// writeAnthropicError is the api_error convenience wrapper for the auth/read/setup
// failures in this file; the upstream and stream paths use the typed
// WriteAnthropicError / ClassifyUpstreamError in resilience.go directly.
func writeAnthropicError(w http.ResponseWriter, status int, msg string) {
	WriteAnthropicError(w, status, "api_error", msg)
}
