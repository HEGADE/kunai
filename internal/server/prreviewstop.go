package server

// Stopping a review.
//
// There was no way to, which is the sort of gap that only shows up when somebody
// wants out: a review is minutes of work on a large pull request, and a reader
// who has seen enough, or who started one by mistake, had nothing to press. The
// obvious move -- open the conversation and press Stop -- looks like it should
// work and does not, and the reason is the engine. Stop interrupts the running
// TURN, and the answer hook fires at the end of every turn and feeds the next
// phase in, so interrupting one turn just makes the machine ask for the next.
// The review carried on with the Stop button apparently doing nothing.
//
// So stopping is a property of the RUN, not of a turn: the phase machine is
// cancelled first, and only then is anything interrupted. Both halves are
// needed. Without the flag the next phase starts anyway; without the interrupt
// the turn already in flight keeps burning quota until it finishes.
//
// What was found so far is deliberately NOT kept. Findings that have not been
// through verification are candidates, and the whole engine exists to stop those
// being presented as a review; saving them here would put a draft on the record
// that nothing checked and that the screen would show as a finished reading. The
// conversation is still there to read if the reader wants what it had.

import (
	"log"
	"net/http"
)

func (s *Server) handleStopReview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.prReviews == nil {
		writeErr(w, http.StatusNotFound, "this session is not a pull-request review")
		return
	}
	if _, ok := s.prReviews.get(id); !ok {
		writeErr(w, http.StatusNotFound, "this session is not a pull-request review")
		return
	}
	holder, running := s.reviewRuns.get(id)
	if !running {
		// Already over, by any route. Not an error: the button and the review can
		// race, and the answer to "stop it" when it has stopped is yes.
		writeJSON(w, http.StatusOK, map[string]bool{"stopped": true})
		return
	}

	// The flag first, and under the run's own lock, so a phase transition that is
	// already under way finishes and finds the review cancelled rather than
	// starting the next one behind us.
	holder.mu.Lock()
	holder.cancelled = true
	verify := holder.verify
	holder.mu.Unlock()

	// The borrowed session goes, the runner is forgotten, and the review's own
	// session is interrupted but kept: it holds the reading, and reading it is
	// the one useful thing left to do with a review somebody stopped.
	s.endRun(holder)
	for _, sid := range []string{id, verify} {
		if sid == "" {
			continue
		}
		if sess, live := s.mgr.Get(sid); live {
			_ = sess.Interrupt()
		}
	}
	log.Printf("pr review: %s was stopped by hand", id)
	writeJSON(w, http.StatusOK, map[string]bool{"stopped": true})
}
