package server

// Account auto-failover (opt-in, per machine). When the active account hits its
// usage wall mid-turn, kunai rolls the session onto the account with the most
// headroom -- Claude accounts and non-Claude providers (Codex/Grok) alike -- and
// resends the prompt, so work survives a spent 5-hour or 7-day window instead of
// stopping. Off by default; toggled in Settings and persisted to failover.json.
//
// The pick is deliberately simple and honest (pickFailover): an account's usable
// headroom is its BINDING window -- min(5h-left, 7d-left) -- so "100% of the
// 5-hour but 5% of the weekly" scores 5, not 100, because the weekly is what walls
// you; "70% left" scores 70 and wins. Anything at/under a small floor is treated
// as walled and skipped. Among the usable, most headroom wins.
//
// Scope: v1 covers ordinary sessions. A running loop keeps its own limit handling
// (it stops at the wall); loop failover is a follow-up, since it must thread the
// loop's own resume/persistence.

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hegade/kunai/internal/session"
)

const (
	// failoverFloor is the headroom % below which a window counts as walled, not
	// usable -- switching to an account with ~0 left would just re-reject.
	failoverFloor = 2.0
	// unknownHeadroom is the neutral score for a candidate we could not read usage
	// for (e.g. a Grok free account before its first 429): usable, but ranked below
	// a known-healthy account and above a known-low one. If the guess is wrong the
	// tried-set stops us from picking it twice.
	unknownHeadroom = 50.0
)

// availability is one candidate account's readiness to take a turn right now.
type availability struct {
	Name      string
	Provider  bool    // a non-Claude (Codex/Grok/...) account
	Known     bool    // we have a real usage reading (else Remaining is unknownHeadroom)
	Remaining float64 // headroom 0..100 of the binding (soonest-to-wall) window
	ResetsAt  int64   // when the binding window resets, unix secs (0 = unknown)
}

// pickFailover chooses the best account to fail over to, skipping `current` and
// anything already tried this chain. ok is false when nothing clears the floor --
// the caller then leaves the wall to the normal rate-limit UI rather than churning
// through spent accounts.
func pickFailover(current string, cands []availability, tried map[string]bool) (string, bool) {
	var best availability
	found := false
	for _, c := range cands {
		if strings.EqualFold(c.Name, current) || tried[strings.ToLower(c.Name)] {
			continue
		}
		if c.Remaining < failoverFloor {
			continue
		}
		if !found || betterCandidate(c, best) {
			best, found = c, true
		}
	}
	return best.Name, found
}

// betterCandidate ranks a above b: more headroom first; on a tie prefer a Claude
// account (the genuine agent) over a provider; then the sooner reset (more runway
// ahead once it replenishes).
func betterCandidate(a, b availability) bool {
	if a.Remaining != b.Remaining {
		return a.Remaining > b.Remaining
	}
	if a.Provider != b.Provider {
		return !a.Provider
	}
	ar, br := a.ResetsAt, b.ResetsAt
	if ar == 0 {
		ar = 1 << 62
	}
	if br == 0 {
		br = 1 << 62
	}
	return ar < br
}

// availabilityFromUsage reduces a quota reading to a single headroom number: the
// binding window (the one with the least left) is what will wall the account, so
// that window's remaining and reset are what matter.
func availabilityFromUsage(name string, provider bool, u *Usage) availability {
	a := availability{Name: name, Provider: provider}
	if u == nil {
		a.Remaining = unknownHeadroom
		return a
	}
	rem := 100.0
	consider := func(w *UsageWindow) {
		if w == nil {
			return
		}
		r := 100 - w.Percent
		if r < 0 {
			r = 0
		}
		if r < rem {
			rem = r
			a.ResetsAt = w.ResetsAt
		}
	}
	consider(u.Session)
	consider(u.Weekly)
	if u.Session == nil && u.Weekly == nil {
		a.Remaining = unknownHeadroom // a reading with no windows tells us nothing
		return a
	}
	a.Known = true
	a.Remaining = rem
	return a
}

// failoverController owns the opt-in flag and the per-session tried-set, and drives
// the switch when a turn ends against the wall.
type failoverController struct {
	srv *Server

	mu      sync.Mutex
	enabled bool
	tried   map[string]map[string]bool // sessionID -> lowercased account names already used this chain
}

func newFailoverController(srv *Server) *failoverController {
	return &failoverController{srv: srv, tried: map[string]map[string]bool{}}
}

func (fc *failoverController) path() string {
	return filepath.Join(fc.srv.cfg.DataDir, "failover.json")
}

// load re-applies the persisted opt-in at boot (best-effort; default off).
func (fc *failoverController) load() {
	b, err := os.ReadFile(fc.path())
	if err != nil {
		return
	}
	var p struct {
		Enabled bool `json:"enabled"`
	}
	if json.Unmarshal(b, &p) == nil {
		fc.mu.Lock()
		fc.enabled = p.Enabled
		fc.mu.Unlock()
	}
}

func (fc *failoverController) Enabled() bool {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return fc.enabled
}

func (fc *failoverController) SetEnabled(v bool) {
	fc.mu.Lock()
	fc.enabled = v
	fc.mu.Unlock()
	b, _ := json.Marshal(struct {
		Enabled bool `json:"enabled"`
	}{v})
	_ = os.WriteFile(fc.path(), b, 0o600)
}

func (fc *failoverController) clearTried(id string) {
	fc.mu.Lock()
	delete(fc.tried, id)
	fc.mu.Unlock()
}

// triedSnapshot returns the chain's tried-set for id with current folded in, so
// pickFailover never re-picks an account already used this chain.
func (fc *failoverController) triedSnapshot(id, current string) map[string]bool {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	set := fc.tried[id]
	if set == nil {
		set = map[string]bool{}
		fc.tried[id] = set
	}
	set[strings.ToLower(current)] = true
	out := make(map[string]bool, len(set))
	for k := range set {
		out[k] = true
	}
	return out
}

func (fc *failoverController) addTried(id, name string) {
	fc.mu.Lock()
	if fc.tried[id] == nil {
		fc.tried[id] = map[string]bool{}
	}
	fc.tried[id][strings.ToLower(name)] = true
	fc.mu.Unlock()
}

// onTurnEnd is the session turn-end hook. A clean turn clears the chain; a turn
// that ended against the wall triggers a failover when enabled.
func (fc *failoverController) onTurnEnd(sess *session.Session, rateLimited bool) {
	id := sess.Meta().ID
	if !rateLimited {
		fc.clearTried(id)
		return
	}
	if !fc.Enabled() || sess.InLoop() {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	current := sess.Meta().CLI
	tried := fc.triedSnapshot(id, current)
	cands := fc.srv.failoverCandidates(ctx, sess.Cwd)
	target, ok := pickFailover(current, cands, tried)
	if !ok {
		win, reset := sess.LastLimit()
		log.Printf("failover: session %s hit the wall on %q (%s) and no other account has headroom; leaving it for reset %s",
			id, current, win, resetLabel(reset))
		return
	}

	prompt := sess.LastPromptText()
	restarted, err := fc.srv.switchSessionToAccount(ctx, sess, fc.srv.resolveCLI(target))
	if err != nil {
		log.Printf("failover: switch %s from %q to %q failed: %v", id, current, target, err)
		return
	}
	fc.addTried(id, target)
	log.Printf("failover: session %s rolled from %q to %q (resending the last prompt)", id, current, target)
	if prompt != "" {
		// Resend on a goroutine: the respawn is async and this hook must not block
		// the turn pump. The new account's turn-end fires the hook again, so a
		// second wall chains to the next account until the tried-set is exhausted.
		go func() { _ = restarted.Prompt(prompt, nil, nil) }()
	}
}

func resetLabel(unix int64) string {
	if unix <= 0 {
		return "unknown"
	}
	return time.Unix(unix, 0).Format("Jan 2 15:04")
}

// failoverCandidates reads current availability for every account and provider on
// this machine. Claude accounts come from the /usage cache; a Codex provider from
// its ChatGPT quota; a Grok provider from the free-tier quota captured on a 429.
// Providers are ranked as first-class candidates, not last resorts.
func (s *Server) failoverCandidates(ctx context.Context, cwd string) []availability {
	var out []availability
	for _, p := range s.cliList() {
		if isProxyProfile(p) {
			continue // a provider that snuck into the account list; handled below
		}
		u, _ := s.usage.get(ctx, p, cwd)
		out = append(out, availabilityFromUsage(p.Name, false, u))
	}
	for _, p := range s.providerList() {
		out = append(out, availabilityFromUsage(p.Name, true, s.providerUsage(ctx, p)))
	}
	return out
}

// handleFailover reads (GET) or sets (POST) the opt-in flag. The setting is
// per-machine, mirroring the awake/thermal toggles.
func (s *Server) handleFailover(w http.ResponseWriter, r *http.Request) {
	if s.failover == nil {
		writeJSON(w, http.StatusOK, map[string]bool{"enabled": false})
		return
	}
	if r.Method == http.MethodPost {
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		s.failover.SetEnabled(body.Enabled)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": s.failover.Enabled()})
}

// providerUsage returns a provider's quota reading, or nil when none is available
// (which availabilityFromUsage treats as neutral headroom).
func (s *Server) providerUsage(ctx context.Context, p Provider) *Usage {
	model := providerDisplayModel(p)
	switch {
	case isCodexModel(model) && s.codexUC != nil:
		return s.codexUC.get(ctx, s.cfg.DataDir)
	case isGrokModel(model) && s.nativeGrok != nil:
		return s.nativeGrok.freeQuotaUsage()
	}
	return nil
}
