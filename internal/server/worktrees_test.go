package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hegade/kunai/internal/session"
	"github.com/hegade/kunai/internal/worktree"
)

// wtRepo is a real git repository plus a store pointed at a temp data dir. The
// worktree layer is git's behaviour end to end, so there is nothing useful to
// fake here.
type wtRepo struct {
	t       *testing.T
	dir     string
	dataDir string
	store   *worktreeStore
	// sessions is what the store sees as live, so a test can pretend someone is
	// working in a worktree without spawning a CLI.
	sessions []session.Meta
}

func newWTRepo(t *testing.T) *wtRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	base := t.TempDir()
	r := &wtRepo{t: t, dir: filepath.Join(base, "repo"), dataDir: filepath.Join(base, "data")}
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(r.dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	r.run("git", "init", "-q", "-b", "main")
	r.run("git", "config", "user.name", "test")
	r.run("git", "config", "user.email", "test@localhost")
	r.writeFile("README.md", "hello\n")
	r.run("git", "add", "-A")
	r.run("git", "commit", "-q", "-m", "initial")

	r.store = newWorktreeStore(r.dataDir, func() []session.Meta { return r.sessions })
	return r
}

func (r *wtRepo) run(name string, args ...string) {
	r.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@localhost",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@localhost")
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("%s %s: %v: %s", name, strings.Join(args, " "), err, out)
	}
}

func (r *wtRepo) writeFile(name, content string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, name), []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// server returns a Server with just enough wired to serve the worktree routes.
func (r *wtRepo) server() *Server {
	return &Server{cfg: Config{DataDir: r.dataDir}, worktrees: r.store}
}

func TestStoreCreateRunsSetupAndRecordsIt(t *testing.T) {
	r := newWTRepo(t)
	r.writeFile(".env", "SECRET=1\n")

	rec, err := r.store.create(createRequest{
		Repo: r.dir, Name: "fix auth", Base: "main",
		Setup: strptr(`ln -sf "$KUNAI_PROJECT_ROOT/.env" .env && echo prepared > prepared.txt`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Setup.State != worktree.SetupRunning {
		t.Errorf("create returned state %q; setup should not be awaited on the request path", rec.Setup.State)
	}

	r.store.waitReady(context.Background(), rec.Path)
	done, ok := r.store.get(rec.Path)
	if !ok {
		t.Fatal("the record vanished")
	}
	if done.Setup.State != worktree.SetupOK {
		t.Fatalf("setup state = %q, output:\n%s", done.Setup.State, done.Setup.Output)
	}
	if _, err := os.Stat(filepath.Join(rec.Path, "prepared.txt")); err != nil {
		t.Error("the setup command did not run in the worktree")
	}
	// The links are read back off disk, so the record describes what actually
	// happened rather than what the command claimed it would do.
	if len(done.Shared) != 1 || done.Shared[0] != ".env" {
		t.Errorf("shared = %v, want [.env]", done.Shared)
	}
}

// A worktree the user has to be warned about is one whose setup failed, and the
// warning has to reach the agent, not just the UI.
func TestBriefCarriesSetupOutcomeAndSharedPaths(t *testing.T) {
	r := newWTRepo(t)
	r.writeFile(".env", "SECRET=1\n")

	rec, err := r.store.create(createRequest{
		Repo: r.dir, Name: "w", Base: "main",
		Setup: strptr(`ln -sf "$KUNAI_PROJECT_ROOT/.env" .env; echo "boom" 1>&2; exit 2`),
	})
	if err != nil {
		t.Fatal(err)
	}
	r.store.waitReady(context.Background(), rec.Path)
	done, _ := r.store.get(rec.Path)

	brief := done.brief()
	if !strings.Contains(brief, "did not succeed") {
		t.Errorf("the failed setup is not in the brief:\n%s", brief)
	}
	if !strings.Contains(brief, "boom") {
		t.Error("the failure output is not in the brief")
	}
	if !strings.Contains(brief, "SHARED") || !strings.Contains(brief, ".env") {
		t.Error("the shared .env is not flagged to the agent")
	}
	if !strings.Contains(brief, done.Path) || !strings.Contains(brief, r.dir) {
		t.Error("the brief does not name both checkouts")
	}
}

// The whole point of waiting: a session must not start against a tree that is
// still being installed into.
func TestApplyWorktreeWaitsForSetup(t *testing.T) {
	r := newWTRepo(t)
	rec, err := r.store.create(createRequest{
		Repo: r.dir, Name: "w", Base: "main",
		Setup: strptr("sleep 0.4 && echo done > installed.txt"),
	})
	if err != nil {
		t.Fatal(err)
	}

	var opts session.CreateOptions
	start := time.Now()
	if err := r.server().applyWorktree(context.Background(), &opts, rec.Path); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 300*time.Millisecond {
		t.Errorf("applyWorktree returned after %v; it did not wait for setup", elapsed)
	}
	if _, err := os.Stat(filepath.Join(rec.Path, "installed.txt")); err != nil {
		t.Error("the session would have started before setup finished")
	}
	if opts.Cwd != rec.Path {
		t.Errorf("cwd = %q, want the worktree", opts.Cwd)
	}
	if !strings.Contains(opts.AppendSystemPrompt, "git worktree") {
		t.Error("the brief was not attached to the session options")
	}
}

// A caller that gives up must not leave the create path blocked forever.
func TestApplyWorktreeGivesUpWithItsContext(t *testing.T) {
	r := newWTRepo(t)
	rec, err := r.store.create(createRequest{
		Repo: r.dir, Name: "w", Base: "main", Setup: strptr("sleep 5"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	var opts session.CreateOptions
	start := time.Now()
	if err := r.server().applyWorktree(ctx, &opts, rec.Path); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("waited %v after the context expired", elapsed)
	}
}

func TestDeleteRefusesWhileASessionIsWorkingThere(t *testing.T) {
	r := newWTRepo(t)
	rec, err := r.store.create(createRequest{Repo: r.dir, Name: "w", Base: "main"})
	if err != nil {
		t.Fatal(err)
	}
	r.sessions = []session.Meta{{ID: "s1", Cwd: rec.Path}}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/worktrees?path="+rec.Path, nil)
	r.server().handleDeleteWorktree(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", w.Code)
	}
	if _, err := os.Stat(rec.Path); err != nil {
		t.Error("the refused delete removed the worktree anyway")
	}

	// With nobody there, it goes.
	r.sessions = nil
	w = httptest.NewRecorder()
	r.server().handleDeleteWorktree(w, httptest.NewRequest(http.MethodDelete, "/api/worktrees?path="+rec.Path, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body)
	}
	if _, err := os.Stat(rec.Path); !os.IsNotExist(err) {
		t.Error("the worktree directory survived the delete")
	}
	if _, ok := r.store.get(rec.Path); ok {
		t.Error("a deleted worktree is still offered as one")
	}
	if got := r.store.all(); len(got) != 0 {
		t.Errorf("a deleted worktree is still listed: %+v", got)
	}
	// But what it knew outlives it: the sessions that ran there are still on
	// disk, and this record is the only thing that says which repository they
	// belonged to. Dropping it stranded them under a heading named after a
	// directory that no longer exists.
	if got := r.store.repoFor(rec.Path); got != r.dir {
		t.Errorf("repoFor after delete = %q, want %q", got, r.dir)
	}
}

// A worktree may hold more than one session, so "in use" is about who is there
// now, not about which session created it.
func TestSessionsInCountsEverySessionInAWorktree(t *testing.T) {
	r := newWTRepo(t)
	rec, err := r.store.create(createRequest{Repo: r.dir, Name: "w", Base: "main"})
	if err != nil {
		t.Fatal(err)
	}
	r.sessions = []session.Meta{
		{ID: "a", Cwd: rec.Path},
		{ID: "b", Cwd: rec.Path},
		{ID: "elsewhere", Cwd: r.dir},
	}
	if got := r.store.sessionsIn(rec.Path); len(got) != 2 {
		t.Errorf("sessionsIn = %v, want the two in the worktree", got)
	}
}

func TestSettingsDefaultToStartingFromOrigin(t *testing.T) {
	r := newWTRepo(t)
	if !r.store.getSettings().FromOrigin {
		t.Error("FromOrigin should default to true")
	}

	// A file written before the settings section existed must not read as
	// "FromOrigin off" when it is loaded back.
	if err := os.WriteFile(filepath.Join(r.dataDir, "worktrees.json"),
		[]byte(`{"repos":{"/x":{"setup":"make"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	reloaded := newWorktreeStore(r.dataDir, nil)
	if !reloaded.getSettings().FromOrigin {
		t.Error("a file with no settings section flipped FromOrigin off")
	}
}

func TestSetupIsRememberedPerRepo(t *testing.T) {
	r := newWTRepo(t)
	r.writeFile("package-lock.json", "")

	if got := r.store.setupFor(r.dir); got.Source != worktree.SourceSuggested {
		t.Errorf("source = %q, want a suggestion before anything is stored", got.Source)
	}

	if _, err := r.store.create(createRequest{
		Repo: r.dir, Name: "w", Base: "main", Setup: strptr("make bootstrap"), Remember: true,
	}); err != nil {
		t.Fatal(err)
	}
	got := r.store.setupFor(r.dir)
	if got.Command != "make bootstrap" {
		t.Errorf("command = %q, want the remembered one", got.Command)
	}

	// Reloading from disk keeps it, which is the point of remembering.
	if again := newWorktreeStore(r.dataDir, nil).setupFor(r.dir); again.Command != "make bootstrap" {
		t.Errorf("after reload: %q", again.Command)
	}
}

// The repository's own file wins over this machine's, because that is the one
// that travels with the project.
func TestProjectFileBeatsTheStoredCommand(t *testing.T) {
	r := newWTRepo(t)
	r.store.rememberSetup(r.dir, "machine command")
	r.writeFile(worktree.ProjectFile, `{"setup":"project command"}`)

	got := r.store.setupFor(r.dir)
	if got.Command != "project command" {
		t.Errorf("command = %q, want the project's", got.Command)
	}
}

func TestListReportsStatusAndDropsWorktreesThatAreGone(t *testing.T) {
	r := newWTRepo(t)
	rec, err := r.store.create(createRequest{Repo: r.dir, Name: "w", Base: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rec.Path, "new.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	r.server().handleWorktrees(w, httptest.NewRequest(http.MethodGet, "/api/worktrees", nil))
	var views []worktreeView
	if err := json.Unmarshal(w.Body.Bytes(), &views); err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 {
		t.Fatalf("got %d worktrees, want 1", len(views))
	}
	if views[0].Status == nil || views[0].Status.Dirty != 1 {
		t.Errorf("status = %+v, want one dirty file", views[0].Status)
	}

	// Deleted from a terminal: git is the truth, so the record goes with it.
	if err := os.RemoveAll(rec.Path); err != nil {
		t.Fatal(err)
	}
	if got := r.store.all(); len(got) != 0 {
		t.Errorf("a worktree removed from disk is still listed: %+v", got)
	}
}

// strptr is the pointer form the create request needs, since absent and empty
// mean different things there: absent resolves the repository's own command,
// empty means the user looked at it and chose none.
func strptr(s string) *string { return &s }

// --- naming -------------------------------------------------------------------

// The launcher has the task text when the worktree is made, so the branch names
// itself and nobody is asked to name a branch before describing the work.
func TestAWorktreeNamesItselfFromTheTask(t *testing.T) {
	r := newWTRepo(t)

	rec, err := r.store.create(createRequest{
		Repo: r.dir, Base: "main",
		Prompt: "please fix the login redirect loop",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Branch != "kunai/fix-login-redirect-loop" {
		t.Errorf("branch = %q", rec.Branch)
	}
}

// A name someone typed always wins: they were more specific than we can be.
func TestATypedNameWinsOverTheTask(t *testing.T) {
	r := newWTRepo(t)

	rec, err := r.store.create(createRequest{
		Repo: r.dir, Base: "main",
		Name: "spike", Prompt: "fix the login redirect loop",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Branch != "kunai/spike" {
		t.Errorf("branch = %q, want the typed name", rec.Branch)
	}
}

// The sidebar's one-tap start asks nothing, so there is nothing to name it after
// until the first turn ends. Then it takes a name, as t3code does, minus the
// model call.
func TestAPlaceholderTakesItsNameFromTheFirstPrompt(t *testing.T) {
	r := newWTRepo(t)

	rec, err := r.store.create(createRequest{Repo: r.dir, Base: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Branch != "kunai/work" {
		t.Fatalf("a nameless start should get a placeholder, got %q", rec.Branch)
	}

	r.store.nameFromFirstPrompt(rec.Path, "add a retry to the discovery poller")

	named, ok := r.store.get(rec.Path)
	if !ok {
		t.Fatal("the record vanished")
	}
	if named.Branch != "kunai/add-retry-discovery-poller" {
		t.Errorf("branch = %q", named.Branch)
	}
	// The directory stays put: a session's claude process is running in it.
	if named.Path != rec.Path {
		t.Errorf("the directory moved to %q, out from under the running session", named.Path)
	}
	// And it survives a reload, since the record is what the sidebar reads.
	if again := newWorktreeStore(r.dataDir, nil); func() bool {
		got, ok := again.get(rec.Path)
		return !ok || got.Branch != named.Branch
	}() {
		t.Error("the new name was not persisted")
	}
}

// A name someone chose must never be overwritten by a later prompt.
func TestARealNameIsNeverReplaced(t *testing.T) {
	r := newWTRepo(t)

	rec, err := r.store.create(createRequest{Repo: r.dir, Base: "main", Name: "my spike"})
	if err != nil {
		t.Fatal(err)
	}
	r.store.nameFromFirstPrompt(rec.Path, "completely different work")

	got, _ := r.store.get(rec.Path)
	if got.Branch != "kunai/my-spike" {
		t.Errorf("branch = %q; a chosen name was overwritten", got.Branch)
	}
}

// Renaming twice would rewrite the branch on every turn.
func TestTheNameIsTakenOnlyOnce(t *testing.T) {
	r := newWTRepo(t)

	rec, _ := r.store.create(createRequest{Repo: r.dir, Base: "main"})
	r.store.nameFromFirstPrompt(rec.Path, "add retries")
	first, _ := r.store.get(rec.Path)

	r.store.nameFromFirstPrompt(rec.Path, "something else entirely")
	second, _ := r.store.get(rec.Path)

	if first.Branch != second.Branch {
		t.Errorf("the branch was renamed again on a later turn: %q -> %q", first.Branch, second.Branch)
	}
}

// A prompt with nothing nameable in it leaves the placeholder alone rather than
// producing a branch called after punctuation.
func TestAnUnnameablePromptLeavesThePlaceholder(t *testing.T) {
	r := newWTRepo(t)

	rec, _ := r.store.create(createRequest{Repo: r.dir, Base: "main"})
	r.store.nameFromFirstPrompt(rec.Path, "!!! ???")

	got, _ := r.store.get(rec.Path)
	if got.Branch != rec.Branch {
		t.Errorf("branch = %q, want the placeholder %q", got.Branch, rec.Branch)
	}
}

// --- folders that are not repositories ------------------------------------------

// The sidebar asks before it offers, so a folder that cannot hold a worktree
// never shows the button. Finding out by failing is what put a raw
// "worktree: not a git repository" where the session list should be.
func TestIsRepoAnswersForBothKinds(t *testing.T) {
	r := newWTRepo(t)
	plain := t.TempDir()

	for path, want := range map[string]bool{r.dir: true, plain: false} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/worktrees/repo?path="+path, nil)
		r.server().handleIsRepo(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d", path, w.Code)
		}
		var body struct {
			Repo bool `json:"repo"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Repo != want {
			t.Errorf("%s: repo = %v, want %v", path, body.Repo, want)
		}
	}
}

// If one does slip through, what reaches the user has to read like a sentence.
// "worktree: not a git repository" is a Go value, and it was being rendered.
func TestErrorsReachTheUserAsSentences(t *testing.T) {
	r := newWTRepo(t)
	plain := t.TempDir()

	w := httptest.NewRecorder()
	body := strings.NewReader(`{"repo":"` + plain + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/worktrees", body)
	r.server().handleCreateWorktree(w, req)

	if w.Code == http.StatusOK {
		t.Fatal("creating a worktree of a non-repository should fail")
	}
	var out struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(out.Error, "worktree:") {
		t.Errorf("a Go error prefix reached the user: %q", out.Error)
	}
	if !strings.Contains(out.Error, "not a git repository") {
		t.Errorf("the message does not say what is wrong: %q", out.Error)
	}
}
