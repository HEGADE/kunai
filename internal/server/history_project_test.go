package server

import (
	"os"
	"path/filepath"
	"testing"
)

// mkdirs makes each path under base and returns base.
func mkdirs(t *testing.T, base string, rel ...string) string {
	t.Helper()
	for _, r := range rel {
		if err := os.MkdirAll(filepath.Join(base, r), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return base
}

func TestProjectDirPrefersTheCheckoutCwdIsInside(t *testing.T) {
	base := mkdirs(t, t.TempDir(), "coding/kunai/.git", "coding/kunai/web")
	repo := filepath.Join(base, "coding", "kunai")
	cwd := filepath.Join(repo, "web")

	// No transcript needed, and the histogram must not get a say: cwd is already
	// inside a codebase, so that codebase is the answer.
	got := projectDir(cwd, map[string]int{filepath.Join(cwd, "src"): 99})
	if got != repo {
		t.Errorf("projectDir = %q, want %q", got, repo)
	}
}

func TestProjectDirRescuesASessionLaunchedFromAContainerFolder(t *testing.T) {
	base := mkdirs(t, t.TempDir(), "coding/hiring-god/.git", "coding/hiring-god/web", "coding/voxhail")
	container := filepath.Join(base, "coding")
	want := filepath.Join(container, "hiring-god")

	// Buckets are by immediate child, or the session below (which spent most of
	// its time in hiring-god/web) would be filed under a heading called "web".
	got := projectDir(container, map[string]int{
		container:                           40,
		filepath.Join(want, "web"):          30,
		want:                                12,
		filepath.Join(container, "voxhail"): 5,
	})
	if got != want {
		t.Errorf("projectDir = %q, want %q", got, want)
	}
}

func TestProjectDirKeepsAMultiCodebaseSessionUnderItsFolder(t *testing.T) {
	base := mkdirs(t, t.TempDir(), "coding/alpha", "coding/beta")
	container := filepath.Join(base, "coding")

	// A near tie is not noise to break: the session genuinely spanned two
	// codebases, so there is no single honest heading and it stays under its
	// folder, where naming it a workspace is the answer the user gives.
	got := projectDir(container, map[string]int{
		filepath.Join(container, "alpha"): 20,
		filepath.Join(container, "beta"):  19,
	})
	if got != "" {
		t.Errorf("projectDir = %q, want empty for a split session", got)
	}
}

func TestProjectDirIgnoresStrayAndUnusableCandidates(t *testing.T) {
	base := mkdirs(t, t.TempDir(), "coding/real", "coding/.cache")
	container := filepath.Join(base, "coding")

	cases := map[string]map[string]int{
		"a single stray cd": {filepath.Join(container, "real"): 2},
		"a dotfolder":       {filepath.Join(container, ".cache"): 50},
		"a deleted folder":  {filepath.Join(container, "gone"): 50},
		"nothing at all":    {container: 80},
	}
	for name, dirs := range cases {
		if got := projectDir(container, dirs); got != "" {
			t.Errorf("%s: projectDir = %q, want empty", name, got)
		}
	}
}

func TestProjectDirIsEmptyWithoutACwd(t *testing.T) {
	if got := projectDir("", map[string]int{"/anywhere": 9}); got != "" {
		t.Errorf("projectDir with no cwd = %q, want empty", got)
	}
}
