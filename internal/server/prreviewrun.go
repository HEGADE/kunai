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
	"context"
	"log"
	"sync"
	"time"

	"github.com/hegade/kunai/internal/ghapp"
	"github.com/hegade/kunai/internal/review"
	"github.com/hegade/kunai/internal/session"
)

// reviewRun is one review in flight: the phase machine plus the diff its
// findings will eventually be placed against.
type reviewRun struct {
	mu    sync.Mutex
	run   *review.Run
	files []ghapp.FileDiff

	// owner is the session the REVIEW belongs to: the one prReviews is keyed by,
	// the one the sidebar shows and the one the view opens. A phase running in a
	// session of its own registers that session against this same holder, so its
	// answer routes back here, but everything recorded is recorded against this.
	owner string
	// verify is the session running the verification phase, empty when that phase
	// is not running or is sharing the owner's session.
	verify string
	// worktree and spawn are what a phase needs to be given a session of its own:
	// the checkout to read and the account to run on.
	worktree string
	spawn    session.CreateOptions
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

// ownerOf is the review a session belongs to, which for a session a phase
// borrowed is not the same as the session itself. Empty when this session is
// not part of any review.
func (r *reviewRunners) ownerOf(sessionID string) string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if run, ok := r.runs[sessionID]; ok {
		return run.owner
	}
	return ""
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
		s.prReviews.update(holder.owner, func(rec *prReview) {
			rec.ParseError = err.Error()
			rec.Phase = string(review.PhaseDone)
		})
		s.endRun(holder)
		log.Printf("pr review: session %s produced no usable review block: %v", sessionID, err)
		return
	}

	prompt, brief, more := holder.run.Next()
	now := time.Now()
	s.prReviews.update(holder.owner, func(rec *prReview) {
		rec.Phase = string(holder.run.Phase)
		rec.beganPhase(rec.Phase, now)
		// The survey, saved the moment it exists. It used to be built into the
		// find prompt and dropped, which threw away the only account of what the
		// reviewer decided to look at: the thing worth reading during the several
		// minutes finding takes, and the thing to argue with when a review comes
		// back having looked in the wrong place.
		if holder.run.Survey.Intent != "" || len(holder.run.Survey.Areas) > 0 {
			survey := holder.run.Survey
			rec.Survey = &survey
		}
	})

	if !more {
		s.finishReview(holder)
		return
	}

	// The verification phase gets a session of ITS OWN.
	//
	// Independence is the whole reason the phase exists, and running it in the
	// session that produced the findings does not have it: that context still
	// holds the reasoning that wrote them, so the model orchestrating the check
	// is the same one being checked, agreeing with itself. Only the Task
	// subagents were ever really independent. A fresh session is the honest
	// version, and the prompt now carries everything it needs (see VerifyPrompt).
	//
	// It is also where the money is. That session's context starts at the system
	// prompt plus the claims, tens of thousands of tokens, where the find session
	// ends around 210k on a large review and re-bills all of it on every step.
	if holder.run.Phase == review.PhaseVerify && holder.verify == "" {
		if s.startVerifySession(holder, prompt, brief) {
			return
		}
		// Falling through is deliberate: a session that could not be created must
		// cost a weaker check, never the whole phase.
		log.Printf("pr review: could not give %s its own session to verify in; checking in place", holder.owner)
	}

	target := holder.owner
	if holder.verify != "" {
		target = holder.verify
	}
	sess, live := s.mgr.Get(target)
	if !live {
		// The session was closed mid-review. Whatever was found so far is still
		// worth keeping, so it is saved rather than thrown away.
		log.Printf("pr review: session %s went away during the %s phase", target, holder.run.Phase)
		s.finishReview(holder)
		return
	}
	if err := sess.PromptBrief(prompt, brief); err != nil {
		log.Printf("pr review: session %s could not start the %s phase: %v", target, holder.run.Phase, err)
		s.finishReview(holder)
	}
}

// startVerifySession runs the verification phase in a session of its own,
// reporting whether it managed to.
//
// The session reads the same worktree and runs on the same account, and it is
// registered against the SAME holder so its answer comes back to this review
// rather than being taken for a conversation. It is closed the moment it
// answers: it exists to be asked one question.
func (s *Server) startVerifySession(holder *reviewRun, prompt, brief string) bool {
	if holder.worktree == "" {
		return false
	}
	opts := holder.spawn
	opts.Cwd = holder.worktree
	opts.Title = brief

	sess, err := s.mgr.Create(context.Background(), opts)
	if err != nil {
		log.Printf("pr review: verify session for %s would not start: %v", holder.owner, err)
		return false
	}
	s.armSession(sess)
	holder.verify = sess.ID
	// Registered under the new id as well, so the hook finds this same holder.
	s.reviewRuns.put(sess.ID, holder)
	sess.SetAnswerHook(func(text string) { s.advanceReview(sess.ID, text) })

	if err := sess.PromptBrief(prompt, brief); err != nil {
		log.Printf("pr review: verify session for %s would not accept its prompt: %v", holder.owner, err)
		s.closeVerifySession(holder)
		return false
	}
	log.Printf("pr review: checking %s's findings in session %s, with no memory of what wrote them", holder.owner, sess.ID)
	return true
}

// closeVerifySession ends the borrowed session and forgets it. Safe to call when
// there is none.
func (s *Server) closeVerifySession(holder *reviewRun) {
	if holder.verify == "" {
		return
	}
	id := holder.verify
	holder.verify = ""
	s.reviewRuns.drop(id)
	if s.mgr == nil {
		return
	}
	// On its own goroutine: this runs inside that session's own answer hook, and
	// closing waits for the driver to stop.
	go s.mgr.Close(id)
}

// endRun forgets a finished review, including any session a phase borrowed.
func (s *Server) endRun(holder *reviewRun) {
	s.closeVerifySession(holder)
	s.reviewRuns.drop(holder.owner)
}

// finishReview places the findings against the diff and records the outcome.
// Called with holder.mu held.
func (s *Server) finishReview(holder *reviewRun) {
	sessionID := holder.owner
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
	s.endRun(holder)

	blocker, major, minor := draft.Counts()
	log.Printf("pr review: session %s drafted %d finding(s) (%d blocker, %d major, %d minor), %d inline and %d in the summary; %d refuted",
		sessionID, total, blocker, major, minor, inline, summary, len(dropped))
}
