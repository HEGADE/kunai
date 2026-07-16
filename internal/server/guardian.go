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

	// A hard trip's action. Sleep (the default) is Phase 1's proven stop-and-cool
	// and needs no privilege; poweroff is the Phase 2 escalation for a host still
	// climbing after everything of ours was already stopped, and needs the
	// admin-installed privilege the plain service lacks.
	actionSleep    = "sleep"
	actionPowerOff = "poweroff"
)

// guardConfig is the user-tunable policy. It is persisted to thermal.json and
// seeded from flags, mirroring the keep-awake toggle.
type guardConfig struct {
	Enabled  bool    `json:"enabled"`
	SoftC    float64 `json:"soft_c"`    // stop everything at/above this (0 = no temp check)
	MaxHours float64 `json:"max_hours"` // cap on an unattended awake hold (0 = no cap)
	// HardC and Action are the Phase 2 escalation: if the host is STILL over HardC
	// after the soft trip already stopped our load, the heat is not ours, and with
	// Action=="poweroff" the machine is shut down. Default action is "sleep", which
	// means the hard ceiling does nothing beyond the soft trip, so nothing
	// privileged happens unless the owner deliberately turns it on.
	HardC  float64 `json:"hard_c"`
	Action string  `json:"action"`
}

func (c guardConfig) powersOff() bool { return c.Action == actionPowerOff && c.HardC > 0 }

type guardian struct {
	mgr   stopper
	awake awake.Keeper
	// notify wakes the phone when the guard trips; the string is generic, never
	// host detail (the relay-free promise). May be nil.
	notify func(kind, detail string)

	// releaseLid, if set, drops any lid-closed hold on a trip so the machine can
	// actually sleep. Optional (nil until Phase 2 wires it), so the guardian never
	// depends on a lid subsystem existing.
	releaseLid func()

	mu        sync.Mutex
	cfg       guardConfig
	over      int       // consecutive over-soft reads so far
	overHard  int       // consecutive over-hard reads so far
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
		g.overHard = 0
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

	// Hard ceiling first, and independent of the soft latch: the whole point is a
	// host still climbing past the danger line after the soft trip already stopped
	// everything of ours. That means the heat is not our load, so we pull the plug.
	// Only ever fires when the owner has explicitly armed the poweroff action.
	if cfg.powersOff() && temp >= cfg.HardC {
		g.overHard++
	} else {
		g.overHard = 0
	}
	if g.overHard >= guardTripN {
		g.overHard = 0
		g.mu.Unlock()
		g.hardTrip(temp)
		return
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

	// Act with the lock released: stopping sessions and dropping the holds both do
	// real work and must not block a concurrent config change or stats read.
	n := g.mgr.StopForThermal()
	g.releaseHolds()
	log.Printf("thermal guard tripped (%s): stopped %d session(s), released keep-awake", reason, n)
	if g.notify != nil {
		g.notify("thermal", reason)
	}
}

// hardTrip is the escalation: the host is still over the danger line after the
// soft trip stopped everything ours, so this powers the machine off. It stops the
// sessions again first (belt and braces, in case the soft trip never ran) and
// drops the holds so a poweroff that is denied still leaves the machine cooling.
func (g *guardian) hardTrip(temp float64) {
	g.mu.Lock()
	g.trip = true
	g.mu.Unlock()

	n := g.mgr.StopForThermal()
	g.releaseHolds()
	log.Printf("thermal guard HARD trip at %.0fC: stopped %d session(s), powering off", temp, n)
	if g.notify != nil {
		g.notify("thermal", "powering off: host too hot")
	}
	if err := hostPowerOff(); err != nil {
		// The soft stop already happened, so a denied poweroff is not a disaster:
		// it means the escalation lacked privilege, not that nothing was done.
		log.Printf("thermal guard: poweroff failed (needs the install-time privilege): %v", err)
	}
}

// releaseHolds drops the keep-awake hold and any lid-closed hold, so a closed-lid
// machine is free to sleep and cool.
func (g *guardian) releaseHolds() {
	if g.awake != nil {
		_ = g.awake.Set(false)
	}
	if g.releaseLid != nil {
		g.releaseLid()
	}
}
