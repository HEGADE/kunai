package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	resp, err := changes(context.Background(), dir)
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
