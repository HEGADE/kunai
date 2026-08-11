package server

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

// cwdsFor is the platform-specific half of attribution, and the half that made
// the feature work at all: a backgrounded dev server is orphaned to init, so its
// directory is the only thing still tying it to a session.
//
// Run against a real process, because the two ways of reading a cwd both depend
// on the machine: /proc exists only on Linux, and this host's lsof declined to
// report cwd at all. A fixture cannot notice either.
func TestCwdsForReadsARealProcess(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	child := exec.CommandContext(ctx, "sleep", "30")
	child.Dir = dir
	if err := child.Start(); err != nil {
		t.Skipf("could not start a child: %v", err)
	}
	defer func() { _ = child.Process.Kill() }()

	got := cwdsFor([]int{child.Process.Pid})
	dirGot, ok := got[child.Process.Pid]
	if !ok {
		t.Fatalf("no working directory read for a live child; neither /proc nor lsof answered")
	}
	// t.TempDir can sit behind a symlink (/tmp -> /private/tmp on macOS), so
	// compare what the OS resolves rather than the string we asked for.
	want, _ := os.Readlink("/proc/self/cwd")
	_ = want
	if dirGot != dir {
		// Resolve both before calling it a failure.
		a, _ := os.Stat(dirGot)
		b, _ := os.Stat(dir)
		if a == nil || b == nil || !os.SameFile(a, b) {
			t.Errorf("cwd = %q, want %q", dirGot, dir)
		}
	}

	// A pid that does not exist contributes nothing, rather than an empty string
	// that would then match every session with an empty cwd.
	if _, ok := cwdsFor([]int{999999})[999999]; ok {
		t.Error("a dead pid produced a working directory")
	}
}
