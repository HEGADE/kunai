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
	"strings"

	"github.com/hegade/kunai/internal/ghapp"
	"github.com/hegade/kunai/internal/review"
)

// postReview sends a drafted review to GitHub.
//
// keep, when non-empty, is the set of finding indexes the user chose to keep, so
// pruning in the UI is honoured server-side rather than trusted to have already
// happened.
func (s *Server) postReview(ctx context.Context, sessionID string, keep []int) (ghapp.SubmittedReview, error) {
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

	// 2. Is the draft still about the code on the pull request? A push between
	// reviewing and posting moves the head, and comments anchored to the old
	// commit would land on lines nobody wrote.
	pr, err := app.PullRequest(ctx, repo, rec.Number)
	if err != nil {
		return ghapp.SubmittedReview{}, err
	}
	if pr.Head.SHA != "" && !strings.EqualFold(pr.Head.SHA, rec.HeadSHA) {
		return ghapp.SubmittedReview{}, fmt.Errorf(
			"%s#%d has moved on since this review (now %s); review it again before posting",
			repo, rec.Number, shortSHA(pr.Head.SHA))
	}

	// 3. Does every comment anchor to a line the diff actually touches? GitHub
	// rejects the WHOLE review over one bad line, so anything that cannot anchor
	// is moved into the summary rather than risking the submission.
	files, err := app.PullRequestFiles(ctx, repo, rec.Number)
	if err != nil {
		return ghapp.SubmittedReview{}, err
	}
	plan := review.Build(kept(*rec.Draft, keep), review.ParseDiff(toReviewFiles(files)))

	meta := review.Meta{Owner: rec.Owner, Repo: rec.Repo, HeadSHA: rec.HeadSHA, Requester: rec.Requester}
	req := ghapp.ReviewRequest{
		CommitID: rec.HeadSHA,
		Event:    ghapp.EventComment,
		Body:     review.Body(plan, meta),
		Comments: comments(plan),
	}
	if total, _, _ := plan.Counts(); total == 0 {
		// Finding nothing is a result worth reporting: silence and "I looked, it
		// is fine" are not the same message to the person waiting on a review.
		req.Body = review.EmptyBody(meta)
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

// kept filters a draft to the findings the user chose to keep. An empty selection
// means "all of them", so a client that does not implement pruning still posts a
// complete review rather than an empty one.
func kept(d review.Draft, keep []int) review.Draft {
	if len(keep) == 0 {
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
