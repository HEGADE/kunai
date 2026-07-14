package schedule

import (
	"path/filepath"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time { return c.t }

func newTest(fire func(Job) error) (*Scheduler, *fakeClock) {
	fc := &fakeClock{t: time.Unix(1000, 0)}
	s := New("", fire)
	s.clock = fc
	return s, fc
}

func TestAtFiresOnceAndDisables(t *testing.T) {
	fired := 0
	s, fc := newTest(func(Job) error { fired++; return nil })
	s.Create(Job{Trigger: Trigger{Kind: "at", At: time.Unix(1060, 0)}, Target: Target{Cwd: "/x"}, Prompt: "hi"})

	s.tick()
	if fired != 0 {
		t.Fatal("fired before its time")
	}
	fc.t = time.Unix(1070, 0)
	s.tick()
	if fired != 1 {
		t.Fatalf("want 1 fire, got %d", fired)
	}
	fc.t = time.Unix(9999, 0)
	s.tick()
	if fired != 1 {
		t.Fatalf("one-shot re-fired, got %d", fired)
	}
	if s.List()[0].Enabled {
		t.Fatal("one-shot job should be disabled after firing")
	}
}

func TestAtRearmDaily(t *testing.T) {
	fired := 0
	s, fc := newTest(func(Job) error { fired++; return nil })
	s.Create(Job{Trigger: Trigger{Kind: "at", At: time.Unix(1000, 0)}, Rearm: true, Target: Target{Cwd: "/x"}, Prompt: "x"})
	fc.t = time.Unix(1001, 0)
	s.tick()
	if fired != 1 {
		t.Fatalf("want 1, got %d", fired)
	}
	// re-armed to +24h; not due until then
	fc.t = time.Unix(1000+3600, 0)
	s.tick()
	if fired != 1 {
		t.Fatalf("re-armed job fired early, got %d", fired)
	}
	fc.t = time.Unix(1000+86400+5, 0)
	s.tick()
	if fired != 2 {
		t.Fatalf("want 2 after a day, got %d", fired)
	}
}

func TestResetFireAndRearm(t *testing.T) {
	fired := 0
	s, fc := newTest(func(Job) error { fired++; return nil })
	s.Create(Job{Trigger: Trigger{Kind: "reset", Window: "five_hour", OffsetSec: 60}, Rearm: true, Target: Target{Cwd: "/x"}, Prompt: "go"})

	s.tick()
	if fired != 0 {
		t.Fatal("reset job with no observed reset must stay pending")
	}
	s.NoteReset("five_hour", 1100) // fires at 1160
	fc.t = time.Unix(1150, 0)
	s.tick()
	if fired != 0 {
		t.Fatal("fired before reset+offset")
	}
	fc.t = time.Unix(1200, 0)
	s.tick()
	if fired != 1 {
		t.Fatalf("want 1, got %d", fired)
	}
	// Re-armed: same window shouldn't re-fire.
	fc.t = time.Unix(1500, 0)
	s.tick()
	if fired != 1 {
		t.Fatalf("re-fired the same window, got %d", fired)
	}
	// Next window's reset arrives -> fires again.
	s.NoteReset("five_hour", 20000)
	fc.t = time.Unix(20100, 0)
	s.tick()
	if fired != 2 {
		t.Fatalf("want 2 after next reset, got %d", fired)
	}
}

func TestCatchupSkipsWhenTooOverdue(t *testing.T) {
	fired := 0
	s, fc := newTest(func(Job) error { fired++; return nil })
	s.Create(Job{Trigger: Trigger{Kind: "at", At: time.Unix(1000, 0)}, Rearm: true, Target: Target{Cwd: "/x"}, Prompt: "x"})
	fc.t = time.Unix(1000+2*86400, 0) // 2 days overdue (> 24h limit)
	s.tick()
	if fired != 0 {
		t.Fatalf("should skip a job overdue beyond the limit, fired %d", fired)
	}
	if st := s.List()[0].LastStatus; st == "" || st == "ok" {
		t.Fatalf("want a skipped status, got %q", st)
	}
}

func TestPersistRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "schedule.json")
	s := New(p, func(Job) error { return nil })
	s.Create(Job{Name: "nightly", Trigger: Trigger{Kind: "at", At: time.Unix(5000, 0)}, Target: Target{Cwd: "/x"}, Prompt: "p"})

	s2 := New(p, func(Job) error { return nil })
	got := s2.List()
	if len(got) != 1 || got[0].Name != "nightly" || got[0].Prompt != "p" {
		t.Fatalf("jobs not persisted: %+v", got)
	}
}
