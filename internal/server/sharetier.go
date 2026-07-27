package server

// Applying a share's tier to the session it shares.
//
// A tier is not a runtime setting. The tools a guest's agent may run are decided
// by --disallowedTools, which the CLI reads once at spawn, so changing the tier
// means replacing the process. That is a path kunai already walks for effort and
// account changes, and the conversation survives it through --resume.

import (
	"context"
	"log"
	"time"

	"github.com/hegade/kunai/internal/share"
)

func logShare(format string, args ...any) { log.Printf("share: "+format, args...) }

// applyShareTier respawns the shared session with the tier's tool restrictions in
// force, and with the share's standing permission mode if it named one.
//
// Doing this at share time rather than at prompt time is the point: by the time a
// guest's words reach the model, the model's toolset is already whatever it was
// spawned with, and there is no second chance to take Bash away.
func (s *Server) applyShareTier(ctx context.Context, sh *share.Share) error {
	if err := s.restrictSession(ctx, sh.SessionID, sh.Tier.DisallowedTools(), sh.Mode); err != nil {
		return err
	}
	// The guard goes on AFTER the respawn, because the respawn produced a new
	// Session object and the old one's guard went with the old process.
	if sess, ok := s.mgr.Get(sh.SessionID); ok {
		sess.SetToolGuard(newShareGuard(sh))
	}
	return nil
}

// clearShareTier gives the session its full toolset back when the share ends,
// and takes the guard off with it.
//
// The explicit removal is needed for the case where restrictSession decides no
// respawn is necessary: a respawn produces a new Session that has no guard by
// construction, but a skipped one leaves the old object in place with its guard
// still attached. Nothing can reach it in the meantime either way, because the
// share is revoked before this is called and only a guest's turn is guarded.
func (s *Server) clearShareTier(ctx context.Context, sessionID string) error {
	if sess, ok := s.mgr.Get(sessionID); ok {
		sess.SetToolGuard(nil)
	}
	return s.restrictSession(ctx, sessionID, nil, "")
}

// restrictSession is the one place a session's tool restrictions change, so the
// respawn and the reasons for it stay together.
func (s *Server) restrictSession(ctx context.Context, sessionID string, denied []string, mode string) error {
	sess, ok := s.mgr.Get(sessionID)
	if !ok {
		return nil // already gone; nothing to restrict
	}
	if sameTools(sess.DisallowedTools(), denied) {
		// Nothing about the process would change, and a respawn costs the guest a
		// reconnect and the model a re-read of its context.
		return nil
	}
	next, err := s.mgr.RestartWithTools(ctx, sessionID, denied, mode, loadTranscriptTurns)
	if err != nil {
		return err
	}
	s.armSession(next)
	return nil
}

// shareReconcile is how often a session is checked against the share that
// restricted it. Slow, because the only thing it corrects is a session left more
// restricted than it needs to be, which is a nuisance rather than a risk.
const shareReconcile = time.Minute

// reconcileShares gives back the toolset of any session whose share is gone.
//
// Revoking clears the restriction itself, but revoking is not the only way a
// share ends. A link that simply runs out of time is swept from the store by
// whatever next reads it, and nothing was watching for that: the session kept
// running without Bash, with the guard still installed, for as long as it lived.
// Silently, because from the outside it looks exactly like a session that was
// never shared.
//
// Reconciling against the store rather than hooking the sweep is deliberate. It
// catches every way a share can disappear, including ones nobody has thought of
// yet, and the check is cheap: a session that was never shared has no
// restrictions to compare.
func (s *Server) reconcileShares(ctx context.Context) {
	t := time.NewTicker(shareReconcile)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.restoreOrphanedSessions(ctx)
		}
	}
}

func (s *Server) restoreOrphanedSessions(ctx context.Context) {
	if s.shares == nil {
		return
	}
	for _, meta := range s.mgr.List() {
		sess, ok := s.mgr.Get(meta.ID)
		if !ok || len(sess.DisallowedTools()) == 0 {
			continue // never shared, or already given back
		}
		if _, live := s.shares.BySession(meta.ID); live {
			continue
		}
		logShare("session %s outlived its share; restoring its tools", meta.ID)
		if err := s.clearShareTier(ctx, meta.ID); err != nil {
			logShare("could not restore session %s: %v", meta.ID, err)
		}
	}
}

func sameTools(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
