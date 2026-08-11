package ghapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeGitHub is a stand-in for the REST API: it mints access tokens, counts how
// often it was asked to, and serves whatever else a test registers. It owns the
// clock so a token's expiry moves with the App's own view of time.
type fakeGitHub struct {
	t      *testing.T
	mux    *http.ServeMux
	clock  time.Time
	tokens int // how many times an installation token was minted
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	g := &fakeGitHub{t: t, mux: http.NewServeMux(), clock: time.Unix(1_700_000_000, 0)}
	g.mux.HandleFunc("POST /app/installations/{id}/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		g.tokens++
		// Minting a token is the one thing only the App JWT may do, so the hop is
		// asserted here rather than trusted at the call site.
		if auth := r.Header.Get("Authorization"); !strings.HasPrefix(auth, "Bearer ey") {
			t.Errorf("access_tokens called without an App JWT: %q", auth)
		}
		g.reply(w, map[string]any{
			"token":      fmt.Sprintf("ghs_token_%d", g.tokens),
			"expires_at": g.clock.Add(time.Hour).Format(time.RFC3339),
		})
	})
	return g
}

// handle registers a route on the fake, e.g. "GET /repos/{owner}/{repo}/pulls".
func (g *fakeGitHub) handle(pattern string, fn http.HandlerFunc) *fakeGitHub {
	g.mux.HandleFunc(pattern, fn)
	return g
}

// reply writes a JSON body, and status writes a JSON error with a status.
func (g *fakeGitHub) reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// start wires an App to this fake, sharing its clock. Advance time with
// g.advance; the App sees it immediately.
func (g *fakeGitHub) start() *App {
	g.t.Helper()
	srv := httptest.NewServer(g.mux)
	g.t.Cleanup(srv.Close)

	app := New(testCreds(g.t))
	app.base = srv.URL
	app.now = func() time.Time { return g.clock }
	return app
}

func (g *fakeGitHub) advance(d time.Duration) { g.clock = g.clock.Add(d) }

// installed registers the installation lookup every repo-scoped call makes first.
func (g *fakeGitHub) installed(id int64) *fakeGitHub {
	return g.handle("GET /repos/{owner}/{repo}/installation", func(w http.ResponseWriter, r *http.Request) {
		g.reply(w, map[string]any{"id": id})
	})
}

// A token is minted once and reused: a single review makes several calls, and
// re-exchanging per call would be slow and rude to GitHub.
func TestInstallationTokenIsCached(t *testing.T) {
	g := newFakeGitHub(t)
	app := g.start()
	ctx := context.Background()

	first, err := app.InstallationToken(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.InstallationToken(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("token changed on the second call: %q then %q", first, second)
	}
	if g.tokens != 1 {
		t.Errorf("exchanged %d times, want 1", g.tokens)
	}

	// A different installation is a different token: they are scoped separately
	// and sharing one would leak access between orgs.
	other, err := app.InstallationToken(ctx, 99)
	if err != nil {
		t.Fatal(err)
	}
	if other == first {
		t.Error("two installations were given the same token")
	}
}

// The cache refreshes BEFORE expiry. A token that lapses between the check and
// the request fails a review that has already spent real quota, and being early
// costs one extra exchange an hour.
func TestInstallationTokenRefreshesEarly(t *testing.T) {
	g := newFakeGitHub(t)
	app := g.start()
	ctx := context.Background()

	if _, err := app.InstallationToken(ctx, 42); err != nil {
		t.Fatal(err)
	}

	g.advance(30 * time.Minute) // half an hour left: still good
	if _, err := app.InstallationToken(ctx, 42); err != nil {
		t.Fatal(err)
	}
	if g.tokens != 1 {
		t.Fatalf("re-exchanged after 30 minutes (%d calls); the token had half an hour left", g.tokens)
	}

	g.advance(29*time.Minute + 30*time.Second) // inside the margin, not yet expired
	if _, err := app.InstallationToken(ctx, 42); err != nil {
		t.Fatal(err)
	}
	if g.tokens != 2 {
		t.Fatalf("did not refresh inside the margin (%d calls); the token would expire mid-review", g.tokens)
	}
}

// A repository call must carry the INSTALLATION token, never the App JWT. The two
// are not interchangeable, and confusing them produces a 403 that reads like a
// permissions problem.
func TestRepoCallsUseTheInstallationToken(t *testing.T) {
	g := newFakeGitHub(t).installed(42)
	g.handle("GET /repos/{owner}/{repo}/pulls", func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "Bearer ghs_token_1" {
			t.Errorf("pulls called with %q, want the installation token", auth)
		}
		if got := r.Header.Get("X-GitHub-Api-Version"); got != apiVersion {
			t.Errorf("API version header = %q, want %q", got, apiVersion)
		}
		g.reply(w, []map[string]any{
			{"number": 128, "title": "Snooze the sidebar rows", "additions": 214, "deletions": 31},
		})
	})

	prs, err := g.start().OpenPullRequests(context.Background(), Repo{Owner: "lyzr", Name: "kunai"})
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 || prs[0].Number != 128 {
		t.Fatalf("got %+v, want one PR numbered 128", prs)
	}
}

// GitHub puts the real reason for a rejected review in the per-field errors, not
// in the top-level message, which says only "Validation Failed". Losing that
// detail loses the one line that explains what to fix.
func TestAPIErrorSurfacesFieldErrors(t *testing.T) {
	g := newFakeGitHub(t)
	g.handle("GET /app/installations", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		g.reply(w, map[string]any{
			"message": "Validation Failed",
			"errors": []map[string]string{{
				"resource": "PullRequestReview", "field": "line", "code": "invalid",
				"message": "line must be part of the diff",
			}},
		})
	})

	_, err := g.start().Installations(context.Background())
	if err == nil {
		t.Fatal("a 422 was reported as success")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not an *APIError: %T", err)
	}
	if apiErr.Status != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", apiErr.Status)
	}
	if !strings.Contains(apiErr.Error(), "line must be part of the diff") {
		t.Errorf("the field error was lost: %q", apiErr.Error())
	}
}

// A body that is not GitHub's documented error shape must still produce a usable
// error, because an unparseable failure is exactly when the status matters most.
func TestAPIErrorSurvivesAnUnexpectedBody(t *testing.T) {
	g := newFakeGitHub(t)
	g.handle("GET /app/installations", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>gateway timeout</html>"))
	})
	_, err := g.start().Installations(context.Background())
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("got %v, want an error naming the status", err)
	}
}

// The duplicate-work defence, and the only coordination two colleagues' installs
// have: GitHub itself. Matched on the COMMIT, because a review of an older commit
// says nothing about the code as it stands now.
func TestReviewedAtMatchesTheCommitAndTheBot(t *testing.T) {
	const head = "abc123"
	g := newFakeGitHub(t).installed(42)
	g.handle("GET /repos/{owner}/{repo}/pulls/{n}/reviews", func(w http.ResponseWriter, r *http.Request) {
		g.reply(w, []map[string]any{
			// A human's review of this very commit: not ours, does not count.
			{"commit_id": head, "submitted_at": "2026-07-29T10:00:00Z",
				"user": map[string]any{"login": "shorya", "type": "User"}},
			// Our own review, but of an older commit: the code has moved on.
			{"commit_id": "old999", "submitted_at": "2026-07-29T11:00:00Z",
				"user": map[string]any{"login": "kunai[bot]", "type": "Bot"}},
		})
	})
	app := g.start()
	repo := Repo{Owner: "lyzr", Name: "kunai"}
	ctx := context.Background()

	if _, found, err := app.ReviewedAt(ctx, repo, 128, head); err != nil || found {
		t.Fatalf("found=%v err=%v; neither review should count for this commit", found, err)
	}

	// Now the bot has reviewed this commit, on a colleague's machine.
	g = newFakeGitHub(t).installed(42)
	g.handle("GET /repos/{owner}/{repo}/pulls/{n}/reviews", func(w http.ResponseWriter, r *http.Request) {
		g.reply(w, []map[string]any{
			{"commit_id": head, "submitted_at": "2026-07-29T12:00:00Z",
				"user": map[string]any{"login": "kunai[bot]", "type": "Bot"}},
		})
	})
	when, found, err := g.start().ReviewedAt(ctx, repo, 128, head)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v; the bot's review of this commit should count", found, err)
	}
	if when.Hour() != 12 {
		t.Errorf("reported %v, want the submission time", when)
	}
}

// Whether a PR is from a fork decides the agent's toolset, so it is read from the
// head REPOSITORY rather than the author: a maintainer can open a PR from their
// own fork, and a stranger cannot push a branch to the base repo.
func TestFromForkReadsTheHeadRepository(t *testing.T) {
	base := Repo{Owner: "lyzr", Name: "kunai"}
	repoNamed := func(full string) PullRequest {
		var pr PullRequest
		pr.Head.Repo = &struct {
			FullName string `json:"full_name"`
		}{FullName: full}
		return pr
	}

	if repoNamed("lyzr/kunai").FromFork(base) {
		t.Error("a branch on the base repo was treated as a fork")
	}
	if repoNamed("LYZR/KUNAI").FromFork(base) {
		t.Error("the repository comparison should not be case-sensitive")
	}
	if !repoNamed("stranger/kunai").FromFork(base) {
		t.Error("a fork was treated as trusted, which would hand it Bash")
	}
	// A head repo GitHub declines to name (a deleted fork) is treated as a fork:
	// the safe reading of "unknown" is "not ours".
	if !(PullRequest{}).FromFork(base) {
		t.Error("an unknown head repository must be treated as a fork")
	}
}
