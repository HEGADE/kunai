package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Every test here runs against a real git repository in a temp dir, the way
// internal/checkpoint's do. The whole package is git's behaviour, so a fake git
// would only prove that the fake matches what we assumed.

type repo struct {
	t    *testing.T
	dir  string
	root string // where worktrees are created
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	base := t.TempDir()
	r := &repo{t: t, dir: filepath.Join(base, "repo"), root: filepath.Join(base, "worktrees")}
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r.git("init", "-q", "-b", "main")
	r.git("config", "user.name", "test")
	r.git("config", "user.email", "test@localhost")
	r.write("README.md", "hello\n")
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "initial")
	return r
}

// git runs a command in the main checkout and fails the test if it errors.
func (r *repo) git(args ...string) string {
	r.t.Helper()
	return r.gitIn(r.dir, args...)
}

func (r *repo) gitIn(dir string, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@localhost",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@localhost",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *repo) write(name, content string) {
	r.t.Helper()
	r.writeIn(r.dir, name, content)
}

func (r *repo) writeIn(dir, name, content string) {
	r.t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *repo) read(dir, name string) string {
	r.t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		r.t.Fatal(err)
	}
	return string(b)
}

// commit stages everything in dir and commits it.
func (r *repo) commit(dir, message string) {
	r.t.Helper()
	r.gitIn(dir, "add", "-A")
	r.gitIn(dir, "commit", "-q", "-m", message)
}

// create makes a worktree with the package under test, failing on error.
func (r *repo) create(name, base string) Info {
	r.t.Helper()
	info, err := Create(CreateOptions{Repo: r.dir, Root: r.root, Name: name, Base: base})
	if err != nil {
		r.t.Fatalf("create %q: %v", name, err)
	}
	return info
}

// addRemote gives the repo an "origin" pointing at a second real repository, so
// the origin-preferring paths can be exercised without a network.
func (r *repo) addRemote() string {
	r.t.Helper()
	remote := filepath.Join(r.t.TempDir(), "origin.git")
	r.gitIn(filepath.Dir(remote), "init", "-q", "--bare", "-b", "main", remote)
	r.git("remote", "add", "origin", remote)
	r.git("push", "-q", "origin", "main")
	r.git("fetch", "-q", "origin")
	return remote
}
