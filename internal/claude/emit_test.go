package claude

import "testing"

// A respawn closes the session while readLoop may still be draining stdout and
// calling emit. shutdown closes s.events, and a select can pick the (panicking)
// send-on-closed case, which used to crash the whole server. emit must survive
// concurrent emits after shutdown by dropping the event, not panicking.
func TestEmitAfterShutdownDoesNotPanic(t *testing.T) {
	s := &Session{events: make(chan Event, 1), closed: make(chan struct{})}
	s.shutdown() // closes both closed and events

	// Hammer emit after teardown; without the recover this panics with
	// "send on closed channel" on one of the iterations (the select is random).
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 2000; i++ {
			s.emit(Event{Kind: EventError})
		}
	}()
	<-done // reaching here without a panic is the assertion
}
