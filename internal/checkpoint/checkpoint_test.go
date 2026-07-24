package checkpoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// newRepo builds a temp git repo with one commit and returns its root.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	write(t, dir, ".gitignore", "node_modules/\n*.log\n")
	write(t, dir, "keep.txt", "keep me\n")
	write(t, dir, "change.txt", "original\n")
	write(t, dir, "delete.txt", "will be deleted\n")
	run("add", "-A")
	run("commit", "-qm", "init")
	return dir
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		return "<missing>"
	}
	return string(b)
}

func exists(dir, rel string) bool {
	_, err := os.Stat(filepath.Join(dir, rel))
	return err == nil
}

// The core guarantee: capture the working tree, diverge in every way (modify, add,
// delete, recreate-a-deleted-file), then restore to an EXACT match, with ignored
// files left alone.
func TestCaptureRestore_RoundTrip(t *testing.T) {
	dir := newRepo(t)
	// State to snapshot: modify change.txt, add new.txt, delete delete.txt.
	write(t, dir, "change.txt", "AT CHECKPOINT\n")
	write(t, dir, "new.txt", "new at checkpoint\n")
	os.Remove(filepath.Join(dir, "delete.txt"))
	// An ignored file must survive a restore untouched.
	write(t, dir, "node_modules/lib.js", "ignored junk\n")

	ref := RefFor("sess1", 1)
	if _, err := Capture(dir, ref, "turn 1"); err != nil {
		t.Fatalf("capture: %v", err)
	}

	// The agent now diverges from the checkpoint every possible way.
	write(t, dir, "change.txt", "AGENT CHANGED IT\n") // modify again
	write(t, dir, "agent_new.txt", "agent added\n")   // new untracked file
	os.Remove(filepath.Join(dir, "new.txt"))          // delete the checkpoint's file
	write(t, dir, "delete.txt", "agent recreated\n")  // recreate a deleted file
	write(t, dir, "node_modules/lib.js", "AGENT TOUCHED IGNORED\n")

	safety, err := Restore(dir, ref, SafetyRefFor("sess1", 1))
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if safety == "" {
		t.Error("restore should return a safety ref")
	}

	// Exact match of the checkpoint state.
	if got := read(t, dir, "change.txt"); got != "AT CHECKPOINT\n" {
		t.Errorf("change.txt = %q, want the checkpoint content", got)
	}
	if got := read(t, dir, "new.txt"); got != "new at checkpoint\n" {
		t.Errorf("new.txt = %q, want it restored", got)
	}
	if exists(dir, "delete.txt") {
		t.Error("delete.txt should be gone (it was deleted at checkpoint)")
	}
	if exists(dir, "agent_new.txt") {
		t.Error("agent_new.txt should be removed (added after checkpoint)")
	}
	if got := read(t, dir, "keep.txt"); got != "keep me\n" {
		t.Errorf("keep.txt = %q, want unchanged", got)
	}
	// The ignored file must be preserved, NOT reverted or removed.
	if got := read(t, dir, "node_modules/lib.js"); got != "AGENT TOUCHED IGNORED\n" {
		t.Errorf("ignored node_modules/lib.js = %q, want it left alone", got)
	}
}

// Capturing must not disturb the user's working tree, index, or HEAD.
func TestCapture_NonDestructive(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "change.txt", "dirty\n")
	write(t, dir, "untracked.txt", "u\n")

	statusBefore := gitOut(t, dir, "status", "--porcelain")
	headBefore := gitOut(t, dir, "rev-parse", "HEAD")

	if _, err := Capture(dir, RefFor("s", 1), "cp"); err != nil {
		t.Fatalf("capture: %v", err)
	}

	if gitOut(t, dir, "status", "--porcelain") != statusBefore {
		t.Error("capture changed the working tree / index status")
	}
	if gitOut(t, dir, "rev-parse", "HEAD") != headBefore {
		t.Error("capture moved HEAD")
	}
	if read(t, dir, "change.txt") != "dirty\n" {
		t.Error("capture altered a working-tree file")
	}
}

// The safety snapshot a Restore takes must let us undo the revert.
func TestRestore_IsUndoable(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "change.txt", "state A\n")
	cpA := RefFor("s", 1)
	if _, err := Capture(dir, cpA, "A"); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "change.txt", "state B\n") // the state we'll revert away from

	safety, err := Restore(dir, cpA, SafetyRefFor("s", 1))
	if err != nil {
		t.Fatal(err)
	}
	if read(t, dir, "change.txt") != "state A\n" {
		t.Fatal("restore did not revert to A")
	}
	// Undo the revert by restoring the safety ref -> back to B.
	if _, err := Restore(dir, safety, SafetyRefFor("s", 2)); err != nil {
		t.Fatal(err)
	}
	if got := read(t, dir, "change.txt"); got != "state B\n" {
		t.Errorf("undo-revert = %q, want state B restored", got)
	}
}

func TestCapture_NotGit(t *testing.T) {
	if _, err := Capture(t.TempDir(), RefFor("s", 1), "cp"); err != ErrNotGit {
		t.Errorf("non-git dir: err = %v, want ErrNotGit", err)
	}
	if IsRepo(t.TempDir()) {
		t.Error("IsRepo true for a non-git dir")
	}
}

func TestRestore_MissingRef(t *testing.T) {
	dir := newRepo(t)
	if _, err := Restore(dir, RefFor("nope", 99), SafetyRefFor("nope", 99)); err != ErrNoRef {
		t.Errorf("missing ref: err = %v, want ErrNoRef", err)
	}
}

// A repo with no commits yet must still capture (a parentless snapshot).
func TestCapture_NoHead(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %v: %s", err, out)
	}
	write(t, dir, "a.txt", "hello\n")
	ref, err := Capture(dir, RefFor("s", 1), "first")
	if err != nil {
		t.Fatalf("capture no-head: %v", err)
	}
	if !Exists(dir, ref) {
		t.Error("checkpoint ref should exist after a no-head capture")
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}
