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

	// Sessions begin in "starting", so the result also emits the flip to idle:
	// delta, delta, state(idle), result.
	got := drain(t, sub, 4)
	for i, ev := range got {
		if ev.Seq != uint64(i+1) {
			t.Fatalf("seqs not monotonic: %+v", got)
		}
	}
	if got[0].Text != "he" || got[2].T != EvState || got[2].State != StateIdle || got[3].T != EvResult {
		t.Fatalf("unexpected events: %+v", got)
	}

	// A fresh reconnect from seq 2 must replay events 3 and 4 only.
	hello, backlog, _ := s.Attach(2)
	if hello.HighSeq != 4 {
		t.Fatalf("hello.HighSeq want 4, got %d", hello.HighSeq)
	}
	if len(backlog) != 2 || backlog[0].Seq != 3 || backlog[1].Seq != 4 {
		t.Fatalf("replay from seq2 want [3,4], got %+v", backlog)
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
	if err := s.ResolvePermission("req-1", "allow", true); err != nil {
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
	if err := s.Prompt("hi", nil); err != nil {
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
