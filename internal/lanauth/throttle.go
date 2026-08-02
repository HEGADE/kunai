package lanauth

import "time"

// The throttle is what makes a six-digit PIN safe, so it is worth stating plainly
// what it is defending against and how.
//
// A million combinations falls in minutes to anything unthrottled. The defence is
// not to make each guess slow (that only costs the server) but to make the NUMBER
// of guesses tiny, and to make that limit hold against the obvious ways round it:
//
//   - Per-source counting alone is not enough. On a local network an attacker
//     picks their own source address and can change it at will, so per-IP limits
//     are an inconvenience rather than a bound. There is therefore a GLOBAL
//     counter as well, and it is the one that actually holds the line.
//   - Restarting does not help them either: the state is persisted with the rest
//     of the store, so a crash or an update does not hand back a fresh budget.
//   - The table is pruned and capped, because a map keyed by something the
//     attacker chooses is otherwise a way to exhaust memory instead of guessing.
//
// The cost of a global limit is that an attacker can lock the owner out, which is
// a denial of service. That is accepted deliberately: it is bounded (locks expire),
// and the owner is never locked out of the machine itself, because the loopback
// listener does not authenticate at all. Losing the tablet for fifteen minutes
// beats losing the agent.

// Policy is one set of limits. Two are used: a tight one per source address and a
// looser one across all of them.
type Policy struct {
	// FreeAttempts is how many failures are forgiven before locking begins, so an
	// owner mistyping their own PIN is not punished.
	FreeAttempts int
	// BaseLock is the first lockout; each further failure doubles it, up to MaxLock.
	BaseLock time.Duration
	MaxLock  time.Duration
	// Decay resets the count after a quiet spell, so yesterday's fumbling does not
	// leave the lock permanently at its maximum.
	Decay time.Duration
}

// DefaultPerSource is tight: a person typing their own PIN rarely misses five
// times, and an attacker gets nothing useful from these.
var DefaultPerSource = Policy{
	FreeAttempts: 4,
	BaseLock:     30 * time.Second,
	MaxLock:      time.Hour,
	Decay:        time.Hour,
}

// DefaultGlobal is the real bound, since it cannot be sidestepped by changing
// address. It is deliberately looser so that a household of devices does not trip
// it, and its cap is shorter because it affects everybody.
//
// Once tripped, the budget settles to roughly one guess per lock window. At a
// fifteen minute cap that is about a hundred guesses a day against a million
// possibilities, which is not an attack anybody finishes.
var DefaultGlobal = Policy{
	FreeAttempts: 12,
	BaseLock:     time.Minute,
	MaxLock:      15 * time.Minute,
	Decay:        2 * time.Hour,
}

// maxSources bounds the per-source table. Reached only under an attack that is
// rotating addresses, and in that case the global counter is already doing the
// work, so dropping the oldest entries costs nothing.
const maxSources = 512

// Counter is the failure state for one key.
type Counter struct {
	Fails int       `json:"fails"`
	Last  time.Time `json:"last"`
	Until time.Time `json:"until,omitempty"`
}

// Throttle holds the counters. Not safe for concurrent use on its own; the store
// that owns it provides the lock.
type Throttle struct {
	Sources map[string]*Counter `json:"sources,omitempty"`
	Global  Counter             `json:"global"`
}

// RetryAfter reports how long the caller must wait before another attempt is
// accepted. Zero means go ahead.
//
// Both limits are consulted and the longer wait wins, so neither can be used to
// shorten the other.
func (t *Throttle) RetryAfter(now time.Time, source string) time.Duration {
	wait := remaining(now, &t.Global, DefaultGlobal)
	if c := t.sourceCounter(source, false); c != nil {
		if d := remaining(now, c, DefaultPerSource); d > wait {
			wait = d
		}
	}
	return wait
}

// Fail records a wrong PIN and extends the locks.
func (t *Throttle) Fail(now time.Time, source string) {
	note(now, &t.Global, DefaultGlobal)
	note(now, t.sourceCounter(source, true), DefaultPerSource)
	t.prune(now)
}

// Succeed clears the source's count and eases the global one.
//
// The global counter is decremented rather than cleared, so a correct PIN cannot
// be used to wipe the evidence of an attack in progress: somebody guessing
// alongside the owner's normal use should not have their budget refilled every
// time the owner logs in.
func (t *Throttle) Succeed(now time.Time, source string) {
	if t.Sources != nil {
		delete(t.Sources, source)
	}
	if t.Global.Fails > 0 {
		t.Global.Fails--
	}
	t.Global.Until = time.Time{}
}

// sourceCounter returns the counter for a key, creating it only when asked.
func (t *Throttle) sourceCounter(source string, create bool) *Counter {
	if t.Sources == nil {
		if !create {
			return nil
		}
		t.Sources = make(map[string]*Counter)
	}
	c := t.Sources[source]
	if c == nil && create {
		c = &Counter{}
		t.Sources[source] = c
	}
	return c
}

// prune drops decayed entries, and if the table is still oversized drops the
// least recently active until it fits.
func (t *Throttle) prune(now time.Time) {
	for k, c := range t.Sources {
		if now.Sub(c.Last) > DefaultPerSource.Decay && now.After(c.Until) {
			delete(t.Sources, k)
		}
	}
	for len(t.Sources) > maxSources {
		oldestKey, oldest := "", time.Time{}
		for k, c := range t.Sources {
			if oldest.IsZero() || c.Last.Before(oldest) {
				oldestKey, oldest = k, c.Last
			}
		}
		delete(t.Sources, oldestKey)
	}
}

// remaining is how long this counter says to wait.
func remaining(now time.Time, c *Counter, p Policy) time.Duration {
	if c == nil || !now.Before(c.Until) {
		return 0
	}
	return c.Until.Sub(now)
}

// note records a failure against a counter and sets its next lock.
func note(now time.Time, c *Counter, p Policy) {
	if c == nil {
		return
	}
	// A quiet spell forgives what came before, so the lock reflects the attempt
	// happening now rather than every mistake ever made.
	if !c.Last.IsZero() && now.Sub(c.Last) > p.Decay {
		c.Fails = 0
	}
	c.Fails++
	c.Last = now
	over := c.Fails - p.FreeAttempts
	if over <= 0 {
		return
	}
	lock := p.BaseLock
	for i := 1; i < over && lock < p.MaxLock; i++ {
		lock *= 2
	}
	if lock > p.MaxLock {
		lock = p.MaxLock
	}
	c.Until = now.Add(lock)
}
