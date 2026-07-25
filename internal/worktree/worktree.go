// Package worktree drives git worktrees so several agents can work on one
// repository at once without touching each other's files.
//
// A worktree is a second checkout of the same repository on its own branch,
// sharing one object store. Giving a session a worktree as its cwd is the whole
// isolation mechanism: nothing else in kunai has to change, because a worktree
// session is just a session with a different working directory.
//
// The package is deliberately git-only. It shells out, parses porcelain, and
// knows nothing about sessions, HTTP or the data directory, so it can be
// exercised against real temporary repositories the way internal/checkpoint is.
//
// What is NOT here, on purpose: nothing copies or clones files into a new
// worktree. A fresh worktree has no node_modules, no .env and no build cache, and
// the answer to that is a per-repo setup command (see setup.go) rather than a
// carry-over list, because a shell command handles every package manager without
// this package having to know any of them.
package worktree

import (
	"path/filepath"
	"strings"
)

// Info describes one worktree kunai created.
type Info struct {
	// Path is the worktree's checkout, and is what a session runs in.
	Path string `json:"path"`
	// Repo is the main checkout this worktree belongs to.
	Repo string `json:"repo"`
	// Branch is the branch checked out here, always under BranchPrefix.
	Branch string `json:"branch"`
	// Base is the ref the branch was created from, as the user chose it
	// ("main", or "origin/main" when started from the remote).
	Base string `json:"base"`
	// BaseSHA pins what Base pointed at when the worktree was created, so a
	// later diff can be honest about where the work actually diverged.
	BaseSHA string `json:"base_sha"`
}

// Entry is a worktree as git reports it, including ones kunai did not create.
type Entry struct {
	Path   string `json:"path"`
	Branch string `json:"branch"` // empty for a detached HEAD
	Head   string `json:"head"`
	// Main is true for the repository's original checkout, which is never
	// removable and is where merges happen.
	Main bool `json:"main"`
	// Locked and Prunable mirror git's own flags, so the UI can explain why a
	// removal would be refused instead of just failing.
	Locked   bool `json:"locked,omitempty"`
	Prunable bool `json:"prunable,omitempty"`
}

// BranchPrefix namespaces every branch kunai creates, so a worktree branch is
// recognisable in `git branch` and can never be confused with one you made.
const BranchPrefix = "kunai/"

// Root returns the main checkout of the repository containing dir. Every
// mutating operation runs there rather than in a linked worktree: git allows
// most of them from either, but "which repo is this" is a question with one
// answer and it is better asked once.
func Root(dir string) (string, error) {
	entries, err := List(dir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.Main {
			return e.Path, nil
		}
	}
	return "", ErrNotGit
}

// List returns every worktree of the repository containing dir, main checkout
// first. It parses `git worktree list --porcelain`, whose records are separated
// by blank lines and whose first record is always the main checkout.
func List(dir string) ([]Entry, error) {
	out, err := git(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, ErrNotGit
	}
	var (
		entries []Entry
		cur     *Entry
	)
	flush := func() {
		if cur != nil {
			cur.Main = len(entries) == 0
			entries = append(entries, *cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			flush()
			continue
		}
		key, value, _ := strings.Cut(line, " ")
		switch key {
		case "worktree":
			flush()
			cur = &Entry{Path: value}
		case "HEAD":
			if cur != nil {
				cur.Head = value
			}
		case "branch":
			if cur != nil {
				cur.Branch = strings.TrimPrefix(value, "refs/heads/")
			}
		case "locked":
			if cur != nil {
				cur.Locked = true
			}
		case "prunable":
			if cur != nil {
				cur.Prunable = true
			}
		}
	}
	flush()
	if len(entries) == 0 {
		return nil, ErrNotGit
	}
	return entries, nil
}

// Kunai returns only the worktrees on a kunai branch, which is how the app tells
// the ones it manages from a worktree the user made in a terminal. The user's own
// worktrees are listed but never removed or merged by kunai.
func Kunai(dir string) ([]Entry, error) {
	all, err := List(dir)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, e := range all {
		if !e.Main && strings.HasPrefix(e.Branch, BranchPrefix) {
			out = append(out, e)
		}
	}
	return out, nil
}

// Prune drops git's records of worktrees whose directories have been deleted
// from under it. Safe to call at any time; it never touches a live worktree.
func Prune(repo string) error {
	_, err := git(repo, "worktree", "prune")
	return err
}

// RepoName is the repository as a person would say it: the last segment of its
// main checkout path. Used to group worktrees on disk under one directory.
func RepoName(repo string) string {
	return filepath.Base(strings.TrimRight(filepath.Clean(repo), string(filepath.Separator)))
}
