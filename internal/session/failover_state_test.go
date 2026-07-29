package session

import "testing"

// The reported failure this exists to prevent: auto-failover fired, worked, and
// moved the session onto another account, but nothing told the client. The
// composer went on naming the walled account for the several seconds the decision
// took, so a working failover was indistinguishable from one that never fired,
// and the obvious response was to switch accounts by hand.
func TestFailoverIsAnnouncedAndRetracted(t *testing.T) {
	f := newFakeDriver()
	s := newSession("fo", "/tmp/p", "", f)
	defer s.Close()
	_, _, sub := s.Attach(0)

	s.BeginFailover()
	got := drain(t, sub, 1)[0]
	if got.T != EvFailover || got.FailoverState != FailoverDeciding {
		t.Fatalf("begin sent %+v, want a failover/deciding event", got)
	}
	if !s.FailingOver() {
		t.Error("the session does not report that a failover is in flight")
	}

	const why = "No other account has headroom, so this session stays put."
	s.EndFailover(why)
	got = drain(t, sub, 1)[0]
	if got.T != EvFailover || got.FailoverState != FailoverEnded || got.Message != why {
		t.Fatalf("end sent %+v, want failover/ended carrying the reason", got)
	}
	if s.FailingOver() {
		t.Error("the session still reports a failover after it ended")
	}
}

// A client that attaches DURING the decision has to see it too: the several
// seconds a failover takes is exactly when a phone gets picked up, and the state
// is not in the event backlog it replays, it is a fact about the session now.
func TestHelloCarriesAnInFlightFailover(t *testing.T) {
	f := newFakeDriver()
	s := newSession("fo-hello", "/tmp/p", "", f)
	defer s.Close()

	if hello, _, _ := s.Attach(0); hello.FailoverState != "" {
		t.Fatalf("an ordinary session's hello claims failover state %q", hello.FailoverState)
	}

	s.BeginFailover()
	hello, _, _ := s.Attach(0)
	if hello.FailoverState != FailoverDeciding {
		t.Fatalf("hello.FailoverState = %q, want %q", hello.FailoverState, FailoverDeciding)
	}

	// And once it is over, a fresh attach is told nothing, because there is
	// nothing in flight to report.
	s.EndFailover("")
	if hello, _, _ = s.Attach(0); hello.FailoverState != "" {
		t.Fatalf("hello still reports %q after the failover ended", hello.FailoverState)
	}
}

// Beginning twice announces once. The turn-end hook can fire more than once for
// one wall (a chained failover re-enters it), and a client should not see the
// state flicker for a decision that never stopped.
func TestBeginFailoverIsIdempotent(t *testing.T) {
	f := newFakeDriver()
	s := newSession("fo-twice", "/tmp/p", "", f)
	defer s.Close()
	_, _, sub := s.Attach(0)

	s.BeginFailover()
	s.BeginFailover()
	drain(t, sub, 1) // the first announcement

	select {
	case ev := <-sub.ch:
		t.Fatalf("a second BeginFailover announced again: %+v", ev)
	default:
	}
}

// EndFailover with nothing to retract and nothing to say stays quiet, so a
// stand-down on a session that was never announced does not post a mystery event.
func TestEndFailoverSaysNothingWhenThereIsNothingToSay(t *testing.T) {
	f := newFakeDriver()
	s := newSession("fo-quiet", "/tmp/p", "", f)
	defer s.Close()
	_, _, sub := s.Attach(0)

	s.EndFailover("")
	select {
	case ev := <-sub.ch:
		t.Fatalf("EndFailover spoke with nothing to retract: %+v", ev)
	default:
	}
}
