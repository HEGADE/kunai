package server

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hegade/kunai/internal/share"
)

// withShare gives a server one live share and a gate on a known port.
func withShare(t *testing.T, gatePort int) *Server {
	t.Helper()
	dir := t.TempDir()
	store := share.NewStore(filepath.Join(dir, "shares.json"))
	if _, err := store.Create(share.Share{SessionID: "s", Tier: share.TierView}, time.Hour); err != nil {
		t.Fatal(err)
	}
	s := &Server{shares: store, gate: newShareGate(store, noSessions{}, testPWA{}, "", "", nil)}
	s.gate.port = gatePort
	// In memory: an empty path means nothing is persisted, which is what a test
	// wants and is the same shape a machine with no data dir gets.
	s.funnelOurs = newFunnelOurs("")
	return s
}

// The bug this exists for: kunai self-updates unattended, the gate comes back on
// a different loopback port, and the Funnel mapping still points at the old one.
// The tailnet path keeps working, so from the owner's own machine nothing looks
// wrong while every public link is dead.
func TestAStaleFunnelIsReAimedAtTheGate(t *testing.T) {
	s := withShare(t, 43671)
	s.funnelOurs.add(443) // this machine opened it, which is what makes it ours to move
	s.funnelStatusFn = func(int) funnelState {
		// 443 is served but points at a loopback port with nothing behind it.
		return funnelState{Available: true, Port: 0, Free: []int{443}, Stale: []int{443}}
	}
	var ran []string
	prev := execOut
	execOut = func(name string, args ...string) (string, error) {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return "", nil
	}
	defer func() { execOut = prev }()

	s.reopenPublicPortIfStale()
	if len(ran) != 1 {
		t.Fatalf("ran %d commands, want 1: %v", len(ran), ran)
	}
	if !strings.Contains(ran[0], "--https=443") || !strings.Contains(ran[0], "http://127.0.0.1:43671") {
		t.Errorf("wrong command: %s", ran[0])
	}
}

func TestNothingIsReAimedWhenItNeedNotBe(t *testing.T) {
	cases := []struct {
		name  string
		port  int
		state funnelState
		empty bool
	}{
		{
			// Already aimed at us. Repointing would be a no-op that still shells out
			// on every tick.
			name: "the mapping already points here", port: 43671,
			state: funnelState{Available: true, Port: 443},
		},
		{
			// A port free because nothing was ever served on it is not ours to
			// claim: the owner never asked for this machine to be public.
			name: "a port that was never served", port: 43671,
			state: funnelState{Available: true, Free: []int{443}},
		},
		{
			// Somebody else's Funnel, the same rule closePublicPortIfIdle follows.
			name: "someone else's mapping", port: 43671,
			state: funnelState{Available: true, InUse: map[int]string{443: "tailscale is serving http://127.0.0.1:8501 here"}},
		},
		{
			name: "no tailscale to ask", port: 43671,
			state: funnelState{Available: false, Stale: []int{443}},
		},
		{
			// The gate is not up, so there is nothing to point at yet.
			name: "the gate has no port", port: 0,
			state: funnelState{Available: true, Stale: []int{443}},
		},
		{
			// Nothing is shared, so nothing should be public: that is
			// closePublicPortIfIdle's job and this must not fight it.
			name: "nothing is shared", port: 43671,
			state: funnelState{Available: true, Stale: []int{443}}, empty: true,
		},
	}
	for _, c := range cases {
		s := withShare(t, c.port)
		if c.empty {
			dir := t.TempDir()
			s.shares = share.NewStore(filepath.Join(dir, "shares.json"))
		}
		s.funnelStatusFn = func(int) funnelState { return c.state }
		var ran []string
		prev := execOut
		execOut = func(name string, args ...string) (string, error) {
			ran = append(ran, name)
			return "", nil
		}
		s.reopenPublicPortIfStale()
		execOut = prev
		if len(ran) != 0 {
			t.Errorf("%s: repointed anyway (%v)", c.name, ran)
		}
	}
}

// Lowest first, so the choice does not wander between calls and a machine with
// two stale mappings does not alternate.
func TestTheStalePortChoiceIsStable(t *testing.T) {
	f := funnelState{Stale: []int{10000, 443, 8443}}
	for i := 0; i < 3; i++ {
		if got, ok := f.StaleLoopback(); !ok || got != 443 {
			t.Fatalf("got %d,%v want 443,true", got, ok)
		}
	}
	if _, ok := (funnelState{}).StaleLoopback(); ok {
		t.Error("an empty state claimed a stale port")
	}
}

// The owner's own Funnel is not kunai's to take.
//
// staleLoopback is true of ANY funnel pointing at a loopback port nothing
// answers on, and that is exactly what an owner's mapping to their own app looks
// like while the app is stopped, restarting or being rebuilt. Adopting it would
// rewrite somebody's public surface to the share gate with nobody watching, and
// it would not come back when their service did. Only a port this machine
// recorded funnelling is repointed.
func TestAFunnelThisMachineDidNotMakeIsLeftAlone(t *testing.T) {
	s := withShare(t, 43671)
	// 10000 -> 127.0.0.1:3000, the owner's own app, currently down.
	s.funnelStatusFn = func(int) funnelState {
		return funnelState{Available: true, Port: 0, Free: []int{10000}, Stale: []int{10000}}
	}
	var ran []string
	prev := execOut
	execOut = func(name string, args ...string) (string, error) {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return "", nil
	}
	defer func() { execOut = prev }()

	s.reopenPublicPortIfStale()
	if len(ran) != 0 {
		t.Fatalf("kunai took over a mapping it did not make: %v", ran)
	}

	// And once kunai has opened that port itself, the same state IS its to fix.
	s.funnelOurs.add(10000)
	s.reopenPublicPortIfStale()
	if len(ran) != 1 {
		t.Fatalf("ran %d commands after recording the port, want 1: %v", len(ran), ran)
	}
}

// Closing the port forgets it, so a port the owner later funnels for their own
// app is not still remembered as kunai's.
func TestClosingAPublicPortForgetsIt(t *testing.T) {
	f := newFunnelOurs("")
	f.add(443)
	if !f.has(443) {
		t.Fatal("a port kunai opened was not recorded")
	}
	f.drop(443)
	if f.has(443) {
		t.Error("a closed port is still claimed")
	}
}

// The record outlives a restart, because the repoint it guards runs unattended
// and a reboot must not turn a recorded fact back into a guess.
func TestTheRecordSurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "funnel-ours.json")
	newFunnelOurs(path).add(8443)
	if !newFunnelOurs(path).has(8443) {
		t.Error("the port kunai funnelled was forgotten across a restart")
	}
	// A file that is not there at all is safe in the direction that matters: an
	// unrecorded port is left alone rather than adopted.
	if newFunnelOurs(filepath.Join(t.TempDir(), "absent.json")).has(8443) {
		t.Error("a missing record claimed a port anyway")
	}
}
