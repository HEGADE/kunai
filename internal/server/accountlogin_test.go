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

// TestCopyFileSelfCopyPreserves guards the data-loss bug where copying a
// transcript onto itself truncated it to zero (os.Create truncates first). A
// self-copy must be a no-op that keeps the content intact.
func TestCopyFileSelfCopyPreserves(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/t.jsonl"
	want := "line one\nline two\n"
	if err := os.WriteFile(p, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(p, p); err != nil {
		t.Fatalf("self-copy: %v", err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("self-copy truncated the file: got %q, want %q", got, want)
	}
}

// TestCopyFileNormal copies to a distinct path (the real switch case).
func TestCopyFileNormal(t *testing.T) {
	dir := t.TempDir()
	src, dst := dir+"/a.jsonl", dir+"/sub/b.jsonl"
	want := "hello\n"
	if err := os.WriteFile(src, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copy: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("copy = %q, want %q", got, want)
	}
	// The source must be untouched.
	if s, _ := os.ReadFile(src); string(s) != want {
		t.Fatalf("source changed: %q", s)
	}
}
