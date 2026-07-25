package server

import (
	"testing"
	"time"
)

func ids(ms []MachineInfo) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

func eqIDs(t *testing.T, ms []MachineInfo, want ...string) {
	t.Helper()
	got := ids(ms)
	if len(got) != len(want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	}
}

func peer(id string) MachineInfo { return MachineInfo{ID: id, Label: id, URL: "https://" + id} }

// A scan that fails to reach tailscale (ok=false) must not drop a live peer:
// this is the flicker bug (machines vanishing from the sidebar until a hard
// refresh) reproduced at the cache layer.
func TestDiscoveryFailedScanKeepsPeers(t *testing.T) {
	var d discoveryCache
	t0 := time.Now()

	d.merge([]MachineInfo{peer("mac"), peer("linux")}, nil, true, t0)
	d.mu.Lock()
	eqIDs(t, d.currentLocked(), "linux", "mac") // sorted by id
	d.mu.Unlock()

	// tailscale hiccups a few seconds later: found nothing, ok=false.
	d.merge(nil, nil, false, t0.Add(5*time.Second))
	d.mu.Lock()
	eqIDs(t, d.currentLocked(), "linux", "mac") // both survive untouched
	d.mu.Unlock()

	// Even long past the grace window, a failed scan must not prune: we cannot
	// query tailscale, so we do not KNOW the peers are gone (the client's own
	// probe marks the offline dot). A treat-failure-as-empty bug would drop them.
	d.merge(nil, nil, false, t0.Add(peerTTL+time.Hour))
	d.mu.Lock()
	eqIDs(t, d.currentLocked(), "linux", "mac")
	d.mu.Unlock()
}

// A single successful scan that momentarily misses a peer (its probe blipped)
// must keep the peer for the grace window, then drop it once it has been unseen
// for the whole peerTTL.
func TestDiscoveryBlippedPeerSurvivesThenExpires(t *testing.T) {
	var d discoveryCache
	t0 := time.Now()

	d.merge([]MachineInfo{peer("mac"), peer("linux")}, nil, true, t0)

	// Next round finds only mac; linux was not seen but is still recent.
	d.merge([]MachineInfo{peer("mac")}, nil, true, t0.Add(20*time.Second))
	d.mu.Lock()
	eqIDs(t, d.currentLocked(), "linux", "mac")
	d.mu.Unlock()

	// Well past the grace window with linux still unseen: it finally drops.
	d.merge([]MachineInfo{peer("mac")}, nil, true, t0.Add(peerTTL+time.Minute))
	d.mu.Lock()
	eqIDs(t, d.currentLocked(), "mac")
	d.mu.Unlock()
}

// A peer that blipped but then answers again before the window elapses keeps its
// place with a refreshed last-seen (never drops).
func TestDiscoveryRecoveredPeerRefreshes(t *testing.T) {
	var d discoveryCache
	t0 := time.Now()

	d.merge([]MachineInfo{peer("mac"), peer("linux")}, nil, true, t0)
	d.merge([]MachineInfo{peer("mac")}, nil, true, t0.Add(2*time.Minute))                // linux missed once
	d.merge([]MachineInfo{peer("mac"), peer("linux")}, nil, true, t0.Add(3*time.Minute)) // linux back

	// Long after t0 but linux was re-seen at 3m, so it stays.
	d.merge([]MachineInfo{peer("mac"), peer("linux")}, nil, true, t0.Add(3*time.Minute+peerTTL-time.Second))
	d.mu.Lock()
	eqIDs(t, d.currentLocked(), "linux", "mac")
	d.mu.Unlock()
}

// The bug this fixes, in the shape it actually happened: the Mac is up on the
// tailnet and running kunai, but it is mid-turn, so its /api/stats does not answer
// inside the probe window. Every scan therefore "finds" only the other machine.
// Before, the Mac's last-seen stopped advancing and after peerTTL it was deleted --
// so a demonstrably live machine disappeared from the fleet, and because the client
// mirrors this list verbatim, only a reload brought it back.
func TestDiscoveryLivePeerSurvivesRepeatedProbeFailures(t *testing.T) {
	var d discoveryCache
	t0 := time.Now()
	online := map[string]bool{"mac": true, "linux": true}

	// Both answer once, so both are known to run kunai.
	d.merge([]MachineInfo{peer("mac"), peer("linux")}, online, true, t0)

	// Now the Mac stops answering the probe for well past the grace window, while
	// tailscale keeps reporting it online the whole time.
	for i := 1; i <= 12; i++ {
		d.merge([]MachineInfo{peer("linux")}, online, true, t0.Add(time.Duration(i)*time.Minute))
	}

	d.mu.Lock()
	ids := idsOf(d.currentLocked())
	d.mu.Unlock()
	if !ids["mac"] {
		t.Fatal("a peer tailscale reports online was pruned because our probe timed out")
	}
	if !ids["linux"] {
		t.Fatal("linux should still be present")
	}
}

// The counterpart: a peer that genuinely leaves the tailnet must still age out, or
// the list would accumulate machines that no longer exist.
func TestDiscoveryPeerGoneFromTailnetStillExpires(t *testing.T) {
	var d discoveryCache
	t0 := time.Now()
	d.merge([]MachineInfo{peer("mac"), peer("linux")}, map[string]bool{"mac": true, "linux": true}, true, t0)

	// The Mac shuts down: tailscale stops reporting it, and it stops answering.
	gone := map[string]bool{"linux": true}
	d.merge([]MachineInfo{peer("linux")}, gone, true, t0.Add(peerTTL+time.Minute))

	d.mu.Lock()
	ids := idsOf(d.currentLocked())
	d.mu.Unlock()
	if ids["mac"] {
		t.Error("a peer absent from the tailnet past peerTTL should be pruned")
	}
	if !ids["linux"] {
		t.Error("linux should still be present")
	}
}

func idsOf(ms []MachineInfo) map[string]bool {
	out := map[string]bool{}
	for _, m := range ms {
		out[m.ID] = true
	}
	return out
}
