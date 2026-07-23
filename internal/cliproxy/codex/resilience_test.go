package codex

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestModelWindow(t *testing.T) {
	cases := map[string]int{
		"grok-4.5":       WindowGrok,
		"grok-composer":  WindowGrok,
		"gpt-5.5":        WindowCodex,
		"codex-mini":     WindowCodex,
		"o3":             WindowCodex,
		"claude-opus-4":  WindowDefault,
		"something-else": WindowDefault,
	}
	for model, want := range cases {
		if got := ModelWindow(model); got != want {
			t.Errorf("ModelWindow(%q)=%d, want %d", model, got, want)
		}
	}
}

func TestGuardContextWindow(t *testing.T) {
	// Under the window: no guard. (100KB / 4 = 25k tokens, well under any window.)
	if tooLong, _, _, _ := GuardContextWindow("grok-4.5", make([]byte, 100_000)); tooLong {
		t.Error("small request wrongly guarded")
	}
	// Over the Grok window: 1.3MB / 4 = 325k tokens > 240k.
	tooLong, status, errType, msg := GuardContextWindow("grok-4.5", make([]byte, 1_300_000))
	if !tooLong {
		t.Fatal("over-window grok request not guarded")
	}
	if status != 400 || errType != "invalid_request_error" {
		t.Errorf("guard status/type = %d/%s, want 400/invalid_request_error", status, errType)
	}
	if !strings.Contains(msg, "prompt is too long") {
		t.Errorf("guard message %q missing the prompt-too-long phrasing the CLI recognizes", msg)
	}
	// Codex has a larger window than Grok, so a body between the two (1.0MB / 4 =
	// 250k tokens) trips Grok (240k) but not Codex (260k).
	if tooLong, _, _, _ := GuardContextWindow("grok-4.5", make([]byte, 1_000_000)); !tooLong {
		t.Error("250k-token request should trip the Grok window")
	}
	if tooLong, _, _, _ := GuardContextWindow("gpt-5.5", make([]byte, 1_000_000)); tooLong {
		t.Error("250k-token request wrongly guarded under the larger Codex window")
	}
}

func TestClassifyUpstreamError(t *testing.T) {
	// An upstream context-length rejection becomes prompt-too-long so the CLI
	// compacts instead of just surfacing a raw upstream string.
	st, et, _ := ClassifyUpstreamError(400, []byte(`{"error":{"message":"This model's maximum context length is 256000 tokens however you requested 300000"}}`))
	if st != 400 || et != "invalid_request_error" {
		t.Errorf("overflow => %d/%s, want 400/invalid_request_error", st, et)
	}
	// A permanent condition => non-retryable 400.
	if st, _, _ := ClassifyUpstreamError(429, []byte(`{"error":{"message":"insufficient_quota"}}`)); st != 400 {
		t.Errorf("permanent => %d, want 400", st)
	}
	// A transient server error passes through so the CLI can retry it.
	if st, et, _ := ClassifyUpstreamError(503, []byte(`{"error":"overloaded"}`)); st != 503 || et != "api_error" {
		t.Errorf("transient => %d/%s, want 503/api_error", st, et)
	}
}

func TestLooksLikeOverflow(t *testing.T) {
	yes := []string{
		`maximum context length is 200000`,
		`context_length_exceeded`,
		`Please reduce the length of the messages`,
		`prompt is too long`,
		`exceeds the maximum number of tokens`,
	}
	for _, s := range yes {
		if !looksLikeOverflow([]byte(s)) {
			t.Errorf("looksLikeOverflow(%q) = false, want true", s)
		}
	}
	if looksLikeOverflow([]byte(`internal server error`)) {
		t.Error("non-overflow error mis-detected as overflow")
	}
}

// a minimal, well-formed Codex stream that the translator turns into a complete
// Anthropic message (message_start ... message_stop).
var cleanCodexStream = strings.Join([]string{
	`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5"}}`,
	`data: {"type":"response.output_item.added","item":{"type":"message","status":"in_progress"},"output_index":1}`,
	`data: {"type":"response.output_text.delta","delta":"hello","output_index":1}`,
	`data: {"type":"response.output_item.done","item":{"type":"message","status":"completed"},"output_index":1}`,
	`data: {"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":2}}}`,
}, "\n")

func runStream(t *testing.T, body string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	original := []byte(`{"model":"x","messages":[]}`)
	StreamTranslate(context.Background(), rec, "test", "gpt-5", original, strings.NewReader(body))
	return rec.Body.String()
}

func TestStreamTranslate_CleanPass(t *testing.T) {
	out := runStream(t, cleanCodexStream)
	if !strings.Contains(out, `"type":"message_stop"`) {
		t.Error("clean stream missing message_stop")
	}
	if strings.Contains(out, "event: error") {
		t.Errorf("clean stream wrongly emitted an error event:\n%s", out)
	}
}

func TestStreamTranslate_DroppedBeforeStop(t *testing.T) {
	// The upstream started but the socket dropped before response.completed -- the
	// exact "stream disconnected" case. The proxy must close with a typed error.
	dropped := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5"}}`,
		`data: {"type":"response.output_item.added","item":{"type":"message","status":"in_progress"},"output_index":1}`,
		`data: {"type":"response.output_text.delta","delta":"partial","output_index":1}`,
	}, "\n")
	out := runStream(t, dropped)
	if strings.Contains(out, `"type":"message_stop"`) {
		t.Error("dropped stream should not have a real message_stop")
	}
	if !strings.Contains(out, "event: error") || !strings.Contains(out, "ended before completion") {
		t.Errorf("dropped stream missing a terminal error event:\n%s", out)
	}
	if !strings.Contains(out, `"api_error"`) {
		t.Error("a dropped stream should be a retryable api_error")
	}
}

func TestStreamTranslate_InlineOverflowFailure(t *testing.T) {
	// An inline upstream failure that is a context overflow becomes an
	// invalid_request_error so the CLI compacts rather than retrying forever.
	body := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5"}}`,
		`data: {"type":"response.failed","response":{"error":{"message":"maximum context length exceeded"}}}`,
	}, "\n")
	out := runStream(t, body)
	if !strings.Contains(out, "event: error") {
		t.Fatalf("inline failure not surfaced as an error event:\n%s", out)
	}
	if !strings.Contains(out, `"invalid_request_error"`) {
		t.Errorf("overflow inline failure should be invalid_request_error:\n%s", out)
	}
}

func TestStreamTranslate_EmptyUpstream(t *testing.T) {
	out := runStream(t, "")
	if !strings.Contains(out, "event: error") {
		t.Errorf("empty upstream should still close with an error event, got:\n%q", out)
	}
}

// buildReq builds an Anthropic request with a system prompt and the given messages
// (each a raw JSON object), for the trimming tests.
func buildReq(system string, msgs ...string) []byte {
	body := `{"model":"gpt-5.5","system":"` + system + `","messages":[` + strings.Join(msgs, ",") + `]}`
	return []byte(body)
}

func userMsg(text string) string      { return `{"role":"user","content":"` + text + `"}` }
func assistantMsg(text string) string { return `{"role":"assistant","content":"` + text + `"}` }
func toolResultMsg(id string) string {
	return `{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + id + `","content":"ok"}]}`
}

func TestFitContextToWindow_TrimsOldest(t *testing.T) {
	orig := winCodex
	winCodex = 2000 // ~8KB byte budget (bytes/4)
	defer func() { winCodex = orig }()

	pad := strings.Repeat("x", 1800)
	// 5 messages ending on a user turn; total well over 8KB so it must trim.
	req := buildReq("sys", userMsg("u1 "+pad), assistantMsg("a1 "+pad), userMsg("u2 "+pad), assistantMsg("a2 "+pad), userMsg("LATEST "+pad))
	if EstimateTokens(req) <= winCodex {
		t.Fatal("test request should exceed the window")
	}
	out, dropped, ok := FitContextToWindow("gpt-5.5", req)
	if !ok {
		t.Fatal("should have fit by trimming")
	}
	if dropped == 0 {
		t.Fatal("expected to drop oldest messages")
	}
	if EstimateTokens(out) > winCodex {
		t.Errorf("trimmed request still over window: est %d > %d", EstimateTokens(out), winCodex)
	}
	// The latest turn must survive; the oldest must be gone.
	if !strings.Contains(string(out), "LATEST") {
		t.Error("latest turn was dropped")
	}
	if strings.Contains(string(out), "u1 ") {
		t.Error("oldest turn should have been trimmed")
	}
}

func TestFitContextToWindow_NeverOrphansToolResult(t *testing.T) {
	orig := winCodex
	winCodex = 1200
	defer func() { winCodex = orig }()

	pad := strings.Repeat("y", 1500)
	// A tool_use/tool_result pair: dropping past the assistant would orphan the
	// tool_result, so the trim must not start the conversation on it.
	req := buildReq("sys",
		userMsg("first "+pad),
		`{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Read","input":{}}]}`,
		toolResultMsg("t1"),
		userMsg("LATEST "+pad))
	out, _, ok := FitContextToWindow("gpt-5.5", req)
	if !ok {
		return // couldn't fit at all is acceptable; the point is validity below
	}
	first := gjson.GetBytes(out, "messages.0")
	if first.Get("role").String() == "user" {
		c := first.Get("content")
		if c.IsArray() && c.Array()[0].Get("type").String() == "tool_result" {
			t.Error("trimmed conversation starts with an orphaned tool_result")
		}
	}
}

func TestFitContextToWindow_UnfittableLatestTurn(t *testing.T) {
	orig := winCodex
	winCodex = 100 // 400 bytes: even one padded turn cannot fit
	defer func() { winCodex = orig }()
	req := buildReq("sys", userMsg(strings.Repeat("z", 5000)))
	if _, _, ok := FitContextToWindow("gpt-5.5", req); ok {
		t.Error("a single over-window turn should be unfittable (ok=false)")
	}
}

func TestFitContextToWindow_UnderWindowUnchanged(t *testing.T) {
	req := buildReq("sys", userMsg("hi"))
	out, dropped, ok := FitContextToWindow("gpt-5.5", req)
	if !ok || dropped != 0 || string(out) != string(req) {
		t.Errorf("under-window request should pass through unchanged (dropped=%d ok=%v)", dropped, ok)
	}
}
