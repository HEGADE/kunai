package project

import (
	"os"
	"path/filepath"
)

// Root reports the checkout a directory belongs to: the nearest ancestor,
// starting with the directory itself, that holds a .git.
//
// This is what makes a folder a codebase rather than merely a folder, and the
// sidebar groups sessions by codebase. Taking the directory a session happened
// to start in gave `kunai/web` a heading of its own, split from the repository
// it is part of, and gave the same treatment to every subdirectory anybody ever
// launched from.
//
// It answers the opposite question by returning "", which is the half that
// matters more here: ~/coding and ~ hold no .git, so they are containers, not
// projects, and a heading named after one is claiming something untrue. What to
// do about those is the caller's problem (see projectDir in the server), but
// knowing they are not projects starts here.
//
// .git is stat'd rather than required to be a directory, because a git worktree
// and a submodule both record it as a file.
func Root(dir string) string {
	if dir == "" {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	// Bounded so a pathological path cannot spin. The walk also stops on its own
	// at the filesystem root, which is the ordinary way out.
	for range 64 {
		if _, err := os.Lstat(filepath.Join(abs, ".git")); err == nil {
			return abs
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return ""
		}
		abs = parent
	}
	return ""
}
