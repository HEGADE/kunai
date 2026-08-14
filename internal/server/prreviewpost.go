package server

// Posting a drafted review.
//
// Deliberately a separate step from producing it, and that is the whole point of
// the feature's shape: you are about to write publicly, under an identity your
// team shares, on somebody else's pull request. Automated reviewers post because
// nobody is in the room. Somebody is here, so they read it first and drop what is
// not worth saying.
//
// Three things are checked between the click and the submission, and each one
// exists because of a way this goes wrong in practice.

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/hegade/kunai/internal/ghapp"
	"github.com/hegade/kunai/internal/review"
)

// reviewEdit is one finding as the user rewrote it before posting.
//
// Only the words may be changed, never the anchor. That is a boundary rather
// than an oversight: the file, line and side decide which line of somebody's
// pull request a comment lands on, they were derived server-side from the diff
// that was actually read, and accepting them back from the client would make
// "post this review" a way to write an arbitrary comment on an arbitrary line.
// Severity is included because it is a judgement, and disagreeing with the
// reviewer's judgement is exactly what this screen is for.
type reviewEdit struct {
	Index    int    `json:"index"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Severity string `json:"severity"`
}

// postReview sends a drafted review to GitHub.
//
// keep, when non-empty, is the set of finding indexes the user chose to keep, so
// pruning in the UI is honoured server-side rather than trusted to have already
// happened. edits and summary carry the user's rewording, for the same reason:
// what lands on GitHub is decided here, from what the client asked for, rather
// than assembled by the client and forwarded.
func (s *Server) postReview(ctx context.Context, sessionID string, keep []int, edits []reviewEdit, summary string) (ghapp.SubmittedReview, error) {
	if s.prReviews == nil {
		return ghapp.SubmittedReview{}, fmt.Errorf("no data dir configured, so reviews cannot be posted")
	}
	rec, ok := s.prReviews.get(sessionID)
	if !ok {
		return ghapp.SubmittedReview{}, fmt.Errorf("this session is not a pull-request review")
	}
	if rec.Posted() {
		// Not an error worth failing on: the user pressed Post twice, and the
		// second press should show them the review rather than a complaint.
		return ghapp.SubmittedReview{HTMLURL: rec.PostedURL}, nil
	}
	if rec.Draft == nil {
		return ghapp.SubmittedReview{}, fmt.Errorf("this review has not finished yet")
	}

	app, err := s.githubApp()
	if err != nil {
		return ghapp.SubmittedReview{}, err
	}
	repo := ghapp.Repo{Owner: rec.Owner, Name: rec.Repo}

	// 1. Has somebody else already reviewed this exact commit? Two colleagues'
	// installs cannot see each other, so GitHub is the only shared state, and this
	// is the check that stops a duplicate landing when they both clicked.
	if when, found, err := app.ReviewedAt(ctx, repo, rec.Number, rec.HeadSHA); err == nil && found {
		return ghapp.SubmittedReview{}, fmt.Errorf(
			"%s#%d was already reviewed at this commit (%s); read that review before posting another",
			repo, rec.Number, when.Local().Format("15:04"))
	}

	// 2. Has the pull request moved since it was read? Almost always this is
	// somebody pushing to an unrelated file, and it used to stop the post dead
	// with "review it again", throwing away minutes of work and dollars of tokens
	// over a commit that touched no line any finding was about.
	//
	// A finding is about a line of CODE; the line NUMBER is only how GitHub is
	// told where to put the comment, and it is the only part a push invalidates.
	// So each finding is re-attached to the text it quoted (reanchor.go), which
	// makes a rebase that shifts everything by twelve lines cost nothing at all.
	// Only a finding whose code has genuinely changed is held back, and only that
	// one: it is demoted to the summary saying so, rather than posted onto
	// whatever now occupies its old line.
	pr, err := app.PullRequest(ctx, repo, rec.Number)
	if err != nil {
		return ghapp.SubmittedReview{}, err
	}
	moved := pr.Head.SHA != "" && !strings.EqualFold(pr.Head.SHA, rec.HeadSHA)

	// 3. Does every comment anchor to a line the diff actually touches? GitHub
	// rejects the WHOLE review over one bad line, so anything that cannot anchor
	// is moved into the summary rather than risking the submission.
	files, err := app.PullRequestFiles(ctx, repo, rec.Number)
	if err != nil {
		return ghapp.SubmittedReview{}, err
	}
	draft := kept(applyEdits(*rec.Draft, edits, summary), keep)

	var rep review.ReanchorReport
	if moved {
		draft, rep = review.Reanchor(draft, toReviewFiles(files))
		log.Printf("pr review: %s#%d moved from %s to %s; %d comment(s) unchanged, %d re-attached, %d now stale",
			repo, rec.Number, shortSHA(rec.HeadSHA), shortSHA(pr.Head.SHA), rep.Unchanged, rep.Moved, rep.Stale)
	}
	plan := review.Build(draft, review.ParseDiff(toReviewFiles(files)))

	// Permalinks still point at the commit that was READ, because that is where
	// the code a finding describes actually is. The comments are placed against
	// the CURRENT head, because that is the diff their line numbers now refer to;
	// submitting an old commit id with new line numbers is how a review gets
	// rejected wholesale.
	meta := review.Meta{Owner: rec.Owner, Repo: rec.Repo, HeadSHA: rec.HeadSHA, Requester: rec.Requester}
	commit := rec.HeadSHA
	if moved && pr.Head.SHA != "" {
		commit = pr.Head.SHA
	}
	req := ghapp.ReviewRequest{
		CommitID: commit,
		Event:    ghapp.EventComment,
		Body:     review.Body(plan, meta),
		Comments: comments(plan),
	}
	if total, _, _ := plan.Counts(); total == 0 {
		// Finding nothing is a result worth reporting: silence and "I looked, it
		// is fine" are not the same message to the person waiting on a review.
		req.Body = review.EmptyBody(meta)
	}
	if moved && rep.Any() {
		// Said out loud. The author is entitled to know the reviewer read an
		// older commit, because that is the one thing that could make an
		// otherwise correct finding wrong.
		req.Body = review.MovedNote(rec.HeadSHA, rep) + "\n\n" + req.Body
	}

	posted, err := app.SubmitReview(ctx, repo, rec.Number, req)
	if err != nil {
		return ghapp.SubmittedReview{}, err
	}
	s.prReviews.update(sessionID, func(r *prReview) { r.PostedURL = posted.HTMLURL })
	return posted, nil
}

// comments turns the inline placements into GitHub's comment shape.
//
// The start of a range is a pointer because GitHub rejects a zero start_line: a
// single-line comment must omit the field entirely rather than send 0, which is
// the kind of detail that fails the whole review rather than one comment.
func comments(plan review.Plan) []ghapp.ReviewComment {
	var out []ghapp.ReviewComment
	for _, f := range plan.Inline() {
		c := ghapp.ReviewComment{
			Path: f.File,
			Line: f.LastLine(), // GitHub's `line` is the END of a range
			Side: f.Side,
			Body: review.CommentBody(f),
		}
		if f.EndLine != 0 {
			start := f.StartLine()
			c.StartLine, c.StartSide = &start, f.Side
		}
		out = append(out, c)
	}
	return out
}

// kept filters a draft to the findings the user chose to keep.
//
// nil and empty mean different things, and conflating them was a real bug: a nil
// selection is a client that does not prune, which must still post a complete
// review, while an EMPTY selection is somebody who read every finding and dropped
// them all. Treating both as "post everything" meant dropping the lot and pressing
// Post published the lot, which is the worst possible reading of that gesture.
// JSON gives us the distinction for free: an absent field decodes to nil, `[]`
// does not.
// applyEdits folds the user's rewording into the draft, by index.
//
// Applied BEFORE kept(), so the indexes an edit names are the same ones the
// selection names: both refer to positions in the draft as the client was shown
// it, and filtering first would silently shift every edit onto a neighbouring
// finding.
//
// An empty field means "unchanged" rather than "delete this", because the two
// are indistinguishable over JSON and the harmful reading is the one that
// publishes an empty comment on somebody's line.
func applyEdits(d review.Draft, edits []reviewEdit, summary string) review.Draft {
	if summary = strings.TrimSpace(summary); summary != "" {
		d.Summary = summary
	}
	if len(edits) == 0 {
		return d
	}

	byIndex := make(map[int]reviewEdit, len(edits))
	for _, e := range edits {
		byIndex[e.Index] = e
	}
	out := review.Draft{Summary: d.Summary, Findings: make([]review.Finding, len(d.Findings))}
	copy(out.Findings, d.Findings)
	for i := range out.Findings {
		e, ok := byIndex[i]
		if !ok {
			continue
		}
		if t := strings.TrimSpace(e.Title); t != "" {
			out.Findings[i].Title = t
		}
		if b := strings.TrimSpace(e.Body); b != "" {
			out.Findings[i].Body = b
		}
		if sev := strings.TrimSpace(e.Severity); sev != "" {
			out.Findings[i].Severity = review.Severity(sev)
		}
	}
	// Deliberately NOT normalised here, even though an edited severity may be
	// anything the client sent. Normalise sorts, and sorting now would move the
	// findings out from under the indexes that kept() is about to use, quietly
	// posting a different set than the one that was chosen. Build normalises at
	// the end of the chain, which repairs an unrecognised severity in time and
	// after the last thing that cares about position.
	return out
}

func kept(d review.Draft, keep []int) review.Draft {
	if keep == nil {
		return d
	}
	wanted := make(map[int]bool, len(keep))
	for _, i := range keep {
		wanted[i] = true
	}
	out := review.Draft{Summary: d.Summary}
	for i, f := range d.Findings {
		if wanted[i] {
			out.Findings = append(out.Findings, f)
		}
	}
	return out
}

func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
