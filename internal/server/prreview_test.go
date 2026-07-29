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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hegade/kunai/internal/ghapp"
	"github.com/hegade/kunai/internal/review"
	"github.com/hegade/kunai/internal/session"
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

// The findings are in an assistant message's BLOCKS, never in its Text.
//
// This shipped reading ev.Text on an assistant event, which is only ever set on
// delta, thinking and user events. So the collected draft was the empty string,
// every review recorded a parse error, and it read as the model failing to
// follow the output format rather than as kunai never seeing the answer. The
// review of PR #4 found it before this test did.
func TestDraftTextComesFromAssistantBlocks(t *testing.T) {
	ev := session.AppEvent{
		T: session.EvAssistant,
		Blocks: []session.AppBlock{
			{Type: "thinking", Text: "considering"},
			{Type: "tool_use", Name: "Read"},
			{Type: "text", Text: "Here is what I found."},
		},
	}
	if ev.Text != "" {
		t.Fatal("this test's premise is wrong: an assistant event now carries Text")
	}
	got := assistantBlockText(ev)
	if got != "Here is what I found." {
		t.Errorf("collected %q, want the text block's content", got)
	}
	// Thinking and tool calls are not the answer and must not reach the parser.
	if strings.Contains(got, "considering") || strings.Contains(got, "Read") {
		t.Errorf("collected non-answer blocks: %q", got)
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

	// An empty selection means "all of them", so a client with no pruning still
	// posts a complete review rather than an empty one.
	if all := kept(draft, nil); len(all.Findings) != 3 {
		t.Errorf("an empty selection kept %d findings, want all 3", len(all.Findings))
	}
}

// The trust decision has one home, so it cannot be made twice and differently.
// The distinction is what decides whether the reviewing agent can execute
// anything at all while reading a stranger's diff.
func TestToolsetForWithholdsExecutionOnForks(t *testing.T) {
	fork := toolsetFor(true)
	if !contains(fork, "Bash") {
		t.Error("a fork review was given Bash; a stranger's diff must not be able to run anything")
	}
	for _, tool := range []string{"Write", "Edit", "MultiEdit"} {
		if !contains(fork, tool) {
			t.Errorf("a fork review can still %s", tool)
		}
	}

	// On your own team's pull request the agent may run tests, which is a real
	// step up in review quality, but it still has no business editing the tree it
	// is reading: a review that modifies files makes its own findings
	// unreproducible.
	trusted := toolsetFor(false)
	if contains(trusted, "Bash") {
		t.Error("a trusted review lost Bash, so it cannot run the tests")
	}
	if !contains(trusted, "Write") || !contains(trusted, "Edit") {
		t.Error("a review should never be able to edit the checkout it is reviewing")
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
