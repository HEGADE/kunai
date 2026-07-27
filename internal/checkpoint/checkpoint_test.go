package checkpoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// List reads checkpoints straight from the git shadow refs, so they survive a
// kunai restart (the in-memory map does not, but the refs do). It must return the
// per-turn snapshots in Seq order and exclude the safety refs.
func TestList_ReadsFromRefsInSeqOrder(t *testing.T) {
	dir := newRepo(t)
	sid := "sess-abc"
	// Capture three turns out of order, plus a safety ref that must NOT appear.
	for _, n := range []uint64{2, 10, 1} {
		if _, err := Capture(dir, RefFor(sid, n), "turn"); err != nil {
			t.Fatalf("capture %d: %v", n, err)
		}
	}
	if _, err := Capture(dir, SafetyRefFor(sid, 99), "safety"); err != nil {
		t.Fatal(err)
	}

	got := List(dir, sid)
	if len(got) != 3 {
		t.Fatalf("want 3 turn snapshots, got %d: %+v", len(got), got)
	}
	wantSeq := []uint64{1, 2, 10}
	for i, s := range got {
		if s.Seq != wantSeq[i] {
			t.Errorf("snapshot %d: seq %d, want %d", i, s.Seq, wantSeq[i])
		}
		if s.Ref != RefFor(sid, s.Seq) {
			t.Errorf("snapshot %d: ref %q, want %q", i, s.Ref, RefFor(sid, s.Seq))
		}
	}
}

// A session with no checkpoints, and a non-git dir, both return empty (never a
// panic or error the caller must handle).
func TestList_EmptyAndNonGit(t *testing.T) {
	if got := List(newRepo(t), "no-such-session"); len(got) != 0 {
		t.Errorf("empty session should list nothing, got %+v", got)
	}
	if got := List(t.TempDir(), "sess"); got != nil {
		t.Errorf("non-git dir should list nil, got %+v", got)
	}
}

// Preview is what a confirmation dialog is built on, so it has to report the full
// blast radius of a Restore rather than only the files the agent touched. Restore
// resets the whole repository: it reverts tracked edits made since the snapshot,
// deletes tracked files added since, restores tracked files deleted since, and
// removes untracked non-ignored files anywhere in the repo. Every one of those is
// work somebody could lose without being told.
func TestPreviewReportsTheWholeBlastRadius(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if _, err := git(dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@localhost")
	run("config", "user.name", "t")
	write := func(name, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("keep.txt", "one\n")
	write("gone.txt", "delete me later\n")
	write(".gitignore", "ignored.txt\n")
	run("add", "-A")
	run("commit", "-q", "-m", "base")

	ref, err := Capture(dir, RefFor("s", 1), "snapshot")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}

	// Everything a restore would touch, one of each kind.
	write("keep.txt", "one\ntwo\n")          // modified since
	write("added.txt", "new tracked file\n") // added and staged since
	run("add", "added.txt")
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	write("untracked/note.md", "not in git at all\n") // clean -df would remove this
	write("ignored.txt", "must survive\n")            // ignored: clean -df must NOT remove it

	changed, removed, err := Preview(dir, ref)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	byPath := map[string]string{}
	for _, c := range changed {
		byPath[c.Path] = c.Status
	}
	for path, want := range map[string]string{"keep.txt": "M", "added.txt": "A", "gone.txt": "D"} {
		if got := byPath[path]; got != want {
			t.Errorf("changed[%q] = %q, want %q (all of %v)", path, got, want, changed)
		}
	}

	var sawUntracked, sawIgnored bool
	for _, p := range removed {
		if strings.Contains(p, "untracked") {
			sawUntracked = true
		}
		if strings.Contains(p, "ignored.txt") {
			sawIgnored = true
		}
	}
	if !sawUntracked {
		t.Errorf("preview did not warn about the untracked file it would delete: %v", removed)
	}
	// An ignored file is not removed by `clean -df`, so promising to remove it would
	// scare someone off a safe revert.
	if sawIgnored {
		t.Errorf("preview claims it would remove an ignored file, which clean -df leaves alone: %v", removed)
	}

	// A repository with nothing to do reports empty lists, not nil: a client that
	// gets null where it expected an array throws on .length.
	run("checkout", "-q", "--", ".")
	if err := os.RemoveAll(filepath.Join(dir, "untracked")); err != nil {
		t.Fatal(err)
	}
	run("reset", "-q", "HEAD")
	write("keep.txt", "one\n")
	write("gone.txt", "delete me later\n")
	changed, removed, err = Preview(dir, ref)
	if err != nil {
		t.Fatalf("preview clean: %v", err)
	}
	if changed == nil || removed == nil {
		t.Errorf("clean preview returned nil slices: changed=%v removed=%v", changed, removed)
	}
}
