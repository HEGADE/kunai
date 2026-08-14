package server

// Driving a review through its phases.
//
// internal/review owns what to ask and how to read the answer; this file owns
// the turning of the handle. Each time the session finishes a turn, the answer
// goes to the Run, the Run says what to ask next, and that goes back to the
// session. When it says there is nothing left to ask, the findings are placed
// against the diff and saved.
//
// Two things here are load-bearing and neither is obvious.
//
// The hook fires at the end of EVERY turn, including turns a person typed. A
// review is a conversation you can argue with, so the moment the phases are
// finished the driver has to stop consuming answers, or asking the reviewer a
// follow-up question would be read as a malformed phase reply and would replace
// a perfectly good draft with a parse error. That was already true of the
// single-phase version and was already a bug; with phases it would be worse,
// because a chat reply could advance a phase.
//
// And the run is held in memory only. A review's phases are meaningless without
// the session that is executing them, and that session does not survive a
// restart either, so persisting the progression would only create a state that
// can never be resumed. What IS persisted is the outcome: the draft, the
// findings that were refuted, and how far it got.

import (
	"log"
	"sync"

	"github.com/hegade/kunai/internal/ghapp"
	"github.com/hegade/kunai/internal/review"
)

// reviewRun is one review in flight: the phase machine plus the diff its
// findings will eventually be placed against.
type reviewRun struct {
	mu    sync.Mutex
	run   *review.Run
	files []ghapp.FileDiff
}

// reviewRunners holds the reviews currently working, by session id.
type reviewRunners struct {
	mu   sync.Mutex
	runs map[string]*reviewRun
}

func newReviewRunners() *reviewRunners {
	return &reviewRunners{runs: map[string]*reviewRun{}}
}

func (r *reviewRunners) put(sessionID string, run *reviewRun) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs[sessionID] = run
}

func (r *reviewRunners) get(sessionID string) (*reviewRun, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[sessionID]
	return run, ok
}

// drop forgets a finished review, so a long-lived server does not accumulate
// one of these per pull request it has ever looked at.
func (r *reviewRunners) drop(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.runs, sessionID)
}

// advanceReview feeds one turn's answer to the phase machine and asks whatever
// comes next. Called on its own goroutine by the session's answer hook.
func (s *Server) advanceReview(sessionID, text string) {
	holder, ok := s.reviewRuns.get(sessionID)
	if !ok {
		// Not a review, or one whose phases are already finished and which is now
		// an ordinary conversation. Either way there is nothing to advance, and
		// saying nothing is the whole point: see the note at the top of the file.
		return
	}

	holder.mu.Lock()
	defer holder.mu.Unlock()

	if err := holder.run.Accept(text); err != nil {
		// Only the find phase reports an error, and it means the review produced
		// nothing readable. Recorded so the view can say what happened rather
		// than showing an empty draft for ever.
		s.prReviews.update(sessionID, func(rec *prReview) {
			rec.ParseError = err.Error()
			rec.Phase = string(review.PhaseDone)
		})
		s.reviewRuns.drop(sessionID)
		log.Printf("pr review: session %s produced no usable review block: %v", sessionID, err)
		return
	}

	prompt, brief, more := holder.run.Next()
	s.prReviews.update(sessionID, func(rec *prReview) {
		rec.Phase = string(holder.run.Phase)
	})

	if !more {
		s.finishReview(sessionID, holder)
		return
	}

	sess, live := s.mgr.Get(sessionID)
	if !live {
		// The session was closed mid-review. Whatever was found so far is still
		// worth keeping, so it is saved rather than thrown away.
		log.Printf("pr review: session %s went away during the %s phase", sessionID, holder.run.Phase)
		s.finishReview(sessionID, holder)
		return
	}
	if err := sess.PromptBrief(prompt, brief); err != nil {
		log.Printf("pr review: session %s could not start the %s phase: %v", sessionID, holder.run.Phase, err)
		s.finishReview(sessionID, holder)
	}
}

// finishReview places the findings against the diff and records the outcome.
// Called with holder.mu held.
func (s *Server) finishReview(sessionID string, holder *reviewRun) {
	draft := holder.run.Draft()
	dropped := holder.run.Dropped

	// What each finding quotes, captured HERE because here is the only place the
	// diff that was actually read is still in hand. It is what lets the review be
	// posted after somebody pushes: the comment is re-attached to the code rather
	// than to the line number, which is the only part a push invalidates. See
	// internal/review/reanchor.go.
	files := toReviewFiles(holder.files)
	for i := range draft.Findings {
		draft.Findings[i].Quote = review.Quote(files, draft.Findings[i])
	}

	plan := review.Build(draft, review.ParseDiff(files))
	total, inline, summary := plan.Counts()

	s.prReviews.update(sessionID, func(rec *prReview) {
		rec.Draft, rec.ParseError = &draft, ""
		rec.Dropped = dropped
		rec.Phase = string(review.PhaseDone)
	})
	s.reviewRuns.drop(sessionID)

	blocker, major, minor := draft.Counts()
	log.Printf("pr review: session %s drafted %d finding(s) (%d blocker, %d major, %d minor), %d inline and %d in the summary; %d refuted",
		sessionID, total, blocker, major, minor, inline, summary, len(dropped))
}
