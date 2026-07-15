package session

import (
	"testing"

	"github.com/hegade/kunai/internal/claude"
)

// Compaction is the one moment the context window shrinks, and the boundary frame
// is the only thing that reports the new size: no assistant message follows it.
// Before this, the boundary was dropped and the meter kept showing the
// pre-compaction number until the next turn happened to correct it.
func TestCompactResetsContextTokens(t *testing.T) {
	f := newFakeDriver()
	s := newSession("c1", "/tmp/p", "", f)
	defer s.Close()
	_, _, sub := s.Attach(0)

	// A turn establishes a large context, the way a long conversation would.
	f.events <- claude.Event{Kind: claude.EventAssistant, Assistant: &claude.AssistantMessage{
		Usage: &claude.MessageUsage{Input: 1000, CacheRead: 835411},
	}}
	// Then the user runs /compact.
	f.events <- claude.Event{Kind: claude.EventCompact, Compact: &claude.Compact{
		Trigger: "manual", PreTokens: 836411, PostTokens: 12537,
	}}

	var compact *AppEvent
	for _, ev := range drain(t, sub, 2) { // assistant, compact
		if ev.T == EvCompact {
			e := ev
			compact = &e
		}
		if ev.T == EvUser {
			t.Fatalf("compaction must not surface the summary as a user message: %q", ev.Text)
		}
	}
	if compact == nil {
		t.Fatal("no compact event broadcast")
	}
	if compact.ContextTokens != 12537 {
		t.Errorf("context_tokens = %d, want 12537 (the post-compaction size)", compact.ContextTokens)
	}
	if compact.PreTokens != 836411 || compact.Trigger != "manual" {
		t.Errorf("pre = %d trigger = %q, want 836411/manual", compact.PreTokens, compact.Trigger)
	}
	// The summary text is context, not conversation: it must never ride the wire.
	if compact.Text != "" {
		t.Errorf("compact event carried text: %q", compact.Text)
	}

	// A client attaching after the compaction must see the new size, not the old:
	// hello is the whole attachable state.
	hello, _, _ := s.Attach(0)
	if hello.ContextTokens != 12537 {
		t.Errorf("hello context_tokens = %d, want 12537", hello.ContextTokens)
	}
}
