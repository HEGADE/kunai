package session

import (
	"testing"

	"github.com/hegade/kunai/internal/claude"
)

// nestedAssistant is an assistant frame produced INSIDE a subagent: it carries the
// spawning Agent call's id, its own (smaller) context usage, and its own words.
func nestedAssistant(f *fakeDriver, parent, text string, ctxTokens int64) {
	f.events <- claude.Event{
		Kind:            claude.EventAssistant,
		ParentToolUseID: parent,
		Assistant: &claude.AssistantMessage{
			Content: []claude.AssistantContentBlock{{Type: "text", Text: text}},
			Usage:   &claude.MessageUsage{Input: ctxTokens},
		},
	}
}

// ctxOf reads the session's context-window occupancy the way a client does.
func ctxOf(s *Session) int64 {
	hello, _, _ := s.Attach(0)
	return hello.ContextTokens
}

// A subagent runs in its OWN context window, so its usage must not become the
// session's context meter -- that made the meter lurch downward mid-turn whenever
// an Agent was spawned.
func TestSubagentUsageDoesNotClobberContextMeter(t *testing.T) {
	f := newFakeDriver()
	s := newSession("sa1", "/tmp/p", "", f)
	defer s.Close()

	// The main agent's own call establishes the real context size.
	f.events <- claude.Event{Kind: claude.EventAssistant, Assistant: &claude.AssistantMessage{
		Content: []claude.AssistantContentBlock{{Type: "text", Text: "working"}},
		Usage:   &claude.MessageUsage{Input: 20000, CacheRead: 100000},
	}}
	quiet()
	if got := ctxOf(s); got != 120000 {
		t.Fatalf("context after the main call = %d, want 120000", got)
	}
	// Now a subagent reports its own small context. The meter must not follow it.
	nestedAssistant(f, "toolu_agent1", "subagent found the file", 3000)
	quiet()
	if got := ctxOf(s); got != 120000 {
		t.Errorf("a subagent clobbered the context meter: %d, want 120000 kept", got)
	}
}

// A loop ends when the model states its completion promise. A SUBAGENT saying the
// promise is not the main agent finishing, so nested text must not satisfy it.
func TestSubagentTextCannotSatisfyLoopPromise(t *testing.T) {
	fastLoop(t)
	f := newFakeDriver()
	s := newSession("sa2", "/tmp/p", "", f)
	defer s.Close()

	if err := s.StartLoop(LoopConfig{Prompt: "go", MaxIters: 5, MaxUSD: 100, Promise: "ALL DONE"}); err != nil {
		t.Fatal(err)
	}
	waitPrompts(t, f, 1)
	// The subagent claims completion; the main agent has said nothing.
	nestedAssistant(f, "toolu_agent1", "<promise>ALL DONE</promise>", 2000)
	endTurn(f, 0.01)
	quiet()

	if st := loopStatus(s); st.State != LoopRunning {
		t.Fatalf("loop state = %q, want still running: a subagent's promise is not the agent's", st.State)
	}
}

// The nesting id must reach the client on every event a subagent produces, so its
// work can be shown under the Agent card instead of as the main agent's own.
func TestSubagentEventsCarryParentID(t *testing.T) {
	f := newFakeDriver()
	s := newSession("sa3", "/tmp/p", "", f)
	defer s.Close()
	_, _, sub := s.Attach(0)
	defer s.Detach(sub)

	nestedAssistant(f, "toolu_agent1", "inner answer", 2000)
	f.events <- claude.Event{Kind: claude.EventToolResult, ParentToolUseID: "toolu_agent1",
		ToolResult: &claude.ToolResult{ToolUseID: "toolu_inner", Content: "grep output"}}
	f.events <- claude.Event{Kind: claude.EventTextDelta, Text: "tok", ParentToolUseID: "toolu_agent1"}

	got := map[string]string{}
	for i := 0; i < 3; i++ {
		ev := <-sub.Events()
		got[ev.T] = ev.ParentToolUseID
	}
	for _, tag := range []string{EvAssistant, EvToolResult, EvDelta} {
		if got[tag] != "toolu_agent1" {
			t.Errorf("%s parent = %q, want toolu_agent1", tag, got[tag])
		}
	}
}

// A top-level (non-subagent) event must carry NO parent, so ordinary work is never
// mistaken for nested work.
func TestTopLevelEventsHaveNoParent(t *testing.T) {
	f := newFakeDriver()
	s := newSession("sa4", "/tmp/p", "", f)
	defer s.Close()
	_, _, sub := s.Attach(0)
	defer s.Detach(sub)

	f.events <- claude.Event{Kind: claude.EventAssistant, Assistant: &claude.AssistantMessage{
		Content: []claude.AssistantContentBlock{{Type: "text", Text: "hi"}},
		Usage:   &claude.MessageUsage{Input: 10},
	}}
	ev := <-sub.Events()
	if ev.ParentToolUseID != "" {
		t.Errorf("top-level event carried parent %q", ev.ParentToolUseID)
	}
}
