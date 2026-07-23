package codex

// The ultimate end-to-end: the REAL `claude` CLI, driven exactly as a kunai
// provider session drives it (ANTHROPIC_BASE_URL + ANTHROPIC_AUTH_TOKEN +
// ANTHROPIC_DEFAULT_*_MODEL), pointed at the native proxy, which forwards to real
// Codex. If this passes, kunai can drive Codex through the native proxy with no
// sidecar. Gated on KUNAI_CODEX_LIVE=1 and KUNAI_CODEX_TOKEN; needs `claude` on PATH.

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestLiveClaudeThroughNativeProxy(t *testing.T) {
	if os.Getenv("KUNAI_CODEX_LIVE") != "1" {
		t.Skip("set KUNAI_CODEX_LIVE=1 and KUNAI_CODEX_TOKEN to run")
	}
	tokenPath := os.Getenv("KUNAI_CODEX_TOKEN")
	if tokenPath == "" {
		t.Fatal("KUNAI_CODEX_TOKEN not set")
	}
	claudeBin, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude not on PATH")
	}
	model := liveModel()

	// Serve the native proxy on a real localhost port.
	p := NewProxy(tokenPath, false)
	srv := httptest.NewServer(p.Handler())
	defer srv.Close()

	// Isolated claude config dir so this never touches the user's real login.
	cfgDir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, claudeBin, "-p",
		"Reply with exactly this word and nothing else: pong")
	cmd.Env = append(os.Environ(),
		"ANTHROPIC_BASE_URL="+srv.URL,
		"ANTHROPIC_AUTH_TOKEN=kunai-native", // the proxy ignores it; presence satisfies claude
		"ANTHROPIC_DEFAULT_OPUS_MODEL="+model,
		"ANTHROPIC_DEFAULT_SONNET_MODEL="+model,
		"ANTHROPIC_DEFAULT_HAIKU_MODEL="+model,
		"ANTHROPIC_MODEL="+model,
		"CLAUDE_CONFIG_DIR="+cfgDir,
		"DISABLE_TELEMETRY=1",
		"DISABLE_AUTOUPDATER=1",
	)
	out, err := cmd.CombinedOutput()
	t.Logf("claude output:\n%s", truncate(string(out), 2000))
	if err != nil {
		t.Fatalf("claude run failed: %v", err)
	}
	if !strings.Contains(strings.ToLower(string(out)), "pong") {
		t.Fatalf("expected 'pong' from claude via native proxy, got:\n%s", truncate(string(out), 2000))
	}
	t.Log("END TO END OK: real claude CLI -> native proxy -> real Codex -> 'pong'")
}
