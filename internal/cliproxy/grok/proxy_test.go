package grok

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func writeGrokToken(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "auth.json")
	// The grok CLI's nested shape: one "<issuer>::<id>" key holding the session token.
	body := `{"https://auth.x.ai::abc-123":{"key":"sess-tok","refresh_token":"rt","expires_at":"2999-01-01T00:00:00Z","oidc_issuer":"https://auth.x.ai","oidc_client_id":"cid"}}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGrokTokenParse(t *testing.T) {
	m := newTokenManager(writeGrokToken(t))
	tok, err := m.token(nil)
	if err != nil {
		t.Fatal(err)
	}
	if tok != "sess-tok" {
		t.Errorf("token = %q, want sess-tok", tok)
	}
}

func TestGrokProxyRoundTrip(t *testing.T) {
	var gotAuth, gotXAuth, gotUA string
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotXAuth = r.Header.Get("X-XAI-Token-Auth")
		gotUA = r.Header.Get("User-Agent")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		sse := strings.Join([]string{
			`data: {"type":"response.created","response":{"id":"r1"}}`,
			`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","role":"assistant"}}`,
			`data: {"type":"response.output_text.delta","delta":"pong"}`,
			`data: {"type":"response.completed","response":{"stop_reason":"stop","usage":{"input_tokens":1,"output_tokens":1}}}`,
			"",
		}, "\n\n")
		_, _ = w.Write([]byte(sse))
	}))
	defer upstream.Close()

	p := NewProxy(writeGrokToken(t))
	p.baseURL = upstream.URL

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"grok-4.5","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	rec := httptest.NewRecorder()
	p.handleMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()
	for _, want := range []string{"message_start", "content_block", "pong", "message_stop"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q; got:\n%s", want, out)
		}
	}
	if gotAuth != "Bearer sess-tok" {
		t.Errorf("upstream Authorization = %q", gotAuth)
	}
	if gotXAuth != "xai-grok-cli" {
		t.Errorf("X-XAI-Token-Auth = %q", gotXAuth)
	}
	if !strings.Contains(gotUA, "xai-grok-workspace") {
		t.Errorf("User-Agent = %q", gotUA)
	}
	if gjson.GetBytes(gotBody, "stream").Bool() != true {
		t.Error("upstream body should request stream")
	}
}

func TestGrokModelOrFallback(t *testing.T) {
	cases := map[string]string{
		"grok-4.5":        "grok-4.5",
		"grok-4.5-fast":   "grok-4.5-fast",
		"claude-opus-4-8": fallbackGrokModel, // a switched session's Claude id
		"gpt-5.5":         fallbackGrokModel,
		"":                fallbackGrokModel,
	}
	for in, want := range cases {
		if got := grokModelOrFallback(in); got != want {
			t.Errorf("grokModelOrFallback(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDropOrphanToolChoice(t *testing.T) {
	// tool_choice with no tools -> stripped
	got := string(dropOrphanToolChoice([]byte(`{"tool_choice":{"type":"auto"}}`)))
	if strings.Contains(got, "tool_choice") {
		t.Errorf("orphan tool_choice not stripped: %s", got)
	}
	// tool_choice with tools -> kept
	got = string(dropOrphanToolChoice([]byte(`{"tool_choice":{"type":"auto"},"tools":[{"name":"x"}]}`)))
	if !strings.Contains(got, "tool_choice") {
		t.Errorf("valid tool_choice wrongly stripped: %s", got)
	}
	// empty tools array -> stripped
	got = string(dropOrphanToolChoice([]byte(`{"tool_choice":{"type":"auto"},"tools":[]}`)))
	if strings.Contains(got, "tool_choice") {
		t.Errorf("tool_choice with empty tools not stripped: %s", got)
	}
}

func TestGrokClientError(t *testing.T) {
	// Permanent conditions -> non-retryable 400 (so the CLI surfaces immediately).
	for _, body := range []string{
		`{"code":"subscription:free-usage-exhausted","error":"used all free usage"}`,
		`{"error":"The model x does not exist or your team does not have access to it"}`,
	} {
		if st, _ := grokClientError(429, []byte(body)); st != 400 {
			t.Errorf("permanent error should map to 400, got %d for %s", st, body)
		}
	}
	// A transient 500 passes through so the CLI can retry.
	if st, _ := grokClientError(500, []byte(`{"error":"internal"}`)); st != 500 {
		t.Errorf("transient 500 should pass through, got %d", st)
	}
}

func TestNoteQuotaFrom429(t *testing.T) {
	p := NewProxy("/nonexistent")
	body := []byte(`{"code":"subscription:free-usage-exhausted","error":"...Usage resets over a rolling 24-hour window — tokens (actual/limit): 1024032/1000000. Upgrade..."}`)
	p.noteQuota(body)
	used, limit, _, ok := p.FreeQuota()
	if !ok || used != 1024032 || limit != 1000000 {
		t.Errorf("FreeQuota = %d/%d ok=%v, want 1024032/1000000", used, limit, ok)
	}
	// A body without the token line leaves it unset.
	p2 := NewProxy("/nonexistent")
	p2.noteQuota([]byte(`{"error":"something else"}`))
	if _, _, _, ok := p2.FreeQuota(); ok {
		t.Error("should not capture without a token line")
	}
}
