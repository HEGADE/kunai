package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunSetupHandsTheCommandBothPaths(t *testing.T) {
	r := newRepo(t)
	info := r.create("w", "main")

	// The command proves for itself that it can reach the main checkout, which is
	// the entire reason the environment variables exist: it symlinks a file out of
	// it exactly as a real setup command would.
	r.write(".env", "SECRET=1\n")
	res, err := RunSetup(context.Background(), info,
		`echo "root=$KUNAI_PROJECT_ROOT" > seen.txt && echo "wt=$KUNAI_WORKTREE_PATH" >> seen.txt && `+
			`echo "cwd=$PWD" >> seen.txt && ln -sf "$KUNAI_PROJECT_ROOT/.env" .env`, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.State != SetupOK {
		t.Fatalf("state = %q, output:\n%s", res.State, res.Output)
	}

	seen := r.read(info.Path, "seen.txt")
	for _, want := range []string{"root=" + r.dir, "wt=" + info.Path, "cwd=" + info.Path} {
		if !strings.Contains(seen, want) {
			t.Errorf("setup environment missing %q, got:\n%s", want, seen)
		}
	}
	if got := r.read(info.Path, ".env"); got != "SECRET=1\n" {
		t.Errorf(".env = %q; the symlink into the main checkout did not work", got)
	}
}

func TestRunSetupReportsAFailureRatherThanErroring(t *testing.T) {
	r := newRepo(t)
	info := r.create("w", "main")

	res, err := RunSetup(context.Background(), info, "echo nope 1>&2; exit 3", 0)
	if err != nil {
		t.Fatalf("a failing command should not be an error here: %v", err)
	}
	if res.State != SetupFailed {
		t.Errorf("state = %q, want failed", res.State)
	}
	if res.ExitCode != 3 {
		t.Errorf("exit code = %d, want 3", res.ExitCode)
	}
	if !strings.Contains(res.Output, "nope") {
		t.Errorf("stderr was dropped; output = %q", res.Output)
	}
	if !res.Failed() {
		t.Error("Failed() should be true for a non-zero exit")
	}
}

// A timeout has to kill the work, not just stop waiting for it. `sh -c` forks
// rather than execs, so the process exec knows about is the shell and the real
// command is its child; without a process-group kill that child survives, which
// for a real setup command means an install still chewing through the machine
// long after kunai gave up on it.
func TestRunSetupTimesOutAndKillsWhatItStarted(t *testing.T) {
	r := newRepo(t)
	info := r.create("w", "main")

	// The child leaves evidence only if it lives long enough to. Asking whether
	// its pid is still alive would be wrong: a killed process is briefly a zombie,
	// and signal 0 succeeds against one, so that check races with reaping and
	// passes either way. Whether the work actually happened does not race.
	start := time.Now()
	res, err := RunSetup(context.Background(), info,
		`(sleep 1 && touch survived.txt) & wait`, 150*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if res.State != SetupTimedOut {
		t.Errorf("state = %q, want timed_out", res.State)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("waited %v; the timeout did not bite", elapsed)
	}
	if !strings.Contains(res.Output, "did not finish") {
		t.Errorf("the timeout should say so in the output, got %q", res.Output)
	}

	time.Sleep(1500 * time.Millisecond) // past when the child would have finished
	if _, err := os.Stat(filepath.Join(info.Path, "survived.txt")); err == nil {
		t.Error("the setup command's child outlived the timeout and kept working")
	}
}

func TestRunSetupWithNoCommandDoesNothing(t *testing.T) {
	r := newRepo(t)
	info := r.create("w", "main")

	res, err := RunSetup(context.Background(), info, "   ", 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.State != SetupNone {
		t.Errorf("state = %q, want none", res.State)
	}
}

func TestTailBufferKeepsTheEnd(t *testing.T) {
	b := &tailBuffer{limit: 32}
	for i := 0; i < 100; i++ {
		b.Write([]byte("line\n"))
	}
	got := b.String()
	if len(got) > 64 {
		t.Errorf("buffer grew to %d bytes despite the limit", len(got))
	}
	if !strings.Contains(got, "earlier output trimmed") {
		t.Error("a trimmed buffer should say it was trimmed")
	}
	if !strings.HasSuffix(strings.TrimSpace(got), "line") {
		t.Errorf("the tail was not kept: %q", got)
	}
}

// --- setup command resolution -------------------------------------------------

func TestProposeSetupPrefersTheProjectFile(t *testing.T) {
	r := newRepo(t)
	r.write(ProjectFile, `{"setup": "make bootstrap"}`)

	got := ProposeSetup(r.dir)
	if got.Source != SourceProject {
		t.Errorf("source = %q, want project", got.Source)
	}
	if got.Command != "make bootstrap" {
		t.Errorf("command = %q", got.Command)
	}
}

func TestProposeSetupIgnoresAnUnusableProjectFile(t *testing.T) {
	r := newRepo(t)
	for _, content := range []string{`not json`, `{}`, `{"setup": "   "}`} {
		r.write(ProjectFile, content)
		if got := ProposeSetup(r.dir); got.Source == SourceProject {
			t.Errorf("%q was accepted as a project setup", content)
		}
	}
}

func TestProposeSetupSuggestsFromTheLockfile(t *testing.T) {
	cases := []struct {
		marker string
		want   string
	}{
		{"pnpm-lock.yaml", "pnpm install --frozen-lockfile"},
		{"package-lock.json", "npm ci"},
		{"uv.lock", "uv sync"},
	}
	for _, tc := range cases {
		r := newRepo(t)
		r.write(tc.marker, "")
		got := ProposeSetup(r.dir)
		if got.Source != SourceSuggested {
			t.Errorf("%s: source = %q, want suggested", tc.marker, got.Source)
		}
		if !strings.Contains(got.Command, tc.want) {
			t.Errorf("%s: command = %q, want it to contain %q", tc.marker, got.Command, tc.want)
		}
		if !strings.Contains(got.Why, tc.marker) {
			t.Errorf("%s: why = %q, should name the evidence", tc.marker, got.Why)
		}
	}
}

// A suggestion only carries a file over when git ignores it. A tracked .env is
// already in the worktree, and linking over it would replace real content with a
// pointer at the main checkout.
func TestProposeSetupOnlyCarriesIgnoredFiles(t *testing.T) {
	r := newRepo(t)
	r.write("package-lock.json", "")
	r.write(".env", "SECRET=1\n")
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "track everything including .env")

	if got := ProposeSetup(r.dir); strings.Contains(got.Command, ".env") {
		t.Errorf("a tracked .env should not be carried: %q", got.Command)
	}

	r.write(".gitignore", ".env\n")
	r.git("rm", "-q", "--cached", ".env")
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "ignore .env")

	got := ProposeSetup(r.dir)
	if !strings.Contains(got.Command, ".env") {
		t.Errorf("an ignored .env should be carried: %q", got.Command)
	}
	if !strings.Contains(got.Command, EnvProjectRoot) {
		t.Errorf("the carry should link out of the main checkout: %q", got.Command)
	}
}

func TestProposeSetupSaysNothingWhenThereIsNothingToSay(t *testing.T) {
	r := newRepo(t)
	got := ProposeSetup(r.dir)
	if got.Source != SourceNone || got.Command != "" {
		t.Errorf("a bare repo suggested %+v", got)
	}
}

// --- shared paths -------------------------------------------------------------

func TestSharedPathsFindsLinksBackIntoTheMainCheckout(t *testing.T) {
	r := newRepo(t)
	info := r.create("w", "main")
	r.write(".env", "SECRET=1\n")
	r.writeIn(r.dir, "infra/relay/.env", "RELAY=1\n")

	link(t, filepath.Join(r.dir, ".env"), filepath.Join(info.Path, ".env"))
	link(t, filepath.Join(r.dir, "infra/relay/.env"), filepath.Join(info.Path, "infra/relay/.env"))
	// A link that stays inside the worktree is not shared and must not be listed.
	r.writeIn(info.Path, "real.txt", "x\n")
	link(t, filepath.Join(info.Path, "real.txt"), filepath.Join(info.Path, "alias.txt"))

	got := SharedPaths(info)
	if !contains(got, ".env") {
		t.Errorf("top-level link missing from %v", got)
	}
	if !contains(got, filepath.Join("infra", "relay", ".env")) {
		t.Errorf("nested link missing from %v", got)
	}
	if contains(got, "alias.txt") {
		t.Errorf("a worktree-internal link was reported as shared: %v", got)
	}
}

// A sibling directory whose name merely starts with the repo's must not count as
// inside it.
func TestSharedPathsDoesNotMatchASiblingPrefix(t *testing.T) {
	if within("/home/me/repo-backup/.env", "/home/me/repo") {
		t.Error("repo-backup was treated as inside repo")
	}
	if !within("/home/me/repo/.env", "/home/me/repo") {
		t.Error("a real child was not recognised")
	}
	if !within("/home/me/repo", "/home/me/repo") {
		t.Error("the directory itself should count as within")
	}
}

func link(t *testing.T, target, at string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
		t.Fatal(err)
	}
	os.Remove(at)
	if err := os.Symlink(target, at); err != nil {
		t.Fatal(err)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
