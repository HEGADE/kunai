package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRootFindsTheCheckoutADirectoryIsPartOf(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "coding", "kunai")
	deep := filepath.Join(repo, "web", "src")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	// A subdirectory belongs to the repository above it: this is what stops
	// `kunai/web` becoming a heading of its own, split from `kunai`.
	if got := Root(deep); got != repo {
		t.Errorf("Root(%s) = %q, want %q", deep, got, repo)
	}
	if got := Root(repo); got != repo {
		t.Errorf("Root at the root itself = %q, want %q", got, repo)
	}
}

func TestRootReportsNothingForAContainerFolder(t *testing.T) {
	base := t.TempDir()
	container := filepath.Join(base, "coding")
	if err := os.MkdirAll(filepath.Join(container, "kunai", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The folder that HOLDS codebases is not one. Answering otherwise is what
	// produced a sidebar heading called "coding".
	if got := Root(container); got != "" {
		t.Errorf("Root(%s) = %q, want empty", container, got)
	}
}

func TestRootAcceptsAGitFile(t *testing.T) {
	// A git worktree and a submodule both record .git as a FILE, so requiring a
	// directory would miss every one of them.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Root(dir); got != dir {
		t.Errorf("Root with a .git file = %q, want %q", got, dir)
	}
}
