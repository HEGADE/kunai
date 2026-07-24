// Package checkpoint captures and restores git working-tree snapshots so a session
// can undo an agent turn's changes. A snapshot is a commit object on a shadow ref
// (refs/kunai/checkpoints/...) built from a throwaway index, so capturing NEVER
// touches the user's working tree, index, or HEAD. Restoring forces the working
// tree back to a snapshot (destructive), first capturing a safety snapshot so the
// revert is itself undoable.
//
// The exact git plumbing was verified to round-trip (modify + add + delete a file,
// snapshot, diverge, restore -> exact match) including preserving .gitignore'd files
// like node_modules; see checkpoint_test.go, which asserts the same against real
// temp repos.
package checkpoint

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ErrNotGit is returned when the directory is not inside a git work tree, so the
// caller can silently skip checkpointing (a non-git session simply has none).
var ErrNotGit = errors.New("checkpoint: not a git repository")

// ErrNoRef is returned when a checkpoint ref does not exist (already GC'd, or a
// bad name).
var ErrNoRef = errors.New("checkpoint: ref not found")

// RefPrefix namespaces every kunai checkpoint ref, well away from refs/heads so it
// never shows as a branch and `git checkout` never lands on one by accident.
const RefPrefix = "refs/kunai/checkpoints/"

// Ref is a fully-qualified checkpoint ref, e.g. refs/kunai/checkpoints/<sid>/3.
type Ref string

// RefFor builds the checkpoint ref for a session and a monotonic index (usually the
// turn's Seq). Names are sanitized so an odd session id can't escape the namespace.
func RefFor(sessionID string, n uint64) Ref {
	return Ref(fmt.Sprintf("%s%s/%d", RefPrefix, sanitize(sessionID), n))
}

// SafetyRefFor is the ref a Restore snapshots the pre-revert state into, so a revert
// can itself be undone.
func SafetyRefFor(sessionID string, n uint64) Ref {
	return Ref(fmt.Sprintf("%ssafety/%s/%d", RefPrefix, sanitize(sessionID), n))
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "session"
	}
	return b.String()
}

// IsRepo reports whether dir is inside a git work tree.
func IsRepo(dir string) bool {
	out, err := git(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// Capture snapshots the current working tree of dir into ref and returns it. It is
// non-destructive: a throwaway index (GIT_INDEX_FILE) is seeded from HEAD, `add -A`
// stages the whole working tree into it (respecting .gitignore), and the resulting
// tree is committed to the shadow ref. The user's index, working tree, and HEAD are
// never touched. Returns ErrNotGit for a non-git dir.
func Capture(dir string, ref Ref, message string) (Ref, error) {
	if !IsRepo(dir) {
		return "", ErrNotGit
	}
	root, err := repoRoot(dir)
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp("", "kunai-cp-index-*")
	if err != nil {
		return "", err
	}
	idx := tmp.Name()
	tmp.Close()
	// git needs to create the index itself; an empty pre-existing file is an invalid
	// index ("smaller than expected"). Remove it so read-tree/add write a fresh one.
	os.Remove(idx)
	defer os.Remove(idx)
	env := []string{"GIT_INDEX_FILE=" + idx}

	// Seed the temp index from HEAD when there is one; a repo with no commits yet
	// starts from an empty index and the snapshot has no parent.
	head, hasHead := headCommit(root)
	if hasHead {
		if _, err := gitEnv(root, env, "read-tree", string(head)); err != nil {
			return "", fmt.Errorf("checkpoint: seed index: %w", err)
		}
	}
	if _, err := gitEnv(root, env, "add", "-A"); err != nil {
		return "", fmt.Errorf("checkpoint: stage: %w", err)
	}
	tree, err := gitEnv(root, env, "write-tree")
	if err != nil {
		return "", fmt.Errorf("checkpoint: write-tree: %w", err)
	}
	tree = strings.TrimSpace(tree)

	args := []string{"commit-tree", tree, "-m", message}
	if hasHead {
		args = append(args, "-p", string(head))
	}
	commit, err := git(root, args...)
	if err != nil {
		return "", fmt.Errorf("checkpoint: commit-tree: %w", err)
	}
	commit = strings.TrimSpace(commit)
	if _, err := git(root, "update-ref", string(ref), commit); err != nil {
		return "", fmt.Errorf("checkpoint: update-ref: %w", err)
	}
	return ref, nil
}

// Restore forces the working tree of dir back to the checkpoint at ref: files the
// agent modified are reverted, files it added are removed, files it deleted are
// recreated -- an exact match of the snapshot, while .gitignore'd files (build
// output, node_modules) are left alone. DESTRUCTIVE. It first captures the current
// state into safetyRef so the revert can itself be undone, and returns that safety
// ref. A commit the agent made is not un-done (only the working tree is restored);
// the caller should surface that.
func Restore(dir string, ref, safetyRef Ref) (Ref, error) {
	if !IsRepo(dir) {
		return "", ErrNotGit
	}
	root, err := repoRoot(dir)
	if err != nil {
		return "", err
	}
	if !refExists(root, ref) {
		return "", ErrNoRef
	}
	// Snapshot the current state before we clobber it, so a revert is undoable.
	safety, err := Capture(root, safetyRef, "kunai pre-revert safety checkpoint")
	if err != nil {
		return "", fmt.Errorf("checkpoint: safety snapshot: %w", err)
	}
	// read-tree -u --reset: index + tracked working-tree files match the snapshot,
	// removing tracked files not in it. clean -df: remove untracked non-ignored
	// files the agent added (never ignored ones). reset HEAD: leave the working tree
	// as-is but put the index back on HEAD so the change shows as ordinary unstaged
	// edits, exactly how it looked when the checkpoint was taken.
	if _, err := git(root, "read-tree", "-u", "--reset", string(ref)); err != nil {
		return "", fmt.Errorf("checkpoint: read-tree: %w", err)
	}
	if _, err := git(root, "clean", "-df", "-q"); err != nil {
		return "", fmt.Errorf("checkpoint: clean: %w", err)
	}
	if _, err := git(root, "reset", "-q", "HEAD"); err != nil {
		return "", fmt.Errorf("checkpoint: reset: %w", err)
	}
	return safety, nil
}

// Exists reports whether a checkpoint ref is present.
func Exists(dir string, ref Ref) bool {
	root, err := repoRoot(dir)
	if err != nil {
		return false
	}
	return refExists(root, ref)
}

// Drop deletes a checkpoint ref (best-effort). The commit object is left for git's
// own GC; only the ref is removed.
func Drop(dir string, ref Ref) error {
	root, err := repoRoot(dir)
	if err != nil {
		return err
	}
	_, err = git(root, "update-ref", "-d", string(ref))
	return err
}

// --- git helpers -------------------------------------------------------------

func repoRoot(dir string) (string, error) {
	out, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", ErrNotGit
	}
	return strings.TrimSpace(out), nil
}

func headCommit(root string) (Ref, bool) {
	out, err := git(root, "rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		return "", false
	}
	c := strings.TrimSpace(out)
	if c == "" {
		return "", false
	}
	return Ref(c), true
}

func refExists(root string, ref Ref) bool {
	_, err := git(root, "rev-parse", "--verify", "-q", string(ref)+"^{commit}")
	return err == nil
}

func git(dir string, args ...string) (string, error) {
	return gitEnv(dir, nil, args...)
}

func gitEnv(dir string, extraEnv []string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
