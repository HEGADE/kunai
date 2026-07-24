package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/hegade/kunai/internal/checkpoint"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}} {
		_ = args
	}
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
	os.WriteFile(filepath.Join(dir, "code.txt"), []byte("v0\n"), 0o644)
	run("add", "-A")
	run("commit", "-qm", "init")
	return dir
}

// The full backend path: capture a pre-turn checkpoint, "run a turn" that edits the
// file, then revert by the turn's seq -> the edit is undone. Exactly what the
// session hook + revert endpoint chain together.
func TestCheckpointManager_CaptureAndRevert(t *testing.T) {
	repo := gitRepo(t)
	m := newCheckpointManager()

	// Turn seq 7 starts: snapshot the pre-turn tree (code.txt == "v0").
	m.capture("sessA", repo, 7)

	ref, ok := m.refForSeq("sessA", 7)
	if !ok {
		t.Fatal("no checkpoint recorded for the turn")
	}
	if len(m.list("sessA")) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(m.list("sessA")))
	}

	// The "agent" edits the file and adds a new one during the turn.
	os.WriteFile(filepath.Join(repo, "code.txt"), []byte("AGENT EDIT\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "extra.txt"), []byte("agent added\n"), 0o644)

	// Revert the turn (what handleRevert does with the ref).
	safety, err := checkpoint.Restore(repo, ref, checkpoint.SafetyRefFor("sessA", 999))
	if err != nil {
		t.Fatalf("revert: %v", err)
	}

	if b, _ := os.ReadFile(filepath.Join(repo, "code.txt")); string(b) != "v0\n" {
		t.Errorf("code.txt = %q, want the pre-turn v0", b)
	}
	if _, err := os.Stat(filepath.Join(repo, "extra.txt")); err == nil {
		t.Error("extra.txt should be removed by the revert")
	}
	if safety == "" {
		t.Error("revert should return a safety ref for undo")
	}
}

func TestCheckpointManager_NonGitAndRecords(t *testing.T) {
	m := newCheckpointManager()
	// A non-git cwd records nothing (silently skipped).
	m.capture("s", t.TempDir(), 1)
	if len(m.list("s")) != 0 {
		t.Error("non-git session should have no checkpoints")
	}
	// record replaces a re-prompted turn's ref rather than duplicating it.
	m.record("s", 3, checkpoint.Ref("refs/kunai/checkpoints/s/3"))
	m.record("s", 3, checkpoint.Ref("refs/kunai/checkpoints/s/3b"))
	if l := m.list("s"); len(l) != 1 || l[0].Ref != "refs/kunai/checkpoints/s/3b" {
		t.Errorf("re-prompt should replace the checkpoint, got %+v", l)
	}
	m.forget("s")
	if len(m.list("s")) != 0 {
		t.Error("forget should clear the session's checkpoints")
	}
}
