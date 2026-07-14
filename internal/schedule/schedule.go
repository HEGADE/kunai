// Package schedule runs prompts at a time, or relative to the Claude usage
// window reset. It is a pure-Go, in-process scheduler: a persisted job list plus
// one goroutine driven by a timer set to the soonest fire time. Firing is
// delegated to a callback (the server starts/resumes a session and sends the
// prompt), so this package stays independent of the session layer and fully
// unit-testable with an injected Clock.
package schedule

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Clock is the time source (real, or injected in tests).
type Clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Trigger is when a job fires: a fixed time, or an offset after a usage window's
// reset (the detected `resetsAt`).
type Trigger struct {
	Kind      string    `json:"kind"`                 // "at" | "reset"
	At        time.Time `json:"at,omitempty"`         // kind=="at"
	Window    string    `json:"window,omitempty"`     // kind=="reset": "five_hour" | "seven_day"
	OffsetSec int       `json:"offset_sec,omitempty"` // kind=="reset": seconds after resetsAt
}

// Target is what the fired job runs against.
type Target struct {
	Kind      string `json:"kind"` // "new" | "resume"
	Cwd       string `json:"cwd,omitempty"`
	Model     string `json:"model,omitempty"`
	Effort    string `json:"effort,omitempty"`
	Mode      string `json:"mode,omitempty"`       // permission mode (default autonomous)
	SessionID string `json:"session_id,omitempty"` // kind=="resume"
}

// Job is one scheduled prompt.
type Job struct {
	ID             string    `json:"id"`
	Name           string    `json:"name,omitempty"`
	Enabled        bool      `json:"enabled"`
	Trigger        Trigger   `json:"trigger"`
	Rearm          bool      `json:"rearm"` // re-schedule after firing (recurring)
	Target         Target    `json:"target"`
	Prompt         string    `json:"prompt"`
	LastRun        time.Time `json:"last_run,omitempty"`
	LastStatus     string    `json:"last_status,omitempty"`
	LastFiredReset int64     `json:"last_fired_reset,omitempty"` // the resetsAt this job last fired for
	NextFire       time.Time `json:"next_fire,omitempty"`        // computed, for display
}

// Scheduler owns the jobs and the firing loop.
type Scheduler struct {
	mu     sync.Mutex
	path   string
	clock  Clock
	fire   func(Job) error
	jobs   []*Job
	resets map[string]int64 // window -> resetsAt (unix seconds)
	wake   chan struct{}
}

// New loads persisted jobs from path and returns a scheduler; fire is called to
// run a job (must return promptly — it should start the session asynchronously).
func New(path string, fire func(Job) error) *Scheduler {
	s := &Scheduler{path: path, clock: realClock{}, fire: fire, resets: map[string]int64{}, wake: make(chan struct{}, 1)}
	s.load()
	return s
}

func windowLen(window string) time.Duration {
	if window == "seven_day" {
		return 7 * 24 * time.Hour
	}
	return 5 * time.Hour // five_hour (default)
}

// nextFireLocked computes when a job should next fire, or the zero time if it is
// pending (a reset trigger whose window hasn't been observed yet).
func (s *Scheduler) nextFireLocked(j *Job) time.Time {
	switch j.Trigger.Kind {
	case "at":
		return j.Trigger.At
	case "reset":
		r := s.resets[j.Trigger.Window]
		if r == 0 {
			return time.Time{}
		}
		fire := time.Unix(r, 0).Add(time.Duration(j.Trigger.OffsetSec) * time.Second)
		if j.LastFiredReset == r {
			// Already fired for this window; predict the next one. A fresh
			// rate_limit_event will correct this once the real next reset lands.
			fire = fire.Add(windowLen(j.Trigger.Window))
		}
		return fire
	}
	return time.Time{}
}

func catchupLimit(j Job) time.Duration {
	if j.Trigger.Kind == "reset" {
		return windowLen(j.Trigger.Window)
	}
	return 24 * time.Hour
}

// Run drives the firing loop until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	for {
		wait := s.tick()
		var timer *time.Timer
		if wait < 0 {
			timer = time.NewTimer(time.Hour) // nothing scheduled; re-check hourly
		} else {
			timer = time.NewTimer(wait)
		}
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

type fireItem struct {
	job Job
	nf  time.Time
}

// tick fires all due jobs and returns the duration until the next one (<0 if
// none). Jobs are copied out under the lock and fired without it, so a slow
// session start never blocks the loop.
func (s *Scheduler) tick() time.Duration {
	now := s.clock.Now()
	s.mu.Lock()
	var due []fireItem
	for _, j := range s.jobs {
		if !j.Enabled {
			continue
		}
		nf := s.nextFireLocked(j)
		j.NextFire = nf
		if !nf.IsZero() && !nf.After(now) {
			due = append(due, fireItem{*j, nf})
		}
	}
	s.mu.Unlock()

	for _, it := range due {
		s.runOne(it, now)
	}

	s.mu.Lock()
	soonest := time.Time{}
	for _, j := range s.jobs {
		if !j.Enabled {
			continue
		}
		nf := s.nextFireLocked(j)
		j.NextFire = nf
		if !nf.IsZero() && (soonest.IsZero() || nf.Before(soonest)) {
			soonest = nf
		}
	}
	s.save()
	s.mu.Unlock()

	if soonest.IsZero() {
		return -1
	}
	if d := soonest.Sub(s.clock.Now()); d > 0 {
		return d
	}
	return 0
}

func (s *Scheduler) runOne(it fireItem, now time.Time) {
	status := "ok"
	if now.Sub(it.nf) > catchupLimit(it.job) {
		status = "skipped (overdue)" // machine was off too long; don't dump a backlog
	} else if err := s.fire(it.job); err != nil {
		status = "error: " + err.Error()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.findLocked(it.job.ID)
	if j == nil {
		return
	}
	j.LastRun = now
	j.LastStatus = status
	if j.Trigger.Kind == "reset" {
		j.LastFiredReset = s.resets[j.Trigger.Window]
	}
	if j.Rearm {
		if j.Trigger.Kind == "at" {
			for !j.Trigger.At.After(s.clock.Now()) {
				j.Trigger.At = j.Trigger.At.Add(24 * time.Hour) // daily
			}
		}
		// reset jobs re-arm implicitly via LastFiredReset + nextFireLocked.
	} else {
		j.Enabled = false
	}
}

// NoteReset records a usage window's reset time (from a rate_limit_event) and
// wakes the loop so reset-triggered jobs recompute.
func (s *Scheduler) NoteReset(window string, resetsAt int64) {
	s.mu.Lock()
	changed := s.resets[window] != resetsAt
	s.resets[window] = resetsAt
	s.mu.Unlock()
	if changed {
		s.kick()
	}
}

func (s *Scheduler) kick() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// --- CRUD ---

func (s *Scheduler) List() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Job, len(s.jobs))
	for i, j := range s.jobs {
		cp := *j
		cp.NextFire = s.nextFireLocked(j)
		out[i] = cp
	}
	return out
}

func (s *Scheduler) Create(j Job) Job {
	s.mu.Lock()
	j.ID = randID()
	j.Enabled = true
	jp := j
	s.jobs = append(s.jobs, &jp)
	s.save()
	s.mu.Unlock()
	s.kick()
	return jp
}

// Replace updates a job's mutable fields by ID (returns false if not found).
func (s *Scheduler) Replace(in Job) bool {
	s.mu.Lock()
	j := s.findLocked(in.ID)
	if j != nil {
		j.Name, j.Enabled, j.Trigger, j.Rearm, j.Target, j.Prompt =
			in.Name, in.Enabled, in.Trigger, in.Rearm, in.Target, in.Prompt
		s.save()
	}
	found := j != nil
	s.mu.Unlock()
	s.kick()
	return found
}

func (s *Scheduler) SetEnabled(id string, on bool) bool {
	s.mu.Lock()
	j := s.findLocked(id)
	if j != nil {
		j.Enabled = on
		s.save()
	}
	found := j != nil
	s.mu.Unlock()
	s.kick()
	return found
}

func (s *Scheduler) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, j := range s.jobs {
		if j.ID == id {
			s.jobs = append(s.jobs[:i], s.jobs[i+1:]...)
			s.save()
			return true
		}
	}
	return false
}

// Resets exposes the observed window reset times (for /api/stats display).
func (s *Scheduler) Resets() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int64, len(s.resets))
	for k, v := range s.resets {
		out[k] = v
	}
	return out
}

// --- internals ---

func (s *Scheduler) findLocked(id string) *Job {
	for _, j := range s.jobs {
		if j.ID == id {
			return j
		}
	}
	return nil
}

func (s *Scheduler) load() {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, &s.jobs)
}

func (s *Scheduler) save() {
	if s.path == "" {
		return
	}
	b, err := json.MarshalIndent(s.jobs, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(s.path, b, 0o600)
}

func randID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%x", b[:])
}
