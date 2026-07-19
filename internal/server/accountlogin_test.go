package server

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Opt-in (spawns a real `claude auth login`, cancelled before completion): proves
// the PTY spawn + URL scrape work against the actual CLI. KUNAI_E2E=1 to run.
func TestAccountLoginStartCapturesURL(t *testing.T) {
	if os.Getenv("KUNAI_E2E") == "" {
		t.Skip("opt-in: set KUNAI_E2E=1 to spawn a real claude auth login")
	}
	m := newLoginManager("claude", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	id, url, dir, err := m.start(ctx, "Work Test")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.cancel(id)
	t.Logf("flow id=%s dir=%s", id, dir)
	t.Logf("url=%s", url)
	if !strings.Contains(url, "oauth") || !strings.HasPrefix(url, "https://") {
		t.Fatalf("url = %q, want an https oauth link", url)
	}
}

func TestAccountSlug(t *testing.T) {
	cases := map[string]string{"Work": "work", "My Work Acct!": "my-work-acct", "  a  b ": "a-b", "": ""}
	for in, want := range cases {
		if got := accountSlug(in); got != want {
			t.Errorf("accountSlug(%q) = %q, want %q", in, got, want)
		}
	}
}
