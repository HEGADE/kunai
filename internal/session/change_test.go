package session

import (
	"sync"
	"testing"
	"time"
)

// The list-changed callback is what lets the server push the session list instead
// of having every client ask for it on a timer. If it stops firing, nothing
// breaks loudly: the sockets simply go quiet and the sidebar silently returns to
// being seconds behind, which is the failure this whole mechanism existed to fix.
// So the wiring is pinned here rather than left to an end-to-end check.
func TestManagerReportsListChanges(t *testing.T) {
	var mu sync.Mutex
	fired := 0
	m := NewManager()
	m.SetOnChange(func() {
		mu.Lock()
		fired++
		mu.Unlock()
	})
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return fired
	}

	// A session the manager did not create, registered by hand: Create spawns a
	// real CLI, and none of what is under test needs one.
	s := newSession("s1", t.TempDir(), "", newFakeDriver())
	m.mu.Lock()
	m.sessions["s1"] = s
	m.mu.Unlock()
	s.SetOnListChange(m.changed)

	// A state change is a list change: the sidebar reports what each agent is
	// doing, so "running" is as much a list update as an arrival.
	s.setState(StateRunning)
	if got := count(); got != 1 {
		t.Fatalf("state change fired %d times, want 1", got)
	}

	// Setting the same state again must NOT fire. A turn's events re-assert the
	// state constantly, and a callback on every one of those would push the full
	// list to every client several times a second.
	s.setState(StateRunning)
	if got := count(); got != 1 {
		t.Errorf("an unchanged state fired the callback (%d times total)", got)
	}

	s.setState(StateIdle)
	if got := count(); got != 2 {
		t.Errorf("second state change fired %d times, want 2", got)
	}

	// Removal is a list change.
	m.remove("s1")
	if got := count(); got != 3 {
		t.Errorf("remove fired %d times, want 3", got)
	}
	// Removing something already gone is not: it would wake every client to send
	// a list identical to the one they hold.
	m.remove("s1")
	if got := count(); got != 3 {
		t.Errorf("removing a missing session fired the callback (%d total)", got)
	}
}

// The callback must not run while the session's own lock is held. A listener fans
// out to every connected socket, and holding the lock across that would let one
// slow client stall the turn that caused the change.
func TestListChangeCallbackRunsOutsideTheLock(t *testing.T) {
	s := newSession("s2", t.TempDir(), "", newFakeDriver())
	done := make(chan struct{})
	s.SetOnListChange(func() {
		// Meta() takes the same mutex setState holds. If the callback were fired
		// under that lock this would deadlock rather than return.
		_ = s.Meta()
		close(done)
	})
	s.setState(StateRunning)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("callback deadlocked: it is being fired while the session lock is held")
	}
}
