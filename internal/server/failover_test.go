package server

import (
	"os"
	"testing"
)

// win is a small helper: a usage window at pct% used, resetting at reset.
func win(pct float64, reset int64) *UsageWindow { return &UsageWindow{Percent: pct, ResetsAt: reset} }

// The headline case the owner described: account A has 70% of its binding window
// left; account B has a full 5-hour window but only 5% of its weekly left. B's
// binding window is the weekly (5%), so A must win -- picking B would wall almost
// immediately.
func TestPickFailover_BindingWindowWins(t *testing.T) {
	a := availabilityFromUsage("A", false, &Usage{Session: win(30, 100), Weekly: win(20, 200)}) // min(70,80)=70
	b := availabilityFromUsage("B", false, &Usage{Session: win(0, 100), Weekly: win(95, 200)})  // min(100,5)=5
	if a.Remaining != 70 {
		t.Fatalf("A remaining = %v, want 70 (binding = 5h)", a.Remaining)
	}
	if b.Remaining != 5 {
		t.Fatalf("B remaining = %v, want 5 (binding = weekly)", b.Remaining)
	}
	got, ok := pickFailover("current", []availability{a, b}, nil)
	if !ok || got != "A" {
		t.Fatalf("pick = %q,%v; want A,true", got, ok)
	}
}

// Nothing with headroom -> no failover (the caller leaves it to the reset UI).
func TestPickFailover_AllWalled(t *testing.T) {
	a := availabilityFromUsage("A", false, &Usage{Session: win(100, 0), Weekly: win(80, 0)})
	b := availabilityFromUsage("B", false, &Usage{Session: win(99.5, 0)})
	if _, ok := pickFailover("current", []availability{a, b}, nil); ok {
		t.Fatal("expected ok=false when every account is walled")
	}
}

// The current account and any already tried this chain are skipped, so failover
// walks through distinct accounts instead of bouncing back.
func TestPickFailover_SkipsCurrentAndTried(t *testing.T) {
	cands := []availability{
		{Name: "cur", Remaining: 90},
		{Name: "used", Remaining: 80},
		{Name: "fresh", Remaining: 60},
	}
	got, ok := pickFailover("cur", cands, map[string]bool{"used": true})
	if !ok || got != "fresh" {
		t.Fatalf("pick = %q,%v; want fresh,true (cur and used excluded)", got, ok)
	}
}

// A provider is a first-class candidate: with more headroom than any Claude
// account it is chosen (use non-Claude accounts effectively), but on an exact tie
// the Claude account is preferred (the genuine agent).
func TestPickFailover_ProviderRanking(t *testing.T) {
	claude := availability{Name: "claude-work", Provider: false, Remaining: 40, Known: true}
	codex := availability{Name: "Codex", Provider: true, Remaining: 85, Known: true}
	if got, _ := pickFailover("cur", []availability{claude, codex}, nil); got != "Codex" {
		t.Fatalf("more-headroom provider should win, got %q", got)
	}
	tie1 := availability{Name: "claude-work", Provider: false, Remaining: 60}
	tie2 := availability{Name: "Grok", Provider: true, Remaining: 60}
	if got, _ := pickFailover("cur", []availability{tie2, tie1}, nil); got != "claude-work" {
		t.Fatalf("on a tie the Claude account should win, got %q", got)
	}
}

// A missing reading is neutral (usable, mid-rank): below a known-healthy account,
// above a known-low one.
func TestAvailabilityFromUsage_Unknown(t *testing.T) {
	a := availabilityFromUsage("prov", true, nil)
	if a.Known || a.Remaining != unknownHeadroom {
		t.Fatalf("nil usage: got known=%v remaining=%v, want false,%v", a.Known, a.Remaining, unknownHeadroom)
	}
	healthy := availabilityFromUsage("c", false, &Usage{Session: win(10, 0)}) // 90 left
	low := availabilityFromUsage("d", false, &Usage{Session: win(70, 0)})     // 30 left
	if got, _ := pickFailover("cur", []availability{a, healthy, low}, nil); got != "c" {
		t.Fatalf("known-healthy should beat unknown, got %q", got)
	}
	if got, _ := pickFailover("cur", []availability{a, low}, nil); got != "prov" {
		t.Fatalf("unknown should beat known-low, got %q", got)
	}
}

// The opt-in defaults off, persists an enable to failover.json, and re-loads it.
func TestFailoverPersistence(t *testing.T) {
	dir := t.TempDir()
	srv := &Server{cfg: Config{DataDir: dir}}
	fc := newFailoverController(srv)
	fc.load()
	if fc.Enabled() {
		t.Fatal("failover must default off")
	}
	fc.SetEnabled(true)
	if _, err := os.Stat(fc.path()); err != nil {
		t.Fatalf("failover.json not written: %v", err)
	}
	// A fresh controller over the same dir re-applies the persisted opt-in.
	fc2 := newFailoverController(srv)
	fc2.load()
	if !fc2.Enabled() {
		t.Fatal("persisted enable not re-applied on boot")
	}
	fc2.SetEnabled(false)
	fc3 := newFailoverController(srv)
	fc3.load()
	if fc3.Enabled() {
		t.Fatal("persisted disable not re-applied")
	}
}

// The tried-set folds in the current account and accumulates across a chain, so a
// second wall rolls to a third account, not back to one already spent.
func TestTriedSnapshotChains(t *testing.T) {
	fc := newFailoverController(&Server{cfg: Config{DataDir: t.TempDir()}})
	s1 := fc.triedSnapshot("sess", "A")
	if !s1["a"] {
		t.Fatal("current account should be in the tried set")
	}
	fc.addTried("sess", "B")
	s2 := fc.triedSnapshot("sess", "B")
	if !s2["a"] || !s2["b"] {
		t.Fatalf("tried set should accumulate A and B, got %v", s2)
	}
	fc.clearTried("sess")
	if len(fc.triedSnapshot("sess", "C")) != 1 {
		t.Fatal("a clean turn should reset the chain")
	}
}
