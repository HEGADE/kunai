package session

// Handing a caller the model's answer, reliably.
//
// The obvious way to collect what a session produced is to subscribe to it and
// watch for the turn to end. That is what pull-request review did, and it did not
// work: emitLocked DROPS a subscriber whose buffer fills and closes its channel,
// which is correct for a phone that cannot keep up but fatal for a watcher that
// must not miss the end. A review runs for minutes and streams thousands of delta
// events, so the watcher was routinely dropped part-way, saw no result, saved
// nothing, and -- worst of all -- logged nothing, because from its side the
// conversation had simply ended.
//
// So the answer is taken from inside the session instead, where lastText is
// already maintained for the loop's completion promise. A hook here cannot be
// dropped for lag, does not compete with a slow client, and fires exactly once
// per turn.
//
// Deliberately separate from SetTurnEndHook, which auto-failover owns: a session
// has one of those, and two features needing the end of a turn must not have to
// fight over it.

// SetAnswerHook registers a callback given the model's spoken text at the end of
// every turn: what it said out loud, with tool calls and thinking excluded.
//
// Called on its own goroutine, for the same reason the turn-end hook is: this
// runs on the driver's event pump, and a handler that blocks (or that respawns
// the session) would deadlock the pump against itself.
func (s *Session) SetAnswerHook(fn func(text string)) {
	s.mu.Lock()
	s.onAnswer = fn
	s.mu.Unlock()
}

// LastAnswer is what the model said out loud on the most recent turn, for a
// caller that wants it without waiting for the next one.
func (s *Session) LastAnswer() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastText
}

// fireAnswerHook is called from afterTurn, under no lock.
func (s *Session) fireAnswerHook() {
	s.mu.Lock()
	fn, text := s.onAnswer, s.lastText
	s.mu.Unlock()
	if fn != nil && text != "" {
		go fn(text)
	}
}
