package server

// Putting a finished review's checkout back.
//
// This is the half of reopening that can actually go wrong, because it is git:
// the session, the seed and the account are ordinary plumbing that every other
// resume already exercises. Against a real repository in a temp dir, the way
// internal/worktree's own tests do, since a fake git would only prove the fake
// matches what was assumed.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hegade/kunai/internal/worktree"
)

func reopenRepo(t *testing.T) (dir string, sha string, root string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	base := t.TempDir()
	dir = filepath.Join(base, "repo")
	root = filepath.Join(base, "worktrees")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@localhost",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@localhost",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	run("config", "user.name", "test")
	run("config", "user.email", "test@localhost")
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "initial")
	return dir, run("rev-parse", "HEAD"), root
}

// A swept checkout is made again at the commit that was READ, which is what
// keeps the conversation being resumed about the same code it was about.
func TestAReviewCheckoutIsMadeAgainAtTheCommitItRead(t *testing.T) {
	repo, sha, root := reopenRepo(t)
	s := &Server{worktrees: &worktreeStore{root: root}}

	dir, err := s.reviewWorktree(prReview{Number: 6, RepoDir: repo, HeadSHA: sha, Worktree: "/gone"})
	if err != nil {
		t.Fatalf("reviewWorktree() = %v", err)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Fatalf("checkout %q is not there: %v", dir, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.go")); err != nil {
		t.Errorf("the checkout does not carry the commit's files: %v", err)
	}
	// The path has to be the one the review ran in, or the CLI cannot find the
	// transcript to resume: a conversation lives in a folder named after the
	// directory it happened in.
	if want := filepath.Join(root, filepath.Base(repo), "review", "6"); dir != want {
		t.Errorf("checkout is at %q, want the review's own deterministic path %q", dir, want)
	}
	_ = worktree.RemoveReview(worktree.Info{Path: dir, Repo: repo})
}

// A checkout still on disk is used as it is. Making it again would fail (git
// refuses to add a worktree over one that exists) and there is nothing to gain.
func TestAnIntactCheckoutIsReusedRatherThanRemade(t *testing.T) {
	repo, sha, root := reopenRepo(t)
	s := &Server{worktrees: &worktreeStore{root: root}}

	first, err := s.reviewWorktree(prReview{Number: 6, RepoDir: repo, HeadSHA: sha})
	if err != nil {
		t.Fatalf("reviewWorktree() = %v", err)
	}
	again, err := s.reviewWorktree(prReview{Number: 6, RepoDir: repo, HeadSHA: sha, Worktree: first})
	if err != nil {
		t.Fatalf("reviewWorktree() on an intact checkout = %v", err)
	}
	if again != first {
		t.Errorf("second call moved the checkout: %q -> %q", first, again)
	}
	_ = worktree.RemoveReview(worktree.Info{Path: first, Repo: repo})
}

// A review that cannot say where it read, or what it read, says so plainly
// rather than failing later as a git error nobody can act on. The earliest
// records have no RepoDir at all.
func TestAReviewThatCannotBeReopenedSaysWhy(t *testing.T) {
	s := &Server{worktrees: &worktreeStore{root: t.TempDir()}}
	if _, err := s.reviewWorktree(prReview{Number: 4, HeadSHA: "abc"}); err == nil {
		t.Error("a review with no repository was reopened anyway")
	}
	if _, err := s.reviewWorktree(prReview{Number: 4, RepoDir: "/tmp"}); err == nil {
		t.Error("a review with no commit was reopened anyway")
	}
}

// A review is a conversation kunai asked for, and Recent has to show it.
//
// Every prompt driving a review is wrapped in <kunai-review>, which looked
// exactly like the CLI's own <system_instruction> boilerplate to the rule that
// decides whether anybody ever asked this session anything. So no review has
// ever appeared in Recent -- on the one screen somebody goes to looking for a
// finished one. A loop's iterations were hidden the same way.
func TestKunaisOwnWrappersCountAsSomebodyAsking(t *testing.T) {
	if !ourWrapper("<kunai-review>\nYou are about to review a pull request") {
		t.Error("a review's own prompt is not recognised as an ask")
	}
	if !ourWrapper(`<loop-iteration n="3" of="50">do the thing`) {
		t.Error("a loop's own prompt is not recognised as an ask")
	}
	// And the rule it lives inside still holds: the CLI's boilerplate is not a
	// conversation.
	if ourWrapper("<system_instruction>you are a helpful assistant") {
		t.Error("the CLI's own system wrapper was taken for somebody asking")
	}
	if ourWrapper("<command-name>/compact</command-name>") {
		t.Error("a slash-command wrapper was taken for somebody asking")
	}
}
