package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hegade/kunai/internal/session"
)

// The persister writes a running loop to disk and deletes the file the moment the
// loop ends. That delete is the whole safety story: a loop with no file is never
// resumed, so a thermal stop or a normal finish can't come back on the next boot.
func TestLoopPersisterWritesWhileRunningAndDeletesOnEnd(t *testing.T) {
	dir := t.TempDir()
	s := &Server{cfg: Config{DataDir: dir}}
	persist := s.loopPersister()

	rec := session.LoopPersist{
		SessionID: "abc-123",
		Cwd:       "/tmp/x",
		Config:    session.LoopConfig{Prompt: "go", MaxIters: 10, MaxUSD: 2},
		Iteration: 3,
		SpentUSD:  0.5,
		State:     session.LoopRunning,
	}
	persist(rec)

	path := filepath.Join(dir, "loops", "abc-123.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("running loop was not written: %v", err)
	}

	// A terminal state deletes it.
	rec.State = session.LoopStopped
	persist(rec)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("ended loop file still present: %v", err)
	}
}

// A record read back is exactly what was written: the boot path depends on it to
// recreate the session with --resume.
func TestLoopPersistRoundTrips(t *testing.T) {
	dir := t.TempDir()
	s := &Server{cfg: Config{DataDir: dir}}
	want := session.LoopPersist{
		SessionID: "round-trip",
		Cwd:       "/home/me/proj",
		Model:     "opus",
		Effort:    "high",
		Config:    session.LoopConfig{Prompt: "fix tests", Promise: "DONE", MaxIters: 20, MaxUSD: 3.5},
		Iteration: 7,
		SpentUSD:  1.25,
		Resumes:   2,
		State:     session.LoopRunning,
	}
	s.loopPersister()(want)

	b, err := os.ReadFile(filepath.Join(dir, "loops", "round-trip.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got session.LoopPersist
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("round-trip = %+v, want %+v", got, want)
	}
}

// An empty data dir (dev runs, no persistence configured) must be a silent no-op,
// never a panic or a stray file.
func TestLoopPersisterNoopWithoutDataDir(t *testing.T) {
	s := &Server{cfg: Config{DataDir: ""}}
	s.loopPersister()(session.LoopPersist{SessionID: "x", State: session.LoopRunning}) // must not panic
}
