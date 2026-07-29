package server

// Throwing away a review's checkout.
//
// Every review makes a full worktree, and on a repository of any size that is
// tens of megabytes each. Without this they accumulate one per pull request ever
// reviewed, for ever: not dangerous, but it quietly fills a disk and it is the
// kind of thing you discover in a month rather than a day.
//
// The obvious implementation is the wrong one. Hanging removal off the session
// ending looks right and is a trap: a RESPAWN also ends the session (an effort
// change, an account switch, auto-failover all close it and build a new one), so
// cleanup on close would delete the checkout out from under a session that is
// coming straight back into it. There is no safe moment to observe the
// difference, because the old session is gone before the new one is registered.
//
// So the question asked is not "did the session end" but "is anyone working
// here", which the worktree store already answers for exactly this reason, plus
// an idle period so a respawn measured in seconds can never race a sweep measured
// in minutes.

import (
	"log"
	"os"
	"time"

	"github.com/hegade/kunai/internal/worktree"
)

const (
	// reviewIdleGrace is how long a review checkout with nobody in it is left
	// alone. Generously longer than any respawn, which is seconds, and short
	// enough that a day of reviewing does not leave a day of checkouts.
	reviewIdleGrace = 20 * time.Minute
	// reviewSweepEvery is the cadence once the server is up. Cheap: it walks a
	// handful of records and stats a directory each.
	reviewSweepEvery = 30 * time.Minute
)

// startReviewSweeper removes finished reviews' checkouts, now and periodically.
//
// The boot pass is the one that matters most: it catches the reviews that were
// running when kunai was killed, restarted or updated, which is precisely when
// nothing else got the chance to clean up.
func (s *Server) startReviewSweeper() {
	if s.prReviews == nil {
		return
	}
	go func() {
		s.sweepReviewWorktrees()
		t := time.NewTicker(reviewSweepEvery)
		defer t.Stop()
		for range t.C {
			s.sweepReviewWorktrees()
		}
	}()
}

// sweepReviewWorktrees removes the checkouts of reviews nobody is looking at.
func (s *Server) sweepReviewWorktrees() {
	if s.prReviews == nil {
		return
	}
	for _, rec := range s.prReviews.all() {
		if rec.Worktree == "" {
			continue // already swept, or a record from before this existed
		}
		if _, err := os.Stat(rec.Worktree); os.IsNotExist(err) {
			// Gone by other means (a manual rm, a wiped data dir). Forget it so the
			// sweep does not keep asking about a directory that is not there.
			s.prReviews.update(rec.SessionID, func(r *prReview) { r.Worktree = "" })
			continue
		}
		if !s.reviewCheckoutIdle(rec) {
			continue
		}
		if err := worktree.RemoveReview(worktree.Info{Path: rec.Worktree, Repo: rec.RepoDir}); err != nil {
			log.Printf("pr review: could not remove %s: %v", rec.Worktree, err)
			continue
		}
		s.prReviews.update(rec.SessionID, func(r *prReview) { r.Worktree = "" })
		log.Printf("pr review: removed the checkout for %s/%s#%d", rec.Owner, rec.Repo, rec.Number)
	}
}

// reviewCheckoutIdle reports that this review's checkout can go: nobody is
// working in it, and it has been sitting long enough that a respawn cannot be in
// flight.
//
// "Nobody is working in it" is asked of the live session list rather than of the
// review's own session id, because a worktree can hold more than one session: you
// may have opened your own alongside the review to look at the code, and pulling
// the directory out from under that would be worse than keeping a few megabytes.
func (s *Server) reviewCheckoutIdle(rec prReview) bool {
	if s.worktrees != nil && len(s.worktrees.sessionsIn(rec.Worktree)) > 0 {
		return false
	}
	// CreatedAt rather than the directory's mtime: a review that ran for an hour
	// has a fresh mtime and is no less finished for it.
	return time.Since(rec.CreatedAt) > reviewIdleGrace
}
