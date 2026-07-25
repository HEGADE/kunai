package worktree

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CreateOptions describe a worktree to be made.
type CreateOptions struct {
	// Repo is any directory inside the repository; the main checkout is resolved
	// from it.
	Repo string
	// Root is the directory worktrees are created under, usually
	// <dataDir>/worktrees. Each repository gets a subdirectory of its own.
	Root string
	// Name is what the user called this piece of work. It is slugged into a
	// branch under BranchPrefix, and suffixed if that branch is taken.
	Name string
	// Base is the branch to start from. Empty means the repository default.
	Base string
	// FromOrigin upgrades a local base to its origin counterpart when one
	// exists, so new work starts from what the team has rather than from a
	// possibly stale local ref. Callers should default this to true.
	FromOrigin bool
}

// Create makes a worktree and returns it.
//
// The steps are ordered so a failure never leaves a half-made worktree behind:
// everything that can be validated is validated before `git worktree add` runs,
// and the one step after it (recording the merge base) is advisory, so its
// failure is not worth undoing a good worktree for.
func Create(opts CreateOptions) (Info, error) {
	repo, err := Root(opts.Repo)
	if err != nil {
		return Info{}, err
	}
	if opts.Root == "" {
		return Info{}, fmt.Errorf("worktree: no root directory configured")
	}

	base, err := ResolveBase(repo, opts.Base, opts.FromOrigin)
	if err != nil {
		return Info{}, err
	}
	baseSHA, err := git(repo, "rev-parse", "--verify", base+"^{commit}")
	if err != nil {
		return Info{}, err
	}

	branch, err := AvailableBranch(repo, BranchFor(opts.Name))
	if err != nil {
		return Info{}, err
	}
	path := PathFor(opts.Root, repo, branch)
	if err := ensureFreePath(path); err != nil {
		return Info{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Info{}, fmt.Errorf("worktree: prepare %s: %w", filepath.Dir(path), err)
	}

	if _, err := git(repo, "worktree", "add", "-b", branch, path, base); err != nil {
		return Info{}, err
	}

	info := Info{Path: path, Repo: repo, Branch: branch, Base: base, BaseSHA: strings.TrimSpace(baseSHA)}
	recordMergeBase(repo, info)
	return info, nil
}

// recordMergeBase writes the base branch into git's own config for the branch,
// where `gh pr create` reads it. Storing it in git rather than a sidecar file
// means it survives kunai forgetting about the worktree, and a pull request
// opened from a terminal targets the same branch the app would have targeted.
//
// Advisory: a repository with a read-only config still gets a working worktree,
// so a failure is logged by the caller at most, never fatal.
func recordMergeBase(repo string, info Info) {
	_, _ = git(repo, "config", "branch."+info.Branch+".gh-merge-base", BaseBranchName(info.Base))
}

// ensureFreePath refuses a path that already holds something. An existing empty
// directory is fine (git will use it), which matters because removing a worktree
// can leave the directory behind on some filesystems.
func ensureFreePath(path string) error {
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("worktree: check %s: %w", path, err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("worktree: %s already exists and is not empty", path)
	}
	return nil
}
