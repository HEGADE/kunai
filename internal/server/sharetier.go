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
	"strings"
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
	if err := s.restrictSession(ctx, sh.SessionID, sh.Tier.DisallowedTools(), share.ToolsOwner, sh.Mode); err != nil {
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
	return s.restrictSession(ctx, sessionID, nil, "", "")
}

// restrictSession is the one place a session's tool restrictions change, so the
// respawn and the reasons for it stay together.
func (s *Server) restrictSession(ctx context.Context, sessionID string, denied []string, owner, mode string) error {
	sess, ok := s.mgr.Get(sessionID)
	if !ok {
		return nil // already gone; nothing to restrict
	}
	if sameTools(sess.DisallowedTools(), denied) {
		// Nothing about the process would change, and a respawn costs the guest a
		// reconnect and the model a re-read of its context.
		return nil
	}
	next, err := s.mgr.RestartWithTools(ctx, sessionID, denied, owner, mode, loadTranscriptTurns)
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
//
// What it must NOT do is infer the share from the restriction. Withheld tools
// were a share's signature only while sharing was the one thing that withheld
// them; a pull request review withholds Bash too, so this read every review as a
// share that had expired and respawned it about a minute in, killing the turn
// mid-work. From the outside that looked like the review giving up on its own.
// The session now records who withheld its tools, and only that owner may give
// them back.
func (s *Server) reconcileShares(ctx context.Context) {
	t := time.NewTicker(shareReconcile)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.restoreOrphanedSessions(ctx)
			s.closePublicPortIfIdle()
			s.reopenPublicPortIfStale()
		}
	}
}

// closePublicPortIfIdle takes the machine off the public internet once the last
// share is gone.
//
// Creating a share opens the gate and the owner points Funnel at it; nothing
// closed either again. A share that simply ran out of time left the port
// forwarding to a listener that answered 404 to the whole internet, indefinitely,
// and the only way back was to remember to turn it off by hand. The Funnel
// mapping outliving the link is exactly what made this dangerous rather than
// untidy: it is still there weeks later, pointing at whatever is on that port.
//
// Only a mapping pointing at OUR gate is touched. A `tailscale funnel` the owner
// set up for something else is on a port funnelPortFor does not report and is
// left alone.
func (s *Server) closePublicPortIfIdle() {
	if s.shares == nil || s.gate == nil || !s.shares.Empty() {
		return
	}
	if port, on := s.funnelPortFor(s.gate.Port()); on {
		args := funnelOffArgs(port)
		if out, err := execOut(args[0], args[1:]...); err != nil {
			// Not fatal, and retried on the next tick: the gate stays up so a link
			// created in the meantime still works, and a port left open is a thing to
			// report rather than a reason to stop.
			logShare("could not close public port %d after the last share ended: %v %s", port, err, strings.TrimSpace(out))
			return
		}
		s.funnelOurs.drop(port)
		s.forgetFunnel()
		logShare("nothing is shared any more; public port %d closed", port)
	}
	s.gate.stop()
}

func (s *Server) restoreOrphanedSessions(ctx context.Context) {
	if s.shares == nil {
		return
	}
	for _, meta := range s.mgr.List() {
		sess, ok := s.mgr.Get(meta.ID)
		if !ok {
			continue
		}
		_, live := s.shares.BySession(meta.ID)
		if !shareShouldRestore(sess.DisallowedTools(), sess.ToolsOwner(), live) {
			continue
		}
		logShare("session %s outlived its share; restoring its tools", meta.ID)
		if err := s.clearShareTier(ctx, meta.ID); err != nil {
			logShare("could not restore session %s: %v", meta.ID, err)
		}
	}
}

// shareShouldRestore decides whether a session's withheld tools are a share's to
// give back. A pure function because it is the whole of the reconciler's
// judgement and the loop around it cannot be tested without spawning a CLI, which
// is the same reason discoveryCache.merge and the thermal guard's policy are
// pure.
//
// All three conditions are load-bearing, and the middle one is the one that was
// missing: without it a restriction imposed by anything else read as an expired
// share.
func shareShouldRestore(denied []string, owner string, shareLive bool) bool {
	return len(denied) > 0 && owner == share.ToolsOwner && !shareLive
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

// reopenPublicPortIfStale re-aims a Funnel mapping at the share gate after the
// gate has moved.
//
// The counterpart to closePublicPortIfIdle, and it exists because a restart is
// not a rare event: kunai self-updates and the service manager brings it back,
// unattended, which is exactly when nobody is watching a link they handed out.
// A Funnel mapping is written into tailscaled and points at a NUMBER, so when
// the gate comes back on a different loopback port the mapping still resolves --
// to nothing. The public link then fails while the tailnet path keeps working,
// which reads as "sharing is broken outside Tailscale" rather than as a stale
// mapping, because from the owner's own machine nothing looks wrong at all.
//
// Nothing repointed it before: funnelStatus knew how to RECOGNISE the stale
// mapping (staleLoopback) and offered the port back, but only the owner clicking
// "make public" again ever acted on it.
//
// Deliberately narrow. It only ever repoints a mapping this machine's own gate
// is the obvious owner of: a funnel port whose target is a loopback address with
// nothing behind it. A mapping pointing anywhere else, including one the owner
// made for their own app, is left exactly as it is -- the same rule that stops
// closePublicPortIfIdle turning off somebody else's Funnel.
func (s *Server) reopenPublicPortIfStale() {
	if s.shares == nil || s.gate == nil || s.shares.Empty() {
		return // nothing shared, so nothing should be public
	}
	port := s.gate.Port()
	if port == 0 {
		return // the gate is not up; there is nothing to point at yet
	}
	st := s.askFunnel(port)
	if !st.Available || st.Port != 0 {
		return // no tailscale to ask, or already aimed at us
	}
	// staleLoopback is what put a served port in Free, so a port that is both
	// served and free is one that has been left behind. A port that is free
	// because nothing was ever served on it is not something to claim: the owner
	// never asked for this machine to be public.
	stale, ok := st.StaleLoopback()
	if !ok {
		return
	}
	// And left behind by US. staleLoopback is true of ANY funnel pointing at a
	// loopback port nothing answers on, which is exactly what an owner's own
	// funnel to their own app looks like while that app is stopped, restarting
	// or being rebuilt: adopting it would silently rewrite somebody's public
	// surface to the share gate, and it would not come back when their service
	// did. That inference was survivable while a person clicked "make public"
	// with the port list in front of them; unattended it is not, so it is
	// replaced by the record of what this machine funnelled itself.
	if !s.funnelOurs.has(stale) {
		return
	}
	args := funnelOnArgs(stale, port)
	if out, err := execOut(args[0], args[1:]...); err != nil {
		// Reported and retried on the next tick, never fatal: a link that cannot be
		// repointed is a thing to say out loud, not a reason to stop serving the
		// people who can already reach it.
		logShare("could not re-aim public port %d at the share gate: %v %s", stale, err, strings.TrimSpace(out))
		return
	}
	s.forgetFunnel()
	logShare("public port %d was pointing at a gate that had moved; re-aimed at 127.0.0.1:%d", stale, port)
}
