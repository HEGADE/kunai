package server

// The HTTP surface for pull-request review.
//
// Five routes, all owner-only. None of them is registered on the share gate:
// these read a machine's repositories and can write to GitHub as the bot, which
// is the opposite of what a public share link should reach.

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/hegade/kunai/internal/ghapp"
	"github.com/hegade/kunai/internal/review"
)

// prSummary is one pull request as the dashboard card shows it.
type prSummary struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	Author    string `json:"author"`
	BaseRef   string `json:"base_ref"`
	HeadSHA   string `json:"head_sha"`
	Draft     bool   `json:"draft"`
	FromFork  bool   `json:"from_fork"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	// ReviewedAt is when kunai last reviewed THIS commit, which is what turns the
	// Review button into "reviewed 2h ago". Zero when it has not been.
	ReviewedAt *time.Time `json:"reviewed_at,omitempty"`
}

// handleGitHubStatus reports whether this machine can act as the App. It never
// returns the key, or anything derived from it.
func (s *Server) handleGitHubStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"configured": githubConfigured(s.cfg.DataDir)}
	if app, err := s.githubApp(); err == nil {
		out["app_id"] = app.AppID()
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSetGitHubApp saves this machine's App id and private key, or clears them.
func (s *Server) handleSetGitHubApp(w http.ResponseWriter, r *http.Request) {
	if s.cfg.DataDir == "" {
		writeErr(w, http.StatusServiceUnavailable, "no data dir configured")
		return
	}
	var req struct {
		AppID      string `json:"app_id"`
		PrivateKey string `json:"private_key"`
		Clear      bool   `json:"clear"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}

	s.ghMu.Lock()
	s.gh = nil // rebuilt from disk on the next use, so a replaced key takes effect
	s.ghMu.Unlock()

	if req.Clear {
		clearGitHubCredentials(s.cfg.DataDir)
		writeJSON(w, http.StatusOK, map[string]any{"configured": false})
		return
	}
	if err := saveGitHubCredentials(s.cfg.DataDir, req.AppID, req.PrivateKey); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": true})
}

// handlePullRequests lists the open pull requests on a repository this machine
// has checked out. `?repo=<path>` is a local directory, which is the constraint
// the whole feature rests on: review needs a real tree to read.
func (s *Server) handlePullRequests(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("repo")
	if dir == "" {
		writeErr(w, http.StatusBadRequest, "repo is required")
		return
	}
	app, err := s.githubApp()
	if err != nil {
		writeErr(w, http.StatusPreconditionFailed, err.Error())
		return
	}
	repo, err := s.repoAt(dir)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	prs, err := app.OpenPullRequests(ctx, repo)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}

	out := enrich(ctx, app, repo, prs)
	writeJSON(w, http.StatusOK, out)
}

// prDetailWorkers bounds how many pull requests are looked up at once. Each one
// costs two GitHub calls, so a repository with thirty open pull requests would
// otherwise open sixty connections at a stroke and spend its rate limit on a
// dashboard nobody is reading yet.
const prDetailWorkers = 6

// enrich fills in what the list endpoint does not carry.
//
// Two calls per pull request, and both are needed. GitHub's LIST endpoint omits
// additions and deletions entirely (they are only on the single-pull-request
// endpoint), so a row built from the list alone reports every change as +0 -0.
// And whether kunai has already reviewed this commit lives in the reviews
// endpoint, which is what turns the button into "reviewed 2h ago" instead of
// spending somebody's quota on a review that already exists.
//
// Run concurrently because they are independent: sequentially, a repository with
// ten open pull requests made twenty round trips to github.com before the card
// could paint.
func enrich(ctx context.Context, app *ghapp.App, repo ghapp.Repo, prs []ghapp.PullRequest) []prSummary {
	out := make([]prSummary, len(prs))
	sem := make(chan struct{}, prDetailWorkers)
	var wg sync.WaitGroup

	for i, pr := range prs {
		wg.Add(1)
		go func(i int, pr ghapp.PullRequest) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			item := prSummary{
				Number: pr.Number, Title: pr.Title, Author: pr.User.Login,
				BaseRef: pr.Base.Ref, HeadSHA: pr.Head.SHA, Draft: pr.Draft,
				FromFork: pr.FromFork(repo),
			}
			// A detail lookup that fails leaves the size unknown rather than taking
			// the row down: knowing a pull request exists is worth more than knowing
			// how big it is.
			if full, err := app.PullRequest(ctx, repo, pr.Number); err == nil {
				item.Additions, item.Deletions = full.Additions, full.Deletions
			}
			if when, found, err := app.ReviewedAt(ctx, repo, pr.Number, pr.Head.SHA); err == nil && found {
				item.ReviewedAt = &when
			}
			out[i] = item
		}(i, pr)
	}
	wg.Wait()
	return out
}

// handleStartReview begins a review. It answers with the new session, which the
// client opens: from that moment it is an ordinary session in the sidebar.
func (s *Server) handleStartReview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Repo      string `json:"repo"`
		Number    int    `json:"number"`
		Requester string `json:"requester"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Repo == "" || req.Number <= 0 {
		writeErr(w, http.StatusBadRequest, "repo and number are required")
		return
	}

	// Generous, and not tied to the request: fetching a large pull request and
	// booting a session takes real time, and the CLI's lifetime must never be
	// bound to an HTTP request context.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	sess, err := s.startReview(ctx, req.Repo, req.Number, req.Requester)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, sess.Meta())
}

// handleReviewDraft returns what a review session produced, so the draft card can
// render it and survive a reload.
func (s *Server) handleReviewDraft(w http.ResponseWriter, r *http.Request) {
	if s.prReviews == nil {
		writeErr(w, http.StatusNotFound, "not a review")
		return
	}
	rec, ok := s.prReviews.get(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "not a review")
		return
	}

	out := map[string]any{
		"owner": rec.Owner, "repo": rec.Repo, "number": rec.Number, "title": rec.Title,
		"head_sha": rec.HeadSHA, "from_fork": rec.FromFork, "requester": rec.Requester,
		"posted_url": rec.PostedURL, "parse_error": rec.ParseError,
		// How far the review has got. A phased review takes longer than the
		// single-shot one did, so "Reviewing 4m" with nothing else to say reads
		// as a hang; naming the phase is what makes the wait legible.
		"phase": rec.Phase,
		// What verification refuted, with its reasons. Shown so the filtering can
		// be audited: three findings from a reviewer that dropped four is a
		// different thing from three findings from a reviewer that found three,
		// and only this can tell them apart.
		"dropped": droppedRows(rec.Dropped),
	}
	// Placement is recomputed here rather than stored, because it depends on the
	// diff and the diff is the thing that changes. The counts the card promises
	// are then always about the pull request as it is now.
	if rec.Draft != nil {
		plan, files := s.planFor(r.Context(), rec)
		out["summary"] = plan.Summary
		out["findings"] = placements(plan, files)
		total, inline, summary := plan.Counts()
		out["total"], out["inline"], out["summary_count"] = total, inline, summary
	}
	writeJSON(w, http.StatusOK, out)
}

// droppedRows is the wire shape of the refuted candidates. Deliberately thinner
// than a finding: these are not going to be posted and cannot be un-dropped, so
// what a reader needs is the claim, where it was, and why it did not survive.
func droppedRows(dropped []review.Dropped) []map[string]any {
	out := make([]map[string]any, 0, len(dropped))
	for _, d := range dropped {
		out = append(out, map[string]any{
			"file":     d.Finding.File,
			"line":     d.Finding.Line,
			"title":    d.Finding.Title,
			"severity": string(d.Finding.Severity),
			"why":      d.Why,
		})
	}
	return out
}

// handlePostReview sends the draft to GitHub.
func (s *Server) handlePostReview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Keep    []int        `json:"keep"`
		Edits   []reviewEdit `json:"edits"`
		Summary string       `json:"summary"`
	}
	// A body is optional: posting everything is the common case.
	_ = json.NewDecoder(r.Body).Decode(&req)

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	posted, err := s.postReview(ctx, r.PathValue("id"), req.Keep, req.Edits, req.Summary)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": posted.HTMLURL})
}

// planFor decides placement against the pull request's current diff, falling back
// to placement with no diff (everything demoted to the summary) when GitHub
// cannot be reached, so the card still renders offline.
// Returns the changed files alongside the plan, because each finding is served
// with the diff lines it is about and those come from the same fetch.
func (s *Server) planFor(ctx context.Context, rec prReview) (review.Plan, []review.FileDiff) {
	app, err := s.githubApp()
	if err != nil {
		return review.Build(*rec.Draft, nil), nil
	}
	files, err := app.PullRequestFiles(ctx, ghapp.Repo{Owner: rec.Owner, Name: rec.Repo}, rec.Number)
	if err != nil {
		return review.Build(*rec.Draft, nil), nil
	}
	rf := toReviewFiles(files)
	return review.Build(*rec.Draft, review.ParseDiff(rf)), rf
}

// placements is the wire shape of the draft card's rows: the finding, and where
// it will land. The badge is the point, so it is computed here rather than
// re-derived by the client from rules it would have to keep in step.
func placements(plan review.Plan, files []review.FileDiff) []map[string]any {
	out := make([]map[string]any, 0, len(plan.Placements))
	for i, pl := range plan.Placements {
		out = append(out, map[string]any{
			"index": i,
			// The diff lines this finding is about, so a card carries its own
			// evidence. A claim with a file and a number attached is not something
			// anybody can judge without going to look it up, and sending them
			// elsewhere to look is how a review becomes a chore.
			"hunk":       review.HunkFor(files, pl.Finding),
			"file":     pl.Finding.File,
			"line":     pl.Finding.Line,
			"end_line": pl.Finding.EndLine,
			"side":     pl.Finding.Side,
			"title":    pl.Finding.Title,
			"body":     pl.Finding.Body,
			// How bad if true, and how sure it is true. Two fields rather than
			// one score, for the reason severity.go gives: they answer different
			// questions and a single number can only lie about one of them.
			"severity":   string(pl.Finding.Severity),
			"confidence": string(pl.Finding.Confidence),
			"category":   pl.Finding.Category,
			"evidence":   pl.Finding.Evidence,
			"verified":   pl.Finding.Verified,
			"suggestion": pl.Finding.Suggestion,
			"inline":     pl.Inline,
			"why":        pl.Why,
		})
	}
	return out
}
