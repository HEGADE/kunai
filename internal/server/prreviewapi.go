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
	"strings"
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
	// Review is what THIS machine holds for this pull request, if anything.
	//
	// Distinct from ReviewedAt, which comes from GitHub and only knows about
	// reviews that were POSTED, possibly by a colleague. This is the local one:
	// running, drafted and waiting to be read, or finished. Without it the row
	// knew about a review only while the tab that started it stayed open, so a
	// refresh offered "Review" again on a pull request that already had one.
	Review *prReviewRef `json:"review,omitempty"`
}

// prReviewRef is the local review behind a row, in the shape the row needs.
type prReviewRef struct {
	SessionID string `json:"session_id"`
	Phase     string `json:"phase"`
	// Running is true while the review is still working on its answer, which is
	// the same question startReview asks before joining one.
	Running  bool `json:"running"`
	Findings int  `json:"findings"`
	Posted   bool `json:"posted"`
	Failed   bool `json:"failed"`
	// Stale is true when the review read a commit that is no longer the head, so
	// the row can offer a fresh reading rather than the old draft.
	Stale bool `json:"stale"`
}

// handleGitHubStatus reports whether this machine can act as the App. It never
// returns the key, or anything derived from it.
//
// `?check=1` additionally asks GitHub whether the App still works and where it
// is installed. Off by default because this endpoint is polled by the dashboard
// and the check costs two round trips to github.com; the settings screen, which
// is where somebody is actually looking at this, asks for it.
func (s *Server) handleGitHubStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"configured": githubConfigured(s.cfg.DataDir)}
	app, err := s.githubApp()
	if err == nil {
		out["app_id"] = app.AppID()
	}
	if err == nil && r.URL.Query().Get("check") != "" {
		if creds, cErr := loadGitHubCredentials(s.cfg.DataDir); cErr == nil {
			check, vErr := verifyGitHubApp(r.Context(), creds)
			if vErr != nil {
				// Credentials that were fine when they were saved and are not any
				// more: a revoked key, a deleted App. Reported as the reason
				// rather than as a bare "not configured", which would send
				// somebody to re-paste a key that was never the problem.
				out["error"] = vErr.Error()
			} else {
				out["check"] = check
			}
		}
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
	// Checked against GitHub BEFORE anything is written, so a mismatched pair is
	// refused while the two fields somebody just pasted are still on screen. The
	// old behaviour was to check only that the PEM parsed, which meant a key from
	// a different App saved cleanly, reported "Configured", and failed days later
	// as an unexplained error on the dashboard.
	creds, err := ghapp.LoadCredentials(req.AppID, []byte(req.PrivateKey))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	check, err := verifyGitHubApp(r.Context(), creds)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := saveGitHubCredentials(s.cfg.DataDir, req.AppID, req.PrivateKey); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": true, "app_id": req.AppID, "check": check})
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
	// What this machine already holds, folded in after the GitHub calls because
	// it is a local map lookup and must not be inside the concurrent fan-out.
	for i := range out {
		out[i].Review = s.reviewRefFor(repo, out[i].Number, out[i].HeadSHA)
	}
	writeJSON(w, http.StatusOK, out)
}

// reviewRefFor is the local review behind one row, or nil when there is none.
//
// head is the pull request's CURRENT head, so a review read at an earlier commit
// reports itself stale and the row can offer a fresh reading. That matters more
// than it sounds: a stale draft still posts (its comments are re-anchored, see
// reanchor.go), but somebody looking at the dashboard should be able to see that
// the branch moved on without opening it.
func (s *Server) reviewRefFor(repo ghapp.Repo, number int, head string) *prReviewRef {
	if s.prReviews == nil {
		return nil
	}
	rec, ok := s.prReviews.latestFor(repo.Owner, repo.Name, number)
	if !ok {
		return nil
	}
	ref := &prReviewRef{
		SessionID: rec.SessionID,
		Phase:     rec.Phase,
		Running:   reviewInFlight(rec),
		Posted:    rec.Posted(),
		Failed:    rec.ParseError != "",
		Stale:     head != "" && rec.HeadSHA != "" && !strings.EqualFold(head, rec.HeadSHA),
	}
	if rec.Draft != nil {
		ref.Findings = len(rec.Draft.Findings)
	}
	return ref
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
		// Whether there is a survey step at all. A small change skips it, and a
		// progress display cannot work that out for itself once the review has
		// moved on.
		"surveyed": rec.Surveyed,
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
