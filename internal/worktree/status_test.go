package worktree

import (
	"testing"
)

func TestStatusOfAFreshWorktreeIsEmpty(t *testing.T) {
	r := newRepo(t)
	info := r.create("w", "main")

	st, err := StatusOf(info)
	if err != nil {
		t.Fatal(err)
	}
	if st.Ahead != 0 || st.Behind != 0 || st.Dirty != 0 || len(st.Files) != 0 {
		t.Errorf("fresh worktree = %+v, want all zero", st)
	}
	if st.BaseMoved {
		t.Error("base reported as moved on a fresh worktree")
	}
}

func TestStatusCountsCommittedAndUncommittedTogether(t *testing.T) {
	r := newRepo(t)
	info := r.create("w", "main")

	r.writeIn(info.Path, "committed.txt", "done\n")
	r.commit(info.Path, "one")
	r.writeIn(info.Path, "loose.txt", "in progress\n")

	st, err := StatusOf(info)
	if err != nil {
		t.Fatal(err)
	}
	if st.Ahead != 1 {
		t.Errorf("ahead = %d, want 1", st.Ahead)
	}
	if st.Dirty != 1 {
		t.Errorf("dirty = %d, want 1", st.Dirty)
	}
	// A card asks "what has this changed", and the answer spans both.
	if !contains(st.Files, "committed.txt") {
		t.Errorf("committed file missing from %v", st.Files)
	}
	if !contains(st.Files, "loose.txt") {
		t.Errorf("uncommitted file missing from %v", st.Files)
	}
}

// Commits made on the base after the worktree branched must not read as the
// worktree being "behind" its own starting point, and must not appear as its
// changes. Two dots instead of three here would get both wrong.
func TestStatusMeasuresFromWhereTheyDiverged(t *testing.T) {
	r := newRepo(t)
	info := r.create("w", "main")

	r.writeIn(info.Path, "mine.txt", "mine\n")
	r.commit(info.Path, "my work")

	r.write("theirs.txt", "theirs\n")
	r.commit(r.dir, "someone else's work")

	st, err := StatusOf(info)
	if err != nil {
		t.Fatal(err)
	}
	if st.Ahead != 1 {
		t.Errorf("ahead = %d, want 1", st.Ahead)
	}
	if st.Behind != 1 {
		t.Errorf("behind = %d, want 1", st.Behind)
	}
	if contains(st.Files, "theirs.txt") {
		t.Errorf("the base's own commit was reported as this worktree's change: %v", st.Files)
	}
	if !st.BaseMoved {
		t.Error("the base moved on and BaseMoved did not say so")
	}
}

func TestStatusReportsARenamesNewName(t *testing.T) {
	r := newRepo(t)
	info := r.create("w", "main")
	r.gitIn(info.Path, "mv", "README.md", "READYOU.md")

	st, err := StatusOf(info)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(st.Files, "READYOU.md") {
		t.Errorf("files = %v, want the new name", st.Files)
	}
}
