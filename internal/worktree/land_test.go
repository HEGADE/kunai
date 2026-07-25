package worktree

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestMergeFastForwardsWithoutTouchingTheCheckout(t *testing.T) {
	r := newRepo(t)
	info := r.create("feature", "main")
	r.writeIn(info.Path, "feature.txt", "work\n")
	r.commit(info.Path, "add feature")

	// The main checkout is deliberately left on another branch AND dirty. A
	// fast-forward moves a ref, so neither should stand in the way.
	r.git("checkout", "-q", "-b", "elsewhere")
	r.write("scratch.txt", "uncommitted\n")

	res, err := Merge(info)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !res.FastForward {
		t.Error("expected a fast-forward")
	}
	if res.Commits != 1 {
		t.Errorf("commits = %d, want 1", res.Commits)
	}
	if got := r.git("log", "-1", "--format=%s", "main"); got != "add feature" {
		t.Errorf("main tip = %q, want the merged commit", got)
	}
	if got := r.git("rev-parse", "--abbrev-ref", "HEAD"); got != "elsewhere" {
		t.Errorf("the merge moved the checkout to %q", got)
	}
	if _, err := os.Stat(r.dir + "/scratch.txt"); err != nil {
		t.Error("the merge disturbed the uncommitted file in the main checkout")
	}
}

func TestMergeCreatesAMergeCommitWhenTheBaseHasMovedOn(t *testing.T) {
	r := newRepo(t)
	info := r.create("feature", "main")
	r.writeIn(info.Path, "feature.txt", "work\n")
	r.commit(info.Path, "add feature")

	// Diverge: main gains a commit the branch does not have, so no fast-forward.
	r.write("other.txt", "meanwhile\n")
	r.commit(r.dir, "meanwhile on main")

	res, err := Merge(info)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if res.FastForward {
		t.Error("a diverged history should not report a fast-forward")
	}
	if got := r.read(r.dir, "feature.txt"); got != "work\n" {
		t.Errorf("feature.txt = %q, the merge did not bring the work over", got)
	}
	if got := r.read(r.dir, "other.txt"); got != "meanwhile\n" {
		t.Errorf("other.txt = %q, the merge lost main's own commit", got)
	}
}

func TestMergeRefusesADirtyMainCheckout(t *testing.T) {
	r := newRepo(t)
	info := r.create("feature", "main")
	r.writeIn(info.Path, "feature.txt", "work\n")
	r.commit(info.Path, "add feature")
	r.write("other.txt", "meanwhile\n")
	r.commit(r.dir, "meanwhile") // forces a real merge, not a fast-forward

	r.write("README.md", "uncommitted edit\n")

	_, err := Merge(info)
	if !errors.Is(err, ErrDirtyRepo) {
		t.Fatalf("err = %v, want ErrDirtyRepo", err)
	}
	// Refusing means refusing: nothing moved, and the edit is still there.
	if got := r.read(r.dir, "README.md"); got != "uncommitted edit\n" {
		t.Errorf("the refused merge stashed or clobbered the working tree: %q", got)
	}
}

func TestMergeRefusesWhenTheCheckoutIsOnAnotherBranch(t *testing.T) {
	r := newRepo(t)
	info := r.create("feature", "main")
	r.writeIn(info.Path, "feature.txt", "work\n")
	r.commit(info.Path, "add feature")
	r.write("other.txt", "meanwhile\n")
	r.commit(r.dir, "meanwhile") // no fast-forward available

	r.git("checkout", "-q", "-b", "elsewhere")

	_, err := Merge(info)
	if !errors.Is(err, ErrNotOnBase) {
		t.Fatalf("err = %v, want ErrNotOnBase", err)
	}
	if !strings.Contains(err.Error(), "elsewhere") {
		t.Errorf("the error should name where the checkout actually is: %v", err)
	}
}

func TestMergeReportsAConflictAsAConflict(t *testing.T) {
	r := newRepo(t)
	info := r.create("feature", "main")
	r.writeIn(info.Path, "README.md", "worktree version\n")
	r.commit(info.Path, "worktree edit")

	r.write("README.md", "main version\n")
	r.commit(r.dir, "main edit")

	_, err := Merge(info)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	// The conflict is left in place for a human, not cleaned up behind their back.
	if !isConflict(r.dir) {
		t.Error("the conflict was abandoned rather than left for the user to resolve")
	}
}

func TestMergeSaysNothingToDoRatherThanFailing(t *testing.T) {
	r := newRepo(t)
	info := r.create("feature", "main")

	res, err := Merge(info)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !res.AlreadyMerged {
		t.Error("a branch with no commits should report AlreadyMerged")
	}
}

func TestRemoveRefusesUncommittedWorkUnlessForced(t *testing.T) {
	r := newRepo(t)
	info := r.create("feature", "main")
	r.writeIn(info.Path, "scratch.txt", "unsaved\n")

	if err := Remove(info, false, true); err == nil {
		t.Fatal("expected a refusal for a worktree with uncommitted changes")
	}
	if _, err := os.Stat(info.Path); err != nil {
		t.Fatal("the refused removal deleted the worktree anyway")
	}

	if err := Remove(info, true, true); err != nil {
		t.Fatalf("forced removal: %v", err)
	}
	if _, err := os.Stat(info.Path); !os.IsNotExist(err) {
		t.Error("the worktree directory is still there after removal")
	}
	if branchExists(r.dir, info.Branch) {
		t.Error("the branch survived a removal that asked for it to go")
	}
}

func TestRemoveCanKeepTheBranch(t *testing.T) {
	r := newRepo(t)
	info := r.create("feature", "main")
	r.writeIn(info.Path, "feature.txt", "work\n")
	r.commit(info.Path, "work")

	if err := Remove(info, false, false); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !branchExists(r.dir, info.Branch) {
		t.Error("the branch was deleted although the caller asked to keep it")
	}
}

func TestUnmergedCommitsCountsWhatWouldBeLost(t *testing.T) {
	r := newRepo(t)
	info := r.create("feature", "main")
	if got := UnmergedCommits(info); got != 0 {
		t.Errorf("fresh worktree has %d unmerged commits, want 0", got)
	}

	r.writeIn(info.Path, "a.txt", "1\n")
	r.commit(info.Path, "one")
	r.writeIn(info.Path, "b.txt", "2\n")
	r.commit(info.Path, "two")

	if got := UnmergedCommits(info); got != 2 {
		t.Errorf("UnmergedCommits = %d, want 2", got)
	}
}
