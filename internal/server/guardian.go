package server

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/hegade/kunai/internal/awake"
)

// stopper is the guardian's view of the session manager: the one action it
// takes on a trip. An interface (not the concrete *session.Manager) so the
// safety logic is unit-testable without spawning real claude processes.
type stopper interface {
	StopForThermal() int
}

// The guardian is a whole-machine safety net for unattended work. A loop, or a
// session a phone walked away from mid-turn, can pin the CPU for hours; with the
// lid shut that is how a laptop cooks. The guardian watches the host and, when it
// runs too hot or has been held awake too long, stops every session and releases
// the keep-awake hold. On a closed-lid machine that lets it sleep, and sleep
// drops the CPU to idle: sleep is the cooldown.
//
// The trip is deliberately mild. The heat is the running turns, so stopping them
// is the fix; the claude processes are left alive so the work stays resumable.
// Powering the machine off is a Phase 2 escalation for when the heat is not ours
// and the machine keeps climbing anyway, and it needs privileges this service
// does not have by default.
//
// Two things arm it, and whichever fires first wins, the same shape as a loop's
// iteration and spend caps:
//   - temperature over a threshold, held for several reads (hysteresis, so a
//     one-off spike never nukes your session). Real on Linux; macOS reads 0 until
//     Phase 2, so there the guard is time-only.
//   - a wall-clock ceiling on how long an unattended keep-awake hold may last.
//     This is the macOS-safe fallback for when temperature cannot be read.

const (
	guardPoll     = 15 * time.Second // how often to read the host
	guardFirst    = 20 * time.Second // first read, a little after boot
	guardTripN    = 3                // consecutive over-temp reads before tripping
	guardRecoverC = 5.0              // must fall this far below soft to re-arm
)

// guardConfig is the user-tunable policy. It is persisted to thermal.json and
// seeded from flags, mirroring the keep-awake toggle.
type guardConfig struct {
	Enabled  bool    `json:"enabled"`
	SoftC    float64 `json:"soft_c"`    // stop everything at/above this (0 = no temp check)
	MaxHours float64 `json:"max_hours"` // cap on an unattended awake hold (0 = no cap)
}

type guardian struct {
	mgr   stopper
	awake awake.Keeper
	// notify wakes the phone when the guard trips; the string is generic, never
	// host detail (the relay-free promise). May be nil.
	notify func(kind, detail string)

	mu        sync.Mutex
	cfg       guardConfig
	over      int       // consecutive over-soft reads so far
	trip      bool      // currently holding everything stopped
	awakeFrom time.Time // when the keep-awake hold began (zero when not held)
}

func newGuardian(mgr stopper, aw awake.Keeper, cfg guardConfig) *guardian {
	return &guardian{mgr: mgr, awake: aw, cfg: cfg}
}

func (g *guardian) tripped() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.trip
}

func (g *guardian) config() guardConfig {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.cfg
}

// setConfig replaces the policy. Turning the guard off clears any latched trip so
// a re-enable starts clean.
func (g *guardian) setConfig(cfg guardConfig) {
	g.mu.Lock()
	g.cfg = cfg
	if !cfg.Enabled {
		g.trip = false
		g.over = 0
	}
	g.mu.Unlock()
}

// run polls the host until ctx ends, following the certKeeper.renewLoop shape: a
// short first delay, then a steady interval, exiting on cancellation.
func (g *guardian) run(ctx context.Context) {
	t := time.NewTimer(guardFirst)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			g.check(cpuTemp())
			t.Reset(guardPoll)
		}
	}
}

// check applies one reading. Split from run so a test can drive it directly with
// no clock. temp is 0 when unreadable, in which case only the wall-clock cap can
// fire.
func (g *guardian) check(temp float64) {
	g.mu.Lock()
	cfg := g.cfg
	if !cfg.Enabled {
		g.over = 0
		g.mu.Unlock()
		return
	}

	// Track how long the keep-awake hold has been held, for the wall-clock cap.
	held := g.awake != nil && g.awake.Enabled()
	if held && g.awakeFrom.IsZero() {
		g.awakeFrom = time.Now()
	} else if !held {
		g.awakeFrom = time.Time{}
	}

	// Already tripped: wait for the machine to cool well below the line before
	// re-arming, so it does not flap on and off around the threshold.
	if g.trip {
		if cfg.SoftC > 0 && temp > 0 && temp < cfg.SoftC-guardRecoverC {
			g.trip = false
			g.over = 0
		}
		g.mu.Unlock()
		return
	}

	tooHot := cfg.SoftC > 0 && temp >= cfg.SoftC
	if tooHot {
		g.over++
	} else {
		g.over = 0
	}
	overHeld := g.over >= guardTripN
	overTime := cfg.MaxHours > 0 && held && time.Since(g.awakeFrom) >= time.Duration(cfg.MaxHours*float64(time.Hour))

	if !overHeld && !overTime {
		g.mu.Unlock()
		return
	}

	reason := "held awake too long"
	if overHeld {
		reason = "host too hot"
	}
	g.trip = true
	g.over = 0
	g.mu.Unlock()

	// Act with the lock released: stopping sessions and dropping the hold both do
	// real work and must not block a concurrent config change or stats read.
	n := g.mgr.StopForThermal()
	if g.awake != nil {
		_ = g.awake.Set(false)
	}
	log.Printf("thermal guard tripped (%s): stopped %d session(s), released keep-awake", reason, n)
	if g.notify != nil {
		g.notify("thermal", reason)
	}
}
