package ghapp

// Posting a review.
//
// The one call in this package that writes anything, and the one with a failure
// mode worth designing around: GitHub validates every inline comment and rejects
// the WHOLE review if any single one names a line that is not part of the diff.
// A review that cost real quota is then lost to one bad line number.
//
// Anchoring is checked before we get here (internal/review decides what may be
// inline), so this layer's job is to send exactly what was decided and to make a
// rejection legible when it happens anyway.

import (
	"context"
	"fmt"
	"net/http"
)

// ReviewComment is one inline comment in a submitted review.
type ReviewComment struct {
	Path string `json:"path"`
	// Line is the last line of the range, which is GitHub's convention: a
	// multi-line comment is start_line..line, and `line` alone is a single line.
	Line int    `json:"line"`
	Side string `json:"side,omitempty"`
	// StartLine and StartSide are set only for a multi-line comment. Both must be
	// omitted otherwise, hence the pointers: a zero start_line is rejected.
	StartLine *int   `json:"start_line,omitempty"`
	StartSide string `json:"start_side,omitempty"`
	Body      string `json:"body"`
}

// ReviewRequest is a review to post.
type ReviewRequest struct {
	// CommitID pins the review to the commit that was actually read. Without it
	// GitHub attaches the review to whatever the head is now, which after a push
	// means commenting on code nobody reviewed.
	CommitID string          `json:"commit_id"`
	Body     string          `json:"body"`
	Event    string          `json:"event"`
	Comments []ReviewComment `json:"comments,omitempty"`
}

// EventComment is the only review event kunai ever sends. Approving or requesting
// changes are decisions with consequences for merging, and an agent does not get
// to make them: the review says what it found and a person decides what that
// means.
const EventComment = "COMMENT"

// SubmittedReview is what GitHub returns once a review is posted.
type SubmittedReview struct {
	ID      int64  `json:"id"`
	HTMLURL string `json:"html_url"`
	State   string `json:"state"`
}

// SubmitReview posts a review to a pull request as the installation.
func (a *App) SubmitReview(ctx context.Context, repo Repo, number int, req ReviewRequest) (SubmittedReview, error) {
	token, err := a.repoToken(ctx, repo)
	if err != nil {
		return SubmittedReview{}, err
	}
	// Guarded rather than defaulted: a caller that forgot to set the event is a
	// bug, and quietly turning it into an approval would be the worst possible
	// recovery.
	if req.Event != EventComment {
		return SubmittedReview{}, fmt.Errorf("kunai only posts %s reviews, not %q", EventComment, req.Event)
	}
	if req.CommitID == "" {
		return SubmittedReview{}, fmt.Errorf("a review must name the commit it reviewed")
	}

	var out SubmittedReview
	path := repo.path("/pulls/%d/reviews", number)
	if err := a.doAuthed(ctx, http.MethodPost, path, authInstallation, token, req, &out); err != nil {
		return SubmittedReview{}, fmt.Errorf("could not post the review to %s#%d: %w", repo, number, err)
	}
	return out, nil
}

// PullRequestFiles lists the changed files and their patches, which is both what
// the agent reviews and what decides where a comment may be anchored.
//
// Paginated properly, unlike the pull-request list: a large refactor really does
// touch more than a page of files, and a truncated diff would silently review only
// part of the change while claiming to have reviewed it.
func (a *App) PullRequestFiles(ctx context.Context, repo Repo, number int) ([]FileDiff, error) {
	token, err := a.repoToken(ctx, repo)
	if err != nil {
		return nil, err
	}
	var all []FileDiff
	for page := 1; page <= maxFilePages; page++ {
		var batch []FileDiff
		path := repo.path("/pulls/%d/files?per_page=100&page=%d", number, page)
		if err := a.doAuthed(ctx, http.MethodGet, path, authInstallation, token, nil, &batch); err != nil {
			return nil, fmt.Errorf("could not read the files changed in %s#%d: %w", repo, number, err)
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			return all, nil
		}
	}
	// Hit the ceiling: better to say so than to review three thousand files.
	return all, fmt.Errorf("%s#%d changes more files than kunai will review in one pass", repo, number)
}

// maxFilePages bounds the paging at GitHub's own limit for this endpoint (3000
// files), so a pathological pull request cannot spin here.
const maxFilePages = 30

// FileDiff is one changed file. Mirrors internal/review.FileDiff deliberately
// rather than importing it: this package is the wire format and that one is the
// logic, and coupling them would make the review rules depend on the API's shape.
type FileDiff struct {
	Filename string `json:"filename"`
	Status   string `json:"status"`
	Patch    string `json:"patch"`
	// Additions and Deletions size one file. Unlike the pull request's own
	// totals these DO come back on this endpoint, and they are what lets the
	// prompt tell the model which files are worth opening first.
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
}
