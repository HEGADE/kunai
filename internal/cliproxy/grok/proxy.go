package grok

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
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
	// fallbackGrokModel is used when the incoming request names a non-Grok model.
	// That happens when claude sends a resolved Claude id (e.g. claude-opus-4-8)
	// instead of the mapped model -- a switched session carries the id the slot env
	// vars do not remap -- and xAI 404s on it. Coercing to a real Grok model keeps
	// the session working instead of returning empty turns.
	fallbackGrokModel = "grok-4.5"
)

// Proxy serves the Anthropic Messages API and forwards to xAI (Grok).
type Proxy struct {
	tokens  *tokenManager
	baseURL string
	client  *http.Client

	// The free tier's token limit is not exposed by any proactive endpoint; it only
	// appears in a 429 body ("tokens (actual/limit): X/Y"). Capture the last one so
	// the dashboard can show it. Zero limit means none seen.
	qmu    sync.Mutex
	qUsed  int64
	qLimit int64
	qAt    time.Time
}

var grokTokenLimitRe = regexp.MustCompile(`tokens \(actual/limit\):\s*(\d+)\s*/\s*(\d+)`)

// noteQuota records the free-tier token usage parsed from a 429 body, if present.
func (p *Proxy) noteQuota(body []byte) {
	m := grokTokenLimitRe.FindSubmatch(body)
	if m == nil {
		return
	}
	used, _ := strconv.ParseInt(string(m[1]), 10, 64)
	limit, _ := strconv.ParseInt(string(m[2]), 10, 64)
	if limit <= 0 {
		return
	}
	p.qmu.Lock()
	p.qUsed, p.qLimit, p.qAt = used, limit, timeNow()
	p.qmu.Unlock()
}

// FreeQuota returns the last-seen free-tier token usage (used, limit, whenSeen),
// ok=false if none has been observed. Stale after a while; the caller decides.
func (p *Proxy) FreeQuota() (used, limit int64, at time.Time, ok bool) {
	p.qmu.Lock()
	defer p.qmu.Unlock()
	if p.qLimit <= 0 {
		return 0, 0, time.Time{}, false
	}
	return p.qUsed, p.qLimit, p.qAt, true
}

// timeNow is a var so tests are deterministic.
var timeNow = time.Now

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

	// Slide the smaller Grok window: drop the oldest turns to fit so an over-window
	// session keeps working instead of the "stream disconnected" symptom. Only when
	// even the latest turn cannot fit do we return the clean prompt-too-long.
	baseModel := grokModelOrFallback(codex.ParseSuffix(model).ModelName)
	if fitted, dropped, ok := codex.FitContextToWindow(baseModel, inbound); !ok {
		est, win := codex.EstimateTokens(inbound), codex.ModelWindow(baseModel)
		log.Printf("grok: request over window for %s and cannot be trimmed to fit (est %d > %d)", baseModel, est, win)
		codex.WriteAnthropicError(w, http.StatusBadRequest, "invalid_request_error", codex.PromptTooLongMessage(est, win))
		return
	} else if dropped > 0 {
		log.Printf("grok: trimmed %d oldest message(s) to fit the %s window", dropped, baseModel)
		inbound = fitted
	}

	body := p.buildGrokRequest(model, inbound)

	token, err := p.tokens.token(r.Context())
	if err != nil {
		// A dead login will not fix itself, so return a non-retryable 400: the CLI
		// retries a 401 (auth is often transient), which just hangs the turn on
		// "Working..." before failing. 400 surfaces the "sign in again" message now.
		codex.WriteAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "grok auth: "+err.Error())
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
		log.Printf("grok: upstream error model=%q: %v", model, err)
		writeAnthropicError(w, http.StatusBadGateway, "grok upstream: "+err.Error())
		return
	}
	defer upResp.Body.Close()
	if upResp.StatusCode < 200 || upResp.StatusCode >= 300 {
		b, _ := io.ReadAll(upResp.Body)
		log.Printf("grok: upstream HTTP %d model=%q body=%.200s", upResp.StatusCode, model, string(b))
		p.noteQuota(b)
		status, errType, msg := codex.ClassifyUpstreamError(upResp.StatusCode, b)
		codex.WriteAnthropicError(w, status, errType, msg)
		return
	}

	if wantStream {
		codex.StreamTranslate(r.Context(), w, "grok", model, inbound, upResp.Body)
		return
	}
	p.bufferBack(r.Context(), w, model, inbound, upResp.Body)
}

// buildGrokRequest translates the Anthropic request to the Responses format and
// applies the same massaging the reference xAI executor does.
func (p *Proxy) buildGrokRequest(model string, inbound []byte) []byte {
	baseModel := grokModelOrFallback(codex.ParseSuffix(model).ModelName)
	body := codex.ConvertClaudeRequestToCodex(baseModel, inbound, false)
	body, _ = sjson.SetBytes(body, "model", baseModel)
	body, _ = sjson.SetBytes(body, "stream", true)
	body, _ = sjson.DeleteBytes(body, "previous_response_id")
	body, _ = sjson.DeleteBytes(body, "prompt_cache_retention")
	body, _ = sjson.DeleteBytes(body, "safety_identifier")
	body, _ = sjson.DeleteBytes(body, "stream_options")
	body = dropOrphanToolChoice(body)
	return body
}

// dropOrphanToolChoice removes a tool_choice that has no tools to choose from. xAI
// rejects such a request ("A tool_choice was set but no tools were specified"), which
// can happen on a turn where the translated body carries a tool_choice but an empty
// or absent tools array.
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

func (p *Proxy) bufferBack(ctx context.Context, w http.ResponseWriter, model string, original []byte, body io.Reader) {
	raw, _ := io.ReadAll(body)
	// Terminal event with output backfilled from the streamed items (Codex's
	// terminal can be empty; see codex/nonstream.go). Shared with the codex proxy.
	completed := codex.CompletedEventForNonStream(raw)
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

// grokModelOrFallback returns the model if it is a Grok model, else the fallback.
// A correctly-mapped request already carries a grok-* model and passes through; only
// a stray Claude/other id gets coerced, so xAI never sees a model it cannot serve.
func grokModelOrFallback(model string) string {
	if strings.HasPrefix(strings.ToLower(model), "grok") {
		return model
	}
	return fallbackGrokModel
}

// writeAnthropicError is the api_error convenience wrapper for the auth/read/setup
// failures in this file; the upstream and stream paths use codex.ClassifyUpstreamError
// / codex.WriteAnthropicError directly.
func writeAnthropicError(w http.ResponseWriter, status int, msg string) {
	codex.WriteAnthropicError(w, status, "api_error", msg)
}
