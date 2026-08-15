package server

// Picking a review up where it stopped.
//
// The measurement that made this worth building, from one evening on one pull
// request: $45.72 across four attempts at #7, of which $20.77 bought nothing at
// all. Every interruption -- a permission ask nobody was there to answer, a
// restart, somebody pressing stop -- meant starting again from the first phase,
// so the survey and the whole find phase were paid for twice and the second
// reading was no better than the first.
//
// Nothing about that was necessary. The phase machine is a pure reducer (a run
// is its phase plus what the phases before it produced), the survey and the
// candidates are on the record, and the checkout can be made again from the
// commit that was read. So a review that stopped in `verify` resumes by asking
// exactly one question -- check these claims -- in a session that never needed
// the find phase's context anyway. On the numbers above that is ~$11 instead of
// ~$25, and the survey and find work is kept rather than repeated.
//
// It is deliberately a BUTTON and not something kunai does by itself on boot.
// A resumed review spends real money without anybody watching, and a review that
// stopped may have stopped because it was going wrong.

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/hegade/kunai/internal/ghapp"
	"github.com/hegade/kunai/internal/review"
	"github.com/hegade/kunai/internal/session"
)

func (s *Server) handleResumeReview(w http.ResponseWriter, r *http.Request) {
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
	if rec.Posted() {
		writeErr(w, http.StatusConflict, "this review has already been posted")
		return
	}
	if rec.Draft != nil {
		writeErr(w, http.StatusConflict, "this review already reached an answer; review the pull request again for a fresh reading")
		return
	}
	if !review.Resumable(review.Phase(rec.Phase)) {
		writeErr(w, http.StatusConflict, "there is nothing left to ask of this review")
		return
	}
	if _, running := s.reviewRuns.get(id); running {
		// Already going. Answering yes is right: the button and the review can
		// race, and what the caller wants is a review that is running.
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "phase": rec.Phase})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	// The change itself, needed to place findings and to quote them later. From
	// the cache when it is warm, which it will be for a review resumed soon after
	// it stopped.
	repo := ghapp.Repo{Owner: rec.Owner, Name: rec.Repo}
	files := s.filesForResume(ctx, rec)
	if len(files) == 0 {
		writeErr(w, http.StatusBadGateway, "could not read the pull request's files again, so there is nothing to resume against")
		return
	}

	dir, err := s.reviewWorktree(rec)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	// The diffs are written again, because the checkout they lived in may have
	// been swept: they are files inside the worktree, not state of their own, and
	// a resumed review that cannot open the diff it is reviewing is not a resumed
	// review. Cheap, deterministic, and it regenerates the file summaries too.
	diff, err := writeDiff(dir, rec.Number, files)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not write the diff into the checkout again: "+err.Error())
		return
	}

	run := review.Resumed(
		review.Request{
			Repo: repo.String(), Number: rec.Number, Title: rec.Title,
			BaseRef: rec.BaseRef, HeadSHA: rec.HeadSHA, FromFork: rec.FromFork,
			DiffPath: diff.Whole, DiffDir: diff.Dir, Files: diff.Files,
		},
		review.Phase(rec.Phase), surveyOf(rec), rec.Candidates, rec.Summary, rec.Dropped,
	)

	acct := rec.CLI
	if acct == "" {
		acct = s.reviewCfg.get().CLI
	}
	cli := s.resolveCLI(acct)
	spawn := session.CreateOptions{
		Cwd:     dir,
		Title:   reviewTitle(rec),
		Model:   s.reviewModel(),
		Effort:  s.effort(),
		CLIName: cli.Name, Bin: cli.Bin, Env: cli.effectiveEnv(),
		Mode:            session.LoopPermissionMode,
		DisallowedTools: reviewToolset,
		Unattended:      reviewReadable,
		ToolsOwner:      reviewToolsOwner,
	}

	// The review's own session, brought back if it went with the interruption.
	// Resumed from its transcript so the conversation is continuous, which is
	// what makes asking it something afterwards worth anything.
	sess, live := s.mgr.Get(id)
	if !live {
		opts := spawn
		opts.Resume = id
		cfg := cli.configDir()
		opts.Seed, opts.HistBefore = loadTranscriptSeed(cfg, id)
		opts.ContextTokens, opts.Overhead = loadTranscriptContextTokens(cfg, id)
		sess, err = s.mgr.Create(ctx, opts)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.armSession(sess)
	}

	holder := &reviewRun{run: run, files: files, owner: id, worktree: dir, spawn: spawn}
	s.reviewRuns.put(id, holder)
	s.prReviews.update(id, func(p *prReview) {
		p.Worktree = dir
		p.Phase = string(run.Phase)
		p.beganPhase(p.Phase, time.Now())
	})

	prompt, brief, more := run.Next()
	if !more {
		s.reviewRuns.drop(id)
		writeErr(w, http.StatusConflict, "there is nothing left to ask of this review")
		return
	}
	// Verification gets a session of its own, exactly as it does in a review that
	// never stopped: it is the phase that must not inherit the reasoning it is
	// checking, and resuming is no reason to give it that.
	if run.Phase == review.PhaseVerify && s.startVerifySession(holder, prompt, brief) {
		log.Printf("pr review: %s resumed at %s in a session of its own", id, run.Phase)
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "phase": string(run.Phase)})
		return
	}
	if err := sess.PromptBrief(prompt, brief); err != nil {
		s.reviewRuns.drop(id)
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("pr review: %s resumed at %s", id, run.Phase)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "phase": string(run.Phase)})
}

// filesForResume is the change under review, read again.
//
// From the diff cache when it is warm and from GitHub when it is not, keyed by
// the commit that was READ rather than by the head: a resume continues the
// reading that was interrupted, so it must be about the same commit, even if the
// branch has moved since.
func (s *Server) filesForResume(ctx context.Context, rec prReview) []ghapp.FileDiff {
	app, err := s.githubApp()
	if err != nil {
		return nil
	}
	files, err := app.PullRequestFiles(ctx, ghapp.Repo{Owner: rec.Owner, Name: rec.Repo}, rec.Number)
	if err != nil {
		return nil
	}
	return files
}

// surveyOf is the recorded survey, or an empty one. A review that skipped the
// survey resumes without one, which is the same thing it ran with.
func surveyOf(rec prReview) review.Survey {
	if rec.Survey == nil {
		return review.Survey{}
	}
	return *rec.Survey
}
