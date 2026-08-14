package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hegade/kunai/internal/ghapp"
	"github.com/hegade/kunai/internal/review"
)

// GitHub rejects a zero start_line, so a single-line comment must OMIT the field
// rather than send 0. That is the difference between a review that posts and a
// 422 that takes the whole thing down.
func TestCommentsOmitStartLineForASingleLine(t *testing.T) {
	patch := "@@ -1,3 +1,5 @@\n one\n+two\n+three\n four"
	anchors := review.ParseDiff([]review.FileDiff{{Filename: "a.go", Patch: patch}})

	plan := review.Build(review.Draft{Findings: []review.Finding{
		{File: "a.go", Line: 2, Title: "single"},
		{File: "a.go", Line: 2, EndLine: 3, Title: "range"},
	}}, anchors)

	got := comments(plan)
	if len(got) != 2 {
		t.Fatalf("got %d comments, want 2", len(got))
	}
	if got[0].StartLine != nil {
		t.Errorf("a single-line comment sent start_line=%d; GitHub rejects that", *got[0].StartLine)
	}
	if got[0].Line != 2 {
		t.Errorf("single-line comment anchored at %d, want 2", got[0].Line)
	}

	// A range sends start_line..line, with `line` being the END. Sending them the
	// other way round is silently accepted by nothing.
	if got[1].StartLine == nil || *got[1].StartLine != 2 {
		t.Errorf("range start = %v, want 2", got[1].StartLine)
	}
	if got[1].Line != 3 {
		t.Errorf("range end = %d, want 3 (GitHub's `line` is the last line)", got[1].Line)
	}
	if got[1].StartSide != got[1].Side {
		t.Errorf("start_side %q does not match side %q", got[1].StartSide, got[1].Side)
	}
}

// GitHub's LIST endpoint omits additions and deletions, so a row built from the
// list alone reports every pull request as +0 -0. Shipped exactly that: a
// 5,300-line pull request rendered as "+0 -0" on the dashboard. The size has to
// come from the per-pull-request lookup.
func TestEnrichTakesTheDiffSizeFromTheDetailCall(t *testing.T) {
	var detailed int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/{owner}/{repo}/installation", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, map[string]any{"id": 1})
	})
	mux.HandleFunc("POST /app/installations/{id}/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, map[string]any{"token": "t", "expires_at": time.Now().Add(time.Hour).Format(time.RFC3339)})
	})
	// The detail endpoint is the only one carrying the totals.
	mux.HandleFunc("GET /repos/{owner}/{repo}/pulls/{n}", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&detailed, 1)
		writeTestJSON(w, map[string]any{"number": 4, "additions": 5266, "deletions": 47})
	})
	mux.HandleFunc("GET /repos/{owner}/{repo}/pulls/{n}/reviews", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, []any{})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	app := testGitHubApp(t, srv.URL)
	// Exactly what the list endpoint gives: no additions, no deletions.
	got := enrich(context.Background(), app, ghapp.Repo{Owner: "o", Name: "r"},
		[]ghapp.PullRequest{{Number: 4, Title: "big"}})

	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1", len(got))
	}
	if got[0].Additions != 5266 || got[0].Deletions != 47 {
		t.Errorf("size = +%d -%d, want the detail call's totals", got[0].Additions, got[0].Deletions)
	}
	if atomic.LoadInt32(&detailed) != 1 {
		t.Error("the per-pull-request detail was never fetched")
	}
}

// A failed detail lookup leaves the size unknown rather than dropping the row:
// knowing a pull request exists is worth more than knowing how big it is.
func TestEnrichSurvivesAFailedDetailLookup(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/{owner}/{repo}/installation", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, map[string]any{"id": 1})
	})
	mux.HandleFunc("POST /app/installations/{id}/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, map[string]any{"token": "t", "expires_at": time.Now().Add(time.Hour).Format(time.RFC3339)})
	})
	mux.HandleFunc("GET /repos/{owner}/{repo}/pulls/{n}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	got := enrich(context.Background(), testGitHubApp(t, srv.URL), ghapp.Repo{Owner: "o", Name: "r"},
		[]ghapp.PullRequest{{Number: 4, Title: "still listed"}})
	if len(got) != 1 || got[0].Title != "still listed" {
		t.Fatalf("got %+v, want the row kept despite the failed lookup", got)
	}
}

// Pruning in the UI is honoured server-side rather than trusted to have already
// happened, so a client that drops a finding cannot be bypassed by a stale post.
func TestKeptFiltersTheDraft(t *testing.T) {
	draft := review.Draft{
		Summary: "s",
		Findings: []review.Finding{
			{Title: "first"}, {Title: "second"}, {Title: "third"},
		},
	}
	got := kept(draft, []int{0, 2})
	if len(got.Findings) != 2 || got.Findings[0].Title != "first" || got.Findings[1].Title != "third" {
		t.Fatalf("got %+v, want the first and third", got.Findings)
	}
	if got.Summary != "s" {
		t.Error("the summary should survive pruning")
	}

	// A NIL selection is a client that does not prune, and must post everything.
	if all := kept(draft, nil); len(all.Findings) != 3 {
		t.Errorf("a nil selection kept %d findings, want all 3", len(all.Findings))
	}

	// An EMPTY selection is somebody who read every finding and dropped them all.
	// Treating that as "post everything" published the lot, which is the worst
	// possible reading of that gesture; the summary alone is what they asked for.
	none := kept(draft, []int{})
	if len(none.Findings) != 0 {
		t.Errorf("dropping every finding still posted %d of them", len(none.Findings))
	}
	if none.Summary != "s" {
		t.Error("the summary should survive dropping every finding: it is still a review")
	}
}

// A review reads and reports, and can do nothing else.
//
// Bash is withheld even on your own team's code, which is a change of mind. It
// was allowed there so a review could run the tests. But a permission mode that
// runs safe work still STOPS to ask about a risky command, and nobody is watching
// a review by design, so the first unusual command parked the whole thing on a
// question that would never be answered. A reviewer that hangs silently is worth
// less than one that cannot run tests.
//
// The consequence worth keeping is that the toolset no longer depends on trust:
// there is one list, so there is no second list to keep in step and no way for a
// stranger's diff to be handed more than your own branch gets.
func TestAReviewCanOnlyRead(t *testing.T) {
	for _, tool := range []string{"Bash", "Write", "Edit", "MultiEdit", "NotebookEdit"} {
		if !contains(reviewToolset, tool) {
			t.Errorf("a review can still use %s, which is either a way to change the tree it is reading or a way to hang on a permission ask", tool)
		}
	}
	// Reading is the whole job, and none of these ever needs permission, which is
	// what makes an unwatched review possible at all.
	//
	// Task is on this list rather than the withheld one, and that is what the
	// verification phase is built on: a subagent starts from a fresh context, so
	// it judges a claim without having seen the reasoning that produced it.
	//
	// Allowing it widens nothing, and that was measured rather than assumed.
	// Probed against a real CLI with --disallowedTools "Bash,Write,Edit", a Task
	// subagent reported its own toolset as Agent, Glob, Grep, Read, Skill and
	// ToolSearch: the restriction propagates, and the run recorded no permission
	// denials, so neither failure the withheld list guards against is reachable
	// through a subagent.
	for _, tool := range []string{"Read", "Grep", "Glob", "Task"} {
		if contains(reviewToolset, tool) {
			t.Errorf("%s is withheld, so the review cannot read the code it is reviewing", tool)
		}
	}
}

// The draft has to outlive the tab. The review text lives in the transcript, but
// the pull request and the COMMIT it belongs to do not, and posting needs both.
func TestPRReviewStorePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prreviews.json")
	st := newPRReviewStore(path)
	st.put(prReview{SessionID: "s1", Owner: "lyzr", Repo: "kunai", Number: 128, HeadSHA: "abc123"})

	reloaded, ok := newPRReviewStore(path).get("s1")
	if !ok {
		t.Fatal("the record did not survive a reload")
	}
	if reloaded.HeadSHA != "abc123" || reloaded.Number != 128 {
		t.Fatalf("got %+v, want the pull request and commit kept", reloaded)
	}

	// Marking it posted is what stops a second click posting twice.
	if _, ok := st.update("s1", func(r *prReview) { r.PostedURL = "https://github.com/x" }); !ok {
		t.Fatal("update reported no record")
	}
	if rec, _ := st.get("s1"); !rec.Posted() {
		t.Error("a posted review does not report itself as posted")
	}
	// And updating something that is not a review says so rather than creating it.
	if _, ok := st.update("nope", func(*prReview) {}); ok {
		t.Error("update invented a record for an unknown session")
	}
}

// testGitHubApp is an App with a throwaway key, pointed at a fake GitHub.
func testGitHubApp(t *testing.T, baseURL string) *ghapp.App {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	creds, err := ghapp.LoadCredentials("123456", pemBytes)
	if err != nil {
		t.Fatal(err)
	}
	return ghapp.NewWithBaseURL(creds, baseURL)
}

func writeTestJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// Editing is by index into the draft the client was shown, and so is keeping.
// The two must agree, which means edits are applied BEFORE the filter: applying
// them after would shift every edit onto a neighbouring finding, silently
// posting one finding's words under another finding's line.
func TestEditsApplyBeforeTheSelectionIsFiltered(t *testing.T) {
	draft := review.Draft{Findings: []review.Finding{
		{File: "a.go", Line: 1, Title: "first", Body: "b1", Severity: review.SeverityBlocker},
		{File: "b.go", Line: 2, Title: "second", Body: "b2", Severity: review.SeverityBlocker},
		{File: "c.go", Line: 3, Title: "third", Body: "b3", Severity: review.SeverityBlocker},
	}}

	edited := applyEdits(draft, []reviewEdit{{Index: 2, Title: "rewritten third"}}, "")
	got := kept(edited, []int{2})

	if len(got.Findings) != 1 {
		t.Fatalf("kept %d findings, want 1", len(got.Findings))
	}
	if got.Findings[0].File != "c.go" || got.Findings[0].Title != "rewritten third" {
		t.Fatalf("got %+v, want the edit on c.go", got.Findings[0])
	}
}

// An empty field means "unchanged", never "delete this". The two are
// indistinguishable over JSON and the harmful reading is the one that publishes
// a blank comment on somebody's line.
func TestAnEmptyEditLeavesTheFindingAlone(t *testing.T) {
	draft := review.Draft{Findings: []review.Finding{
		{File: "a.go", Line: 1, Title: "keep me", Body: "and me", Severity: review.SeverityMajor},
	}}
	got := applyEdits(draft, []reviewEdit{{Index: 0, Title: "", Body: "   "}}, "")
	if got.Findings[0].Title != "keep me" || got.Findings[0].Body != "and me" {
		t.Fatalf("an empty edit blanked the finding: %+v", got.Findings[0])
	}
}

// Only the words may be edited. The anchor decides which line of somebody's
// pull request a comment lands on, and it stays server-side: there is no way to
// express a new file or line here, and that is the point.
func TestAnEditCannotMoveTheAnchor(t *testing.T) {
	draft := review.Draft{Findings: []review.Finding{
		{File: "a.go", Line: 7, Side: review.SideRight, Title: "t", Severity: review.SeverityMinor},
	}}
	got := applyEdits(draft, []reviewEdit{{Index: 0, Title: "new words", Severity: "blocker"}}, "")
	if got.Findings[0].File != "a.go" || got.Findings[0].Line != 7 {
		t.Fatalf("the anchor moved: %+v", got.Findings[0])
	}
	if got.Findings[0].Severity != review.SeverityBlocker {
		t.Errorf("the edited severity was not applied: %q", got.Findings[0].Severity)
	}
}

// A hand-typed severity must not reach GitHub unrecognised. applyEdits
// deliberately does not normalise (it would sort, breaking the indexes above),
// so Build has to be the thing that repairs it.
func TestAnUnrecognisedEditedSeverityIsRepairedByBuild(t *testing.T) {
	draft := review.Draft{Findings: []review.Finding{
		{File: "a.go", Line: 1, Title: "t", Severity: review.SeverityMinor},
	}}
	edited := applyEdits(draft, []reviewEdit{{Index: 0, Severity: "SUPER URGENT"}}, "")
	plan := review.Build(edited, nil)
	if got := plan.Placements[0].Finding.Severity; got != review.SeverityMajor {
		t.Fatalf("severity = %q, want it repaired to major", got)
	}
}

// The summary is editable too, and an untouched one must survive.
func TestSummaryEditIsOptional(t *testing.T) {
	draft := review.Draft{Summary: "the model's words"}
	if got := applyEdits(draft, nil, ""); got.Summary != "the model's words" {
		t.Errorf("an empty summary edit overwrote it: %q", got.Summary)
	}
	if got := applyEdits(draft, nil, "mine"); got.Summary != "mine" {
		t.Errorf("summary = %q, want the edit", got.Summary)
	}
}

// Clicking Review twice in quick succession must join the run in flight rather
// than start a second one: two sessions, two worktrees, two lots of quota, and
// two drafts of which only one can ever be posted.
func TestASecondClickJoinsAReviewStillWorking(t *testing.T) {
	for _, phase := range []string{"survey", "find", "verify"} {
		if !reviewInFlight(prReview{Phase: phase}) {
			t.Errorf("a review in the %s phase was not treated as in flight", phase)
		}
	}
}

// But a review that has ANSWERED must not be joined, and this is the case that
// was wrong. A finished review's session stays alive on purpose (it is a
// conversation you can argue with), so testing the session made a completed
// draft block re-reviewing the same pull request. Asking again after somebody
// pushes is the one moment a fresh reading is most wanted.
func TestAFinishedReviewDoesNotBlockAFreshOne(t *testing.T) {
	cases := []struct {
		name string
		rec  prReview
	}{
		{"a finished draft", prReview{Phase: "done", Draft: &review.Draft{}}},
		{"a draft while the phase still says verify", prReview{Phase: "verify", Draft: &review.Draft{}}},
		{"an unreadable answer", prReview{Phase: "done", ParseError: "the reply contains no review block"}},
		{"a record from before phases existed", prReview{}},
	}
	for _, c := range cases {
		if reviewInFlight(c.rec) {
			t.Errorf("%s was treated as still in flight, so a new review would be refused", c.name)
		}
	}
}
