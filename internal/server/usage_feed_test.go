package server

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/hegade/kunai/internal/schedule"
)

// The reported bug: a "fire after reset" job could not arm because the only
// source of reset times was a live session's rate_limit frame, which is rare and
// wiped on every restart. /usage knows the real reset continuously, so feeding
// it must arm the job even with no session ever running.
func TestFeedSchedulerResetsArmsAResetJob(t *testing.T) {
	dir := t.TempDir()

	// The session window resets in five hours, the way /usage would report it.
	reset := time.Now().Add(5 * time.Hour).Truncate(time.Minute)
	out := "Current session: 20% used · resets " +
		reset.Format("Jan 2, 3:04pm") + " (" + reset.Location().String() + ")\n"
	defer swapRun(func(ctx context.Context, bin string, env []string, d string, args ...string) ([]byte, error) {
		return []byte(out), nil
	})()

	sched := schedule.New(filepath.Join(dir, "schedule.json"), func(schedule.Job) error { return nil })
	sched.Create(schedule.Job{
		Trigger: schedule.Trigger{Kind: "reset", Window: "five_hour", OffsetSec: 60},
		Target:  schedule.Target{Cwd: "/x"},
		Prompt:  "go",
	})
	// Before any reset is known, the job is pending: no fire time.
	if ft := sched.List()[0].NextFire; !ft.IsZero() {
		t.Fatalf("job armed with no reset observed: next_fire = %v", ft)
	}

	s := &Server{
		cfg:   Config{DataDir: dir},
		clis:  defaultCLIs(),
		usage: newUsageCache(),
		sched: sched,
	}
	s.feedSchedulerResets(context.Background())

	// Now it is armed, to the reset /usage reported, without any session running.
	got := sched.List()[0].NextFire
	want := reset.Add(60 * time.Second)
	if got.Unix() != want.Unix() {
		t.Fatalf("job did not arm to the /usage reset: next_fire = %v, want %v", got, want)
	}
}

// With no reset job, the poll must not shell the CLI at all.
func TestFeedSchedulerResetsSkipsWhenNoResetJob(t *testing.T) {
	dir := t.TempDir()
	called := false
	defer swapRun(func(ctx context.Context, bin string, env []string, d string, args ...string) ([]byte, error) {
		called = true
		return []byte("Current session: 5% used\n"), nil
	})()

	sched := schedule.New(filepath.Join(dir, "schedule.json"), func(schedule.Job) error { return nil })
	sched.Create(schedule.Job{
		Trigger: schedule.Trigger{Kind: "at", At: time.Now().Add(time.Hour)},
		Target:  schedule.Target{Cwd: "/x"},
		Prompt:  "x",
	})
	s := &Server{cfg: Config{DataDir: dir}, clis: defaultCLIs(), usage: newUsageCache(), sched: sched}
	s.feedSchedulerResets(context.Background())
	if called {
		t.Error("shelled the CLI with no reset job waiting")
	}
}
