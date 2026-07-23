package grok

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hegade/kunai/internal/cliproxy/codex"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// xAI's CLI chat-proxy speaks the OpenAI-Responses format, the same shape Codex
// uses, so the Anthropic<->Responses translation is reused from the codex package.
// Only the endpoint, auth, and identity headers are Grok-specific.
const (
	defaultGrokBaseURL = "https://cli-chat-proxy.grok.com/v1"
	grokClientVersion  = "0.2.111"
	grokUserAgent      = "xai-grok-workspace/" + grokClientVersion
)

// Proxy serves the Anthropic Messages API and forwards to xAI (Grok).
type Proxy struct {
	tokens  *tokenManager
	baseURL string
	client  *http.Client
}

// NewProxy builds a Grok proxy authenticating with the grok CLI token at tokenPath
// (normally ~/.grok/auth.json).
func NewProxy(tokenPath string) *Proxy {
	return &Proxy{
		tokens:  newTokenManager(tokenPath),
		baseURL: defaultGrokBaseURL,
		client:  &http.Client{Timeout: 10 * time.Minute},
	}
}

// Handler returns the HTTP mux for the proxy.
func (p *Proxy) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/messages", p.handleMessages)
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"grok-4.5","object":"model"}],"object":"list"}`)
	})
	return mux
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

	body := p.buildGrokRequest(model, inbound)

	token, err := p.tokens.token(r.Context())
	if err != nil {
		writeAnthropicError(w, http.StatusUnauthorized, "grok auth: "+err.Error())
		return
	}

	upURL := strings.TrimSuffix(p.baseURL, "/") + "/responses"
	upReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, upURL, bytes.NewReader(body))
	if err != nil {
		writeAnthropicError(w, http.StatusInternalServerError, err.Error())
		return
	}
	applyGrokHeaders(upReq, token)

	upResp, err := p.client.Do(upReq)
	if err != nil {
		writeAnthropicError(w, http.StatusBadGateway, "grok upstream: "+err.Error())
		return
	}
	defer upResp.Body.Close()
	if upResp.StatusCode < 200 || upResp.StatusCode >= 300 {
		b, _ := io.ReadAll(upResp.Body)
		writeAnthropicError(w, upResp.StatusCode, grokErrorMessage(b))
		return
	}

	if wantStream {
		p.streamBack(r.Context(), w, model, inbound, upResp.Body)
		return
	}
	p.bufferBack(r.Context(), w, model, inbound, upResp.Body)
}

// buildGrokRequest translates the Anthropic request to the Responses format and
// applies the same massaging the reference xAI executor does.
func (p *Proxy) buildGrokRequest(model string, inbound []byte) []byte {
	baseModel := codex.ParseSuffix(model).ModelName
	body := codex.ConvertClaudeRequestToCodex(baseModel, inbound, false)
	body, _ = sjson.SetBytes(body, "model", baseModel)
	body, _ = sjson.SetBytes(body, "stream", true)
	body, _ = sjson.DeleteBytes(body, "previous_response_id")
	body, _ = sjson.DeleteBytes(body, "prompt_cache_retention")
	body, _ = sjson.DeleteBytes(body, "safety_identifier")
	body, _ = sjson.DeleteBytes(body, "stream_options")
	return body
}

func (p *Proxy) streamBack(ctx context.Context, w http.ResponseWriter, model string, original []byte, body io.Reader) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)
	var param any
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		for _, out := range codex.ConvertCodexResponseToClaude(ctx, model, original, nil, append([]byte(nil), line...), &param) {
			_, _ = w.Write(out)
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func (p *Proxy) bufferBack(ctx context.Context, w http.ResponseWriter, model string, original []byte, body io.Reader) {
	raw, _ := io.ReadAll(body)
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
		writeAnthropicError(w, http.StatusBadGateway, "grok upstream: no completed event")
		return
	}
	var param any
	out := codex.ConvertCodexResponseToClaudeNonStream(ctx, model, original, nil, completed, &param)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}

// applyGrokHeaders sets the headers xAI's CLI chat-proxy expects (from the
// reference applyXAIChatHeaders for the CLI chat-proxy base URL).
func applyGrokHeaders(r *http.Request, token string) {
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Accept", "text/event-stream")
	r.Header.Set("Connection", "Keep-Alive")
	r.Header.Set("X-XAI-Token-Auth", "xai-grok-cli")
	r.Header.Set("x-grok-client-version", grokClientVersion)
	r.Header.Set("User-Agent", grokUserAgent)
}

func grokErrorMessage(b []byte) string {
	if m := gjson.GetBytes(b, "error.message").String(); m != "" {
		return m
	}
	if m := gjson.GetBytes(b, "message").String(); m != "" {
		return m
	}
	if s := strings.TrimSpace(string(b)); s != "" {
		return s
	}
	return "grok upstream error"
}

func writeAnthropicError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	out := []byte(`{"type":"error","error":{"type":"api_error","message":""}}`)
	out, _ = sjson.SetBytes(out, "error.message", msg)
	_, _ = w.Write(out)
}
