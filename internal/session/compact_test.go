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

// post_tokens is conversation-only; the fixed overhead (system prompt, tool
// schemas, memory, skills) stays resident in the window. The meter, set from an
// assistant usage that included that overhead, must subtract only the dropped
// conversation and keep the overhead. Dropping to the bare post_tokens read far
// too LOW right after a /compact (11.6k when Claude's own /context showed ~49k).
func TestCompactKeepsOverheadInMeter(t *testing.T) {
	f := newFakeDriver()
	s := newSession("c3", "/tmp/p", "", f)
	defer s.Close()
	_, _, sub := s.Attach(0)

	// A big turn: ~813k conversation plus ~37k fixed overhead = 850k resident.
	f.events <- claude.Event{Kind: claude.EventAssistant, Assistant: &claude.AssistantMessage{
		Usage: &claude.MessageUsage{Input: 10, CacheRead: 849990},
	}}
	// /compact reports the conversation shrank 813k -> 11.6k.
	f.events <- claude.Event{Kind: claude.EventCompact, Compact: &claude.Compact{
		Trigger: "manual", PreTokens: 813000, PostTokens: 11600,
	}}

	var compact *AppEvent
	for _, ev := range drain(t, sub, 2) { // assistant, compact
		if ev.T == EvCompact {
			e := ev
			compact = &e
		}
	}
	if compact == nil {
		t.Fatal("no compact event broadcast")
	}
	// 850000 - (813000 - 11600) = 48600: the overhead survives the compaction.
	if compact.ContextTokens != 48600 {
		t.Errorf("context_tokens = %d, want 48600 (overhead preserved, not the bare 11600 post)", compact.ContextTokens)
	}
	hello, _, _ := s.Attach(0)
	if hello.ContextTokens != 48600 {
		t.Errorf("hello context_tokens = %d, want 48600", hello.ContextTokens)
	}
}

// After a compaction the meter must keep climbing as the conversation regrows:
// every later assistant call reports the real (larger) context, and the meter
// has to follow it, not latch on the small post-compaction number. This is the
// exact shape of the reported bug (kunai stuck at post_tokens while Claude's own
// /context showed the true, larger fill).
func TestContextClimbsAfterCompaction(t *testing.T) {
	f := newFakeDriver()
	s := newSession("c2", "/tmp/p", "", f)
	defer s.Close()
	_, _, sub := s.Attach(0)

	// Compact down to a small summary.
	f.events <- claude.Event{Kind: claude.EventCompact, Compact: &claude.Compact{
		Trigger: "manual", PreTokens: 836411, PostTokens: 12537,
	}}
	// The CLI emits a partial assistant frame with no real usage first (all zero),
	// then the completed calls with climbing usage as the window refills.
	f.events <- claude.Event{Kind: claude.EventAssistant, Assistant: &claude.AssistantMessage{
		Usage: &claude.MessageUsage{}, // zero: must not blank the meter
	}}
	f.events <- claude.Event{Kind: claude.EventAssistant, Assistant: &claude.AssistantMessage{
		Usage: &claude.MessageUsage{Input: 2, CacheCreate: 31977, CacheRead: 16630}, // 48609
	}}
	f.events <- claude.Event{Kind: claude.EventAssistant, Assistant: &claude.AssistantMessage{
		Usage: &claude.MessageUsage{Input: 2, CacheRead: 103500}, // ~103.5k, the true fill
	}}

	var last int64
	for _, ev := range drain(t, sub, 4) { // compact, assistant x3
		if ev.T == EvAssistant && ev.ContextTokens > 0 {
			last = ev.ContextTokens
		}
	}
	if last != 103502 {
		t.Errorf("last broadcast context_tokens = %d, want 103502 (the meter must follow the regrowing context, not stay at 12537)", last)
	}
	// And a late client sees the climbed value, not the compaction floor.
	hello, _, _ := s.Attach(0)
	if hello.ContextTokens != 103502 {
		t.Errorf("hello context_tokens = %d, want 103502", hello.ContextTokens)
	}
}
