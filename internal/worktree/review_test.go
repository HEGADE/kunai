package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A review checkout is detached on purpose: it reads somebody else's commit and
// will never be merged, so giving it a branch would leave a permanent entry in
// `git branch` for a review that lasts minutes.
func TestCreateReviewIsDetachedAndNotListedAsWork(t *testing.T) {
	r := newRepo(t)
	r.write("app.go", "package app\n")
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "second")
	head := r.git("rev-parse", "HEAD")

	info, err := CreateReview(ReviewOptions{Repo: r.dir, Root: r.root, Name: "128", SHA: head})
	if err != nil {
		t.Fatal(err)
	}
	if info.Branch != "" {
		t.Errorf("a review checkout took the branch %q; it must be detached", info.Branch)
	}
	if got := r.gitIn(info.Path, "rev-parse", "HEAD"); got != head {
		t.Errorf("checked out %s, want the reviewed commit %s", got, head)
	}
	if got := r.gitIn(info.Path, "rev-parse", "--abbrev-ref", "HEAD"); got != "HEAD" {
		t.Errorf("HEAD is on branch %q, want a detached HEAD", got)
	}
	// The file from the reviewed commit is really on disk: this is what makes the
	// agent able to read around the diff rather than only at it.
	if _, err := os.Stat(filepath.Join(info.Path, "app.go")); err != nil {
		t.Errorf("the reviewed commit's tree is not checked out: %v", err)
	}

	// Kunai() lists work in progress, and a review is not that.
	entries, err := Kunai(r.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Path == info.Path {
			t.Error("a review checkout is listed as a worktree holding work")
		}
	}
}

// Reviewing the same pull request twice is ordinary: a colleague pushes a fix and
// you look again. The second review must move the existing checkout to the new
// commit rather than failing because the first one left a directory behind.
func TestCreateReviewReusesTheCheckoutForANewCommit(t *testing.T) {
	r := newRepo(t)
	first := r.git("rev-parse", "HEAD")
	info, err := CreateReview(ReviewOptions{Repo: r.dir, Root: r.root, Name: "128", SHA: first})
	if err != nil {
		t.Fatal(err)
	}

	r.write("fix.go", "package fix\n")
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "the fix")
	second := r.git("rev-parse", "HEAD")

	again, err := CreateReview(ReviewOptions{Repo: r.dir, Root: r.root, Name: "128", SHA: second})
	if err != nil {
		t.Fatalf("re-reviewing the same pull request failed: %v", err)
	}
	if again.Path != info.Path {
		t.Errorf("second review used %s, want the same checkout %s", again.Path, info.Path)
	}
	if got := r.gitIn(again.Path, "rev-parse", "HEAD"); got != second {
		t.Errorf("checkout is at %s, want the new commit %s", got, second)
	}
}

// Removal leaves nothing behind, so a repository does not accumulate a full
// checkout per pull request anyone has ever reviewed.
func TestRemoveReviewCleansUp(t *testing.T) {
	r := newRepo(t)
	head := r.git("rev-parse", "HEAD")
	info, err := CreateReview(ReviewOptions{Repo: r.dir, Root: r.root, Name: "128", SHA: head})
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveReview(info); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(info.Path); !os.IsNotExist(err) {
		t.Errorf("the review checkout is still on disk: %v", err)
	}
	// And git no longer thinks it exists, so the next review of this PR is clean.
	if out := r.git("worktree", "list"); strings.Contains(out, info.Path) {
		t.Errorf("git still lists the removed checkout:\n%s", out)
	}
}

// A review is always of a specific commit. A moving ref would mean the code
// reviewed is not the code the findings get posted against.
func TestCreateReviewRequiresACommit(t *testing.T) {
	r := newRepo(t)
	if _, err := CreateReview(ReviewOptions{Repo: r.dir, Root: r.root, Name: "128"}); err == nil {
		t.Fatal("a review checkout with no commit was allowed")
	}
}

// The pull request number becomes one path segment, so a name that is not
// filesystem-safe cannot escape the worktree root.
func TestReviewSlugStaysOneSegment(t *testing.T) {
	for _, name := range []string{"../../etc", "128", "feature/thing", ""} {
		got := slug(name)
		if strings.ContainsAny(got, `/\`) || got == ".." || got == "." {
			t.Errorf("slug(%q) = %q, which is not a safe single segment", name, got)
		}
	}
}
