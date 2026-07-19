package server

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// cloneFile is only exercised for real on a filesystem that supports cloning:
// btrfs/XFS on Linux, APFS on macOS. ext4 (this dev box and CI) refuses it, so
// this test adapts rather than skipping, and asserts whichever outcome applies.
//
// Where cloning works, the two properties that make it a safe substitute for
// copying are checked directly: the bytes match, and the clone is INDEPENDENT of
// its source (a hard link would fail the second half, and would let a turn
// written under one account show up in another's folder).
func TestCloneFileWhenSupported(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.jsonl")
	dst := filepath.Join(dir, "dst.jsonl")
	const body = "turn-one\nturn-two\n"
	if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	err := cloneFile(src, dst)
	if err != nil {
		t.Logf("%s: filesystem cannot clone (%v); copyFile takes the copy fallback here", runtime.GOOS, err)
		// The failed clone must not leave a stub behind, or the fallback would
		// stage on top of a partial file.
		if _, statErr := os.Stat(dst); statErr == nil {
			t.Fatal("a failed clone left a destination file behind")
		}
		return
	}

	t.Logf("%s: filesystem supports cloning; verifying the clone", runtime.GOOS)
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("clone content = %q, want %q", got, body)
	}
	// Independence: appending to the clone must leave the source untouched.
	f, err := os.OpenFile(dst, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("turn-three\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if s, _ := os.ReadFile(src); string(s) != body {
		t.Fatalf("source changed after writing to the clone (hard link, not a clone): %q", s)
	}
}

// cloneFile must refuse an existing destination, since that is the contract
// copyFile relies on (it removes any stale temp first). macOS clonefile(2)
// enforces this itself; the Linux path opens with O_EXCL to match.
func TestCloneFileRefusesExistingDestination(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const keep = "existing\n"
	if err := os.WriteFile(dst, []byte(keep), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cloneFile(src, dst); err == nil {
		t.Fatal("clone over an existing file should fail")
	}
	if got, _ := os.ReadFile(dst); string(got) != keep {
		t.Fatalf("existing destination was damaged: %q", got)
	}
}
