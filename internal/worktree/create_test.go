package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateGivesAnIndependentCheckout(t *testing.T) {
	r := newRepo(t)
	info := r.create("Fix Auth", "main")

	if info.Branch != "kunai/fix-auth" {
		t.Errorf("branch = %q, want kunai/fix-auth", info.Branch)
	}
	if info.Repo != r.dir {
		t.Errorf("repo = %q, want %q", info.Repo, r.dir)
	}
	if got := r.read(info.Path, "README.md"); got != "hello\n" {
		t.Errorf("worktree README = %q, want the base content", got)
	}

	// The point of the whole feature: an edit here is invisible over there.
	r.writeIn(info.Path, "README.md", "changed in the worktree\n")
	if got := r.read(r.dir, "README.md"); got != "hello\n" {
		t.Errorf("main checkout README = %q; the worktree leaked into it", got)
	}
	if out := r.git("status", "--porcelain"); out != "" {
		t.Errorf("main checkout is dirty after a worktree edit:\n%s", out)
	}

	// Separate HEADs, one object store.
	if got := strings.TrimSpace(r.gitIn(info.Path, "rev-parse", "--abbrev-ref", "HEAD")); got != info.Branch {
		t.Errorf("worktree HEAD = %q, want %q", got, info.Branch)
	}
	if got := strings.TrimSpace(r.git("rev-parse", "--abbrev-ref", "HEAD")); got != "main" {
		t.Errorf("main checkout HEAD = %q, want main", got)
	}
}

func TestCreateSuffixesATakenBranchName(t *testing.T) {
	r := newRepo(t)
	first := r.create("fix auth", "main")
	second := r.create("fix-auth", "main")

	if first.Branch != "kunai/fix-auth" {
		t.Fatalf("first branch = %q", first.Branch)
	}
	if second.Branch != "kunai/fix-auth-1" {
		t.Errorf("second branch = %q, want kunai/fix-auth-1", second.Branch)
	}
	if first.Path == second.Path {
		t.Errorf("both worktrees landed on %q", first.Path)
	}
	if _, err := os.Stat(second.Path); err != nil {
		t.Errorf("second worktree missing: %v", err)
	}
}

func TestCreateRecordsTheMergeBaseInGitConfig(t *testing.T) {
	r := newRepo(t)
	info := r.create("thing", "main")

	got := r.git("config", "--get", "branch."+info.Branch+".gh-merge-base")
	if got != "main" {
		t.Errorf("gh-merge-base = %q, want main", got)
	}
}

func TestCreatePrefersOriginWhenAsked(t *testing.T) {
	r := newRepo(t)
	r.addRemote()

	// Move the local branch on past the remote, so the two are distinguishable.
	r.write("local-only.txt", "x\n")
	r.commit(r.dir, "local only")
	localSHA := r.git("rev-parse", "main")
	originSHA := r.git("rev-parse", "origin/main")
	if localSHA == originSHA {
		t.Fatal("test setup: local and origin should have diverged")
	}

	fromOrigin, err := Create(CreateOptions{Repo: r.dir, Root: r.root, Name: "from-origin", Base: "main", FromOrigin: true})
	if err != nil {
		t.Fatal(err)
	}
	if fromOrigin.Base != "origin/main" {
		t.Errorf("base = %q, want origin/main", fromOrigin.Base)
	}
	if fromOrigin.BaseSHA != originSHA {
		t.Errorf("base sha = %q, want origin's %q", fromOrigin.BaseSHA, originSHA)
	}
	if _, err := os.Stat(filepath.Join(fromOrigin.Path, "local-only.txt")); err == nil {
		t.Error("worktree started from origin still has the local-only commit's file")
	}

	fromLocal, err := Create(CreateOptions{Repo: r.dir, Root: r.root, Name: "from-local", Base: "main", FromOrigin: false})
	if err != nil {
		t.Fatal(err)
	}
	if fromLocal.Base != "main" {
		t.Errorf("base = %q, want main", fromLocal.Base)
	}
	if fromLocal.BaseSHA != localSHA {
		t.Errorf("base sha = %q, want the local %q", fromLocal.BaseSHA, localSHA)
	}
}

// A branch with no origin counterpart must still work with FromOrigin set,
// rather than failing because the remote ref is missing.
func TestCreateFallsBackWhenOriginHasNoSuchBranch(t *testing.T) {
	r := newRepo(t)
	r.addRemote()
	r.git("branch", "local-feature")

	info, err := Create(CreateOptions{Repo: r.dir, Root: r.root, Name: "w", Base: "local-feature", FromOrigin: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if info.Base != "local-feature" {
		t.Errorf("base = %q, want the local branch", info.Base)
	}
}

func TestCreateRejectsAnUnknownBase(t *testing.T) {
	r := newRepo(t)
	_, err := Create(CreateOptions{Repo: r.dir, Root: r.root, Name: "w", Base: "no-such-branch"})
	if err == nil {
		t.Fatal("expected an error for a base that does not exist")
	}
	if !strings.Contains(err.Error(), "no-such-branch") {
		t.Errorf("error should name the missing base, got: %v", err)
	}
}

func TestCreateRefusesOutsideARepo(t *testing.T) {
	dir := t.TempDir()
	_, err := Create(CreateOptions{Repo: dir, Root: filepath.Join(dir, "wt"), Name: "w"})
	if err != ErrNotGit {
		t.Errorf("err = %v, want ErrNotGit", err)
	}
}

func TestRootAndListSeeEveryWorktree(t *testing.T) {
	r := newRepo(t)
	info := r.create("one", "main")

	// Root answers the same from either side, which is what lets every mutating
	// operation run in the main checkout without the caller tracking where it is.
	for _, from := range []string{r.dir, info.Path} {
		got, err := Root(from)
		if err != nil {
			t.Fatalf("Root(%s): %v", from, err)
		}
		if got != r.dir {
			t.Errorf("Root(%s) = %q, want %q", from, got, r.dir)
		}
	}

	entries, err := List(r.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d worktrees, want 2: %+v", len(entries), entries)
	}
	if !entries[0].Main || entries[0].Path != r.dir {
		t.Errorf("first entry should be the main checkout, got %+v", entries[0])
	}
	if entries[1].Main {
		t.Error("the linked worktree is marked as main")
	}

	kunaiOnly, err := Kunai(r.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(kunaiOnly) != 1 || kunaiOnly[0].Branch != info.Branch {
		t.Errorf("Kunai() = %+v, want just %s", kunaiOnly, info.Branch)
	}
}

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Fix Auth":             "fix-auth",
		"  spaced  out  ":      "spaced-out",
		"UPPER/slash":          "upper-slash",
		"weird!!@#chars":       "weird-chars",
		"trailing---":          "trailing",
		"":                     "work",
		"!!!":                  "work",
		"dots.and_underscores": "dots-and-underscores",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPathForFlattensTheBranch(t *testing.T) {
	got := PathFor("/data/worktrees", "/home/me/kunai", "kunai/fix-auth")
	want := filepath.Join("/data/worktrees", "kunai", "fix-auth")
	if got != want {
		t.Errorf("PathFor = %q, want %q", got, want)
	}
}
