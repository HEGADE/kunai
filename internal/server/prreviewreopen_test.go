package server

// Putting a finished review's checkout back.
//
// This is the half of reopening that can actually go wrong, because it is git:
// the session, the seed and the account are ordinary plumbing that every other
// resume already exercises. Against a real repository in a temp dir, the way
// internal/worktree's own tests do, since a fake git would only prove the fake
// matches what was assumed.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hegade/kunai/internal/review"
	"github.com/hegade/kunai/internal/session"
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

// Applying a suggested change, over HTTP, to a real file.
//
// The rule that matters is that the change lands on the code the finding is
// about and NOWHERE else: a path that resolves outside the repository is
// refused, and an index that does not agree with the file the client named is
// refused, because both failures are silent and both write to the wrong place.
func TestApplyWritesTheChangeIntoTheCheckout(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("func f() {\n\tuse(a)\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Server{prReviews: newPRReviewStore(filepath.Join(t.TempDir(), "p.json"))}
	s.prReviews.put(prReview{SessionID: "s1", RepoDir: repo, Draft: &review.Draft{
		Findings: []review.Finding{{
			File: "a.go", Line: 2, Severity: "major", Confidence: "high",
			Title: "t", Body: "b",
			Quote: []string{"\tuse(a)"}, Suggestion: "\tuse(safe)",
		}},
	}})

	post := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/sessions/s1/review/apply", strings.NewReader(body))
		r.SetPathValue("id", "s1")
		w := httptest.NewRecorder()
		s.handleApplyReviewFix(w, r)
		return w
	}

	if w := post(`{"index":0,"file":"a.go"}`); w.Code != 200 {
		t.Fatalf("apply = %d %s", w.Code, w.Body)
	}
	got, err := os.ReadFile(filepath.Join(repo, "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "func f() {\n\tuse(safe)\n}\n" {
		t.Errorf("file =\n%q\nwant the one line replaced and the trailing newline kept", got)
	}

	// The same request again now finds code that is no longer what the review
	// read, and must refuse rather than write a second time.
	if w := post(`{"index":0,"file":"a.go"}`); w.Code == 200 {
		t.Error("applying twice wrote the change again")
	}

	// An index that disagrees with the file the client is looking at is a stale
	// page, not an instruction.
	if w := post(`{"index":0,"file":"somewhere/else.go"}`); w.Code != http.StatusConflict {
		t.Errorf("mismatched index/file = %d, want 409", w.Code)
	}
}

// A path that resolves outside the repository is refused, and nothing is
// written. The finding's file comes from a model, so this is the guard that
// makes writing files from a review surface acceptable at all.
func TestApplyRefusesAPathOutsideTheRepository(t *testing.T) {
	repo := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("untouched\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Server{prReviews: newPRReviewStore(filepath.Join(t.TempDir(), "p.json"))}
	s.prReviews.put(prReview{SessionID: "s1", RepoDir: repo, Draft: &review.Draft{
		Findings: []review.Finding{{
			File: "../" + filepath.Base(filepath.Dir(outside)) + "/secret", Line: 1,
			Severity: "major", Confidence: "high", Title: "t", Body: "b",
			Quote: []string{"untouched"}, Suggestion: "owned",
		}},
	}})
	r := httptest.NewRequest("POST", "/api/sessions/s1/review/apply", strings.NewReader(`{"index":0}`))
	r.SetPathValue("id", "s1")
	w := httptest.NewRecorder()
	s.handleApplyReviewFix(w, r)
	if w.Code == 200 {
		t.Fatal("a file outside the repository was written")
	}
	b, _ := os.ReadFile(outside)
	if string(b) != "untouched\n" {
		t.Fatalf("the outside file was changed: %q", b)
	}
}

// A review still working must never look finished, and a review that stopped
// must never look clean.
//
// This is the worst failure this screen has: the verification phase runs in a
// session of its OWN, so the session the view is attached to is idle for the
// whole of it. The client read that idle session as "the review is over", found
// no findings, and rendered "Nothing worth reporting" with a button offering to
// post it to GitHub -- on a review three minutes into checking a 122-file pull
// request. A clean bill of health is the one answer a reviewer must never give
// by accident, so the server answers both questions instead of the client
// guessing from a session that is deliberately quiet.
func TestADraftSaysWhetherItIsStillWorking(t *testing.T) {
	dir := t.TempDir()
	s := &Server{
		prReviews:  newPRReviewStore(filepath.Join(dir, "p.json")),
		reviewRuns: newReviewRunners(),
		// A real manager, because the handler asks it whether either session is
		// sitting on a permission ask.
		mgr: session.NewManager(),
	}
	s.prReviews.put(prReview{SessionID: "s1", Owner: "o", Repo: "r", Number: 7, Phase: "verify"})

	read := func() map[string]any {
		r := httptest.NewRequest("GET", "/api/sessions/s1/review", nil)
		r.SetPathValue("id", "s1")
		w := httptest.NewRecorder()
		s.handleReviewDraft(w, r)
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("body: %v", err)
		}
		return out
	}

	// Mid-verify, with a runner driving it: still working, whatever the owner
	// session happens to be doing.
	s.reviewRuns.put("s1", &reviewRun{owner: "s1"})
	if out := read(); out["running"] != true || out["stopped"] != false {
		t.Errorf("running review reported running=%v stopped=%v", out["running"], out["stopped"])
	}

	// The same record with nothing driving it: kunai was restarted in the middle.
	// Neither running nor a review of anything.
	s.reviewRuns.drop("s1")
	out := read()
	if out["running"] != false || out["stopped"] != true {
		t.Errorf("stopped review reported running=%v stopped=%v", out["running"], out["stopped"])
	}

	// And a review that reached an answer is neither, so its findings (or its
	// honest emptiness) are what the screen shows.
	s.prReviews.update("s1", func(p *prReview) {
		p.Phase = "done"
		p.Draft = &review.Draft{Summary: "nothing found"}
	})
	if out := read(); out["running"] != false || out["stopped"] != false {
		t.Errorf("finished review reported running=%v stopped=%v", out["running"], out["stopped"])
	}
}

// Stopping a review has to stop the ENGINE, not a turn.
//
// The answer hook fires at the end of every turn and feeds the next phase in, so
// interrupting a turn is followed by another one: that is why pressing Stop in
// the conversation looked like it did nothing while the review carried on. The
// run itself is cancelled, and a turn already in flight lands on a cancelled run
// and asks for nothing.
func TestStoppingAReviewCancelsTheRunRatherThanATurn(t *testing.T) {
	dir := t.TempDir()
	s := &Server{
		prReviews:  newPRReviewStore(filepath.Join(dir, "p.json")),
		reviewRuns: newReviewRunners(),
		mgr:        session.NewManager(),
	}
	s.prReviews.put(prReview{SessionID: "s1", Owner: "o", Repo: "r", Number: 7, Phase: "find"})
	holder := &reviewRun{owner: "s1", run: review.NewRun(review.Request{Repo: "o/r", Number: 7})}
	s.reviewRuns.put("s1", holder)

	stop := func() int {
		r := httptest.NewRequest("POST", "/api/sessions/s1/review/stop", nil)
		r.SetPathValue("id", "s1")
		w := httptest.NewRecorder()
		s.handleStopReview(w, r)
		return w.Code
	}
	if code := stop(); code != 200 {
		t.Fatalf("stop = %d", code)
	}
	holder.mu.Lock()
	cancelled := holder.cancelled
	holder.mu.Unlock()
	if !cancelled {
		t.Error("the run was not cancelled, so the next phase would still be asked for")
	}
	if _, still := s.reviewRuns.get("s1"); still {
		t.Error("a stopped review is still registered as running")
	}
	// The record keeps the phase it was in and grows no draft, so the view says
	// it stopped before it finished rather than reporting a clean bill of health.
	rec, _ := s.prReviews.get("s1")
	if rec.Draft != nil || rec.Phase == "done" {
		t.Errorf("a stopped review was recorded as an answer: phase=%q draft=%v", rec.Phase, rec.Draft != nil)
	}
	// A turn already in flight lands here and must ask for nothing.
	s.advanceReview("s1", "```kunai-review\n{\"summary\":\"x\",\"findings\":[]}\n```")
	if rec, _ := s.prReviews.get("s1"); rec.Draft != nil {
		t.Error("a turn landing after the stop still advanced the review")
	}
	// And pressing it twice is not an error: the button and the review can race.
	if code := stop(); code != 200 {
		t.Errorf("stopping an already-stopped review = %d, want 200", code)
	}
}
