package server

import (
	"context"
	"os"
	"testing"
)

// decide drives the real decision path (tried-set + candidate gathering + ranking)
// with injected availability, so the wiring is exercised, not just pickFailover.
// It mirrors the field state after a walled turn on the Mac in the screenshot:
// Claude and claude-shob and Grok and Codex are at 100% (walled); claude-work (58%)
// and claude-teams-max-shorya (26%) have headroom.
func TestDecide_ChainsAcrossWalledAccounts(t *testing.T) {
	fc := newFailoverController(&Server{cfg: Config{DataDir: t.TempDir()}})
	fc.candidatesFn = func(context.Context, string) []availability {
		return []availability{
			{Name: "Claude", Remaining: 0, Known: true},                   // weekly 100%
			{Name: "claude-work", Remaining: 42, Known: true},             // session 58% used
			{Name: "claude-teams-max-shorya", Remaining: 74, Known: true}, // session 26% used
			{Name: "claude-shob", Remaining: 0, Known: true},              // session 100%
			{Name: "Grok", Provider: true, Remaining: 0, Known: true},     // session 100%
			{Name: "Codex", Provider: true, Remaining: 0, Known: true},    // weekly 100%
		}
	}
	ctx := context.Background()

	// A session walled on "Claude" fails over to the most-headroom account.
	got, ok := fc.decide(ctx, "sess", "Claude", "")
	if !ok || got != "claude-teams-max-shorya" {
		t.Fatalf("first failover = %q,%v; want claude-teams-max-shorya,true", got, ok)
	}
	// Simulate the switch, then that account also walls: the chain must move on to
	// the next-best, never back to one already tried.
	fc.addTried("sess", got)
	got2, ok := fc.decide(ctx, "sess", got, "")
	if !ok || got2 != "claude-work" {
		t.Fatalf("second failover = %q,%v; want claude-work,true", got2, ok)
	}
	// After the last usable account, nothing is left -> step aside.
	fc.addTried("sess", got2)
	if _, ok := fc.decide(ctx, "sess", got2, ""); ok {
		t.Fatal("expected ok=false once every account with headroom is exhausted")
	}
	// A clean turn resets the chain, so the next wall can reuse those accounts.
	fc.clearTried("sess")
	if got, ok := fc.decide(ctx, "sess", "Claude", ""); !ok || got != "claude-teams-max-shorya" {
		t.Fatalf("after a clean turn the chain resets, got %q,%v", got, ok)
	}
}

// Deciding takes seconds, and the obvious thing to do in those seconds -- when
// you believe failover is broken because nothing on screen says otherwise -- is
// to switch accounts by hand. That switch respawns the session, so the failover
// must stand down rather than roll a closed session and override a choice its
// owner made explicitly.
func TestMovedByHand(t *testing.T) {
	cases := []struct {
		walledOn, nowOn string
		want            bool
	}{
		{"claude-work", "claude-work", false},
		{"claude-work", "CLAUDE-WORK", false}, // a name is a label, not an identifier
		{"claude-work", "claude-shorya", true},
		{"claude-work", "Codex", true},
	}
	for _, c := range cases {
		if got := movedByHand(c.walledOn, c.nowOn); got != c.want {
			t.Errorf("movedByHand(%q, %q) = %v, want %v", c.walledOn, c.nowOn, got, c.want)
		}
	}
}

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
// A provider is the fallback for when no Claude account has runway, not a peer
// competing on percentage points.
//
// This inverts what the code used to do, and the old behaviour was a real
// reported surprise: a walled Claude session was rolled onto Codex because Codex
// read 85% against the other Claude account's 40%, silently changing which model
// answered the next turn. Moving accounts at a wall should change the bill, not
// the agent.
func TestPickFailover_PrefersClaudeOverAProvider(t *testing.T) {
	claude := availability{Name: "claude-work", Provider: false, Remaining: 40, Known: true}
	codex := availability{Name: "Codex", Provider: true, Remaining: 85, Known: true}
	if got, _ := pickFailover("cur", []availability{codex, claude}, nil); got != "claude-work" {
		t.Fatalf("a Claude account with runway should beat a roomier provider, got %q", got)
	}

	// Below the floor the Claude account is too near its own wall to be worth the
	// respawn, so the provider is the better bet after all.
	thin := availability{Name: "claude-work", Provider: false, Remaining: 5, Known: true}
	if got, _ := pickFailover("cur", []availability{thin, codex}, nil); got != "Codex" {
		t.Fatalf("a nearly-walled Claude account should yield to a provider, got %q", got)
	}

	// Among Claude accounts that both clear the floor, headroom still decides.
	more := availability{Name: "claude-shorya", Provider: false, Remaining: 70, Known: true}
	if got, _ := pickFailover("cur", []availability{claude, more}, nil); got != "claude-shorya" {
		t.Fatalf("between two usable Claude accounts the roomier one should win, got %q", got)
	}

	// And between two providers, headroom decides as before.
	grok := availability{Name: "Grok", Provider: true, Remaining: 30, Known: true}
	if got, _ := pickFailover("cur", []availability{grok, codex}, nil); got != "Codex" {
		t.Fatalf("between two providers the roomier one should win, got %q", got)
	}
}

// A missing reading is neutral (usable, mid-rank): below a known-healthy account,
// above a known-low one.
func TestAvailabilityFromUsage_Unknown(t *testing.T) {
	a := availabilityFromUsage("prov", true, nil)
	if a.Known || a.Remaining != unknownHeadroom {
		t.Fatalf("nil usage: got known=%v remaining=%v, want false,%v", a.Known, a.Remaining, unknownHeadroom)
	}
	// Compared within one tier, since a provider and a Claude account are no
	// longer ranked against each other by headroom at all (see
	// TestPickFailover_PrefersClaudeOverAProvider). These three are all Claude
	// accounts, so the headroom ordering is what is under test.
	unknown := availabilityFromUsage("unread", false, nil)                    // 50, no reading
	healthy := availabilityFromUsage("c", false, &Usage{Session: win(10, 0)}) // 90 left
	low := availabilityFromUsage("d", false, &Usage{Session: win(70, 0)})     // 30 left
	if got, _ := pickFailover("cur", []availability{unknown, healthy, low}, nil); got != "c" {
		t.Fatalf("known-healthy should beat unknown, got %q", got)
	}
	if got, _ := pickFailover("cur", []availability{unknown, low}, nil); got != "unread" {
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
