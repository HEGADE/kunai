package server

// Asking a review something after it has finished.
//
// A review's session ends -- you press Done, or kunai restarts -- and the draft
// deliberately outlives it: the findings are on disk, the view reopens on them,
// and posting still works. What did NOT survive is the ability to say anything
// back to the reviewer, and that is the one thing this feature has that a CI
// reviewer does not. Ask opened a transcript with a dead composer, and the
// Reopen offered underneath it failed with "cannot tell which folder this
// session ran in", because a review runs in a throwaway checkout that is swept
// when it ends and the transcript's own cwd no longer exists.
//
// Everything needed to put it back is on the record. The checkout is derived
// (repository plus the pull request number plus the commit that was READ, which
// is exactly what makes the conversation still make sense), and a resumed
// session KEEPS ITS ID (manager.go: `id := opts.Resume`), so the review record
// needs no rekeying and the draft, the verdicts and the posted URL all still
// point at the same place.
//
// It comes back under the same restrictions it ran under. A review is allowed to
// run unattended on somebody else's branch because Write, Edit and Bash are
// withheld; a reopened one that quietly had them back would be a different thing
// wearing the same name.

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/hegade/kunai/internal/session"
	"github.com/hegade/kunai/internal/worktree"
)

// handleReopenReview brings a finished review's session back, resumed from its
// own transcript, and answers with the session to attach to.
func (s *Server) handleReopenReview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.prReviews == nil {
		writeErr(w, http.StatusNotFound, "this session is not a pull-request review")
		return
	}
	rec, ok := s.prReviews.get(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "this session is not a pull-request review")
		return
	}
	// Already live: the answer is the session you already have. Not an error,
	// because two clients can ask at once and a review that is still running is
	// the ordinary case.
	if _, live := s.mgr.Get(id); live {
		writeJSON(w, http.StatusOK, map[string]string{"id": id})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	dir, err := s.reviewWorktree(rec)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	// The account and toolset the review ran under, not the machine's defaults.
	// Its transcript lives in that account's config dir, so getting this wrong
	// resumes an empty conversation rather than the one being asked about. The
	// record carries the account for reviews run since it started doing so; an
	// older one falls back to the configured reviewing account, which is what it
	// would have used.
	acct := rec.CLI
	if acct == "" {
		acct = s.reviewCfg.get().CLI
	}
	cli := s.resolveCLI(acct)
	opts := session.CreateOptions{
		Cwd:     dir,
		Title:   reviewTitle(rec),
		Model:   s.reviewModel(),
		Effort:  s.effort(),
		Resume:  id,
		CLIName: cli.Name, Bin: cli.Bin, Env: cli.effectiveEnv(),
		Mode:            session.LoopPermissionMode,
		DisallowedTools: reviewToolset,
		// Nobody is watching a review, so it answers its own asks rather than
		// stopping on one. See CreateOptions.Unattended.
		Unattended: reviewReadable,
		ToolsOwner: reviewToolsOwner,
	}
	cfg := cli.configDir()
	opts.Seed, opts.HistBefore = loadTranscriptSeed(cfg, id)
	opts.ContextTokens, opts.Overhead = loadTranscriptContextTokens(cfg, id)

	sess, err := s.mgr.Create(ctx, opts)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.armSession(sess)
	// The checkout is real again, so record it: the sweep looks here to know what
	// it may remove.
	s.prReviews.update(id, func(p *prReview) { p.Worktree = dir })
	writeJSON(w, http.StatusOK, map[string]string{"id": sess.ID})
}

// reviewWorktree returns the review's checkout, making it again if it has been
// swept.
//
// Deterministic from the record, which is why this is possible at all: the path
// is the review root plus the pull request number, and the content is the commit
// that was read. A review of a commit nobody can produce any more cannot be
// reopened, and says so.
func (s *Server) reviewWorktree(rec prReview) (string, error) {
	if rec.Worktree != "" {
		if st, err := os.Stat(rec.Worktree); err == nil && st.IsDir() {
			return rec.Worktree, nil
		}
	}
	if rec.RepoDir == "" {
		return "", fmt.Errorf("this review does not record which checkout it read, so it cannot be reopened")
	}
	if rec.HeadSHA == "" {
		return "", fmt.Errorf("this review does not record the commit it read, so it cannot be reopened")
	}
	checkout := func() (worktree.Info, error) {
		return worktree.CreateReview(worktree.ReviewOptions{
			Repo: rec.RepoDir, Root: s.worktreeRoot(), Name: fmt.Sprint(rec.Number), SHA: rec.HeadSHA,
		})
	}
	wt, err := checkout()
	if err != nil {
		// The commit may have been pruned since the review ran, so fetch the pull
		// request again and try once more. Only then is it genuinely gone.
		ref := fmt.Sprintf("refs/pull/%d/head", rec.Number)
		if ferr := worktree.FetchRef(rec.RepoDir, "origin", ref); ferr != nil {
			return "", fmt.Errorf("could not check out %s again: %w", rec.HeadSHA[:min(7, len(rec.HeadSHA))], err)
		}
		if wt, err = checkout(); err != nil {
			return "", fmt.Errorf("could not check out %s again: %w", rec.HeadSHA[:min(7, len(rec.HeadSHA))], err)
		}
	}
	return wt.Path, nil
}

// reviewTitle names a reopened review the same way starting one does, so the tab
// and the sidebar do not suddenly disagree about what it is.
func reviewTitle(rec prReview) string {
	return fmt.Sprintf("Review #%d %s", rec.Number, rec.Title)
}

// reviewModel is the model reviews run on, falling back to the machine's.
func (s *Server) reviewModel() string {
	if m := s.reviewCfg.get().Model; m != "" {
		return m
	}
	return s.model()
}
