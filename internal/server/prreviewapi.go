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

	out := make([]prSummary, 0, len(prs))
	for _, pr := range prs {
		item := prSummary{
			Number: pr.Number, Title: pr.Title, Author: pr.User.Login,
			BaseRef: pr.Base.Ref, HeadSHA: pr.Head.SHA, Draft: pr.Draft,
			FromFork: pr.FromFork(repo), Additions: pr.Additions, Deletions: pr.Deletions,
		}
		// Asked per pull request rather than in bulk because GitHub offers no bulk
		// form, and it is what lets the row say "reviewed 2h ago" instead of
		// spending somebody's quota on a review that already exists.
		if when, found, err := app.ReviewedAt(ctx, repo, pr.Number, pr.Head.SHA); err == nil && found {
			item.ReviewedAt = &when
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, out)
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
	}
	// Placement is recomputed here rather than stored, because it depends on the
	// diff and the diff is the thing that changes. The counts the card promises
	// are then always about the pull request as it is now.
	if rec.Draft != nil {
		plan := s.planFor(r.Context(), rec)
		out["summary"] = plan.Summary
		out["findings"] = placements(plan)
		total, inline, summary := plan.Counts()
		out["total"], out["inline"], out["summary_count"] = total, inline, summary
	}
	writeJSON(w, http.StatusOK, out)
}

// handlePostReview sends the draft to GitHub.
func (s *Server) handlePostReview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Keep []int `json:"keep"`
	}
	// A body is optional: posting everything is the common case.
	_ = json.NewDecoder(r.Body).Decode(&req)

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	posted, err := s.postReview(ctx, r.PathValue("id"), req.Keep)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": posted.HTMLURL})
}

// planFor decides placement against the pull request's current diff, falling back
// to placement with no diff (everything demoted to the summary) when GitHub
// cannot be reached, so the card still renders offline.
func (s *Server) planFor(ctx context.Context, rec prReview) review.Plan {
	app, err := s.githubApp()
	if err != nil {
		return review.Build(*rec.Draft, nil)
	}
	files, err := app.PullRequestFiles(ctx, ghapp.Repo{Owner: rec.Owner, Name: rec.Repo}, rec.Number)
	if err != nil {
		return review.Build(*rec.Draft, nil)
	}
	return review.Build(*rec.Draft, review.ParseDiff(toReviewFiles(files)))
}

// placements is the wire shape of the draft card's rows: the finding, and where
// it will land. The badge is the point, so it is computed here rather than
// re-derived by the client from rules it would have to keep in step.
func placements(plan review.Plan) []map[string]any {
	out := make([]map[string]any, 0, len(plan.Placements))
	for i, pl := range plan.Placements {
		out = append(out, map[string]any{
			"index":      i,
			"file":       pl.Finding.File,
			"line":       pl.Finding.Line,
			"end_line":   pl.Finding.EndLine,
			"side":       pl.Finding.Side,
			"title":      pl.Finding.Title,
			"body":       pl.Finding.Body,
			"suggestion": pl.Finding.Suggestion,
			"inline":     pl.Inline,
			"why":        pl.Why,
		})
	}
	return out
}
