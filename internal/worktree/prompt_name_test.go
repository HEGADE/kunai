package worktree

import (
	"strings"
	"testing"
)

// The point is that you never name a branch: the thing you asked for is the name.
// These are the shapes people actually type.
func TestNameFromPrompt(t *testing.T) {
	cases := []struct{ in, want string }{
		{"fix the login redirect loop", "fix-login-redirect-loop"},
		{"Fix the login redirect loop.", "fix-login-redirect-loop"},
		{"add a test for the transcript parser", "add-test-transcript-parser"},

		// The throat-clearing a prompt opens with is not the work.
		{"please fix the login", "fix-login"},
		{"can you add a retry to the poller", "add-retry-poller"},
		{"could you please refactor the session store", "refactor-session-store"},
		{"I want you to remove the old review endpoint", "remove-old-review-endpoint"},
		{"let's rewrite the scheduler", "rewrite-scheduler"},

		// Only the opening sentence names it; the rest is elaboration.
		{"Fix the flaky test.\n\nIt fails on CI about one run in five, always in\nthe worktree suite.", "fix-flaky-test"},
		{"add caching\nand then also do the other thing", "add-caching"},

		// Punctuation, symbols and paths still produce a legal ref.
		{"fix internal/server/spa.go theme colour", "fix-internal-server-spa-go-theme-colour"},
		{"why is `safeLinks()` dropping my links?", "why-safelinks-dropping-links"},

		// Nothing usable.
		{"", ""},
		{"   \n  ", ""},
		{"!!! ???", ""},
	}
	for _, c := range cases {
		if got := NameFromPrompt(c.in); got != c.want {
			t.Errorf("NameFromPrompt(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

// A prompt made entirely of filler still has to name itself something, because a
// poor name beats a placeholder that says nothing at all.
func TestNameFromPromptFallsBackRatherThanGivingUp(t *testing.T) {
	got := NameFromPrompt("can you do it for me")
	if got == "" {
		t.Fatal("an all-filler prompt produced no name")
	}
	if strings.Contains(got, " ") {
		t.Errorf("not a branch fragment: %q", got)
	}
}

// A name is also a directory, so it has to stay readable at a glance.
func TestNameFromPromptStaysShortAndWhole(t *testing.T) {
	long := "refactor the entire transcript seeding pipeline including the reverse scroll cursor and the compaction overhead measurement"
	got := NameFromPrompt(long)

	if len([]rune(got)) > maxNameRunes {
		t.Errorf("name is %d runes, over the cap: %q", len([]rune(got)), got)
	}
	if strings.HasSuffix(got, "-") || strings.HasPrefix(got, "-") {
		t.Errorf("name has a dangling separator: %q", got)
	}
	// Cut back to a whole word rather than mid-syllable.
	if strings.Count(got, "-") > 0 {
		last := got[strings.LastIndexByte(got, '-')+1:]
		if !strings.Contains(long, last) {
			t.Errorf("the last word %q is not one the user wrote: %q", last, got)
		}
	}
}

// Every name has to survive being turned into a ref and a path.
func TestEveryNameIsALegalBranch(t *testing.T) {
	prompts := []string{
		"fix the login", "add ../../etc/passwd handling", "why?!", "日本語のテスト",
		"deal with a .lock file", "-- dashes -- everywhere --", "UPPER CASE SHOUTING",
	}
	for _, p := range prompts {
		name := NameFromPrompt(p)
		if name == "" {
			continue
		}
		branch := BranchFor(name)
		if strings.Contains(branch, "..") || strings.HasSuffix(branch, ".lock") {
			t.Errorf("%q produced an illegal ref: %q", p, branch)
		}
		if strings.ContainsAny(branch, " ~^:?*[\\") {
			t.Errorf("%q produced a ref git would reject: %q", p, branch)
		}
	}
}

// A placeholder has to be recognisable, or it would never be replaced; and a
// real name must never be mistaken for one, or it would be replaced.
func TestIsPlaceholder(t *testing.T) {
	for _, b := range []string{"kunai/work", "kunai/work-2", "kunai/work-17"} {
		if !IsPlaceholder(b) {
			t.Errorf("%q should be a placeholder", b)
		}
	}
	for _, b := range []string{
		"kunai/work-in-progress", "kunai/workflow", "kunai/fix-login",
		"kunai/rework", "work", "main", "",
	} {
		if IsPlaceholder(b) {
			t.Errorf("%q is a real name and must not be replaced", b)
		}
	}
	// And the placeholder we actually create is one.
	if !IsPlaceholder(BranchFor(PlaceholderName())) {
		t.Error("the name we create placeholders with is not recognised as one")
	}
}

func TestRenameReplacesThePlaceholder(t *testing.T) {
	r := newRepo(t)
	info := r.create(PlaceholderName(), "main")
	if !IsPlaceholder(info.Branch) {
		t.Fatalf("setup: %q is not a placeholder", info.Branch)
	}
	r.writeIn(info.Path, "a.txt", "work\n")
	r.commit(info.Path, "some work")

	renamed, err := Rename(info, "fix login")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Branch != "kunai/fix-login" {
		t.Errorf("branch = %q", renamed.Branch)
	}
	if branchExists(r.dir, info.Branch) {
		t.Error("the placeholder branch is still there")
	}
	// The work follows the branch, and the directory deliberately does not move:
	// a live session is running with it as its cwd.
	if renamed.Path != info.Path {
		t.Errorf("the directory moved to %q; a running session's cwd would be gone", renamed.Path)
	}
	if got := r.gitIn(info.Path, "rev-parse", "--abbrev-ref", "HEAD"); got != renamed.Branch {
		t.Errorf("the worktree is on %q, not the renamed branch", got)
	}
	if got := r.gitIn(info.Path, "log", "-1", "--format=%s"); got != "some work" {
		t.Errorf("the commits did not follow the rename: %q", got)
	}
	// The merge base is stored per branch, so it has to be re-recorded.
	if got := r.git("config", "--get", "branch."+renamed.Branch+".gh-merge-base"); got != "main" {
		t.Errorf("gh-merge-base after rename = %q, want main", got)
	}
}

func TestRenameAvoidsACollision(t *testing.T) {
	r := newRepo(t)
	taken := r.create("fix login", "main")
	placeholder := r.create(PlaceholderName(), "main")

	renamed, err := Rename(placeholder, "fix login")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Branch == taken.Branch {
		t.Fatalf("the rename collided with an existing branch: %q", renamed.Branch)
	}
	if renamed.Branch != "kunai/fix-login-2" {
		t.Errorf("branch = %q, want kunai/fix-login-2", renamed.Branch)
	}
}
