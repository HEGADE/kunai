package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// changes() stitches three git reads plus a real read of each untracked file
// into one tree. Drive it with a canned git so the merge (counts from numstat,
// status from name-status, binary flag, untracked as additions) is asserted
// without spawning git — the same discipline usageRun's tests use.
func TestChangesAssembly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := gitRun
	defer func() { gitRun = orig }()
	gitRun = func(_ context.Context, d string, args ...string) ([]byte, error) {
		if d != dir {
			t.Errorf("git ran in %q, want %q", d, dir)
		}
		key := strings.Join(args, " ")
		switch {
		case strings.Contains(key, "rev-parse --verify -q HEAD"):
			return nil, nil // HEAD exists, so base is HEAD
		case strings.Contains(key, "diff --numstat HEAD"):
			return []byte("3\t1\tsrc/app.ts\n0\t2\tsrc/gone.ts\n-\t-\tlogo.png\n"), nil
		case strings.Contains(key, "diff --name-status HEAD"):
			return []byte("M\tsrc/app.ts\nD\tsrc/gone.ts\nM\tlogo.png\n"), nil
		case strings.Contains(key, "ls-files --others"):
			return []byte("new.txt\n"), nil
		}
		return nil, fmt.Errorf("unexpected git %v", args)
	}

	resp, err := changes(context.Background(), dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Repo {
		t.Fatal("Repo = false, want true")
	}
	if len(resp.Files) != 4 {
		t.Fatalf("got %d files, want 4", len(resp.Files))
	}
	by := map[string]ChangedFile{}
	for _, f := range resp.Files {
		by[f.Path] = f
	}
	if f := by["src/app.ts"]; f.Status != "modified" || f.Added != 3 || f.Removed != 1 {
		t.Errorf("app.ts = %+v, want modified +3/-1", f)
	}
	if f := by["src/gone.ts"]; f.Status != "deleted" || f.Removed != 2 {
		t.Errorf("gone.ts = %+v, want deleted -2", f)
	}
	if f := by["logo.png"]; !f.Binary {
		t.Errorf("logo.png Binary = false, want true")
	}
	if f := by["new.txt"]; f.Status != "added" || f.Added != 3 {
		t.Errorf("new.txt = %+v, want added +3 (counted from disk)", f)
	}
	if resp.Added != 6 || resp.Removed != 3 {
		t.Errorf("totals = +%d/-%d, want +6/-3", resp.Added, resp.Removed)
	}
}

// sessionBase resolves the commit that was HEAD when the session started, so the
// review shows the session's committed work too (not just what is still
// uncommitted). It is derived from the start time via `git rev-list -1 --before`.
func TestSessionBase(t *testing.T) {
	dir := t.TempDir()
	start := time.Unix(1_700_000_000, 0)
	orig := gitRun
	defer func() { gitRun = orig }()

	// The commit at session start is returned, and --before carries the start ts.
	var sawBefore bool
	gitRun = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		key := strings.Join(args, " ")
		switch {
		case strings.Contains(key, "rev-parse --verify -q HEAD"):
			return nil, nil // HEAD exists
		case strings.Contains(key, "rev-list -1 --before=@1700000000 HEAD"):
			sawBefore = true
			return []byte("abc123def456\n"), nil
		}
		return nil, fmt.Errorf("unexpected git %v", args)
	}
	if got := sessionBase(context.Background(), dir, start); got != "abc123def456" {
		t.Errorf("base = %q, want abc123def456", got)
	}
	if !sawBefore {
		t.Error("rev-list --before was never run")
	}

	// A session that predates the first commit (rev-list finds nothing) falls back
	// to HEAD rather than the empty tree, so it never explodes to a whole-repo diff.
	gitRun = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		key := strings.Join(args, " ")
		if strings.Contains(key, "rev-parse --verify -q HEAD") {
			return nil, nil
		}
		return nil, nil // rev-list returns empty
	}
	if got := sessionBase(context.Background(), dir, start); got != "HEAD" {
		t.Errorf("base = %q, want HEAD (fallback)", got)
	}

	// A zero start time (unknown) is just HEAD.
	if got := sessionBase(context.Background(), dir, time.Time{}); got != "HEAD" {
		t.Errorf("base = %q, want HEAD for zero time", got)
	}
}
