package telegram

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeActor counts chat actions, safely: the heartbeat runs on its own
// goroutine.
type fakeActor struct {
	mu      sync.Mutex
	actions []string
	fired   chan struct{}
}

func newFakeActor() *fakeActor {
	return &fakeActor{fired: make(chan struct{}, 64)}
}

func (f *fakeActor) SendChatAction(_ context.Context, _ int64, action string) error {
	f.mu.Lock()
	f.actions = append(f.actions, action)
	f.mu.Unlock()
	select {
	case f.fired <- struct{}{}:
	default:
	}
	return nil
}

func (f *fakeActor) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.actions)
}

// waitFor blocks until n actions have been sent, or fails the test. Counting
// against a sleep would be flaky on a loaded machine.
func (f *fakeActor) waitFor(t *testing.T, n int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for f.count() < n {
		select {
		case <-f.fired:
		case <-deadline:
			t.Fatalf("only %d chat actions after 2s, want %d", f.count(), n)
		}
	}
}

// Telegram expires the typing status after five seconds and clears it whenever
// the bot sends a message, so one call is not enough for a turn that takes
// minutes. It has to keep beating.
func TestTypistRepeatsWhileRunning(t *testing.T) {
	f := newFakeActor()
	ty := newTypist(f, 42)
	ty.every = time.Millisecond
	defer ty.Stop()

	ty.Start(context.Background())
	f.waitFor(t, 3)

	f.mu.Lock()
	defer f.mu.Unlock()
	for i, a := range f.actions {
		if a != "typing" {
			t.Fatalf("action %d is %q, want typing", i, a)
		}
	}
}

// The indicator must go down when the turn ends, or the chat claims the agent is
// working long after it stopped.
func TestTypistStopsBeating(t *testing.T) {
	f := newFakeActor()
	ty := newTypist(f, 42)
	ty.every = time.Millisecond

	ty.Start(context.Background())
	f.waitFor(t, 2)
	ty.Stop()

	settled := f.count()
	time.Sleep(20 * time.Millisecond) // many ticks' worth, had it kept going
	if got := f.count(); got > settled+1 {
		t.Fatalf("kept typing after Stop: %d actions, was %d", got, settled)
	}
}

// Two state events in a row must not leave two heartbeats running: the second
// would double the request rate and outlive the first Stop.
func TestTypistStartIsIdempotent(t *testing.T) {
	f := newFakeActor()
	ty := newTypist(f, 42)
	ty.every = 50 * time.Millisecond

	ty.Start(context.Background())
	ty.Start(context.Background())
	ty.Start(context.Background())
	f.waitFor(t, 1)
	ty.Stop()

	settled := f.count()
	time.Sleep(150 * time.Millisecond)
	if got := f.count(); got != settled {
		t.Fatalf("a second heartbeat survived Stop: %d actions, was %d", got, settled)
	}
}

// Stop before Start, and Stop twice, are both things the event pump can do
// (an idle state event arriving first, then a deferred Stop on shutdown).
func TestTypistStopIsSafeWhenNotRunning(t *testing.T) {
	ty := newTypist(newFakeActor(), 42)
	ty.Stop()
	ty.Start(context.Background())
	ty.Stop()
	ty.Stop()
}

// Cancelling the watcher's context has to end the heartbeat too, or a closed
// chat keeps talking to Telegram forever.
func TestTypistStopsWithContext(t *testing.T) {
	f := newFakeActor()
	ty := newTypist(f, 42)
	ty.every = time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	ty.Start(ctx)
	f.waitFor(t, 2)
	cancel()

	time.Sleep(20 * time.Millisecond)
	settled := f.count()
	time.Sleep(20 * time.Millisecond)
	if got := f.count(); got != settled {
		t.Fatalf("heartbeat outlived its context: %d actions, was %d", got, settled)
	}
}

// The keep-alive rides the typing heartbeat, because "a turn is running" is one
// fact and two tickers saying it would be two things to keep in step.
func TestTypistDrivesTheKeepAlive(t *testing.T) {
	f := newFakeActor()
	ty := newTypist(f, 42)
	ty.every = time.Millisecond
	ty.refresh = time.Millisecond
	beats := make(chan struct{}, 16)
	ty.keepAlive = func(context.Context) {
		select {
		case beats <- struct{}{}:
		default:
		}
	}
	defer ty.Stop()

	ty.Start(context.Background())
	for i := 0; i < 2; i++ {
		select {
		case <-beats:
		case <-time.After(2 * time.Second):
			t.Fatal("the keep-alive never ran, so a long turn shows nothing until it ends")
		}
	}
}

// A typist with no keep-alive set must not panic: not every caller wants one.
func TestTypistWithoutAKeepAliveIsFine(t *testing.T) {
	f := newFakeActor()
	ty := newTypist(f, 42)
	ty.every = time.Millisecond
	defer ty.Stop()
	ty.Start(context.Background())
	f.waitFor(t, 2)
}
