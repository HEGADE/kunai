package session

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/hegade/kunai/internal/claude"
)

// fakeDriver feeds canned claude events into a Session and records commands.
type fakeDriver struct {
	events chan claude.Event

	mu        sync.Mutex
	resolved  map[string]claude.PermissionResult
	prompts   []string
	interrupt int
}

func newFakeDriver() *fakeDriver {
	return &fakeDriver{events: make(chan claude.Event, 64), resolved: map[string]claude.PermissionResult{}}
}

func (f *fakeDriver) Events() <-chan claude.Event { return f.events }
func (f *fakeDriver) SendUser(content any) error  { return nil }
func (f *fakeDriver) SendUserText(text string) error {
	f.mu.Lock()
	f.prompts = append(f.prompts, text)
	f.mu.Unlock()
	return nil
}
func (f *fakeDriver) Resolve(requestID string, r claude.PermissionResult) error {
	f.mu.Lock()
	f.resolved[requestID] = r
	f.mu.Unlock()
	return nil
}
func (f *fakeDriver) Interrupt() error {
	f.mu.Lock()
	f.interrupt++
	f.mu.Unlock()
	return nil
}
func (f *fakeDriver) SetModel(model string) error         { return nil }
func (f *fakeDriver) SetPermissionMode(mode string) error { return nil }
func (f *fakeDriver) Close() error                        { close(f.events); return nil }

// drain reads n events from a Subscriber (fails the test on timeout).
func drain(t *testing.T, sub *Subscriber, n int) []AppEvent {
	t.Helper()
	out := make([]AppEvent, 0, n)
	for i := 0; i < n; i++ {
		select {
		case ev := <-sub.ch:
			out = append(out, ev)
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for event %d/%d", i+1, n)
		}
	}
	return out
}

func TestSequencingAndReplay(t *testing.T) {
	f := newFakeDriver()
	s := newSession("s1", "/tmp/p", "", f)
	defer s.Close()

	// Attach a live Subscriber so we can observe processing order deterministically.
	_, _, sub := s.Attach(0)

	f.events <- claude.Event{Kind: claude.EventTextDelta, Text: "he"}
	f.events <- claude.Event{Kind: claude.EventTextDelta, Text: "llo"}
	f.events <- claude.Event{Kind: claude.EventResult, Raw: json.RawMessage(`{"subtype":"success","duration_ms":42}`)}

	// Streaming deltas reach the live subscriber but are transient: they carry no
	// Seq and are never buffered. Only the durable events are sequenced, so the
	// result (which also flips starting->idle) yields state(seq 1), result(seq 2).
	got := drain(t, sub, 4)
	if got[0].T != EvDelta || got[0].Seq != 0 || got[0].Text != "he" {
		t.Fatalf("delta 0 should be transient (seq 0): %+v", got[0])
	}
	if got[1].T != EvDelta || got[1].Seq != 0 || got[1].Text != "llo" {
		t.Fatalf("delta 1 should be transient (seq 0): %+v", got[1])
	}
	if got[2].T != EvState || got[2].State != StateIdle || got[2].Seq != 1 {
		t.Fatalf("want state(idle) seq 1, got %+v", got[2])
	}
	if got[3].T != EvResult || got[3].Seq != 2 {
		t.Fatalf("want result seq 2, got %+v", got[3])
	}

	// A fresh reconnect from seq 0 replays only the durable events (no deltas).
	hello, backlog, _ := s.Attach(0)
	if hello.HighSeq != 2 {
		t.Fatalf("hello.HighSeq want 2, got %d", hello.HighSeq)
	}
	if len(backlog) != 2 || backlog[0].T != EvState || backlog[1].T != EvResult {
		t.Fatalf("replay from seq0 want [state, result], got %+v", backlog)
	}
}

// TestStreamingDeltasDoNotEvictHistory guards the fix for the bug where the
// replay ring buffered every streaming token delta: a single turn emits hundreds
// of them, so the ring filled up within a few turns and evicted the actual
// conversation, and reopening a session showed only the most recent messages.
// Deltas must be fanned out live but never buffered.
func TestStreamingDeltasDoNotEvictHistory(t *testing.T) {
	f := newFakeDriver()
	s := newSession("s1", "/tmp/p", "", f)
	defer s.Close()

	// A durable turn, then a flood of deltas far exceeding the ring capacity,
	// then another durable turn. Under the old behavior the flood evicted the
	// first turn from the ring.
	f.events <- claude.Event{Kind: claude.EventResult, Raw: json.RawMessage(`{"subtype":"success"}`)}
	for i := 0; i < ringCapacity+1000; i++ {
		f.events <- claude.Event{Kind: claude.EventTextDelta, Text: "x"}
	}
	f.events <- claude.Event{Kind: claude.EventResult, Raw: json.RawMessage(`{"subtype":"success"}`)}

	// Poll the ring snapshot until both durable results have been processed.
	var backlog []AppEvent
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, backlog, _ = s.Attach(0)
		results := 0
		for _, ev := range backlog {
			if ev.T == EvResult {
				results++
			}
		}
		if results >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	for _, ev := range backlog {
		if ev.T == EvDelta {
			t.Fatalf("delta events must never be buffered (ring has %d events)", len(backlog))
		}
	}
	// The ring holds only the handful of durable events despite the flood; the
	// first turn survived rather than being evicted.
	if len(backlog) > 20 {
		t.Fatalf("ring should hold only durable events, got %d", len(backlog))
	}
	if backlog[0].T != EvState || backlog[len(backlog)-1].T != EvResult {
		t.Fatalf("first durable turn should survive the delta flood, got %+v", backlog)
	}
}

func TestPermissionPendingAndResolve(t *testing.T) {
	f := newFakeDriver()
	s := newSession("s1", "/tmp/p", "", f)
	defer s.Close()

	_, _, sub := s.Attach(0)

	sugg := json.RawMessage(`[{"type":"addRules","rules":[{"toolName":"Bash","ruleContent":"git:*"}],"behavior":"allow","destination":"session"}]`)
	f.events <- claude.Event{Kind: claude.EventPermission, Permission: &claude.PermissionAsk{
		RequestID: "req-1", ToolName: "Bash", ToolUseID: "tu-1",
		Input: json.RawMessage(`{"command":"git status"}`), Suggestions: sugg,
	}}

	// Expect a state change (idle→awaiting) and the permission event.
	evs := drain(t, sub, 2)
	var perm *AppEvent
	for i := range evs {
		if evs[i].T == EvPermission {
			perm = &evs[i]
		}
	}
	if perm == nil || perm.RequestID != "req-1" || perm.ToolName != "Bash" {
		t.Fatalf("permission event missing/wrong: %+v", evs)
	}

	// A reconnecting client learns about the pending ask via hello.
	hello, _, _ := s.Attach(perm.Seq)
	if len(hello.Pending) != 1 || hello.Pending[0].RequestID != "req-1" {
		t.Fatalf("hello.Pending should carry req-1, got %+v", hello.Pending)
	}
	if hello.State != StateAwaiting {
		t.Fatalf("state want awaiting, got %q", hello.State)
	}

	// Resolve allow+always → driver gets allow with persisted permissions.
	if err := s.ResolvePermission("req-1", "allow", true, nil); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	f.mu.Lock()
	r, ok := f.resolved["req-1"]
	f.mu.Unlock()
	if !ok || r.Behavior != "allow" {
		t.Fatalf("driver.Resolve not called with allow: %+v", r)
	}
	if len(r.UpdatedPermissions) != 1 || r.UpdatedPermissions[0].Rules[0].RuleContent != "git:*" {
		t.Fatalf("always-allow should forward suggestions, got %+v", r.UpdatedPermissions)
	}

	// Pending must be cleared for future attaches.
	hello2, _, _ := s.Attach(0)
	if len(hello2.Pending) != 0 {
		t.Fatalf("pending should be empty after resolve, got %+v", hello2.Pending)
	}
}

func TestPromptSetsRunningState(t *testing.T) {
	f := newFakeDriver()
	s := newSession("s1", "/tmp/p", "", f)
	defer s.Close()

	_, _, sub := s.Attach(0)
	if err := s.Prompt("hi", nil, nil); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	// Prompt echoes the user turn, then transitions to running.
	got := drain(t, sub, 2)
	if got[0].T != EvUser || got[0].Text != "hi" {
		t.Fatalf("want user echo first, got %+v", got[0])
	}
	if got[1].T != EvState || got[1].State != StateRunning {
		t.Fatalf("want running state event, got %+v", got[1])
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.prompts) != 1 || f.prompts[0] != "hi" {
		t.Fatalf("prompt not forwarded to driver: %+v", f.prompts)
	}
}
