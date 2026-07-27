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

// A respawn builds a new Session behind the same id, so event numbering restarts
// at 1 while every attached client is still holding the old high-water mark. If
// Attach trusted that number, `since(500)` against a ring numbered 1..N would
// match nothing and the client would sit in silence indistinguishable from an
// idle session -- permanently, because the number never comes back down.
//
// The SPA's own tab survived this only by throwing its connection away by hand,
// which no other client knows to do: a second phone, a Telegram view, or a shared
// link just goes deaf. Auto-failover respawns with no initiating client at all,
// so today it blinds everyone attached.
func TestAttachRecoversFromAPreviousIncarnationsSeq(t *testing.T) {
	s := newSession("s3", t.TempDir(), "", newFakeDriver())
	for i := 0; i < 3; i++ {
		s.mu.Lock()
		s.sequenceLocked(AppEvent{T: EvUser, Text: "turn"})
		s.mu.Unlock()
	}

	// A client from the dead process asks for everything after seq 500.
	hello, backlog, sub := s.Attach(500)
	defer s.Detach(sub)
	if len(backlog) != 3 {
		t.Fatalf("backlog had %d events, want all 3 replayed: a stale seq must not silence the session", len(backlog))
	}
	if hello.Epoch == "" {
		t.Error("hello carried no epoch, so a client cannot tell the process changed")
	}

	// A seq this session really has reached still means what it says.
	if _, gap, sub2 := s.Attach(2); len(gap) != 1 {
		t.Errorf("a real seq replayed %d events, want just the 1 after it", len(gap))
		s.Detach(sub2)
	} else {
		s.Detach(sub2)
	}
}

// Two sessions must not share an epoch, or a client could not tell them apart
// across a respawn.
func TestEachSessionGetsItsOwnEpoch(t *testing.T) {
	a := newSession("a", t.TempDir(), "", newFakeDriver())
	b := newSession("b", t.TempDir(), "", newFakeDriver())
	ha, _, sa := a.Attach(0)
	hb, _, sb := b.Attach(0)
	defer a.Detach(sa)
	defer b.Detach(sb)
	if ha.Epoch == "" || ha.Epoch == hb.Epoch {
		t.Fatalf("epochs must be distinct and non-empty, got %q and %q", ha.Epoch, hb.Epoch)
	}
	// Stable for the life of the session: it identifies the process, not the attach.
	if again, _, s2 := a.Attach(0); again.Epoch != ha.Epoch {
		t.Errorf("epoch changed between attaches: %q then %q", ha.Epoch, again.Epoch)
		a.Detach(s2)
	} else {
		a.Detach(s2)
	}
}
