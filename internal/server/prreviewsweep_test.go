package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hegade/kunai/internal/session"
)

// sweepFixture is a server with one review record pointing at a real directory,
// plus control over who is "in" that directory.
func sweepFixture(t *testing.T, age time.Duration, live []session.Meta) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	checkout := filepath.Join(dir, "review", "4")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}

	s := &Server{
		prReviews: newPRReviewStore(filepath.Join(dir, "prreviews.json")),
		worktrees: newWorktreeStore(dir, func() []session.Meta { return live }),
	}
	s.prReviews.put(prReview{
		SessionID: "s1", Owner: "lyzr", Repo: "kunai", Number: 4,
		Worktree: checkout, RepoDir: dir, CreatedAt: time.Now().Add(-age),
	})
	return s, checkout
}

// A finished review's checkout goes, or they accumulate one per pull request
// ever reviewed, for ever.
func TestSweepRemovesAnIdleReviewCheckout(t *testing.T) {
	s, checkout := sweepFixture(t, reviewIdleGrace+time.Minute, nil)

	s.sweepReviewWorktrees()

	if _, err := os.Stat(checkout); !os.IsNotExist(err) {
		t.Errorf("the checkout survived the sweep: %v", err)
	}
	// And the record forgets it, so the sweep does not keep asking about a
	// directory that is not there.
	if rec, _ := s.prReviews.get("s1"); rec.Worktree != "" {
		t.Errorf("the record still points at %q", rec.Worktree)
	}
}

// The trap this design exists to avoid. A respawn (an effort change, an account
// switch, auto-failover) ends the session and builds a new one in the SAME
// checkout, so cleanup keyed on the session ending would delete the directory out
// from under a session that is coming straight back into it. The grace period is
// what makes that impossible: a respawn takes seconds.
func TestSweepLeavesAFreshCheckoutAlone(t *testing.T) {
	s, checkout := sweepFixture(t, time.Minute, nil)

	s.sweepReviewWorktrees()

	if _, err := os.Stat(checkout); err != nil {
		t.Errorf("a checkout minutes old was swept, which a respawn would race: %v", err)
	}
}

// A worktree can hold more than one session: you may have opened your own
// alongside the review to look at the code. Pulling the directory out from under
// that is worse than keeping a few megabytes.
func TestSweepLeavesACheckoutSomebodyIsWorkingIn(t *testing.T) {
	dir := t.TempDir()
	checkout := filepath.Join(dir, "review", "4")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Server{
		prReviews: newPRReviewStore(filepath.Join(dir, "prreviews.json")),
		worktrees: newWorktreeStore(dir, func() []session.Meta {
			// Not the review's own session: somebody else's, in the same directory.
			return []session.Meta{{ID: "mine", Cwd: checkout}}
		}),
	}
	s.prReviews.put(prReview{
		SessionID: "s1", Worktree: checkout, RepoDir: dir,
		CreatedAt: time.Now().Add(-24 * time.Hour),
	})

	s.sweepReviewWorktrees()

	if _, err := os.Stat(checkout); err != nil {
		t.Errorf("a checkout with a live session in it was swept: %v", err)
	}
}

// A directory removed by other means (a manual rm, a wiped data dir) is forgotten
// rather than retried on every pass.
func TestSweepForgetsACheckoutThatIsAlreadyGone(t *testing.T) {
	s, checkout := sweepFixture(t, reviewIdleGrace+time.Minute, nil)
	if err := os.RemoveAll(checkout); err != nil {
		t.Fatal(err)
	}

	s.sweepReviewWorktrees()

	if rec, _ := s.prReviews.get("s1"); rec.Worktree != "" {
		t.Errorf("the record still points at a directory that is gone: %q", rec.Worktree)
	}
}
