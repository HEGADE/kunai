package grok

// Live test against a real Grok (xAI) account via the grok CLI token. Gated on
// KUNAI_GROK_LIVE=1; the proxy reads ~/.grok/auth.json itself (default) or the path
// in KUNAI_GROK_TOKEN. Model defaults to grok-4.5.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

func grokToken() string {
	if p := os.Getenv("KUNAI_GROK_TOKEN"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return home + "/.grok/auth.json"
}

func grokModel() string {
	if m := os.Getenv("KUNAI_GROK_MODEL"); m != "" {
		return m
	}
	return "grok-4.5"
}

func TestLiveGrokRoundTrip(t *testing.T) {
	if os.Getenv("KUNAI_GROK_LIVE") != "1" {
		t.Skip("set KUNAI_GROK_LIVE=1 to run")
	}
	p := NewProxy(grokToken())
	body := `{"model":"` + grokModel() + `","max_tokens":64,"stream":true,` +
		`"messages":[{"role":"user","content":"Reply with exactly this word and nothing else: pong"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	rec := httptest.NewRecorder()
	p.handleMessages(rec, req)

	out := rec.Body.String()
	t.Logf("status=%d", rec.Code)
	if rec.Code != http.StatusOK {
		t.Fatalf("grok request failed: status=%d body=%s", rec.Code, trunc(out, 1500))
	}
	var text strings.Builder
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			d := strings.TrimSpace(line[5:])
			if gjson.Get(d, "type").String() == "content_block_delta" {
				text.WriteString(gjson.Get(d, "delta.text").String())
			}
		}
	}
	got := strings.TrimSpace(text.String())
	t.Logf("grok reply: %q", got)
	if !strings.Contains(strings.ToLower(got), "pong") {
		t.Errorf("expected pong, got %q", got)
	}
}

// The real claude CLI driven through the Grok proxy, exactly as a kunai session does.
func TestLiveClaudeThroughGrok(t *testing.T) {
	if os.Getenv("KUNAI_GROK_LIVE") != "1" {
		t.Skip("set KUNAI_GROK_LIVE=1 to run")
	}
	claudeBin, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude not on PATH")
	}
	p := NewProxy(grokToken())
	srv := httptest.NewServer(p.Handler())
	defer srv.Close()

	cfgDir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	model := grokModel()
	cmd := exec.CommandContext(ctx, claudeBin, "-p", "Reply with exactly this word and nothing else: pong")
	cmd.Env = append(os.Environ(),
		"ANTHROPIC_BASE_URL="+srv.URL,
		"ANTHROPIC_AUTH_TOKEN=kunai-native",
		"ANTHROPIC_DEFAULT_OPUS_MODEL="+model,
		"ANTHROPIC_DEFAULT_SONNET_MODEL="+model,
		"ANTHROPIC_DEFAULT_HAIKU_MODEL="+model,
		"ANTHROPIC_MODEL="+model,
		"CLAUDE_CONFIG_DIR="+cfgDir,
		"DISABLE_TELEMETRY=1", "DISABLE_AUTOUPDATER=1",
	)
	out, err := cmd.CombinedOutput()
	t.Logf("claude output:\n%s", trunc(string(out), 2000))
	if err != nil {
		t.Fatalf("claude via grok failed: %v", err)
	}
	if !strings.Contains(strings.ToLower(string(out)), "pong") {
		t.Fatalf("expected pong from claude via grok, got:\n%s", trunc(string(out), 2000))
	}
	t.Log("END TO END OK: real claude CLI -> grok proxy -> real Grok -> 'pong'")
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...[truncated]"
}
