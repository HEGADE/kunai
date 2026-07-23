package codex

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

// writeToken drops a token file with a far-future expiry so creds() never refreshes.
func writeToken(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "codex-test.json")
	exp := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	body := `{"access_token":"tok-abc","refresh_token":"ref","account_id":"acct-1","expired":"` + exp + `","type":"codex"}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestBuildCodexRequest checks the request massaging: model set, stream true,
// stripped fields gone, instructions present.
func TestBuildCodexRequest(t *testing.T) {
	p := NewProxy(writeToken(t), true)
	in := []byte(`{"model":"gpt-5.6","messages":[{"role":"user","content":"hi"}],"previous_response_id":"x","stream":false}`)
	out := p.buildCodexRequest("gpt-5.6", in)
	if got := gjson.GetBytes(out, "model").String(); got != "gpt-5.6" {
		t.Errorf("model = %q", got)
	}
	if !gjson.GetBytes(out, "stream").Bool() {
		t.Error("stream should be true")
	}
	if gjson.GetBytes(out, "previous_response_id").Exists() {
		t.Error("previous_response_id should be stripped")
	}
	if !gjson.GetBytes(out, "instructions").Exists() {
		t.Error("instructions should be present")
	}
}

// TestProxyStreamRoundTrip is the end-to-end offline proof: an Anthropic request
// goes in, a mock Codex upstream returns a canned SSE stream, and the client gets
// well-formed Anthropic SSE (message_start, a text delta, message_stop). The token
// and account headers reach the upstream.
func TestProxyStreamRoundTrip(t *testing.T) {
	var gotAuth, gotAccount, gotUA string
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccount = r.Header.Get("Chatgpt-Account-Id")
		gotUA = r.Header.Get("User-Agent")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		// A minimal but realistic Codex /responses SSE: text output then completed.
		sse := strings.Join([]string{
			`data: {"type":"response.created","response":{"id":"resp_1"}}`,
			`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","role":"assistant"}}`,
			`data: {"type":"response.output_text.delta","delta":"Hello"}`,
			`data: {"type":"response.output_text.delta","delta":" world"}`,
			`data: {"type":"response.completed","response":{"stop_reason":"stop","usage":{"input_tokens":3,"output_tokens":2}}}`,
			"",
		}, "\n\n")
		_, _ = w.Write([]byte(sse))
	}))
	defer upstream.Close()

	p := NewProxy(writeToken(t), true)
	p.baseURL = upstream.URL

	in := `{"model":"gpt-5.6","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(in))
	rec := httptest.NewRecorder()
	p.handleMessages(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()
	// The client must receive Anthropic streaming events.
	for _, want := range []string{"message_start", "content_block", "message_stop"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q; got:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "Hello") || !strings.Contains(out, "world") {
		t.Errorf("output missing text; got:\n%s", out)
	}
	// Auth reached upstream.
	if gotAuth != "Bearer tok-abc" {
		t.Errorf("upstream Authorization = %q", gotAuth)
	}
	if gotAccount != "acct-1" {
		t.Errorf("upstream Chatgpt-Account-Id = %q", gotAccount)
	}
	if !strings.Contains(gotUA, "codex-tui") {
		t.Errorf("upstream User-Agent = %q", gotUA)
	}
	// The upstream body was Codex-format (has input/instructions, not Anthropic messages).
	if !gjson.GetBytes(gotBody, "instructions").Exists() {
		t.Errorf("upstream body not codex-shaped: %s", gotBody)
	}
	if gjson.GetBytes(gotBody, "stream").Bool() != true {
		t.Error("upstream body should request stream")
	}
}

// TestProxyUpstreamErrorPassthrough maps an upstream error to an Anthropic error.
func TestProxyUpstreamErrorPassthrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	defer upstream.Close()

	p := NewProxy(writeToken(t), true)
	p.baseURL = upstream.URL
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"gpt-5.6","stream":true,"messages":[]}`))
	rec := httptest.NewRecorder()
	p.handleMessages(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "rate limited") {
		t.Errorf("error not passed through: %s", rec.Body.String())
	}
	if gjson.Get(rec.Body.String(), "type").String() != "error" {
		t.Errorf("not an anthropic error envelope: %s", rec.Body.String())
	}
}

func TestCodexModelOrFallback(t *testing.T) {
	cases := map[string]string{
		"gpt-5.5":         "gpt-5.5",
		"gpt-5.6-terra":   "gpt-5.6-terra",
		"codex-1":         "codex-1",
		"o3-mini":         "o3-mini",
		"claude-opus-4-8": fallbackCodexModel,
		"grok-4.5":        fallbackCodexModel,
	}
	for in, want := range cases {
		if got := codexModelOrFallback(in); got != want {
			t.Errorf("codexModelOrFallback(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCodexDropOrphanToolChoice(t *testing.T) {
	if strings.Contains(string(dropOrphanToolChoice([]byte(`{"tool_choice":{"type":"auto"}}`))), "tool_choice") {
		t.Error("orphan tool_choice not stripped")
	}
	if !strings.Contains(string(dropOrphanToolChoice([]byte(`{"tool_choice":{"type":"auto"},"tools":[{"name":"x"}]}`))), "tool_choice") {
		t.Error("valid tool_choice wrongly stripped")
	}
}
