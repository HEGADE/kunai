package session

// Saying out loud that a failover is under way.
//
// Auto-failover is not instant. Choosing where to send a walled session means
// reading every candidate account's quota, and for a Claude account that is a
// `claude /usage` shell at a couple of seconds each. So there is a window of
// seconds between "your turn died on the wall" and "you are now on another
// account", and through all of it the composer correctly showed the account that
// had just been walled, with nothing else to report.
//
// That is how a failover that worked perfectly got reported as never firing: the
// user saw the limit message, saw the old account still named in the composer,
// concluded the feature was broken, and switched accounts by hand. A slow silent
// operation is indistinguishable from a broken one, and the fix is not to make it
// faster but to make it speak.
//
// The in-progress state is deliberately kept ON the session rather than only
// broadcast, because "anything a late or reconnecting client needs belongs in
// hello" -- a phone that attaches mid-decision has to see it too.

// BeginFailover records that auto-failover is now looking for somewhere to move
// this session, and tells every attached client.
func (s *Session) BeginFailover() {
	s.mu.Lock()
	if s.failingOver {
		s.mu.Unlock()
		return
	}
	s.failingOver = true
	s.mu.Unlock()
	s.broadcast(AppEvent{T: EvFailover, FailoverState: FailoverDeciding})
}

// EndFailover clears that state and says why the session was not moved. It is
// called only when the session STAYS where it is: a successful move closes this
// session and builds a new one, whose hello carries the new account and no
// failover state at all, so there is nothing to announce on the way out.
//
// The reason is worth a line of its own. "Rate-limited" already tells you the
// window is spent; what you cannot otherwise learn is whether kunai tried to move
// you and found nowhere to go, or never tried because the feature is off.
func (s *Session) EndFailover(reason string) {
	s.mu.Lock()
	was := s.failingOver
	s.failingOver = false
	s.mu.Unlock()
	if !was && reason == "" {
		return // nothing was announced, so there is nothing to retract
	}
	s.broadcast(AppEvent{T: EvFailover, FailoverState: FailoverEnded, Message: reason})
}

// FailingOver reports whether a failover decision is in flight, for callers that
// must not act on the session while it is being moved.
func (s *Session) FailingOver() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failingOver
}

// failoverStateOf is the hello spelling of the flag: a state string when a
// decision is in flight, and absent otherwise, so an ordinary session's hello
// says nothing about failover at all.
func failoverStateOf(failingOver bool) string {
	if failingOver {
		return FailoverDeciding
	}
	return ""
}
