package codex

// Live test against a real Codex account. Gated on KUNAI_CODEX_LIVE=1 so it never
// runs in the normal suite (it costs quota and needs a token). The proxy reads the
// token file itself; the test never reads credentials.
//
//   KUNAI_CODEX_LIVE=1 KUNAI_CODEX_TOKEN=/path/to/codex-*.json \
//     go test ./internal/cliproxy/codex/ -run TestLive -v
//
// Optional: KUNAI_CODEX_MODEL (default gpt-5.5).

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func liveModel() string {
	if m := os.Getenv("KUNAI_CODEX_MODEL"); m != "" {
		return m
	}
	return "gpt-5.5"
}

func TestLiveCodexRoundTrip(t *testing.T) {
	if os.Getenv("KUNAI_CODEX_LIVE") != "1" {
		t.Skip("set KUNAI_CODEX_LIVE=1 and KUNAI_CODEX_TOKEN to run")
	}
	tokenPath := os.Getenv("KUNAI_CODEX_TOKEN")
	if tokenPath == "" {
		t.Fatal("KUNAI_CODEX_TOKEN not set")
	}
	p := NewProxy(tokenPath, false) // owns=false: never write back to the live token

	body := `{"model":"` + liveModel() + `","max_tokens":64,"stream":true,` +
		`"messages":[{"role":"user","content":"Reply with exactly this word and nothing else: pong"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	rec := httptest.NewRecorder()
	p.handleMessages(rec, req)

	t.Logf("status=%d", rec.Code)
	out := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("live request failed: status=%d body=%s", rec.Code, out)
	}
	// Reconstruct the assistant text from the Anthropic SSE deltas.
	var text strings.Builder
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(line[5:])
		if gjson.Get(data, "type").String() == "content_block_delta" {
			text.WriteString(gjson.Get(data, "delta.text").String())
		}
	}
	got := strings.TrimSpace(text.String())
	t.Logf("assistant text: %q", got)
	if got == "" {
		t.Fatalf("no assistant text in response; raw SSE:\n%s", truncate(out, 2000))
	}
	if !strings.Contains(strings.ToLower(got), "pong") {
		t.Errorf("expected 'pong' in reply, got %q", got)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...[truncated]"
}

// A session switched from Claude carries a resolved Claude id (claude-opus-4-8);
// the proxy must coerce it to a real Codex model instead of 404ing. Live proof.
func TestLiveCodexCoercesClaudeModel(t *testing.T) {
	if os.Getenv("KUNAI_CODEX_LIVE") != "1" {
		t.Skip("set KUNAI_CODEX_LIVE=1 and KUNAI_CODEX_TOKEN to run")
	}
	tokenPath := os.Getenv("KUNAI_CODEX_TOKEN")
	if tokenPath == "" {
		t.Fatal("KUNAI_CODEX_TOKEN not set")
	}
	p := NewProxy(tokenPath, false)
	body := `{"model":"claude-opus-4-8","max_tokens":64,"stream":true,` +
		`"messages":[{"role":"user","content":"Reply with exactly this word and nothing else: pong"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	rec := httptest.NewRecorder()
	p.handleMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("coerced request failed: status=%d body=%s", rec.Code, truncate(rec.Body.String(), 800))
	}
	var text strings.Builder
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			d := strings.TrimSpace(line[5:])
			if gjson.Get(d, "type").String() == "content_block_delta" {
				text.WriteString(gjson.Get(d, "delta.text").String())
			}
		}
	}
	if !strings.Contains(strings.ToLower(text.String()), "pong") {
		t.Errorf("expected pong from a coerced claude model, got %q", text.String())
	}
	t.Log("coercion OK: claude-opus-4-8 -> Codex model -> pong")
}
