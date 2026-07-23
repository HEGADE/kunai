package codex

// resilience.go turns a possibly-broken upstream /responses SSE stream into a
// well-formed Anthropic stream, and guards against the context-window mismatch
// that makes a smaller upstream model drop a turn the `claude` CLI overstuffed.
//
// The failure this exists for: the CLI thinks it is talking to Claude, so it packs
// context up to Claude's window. A Codex/Grok model behind the proxy has a smaller
// window, so a request that outgrew it (typically right after a compaction, once a
// coding turn re-fills the window with file reads and edits) is rejected or dropped
// mid-stream. The CLI then sees a stream that ended without message_stop and reports
// "stream disconnected", or a bare socket error during compaction. Neither is
// actionable. This file makes both cases end as a clean, typed Anthropic error the
// CLI can surface or recover from, and stops an over-window request before it is
// sent so the failure is a clear message instead of a dropped socket.

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Real input-token windows of the upstream models kunai proxies. They are smaller
// than the window the CLI assumes when it believes it is talking to Claude, which
// is the whole reason an over-window request can reach the upstream at all. Kept
// deliberately a little under the vendor maximums so the guard trips before the
// hard wall the upstream enforces.
const (
	WindowCodex   = 260000 // gpt-5.x / codex: ~272k input; guard a little under
	WindowGrok    = 240000 // grok-4.x: ~256k; guard a little under
	WindowDefault = 190000 // unknown model: assume a conservative 200k-class window
)

// Windows are overridable per family, because a real window varies by plan and
// model revision and because a test needs to trip the guard without a 260k-token
// bill. KUNAI_CODEX_WINDOW / KUNAI_GROK_WINDOW / KUNAI_MODEL_WINDOW, in tokens.
var (
	winCodex   = envWindow("KUNAI_CODEX_WINDOW", WindowCodex)
	winGrok    = envWindow("KUNAI_GROK_WINDOW", WindowGrok)
	winDefault = envWindow("KUNAI_MODEL_WINDOW", WindowDefault)
)

func envWindow(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// ModelWindow returns the real input-token window for an upstream model name.
func ModelWindow(model string) int {
	m := strings.ToLower(model)
	switch {
	case strings.HasPrefix(m, "grok"):
		return winGrok
	case strings.HasPrefix(m, "gpt"), strings.HasPrefix(m, "codex"),
		strings.HasPrefix(m, "o1"), strings.HasPrefix(m, "o3"),
		strings.HasPrefix(m, "o4"), strings.HasPrefix(m, "chatgpt"):
		return winCodex
	default:
		return winDefault
	}
}

// EstimateTokens approximates the token count of an Anthropic request body for the
// window guard. It deliberately uses a denser bytes/4.0 ratio than the meter's
// bytes/4.8: a real request is dominated by the system prompt and tool schemas,
// which tokenize to MORE tokens per byte than prose, so bytes/4.8 undercounts them
// (measured: a 156KB request was ~41k real tokens but bytes/4.8 called it ~32k).
// The guard's failure mode is letting an over-window request through, which the
// upstream drops mid-stream, so it must OVER-count, not under-count: firing a little
// early is a clean prompt-too-long, firing a little late is the disconnect this
// whole file exists to prevent.
func EstimateTokens(body []byte) int { return len(body) / 4 }

// promptTooLongMessage mirrors the wording Anthropic's own API returns when a
// request exceeds the model's context window. The CLI recognizes this shape and
// reacts to it (surface / compact) natively, so emitting it -- instead of letting
// the upstream drop the socket -- turns a mysterious disconnect into the CLI's own
// too-long handling.
func promptTooLongMessage(estimate, window int) string {
	return "prompt is too long: " + itoa(estimate) + " tokens > " + itoa(window) +
		" maximum for this model. Compact or start a new session."
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// GuardContextWindow reports whether an inbound Anthropic request is too large for
// the upstream model's real window. When it is, the caller returns the too-long
// error before calling the upstream, so an over-window turn fails cleanly instead
// of dropping the stream. baseModel is the upstream model the request will run on.
func GuardContextWindow(baseModel string, inbound []byte) (tooLong bool, status int, errType, msg string) {
	est := EstimateTokens(inbound)
	win := ModelWindow(baseModel)
	if est <= win {
		return false, 0, "", ""
	}
	return true, http.StatusBadRequest, "invalid_request_error", promptTooLongMessage(est, win)
}

// looksLikeOverflow reports whether an upstream error body is the upstream's own
// context-length rejection, however it phrases it, so the proxy can re-shape it as
// Anthropic's prompt-too-long error the CLI knows how to handle.
func looksLikeOverflow(body []byte) bool {
	low := strings.ToLower(string(body))
	for _, s := range []string{
		"context length", "context_length_exceeded", "maximum context",
		"too many tokens", "reduce the length", "reduce the amount",
		"prompt is too long", "input is too long", "exceeds the maximum",
		"maximum number of tokens", "context window",
	} {
		if strings.Contains(low, s) {
			return true
		}
	}
	return false
}

// ClassifyUpstreamError maps an upstream HTTP error into the Anthropic status,
// error type, and message the client should see. An overflow becomes a
// prompt-too-long invalid_request_error (the CLI compacts / surfaces it); a
// permanent condition (quota exhausted, no model access) becomes a non-retryable
// 400 so the CLI stops instead of backing off for tens of seconds; anything else
// passes the upstream status through so a genuinely transient error is retried.
func ClassifyUpstreamError(upstreamStatus int, body []byte) (status int, errType, msg string) {
	m := errorMessage(body)
	if looksLikeOverflow(body) {
		return http.StatusBadRequest, "invalid_request_error", m
	}
	low := strings.ToLower(string(body))
	permanent := strings.Contains(low, "usage-exhausted") ||
		strings.Contains(low, "subscription:") ||
		strings.Contains(low, "does not exist") ||
		strings.Contains(low, "does not have access") ||
		strings.Contains(low, "insufficient_quota")
	if permanent {
		return http.StatusBadRequest, "invalid_request_error", m
	}
	return upstreamStatus, "api_error", m
}

func errorMessage(b []byte) string {
	// Try the shapes the upstreams actually use: a top-level error object, the
	// nested response.error a streamed response.failed carries, and a bare message.
	for _, path := range []string{"error.message", "response.error.message", "message", "response.error"} {
		if m := gjson.GetBytes(b, path).String(); m != "" {
			return m
		}
	}
	if s := strings.TrimSpace(string(b)); s != "" {
		return s
	}
	return "upstream error"
}

// WriteAnthropicError writes a typed Anthropic error JSON with the given HTTP
// status. errType is the Anthropic error type (api_error, invalid_request_error,
// ...) so the CLI can tell a retryable failure from a permanent one.
func WriteAnthropicError(w http.ResponseWriter, status int, errType, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	out := []byte(`{"type":"error","error":{"type":"api_error","message":""}}`)
	out, _ = sjson.SetBytes(out, "error.type", errType)
	out, _ = sjson.SetBytes(out, "error.message", msg)
	_, _ = w.Write(out)
}

// StreamTranslate pumps an upstream /responses SSE stream through the streaming
// translator and writes Anthropic SSE to the client, guaranteeing a well-formed
// end on every exit path. A normal completion passes the translator's own
// message_stop through untouched. A dropped socket, an early EOF before
// message_stop, or an inline upstream failure event is turned into a typed
// Anthropic error event so the CLI sees a real error instead of a truncated
// stream it would report as "stream disconnected". label ("codex"/"grok") is for
// logging only.
func StreamTranslate(ctx context.Context, w http.ResponseWriter, label, model string, original []byte, body io.Reader) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)

	var param any
	started := false
	emittedStop := false
	var inlineErr []byte // the raw upstream error body of an inline failure event

	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // a codex event can be large (reasoning)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if data, ok := sseData(line); ok {
			switch gjson.GetBytes(data, "type").String() {
			case "response.failed", "error":
				inlineErr = append([]byte(nil), data...)
			}
		}
		for _, out := range ConvertCodexResponseToClaude(ctx, model, original, nil, append([]byte(nil), line...), &param) {
			out = InjectContextEstimate(out, original)
			if bytes.Contains(out, []byte(`"type":"message_start"`)) {
				started = true
			}
			if bytes.Contains(out, []byte(`"type":"message_stop"`)) {
				emittedStop = true
			}
			_, _ = w.Write(out)
		}
		if flusher != nil {
			flusher.Flush()
		}
	}

	scanErr := sc.Err()
	if emittedStop && inlineErr == nil {
		return // a clean, well-formed finish
	}
	if ctx.Err() != nil {
		// The client cancelled the request (it got what it needed, or navigated
		// away). Not an upstream failure, and there is no live client to tell.
		return
	}
	finishBrokenStream(w, flusher, label, started, inlineErr, scanErr)
}

// finishBrokenStream closes an abnormally-ended Anthropic stream with a typed error
// event so the CLI never sees a silently-truncated reply. An overflow inline error
// becomes prompt-too-long (invalid_request_error, so the CLI compacts); a socket
// drop or early EOF becomes a retryable api_error (so the CLI can retry the turn,
// which after a compaction usually fits).
func finishBrokenStream(w http.ResponseWriter, flusher http.Flusher, label string, started bool, inlineErr []byte, scanErr error) {
	errType := "api_error"
	var msg string
	switch {
	case inlineErr != nil:
		if looksLikeOverflow(inlineErr) {
			errType = "invalid_request_error"
		}
		msg = errorMessage(inlineErr)
	case scanErr != nil:
		msg = "upstream stream ended early: " + scanErr.Error()
	default:
		msg = "upstream stream ended before completion"
	}

	ev := []byte(`{"type":"error","error":{"type":"api_error","message":""}}`)
	ev, _ = sjson.SetBytes(ev, "error.type", errType)
	ev, _ = sjson.SetBytes(ev, "error.message", msg)
	_, _ = w.Write(AppendSSEEventBytes(nil, "error", ev, 2))
	if flusher != nil {
		flusher.Flush()
	}
	log.Printf("%s: stream ended abnormally (started=%v): %s", label, started, msg)
}

// sseData extracts the JSON payload of a "data: {...}" SSE line, ok=false for a
// non-data line (event:, comment, blank).
func sseData(line []byte) (data []byte, ok bool) {
	if !bytes.HasPrefix(line, []byte("data:")) {
		return nil, false
	}
	d := bytes.TrimSpace(line[len("data:"):])
	if len(d) == 0 || d[0] != '{' {
		return nil, false
	}
	return d, true
}
