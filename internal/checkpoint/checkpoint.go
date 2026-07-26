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

// Snapshot is one captured turn checkpoint: the turn's Seq, its ref, and the git
// commit time of the snapshot (unix seconds).
type Snapshot struct {
	Seq        uint64
	Ref        Ref
	CapturedAt int64
}

// List returns every turn checkpoint for a session, read straight from the git
// shadow refs — so it survives a kunai restart, since git, not an in-memory map, is
// the store. Safety refs (refs/.../safety/...) are excluded; only the per-turn
// snapshots are returned, ordered by Seq. A non-git dir or a session with none
// returns an empty slice, never an error the caller must handle.
// Change is one path a restore would alter, with git's status letter for it:
// M modified, A added since the snapshot (so the restore deletes it), D deleted
// since (so the restore brings it back).
type Change struct {
	Status string `json:"status"`
	Path   string `json:"path"`
}

// Preview reports exactly what Restore would do, which is the only honest basis
// for asking whether to do it.
//
// This exists because Restore is a whole-REPOSITORY operation, not a per-turn
// one: `read-tree -u --reset` makes every tracked file match the snapshot and
// deletes tracked files absent from it, and `clean -df` removes every untracked
// non-ignored file. So reverting one turn also discards every later turn's edits,
// anything you changed in your editor since, and any new file anywhere in the
// repo. A confirmation naming only the turn's own files would understate that,
// and understating it is how someone loses work they did not know was at stake.
//
// changed is the tracked difference between the snapshot and the working tree;
// removed is what clean would delete, asked of git rather than inferred.
func Preview(dir string, ref Ref) (changed []Change, removed []string, err error) {
	if !IsRepo(dir) {
		return nil, nil, ErrNotGit
	}
	root, err := repoRoot(dir)
	if err != nil {
		return nil, nil, err
	}
	if !refExists(root, ref) {
		return nil, nil, ErrNoRef
	}
	// Non-nil so a caller marshalling this gets [] rather than null, which is how a
	// client ends up calling .length on nothing.
	changed, removed = []Change{}, []string{}

	out, err := git(root, "diff", "--name-status", string(ref))
	if err != nil {
		return nil, nil, fmt.Errorf("checkpoint: diff: %w", err)
	}
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		// "M\tpath" — a rename reports two paths; the destination is the one that
		// matters to someone deciding, so take the last field.
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			continue
		}
		changed = append(changed, Change{Status: fields[0][:1], Path: fields[len(fields)-1]})
	}

	// -nd is exactly what `clean -df` would remove, reported instead of guessed.
	out, err = git(root, "clean", "-nd")
	if err != nil {
		return nil, nil, fmt.Errorf("checkpoint: clean preview: %w", err)
	}
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		removed = append(removed, strings.TrimPrefix(line, "Would remove "))
	}
	return changed, removed, nil
}

func List(dir, sessionID string) []Snapshot {
	root, err := repoRoot(dir)
	if err != nil {
		return nil
	}
	prefix := RefPrefix + sanitize(sessionID) + "/"
	out, err := git(root, "for-each-ref", "--format=%(refname) %(creatordate:unix)", prefix)
	if err != nil {
		return nil
	}
	var snaps []Snapshot
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 1 || !strings.HasPrefix(fields[0], prefix) {
			continue
		}
		tail := strings.TrimPrefix(fields[0], prefix)
		if strings.Contains(tail, "/") { // a nested ref (shouldn't happen for turns); skip
			continue
		}
		var seq uint64
		if _, err := fmt.Sscanf(tail, "%d", &seq); err != nil {
			continue
		}
		var at int64
		if len(fields) > 1 {
			fmt.Sscanf(fields[1], "%d", &at)
		}
		snaps = append(snaps, Snapshot{Seq: seq, Ref: Ref(fields[0]), CapturedAt: at})
	}
	// Order by Seq so the client sees turns in conversation order.
	for i := 1; i < len(snaps); i++ {
		for j := i; j > 0 && snaps[j-1].Seq > snaps[j].Seq; j-- {
			snaps[j-1], snaps[j] = snaps[j], snaps[j-1]
		}
	}
	return snaps
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
	// commit-tree needs an author/committer identity, and the user's repo may have
	// none configured (fresh CI, a repo without `git config user.*`). A fixed kunai
	// identity keeps checkpoints working everywhere; it only ever tags shadow-ref
	// commits the user never sees, so it does not touch their own commit identity.
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=kunai", "GIT_AUTHOR_EMAIL=kunai@localhost",
		"GIT_COMMITTER_NAME=kunai", "GIT_COMMITTER_EMAIL=kunai@localhost",
	)
	cmd.Env = append(env, extraEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
