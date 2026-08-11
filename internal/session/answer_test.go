package session

import (
	"strings"
	"sync"
	"testing"

	"github.com/hegade/kunai/internal/claude"
)

// The answer hook is given what the model said out loud, once per turn.
func TestAnswerHookGetsTheSpokenText(t *testing.T) {
	f := newFakeDriver()
	s := newSession("ans", "/tmp/p", "", f)
	defer s.Close()

	var mu sync.Mutex
	var got []string
	s.SetAnswerHook(func(text string) { mu.Lock(); got = append(got, text); mu.Unlock() })

	if err := s.Prompt("go", nil, nil); err != nil {
		t.Fatal(err)
	}
	waitPrompts(t, f, 1)
	f.events <- claude.Event{Kind: claude.EventAssistant, Assistant: &claude.AssistantMessage{
		Content: []claude.AssistantContentBlock{
			{Type: "thinking", Text: "hmm"},
			{Type: "tool_use", Name: "Read"},
			{Type: "text", Text: "Here is what I found."},
		},
	}}
	endTurn(f, 0.01)
	quiet()

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("hook fired %d times, want once per turn", len(got))
	}
	if !strings.Contains(got[0], "Here is what I found.") {
		t.Errorf("text = %q, want what the model said", got[0])
	}
	// Thinking and tool calls are the model working, not the model answering.
	if strings.Contains(got[0], "hmm") {
		t.Errorf("thinking leaked into the answer: %q", got[0])
	}
}

// The reason this hook exists at all.
//
// Pull-request review collected its findings by SUBSCRIBING to the session and
// waiting for the turn to end. emitLocked drops a subscriber whose buffer fills
// and closes its channel, which is right for a phone that cannot keep up and
// fatal for a collector: a review streams for minutes, the watcher was dropped
// part-way, and it then saved nothing and logged nothing, because from its side
// the conversation had simply ended. Three real reviews produced neither a draft
// nor an error before anyone noticed.
//
// So the hook must fire even when every subscriber has been dropped for lag.
func TestAnswerHookSurvivesADroppedSubscriber(t *testing.T) {
	f := newFakeDriver()
	s := newSession("lag", "/tmp/p", "", f)
	defer s.Close()

	var mu sync.Mutex
	fired := false
	s.SetAnswerHook(func(string) { mu.Lock(); fired = true; mu.Unlock() })

	// A subscriber that never reads, which is what a slow client looks like.
	_, _, sub := s.Attach(0)

	if err := s.Prompt("go", nil, nil); err != nil {
		t.Fatal(err)
	}
	waitPrompts(t, f, 1)
	// Enough events to overflow the subscriber's buffer several times over, so it
	// is certainly dropped before the turn ends.
	for i := 0; i < subChanBuf*3; i++ {
		f.events <- claude.Event{Kind: claude.EventTextDelta, Text: "x"}
	}
	f.events <- claude.Event{Kind: claude.EventAssistant, Assistant: &claude.AssistantMessage{
		Content: []claude.AssistantContentBlock{{Type: "text", Text: "the findings"}},
	}}
	endTurn(f, 0.01)
	quiet()

	// The subscriber really was dropped: its channel is closed.
	drained := false
	for !drained {
		select {
		case _, ok := <-sub.ch:
			if !ok {
				drained = true
			}
		default:
			t.Fatal("the subscriber was not dropped, so this test is not exercising the case it exists for")
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if !fired {
		t.Error("the answer hook did not fire, so the turn's output would be lost exactly as it was in production")
	}
}

// A turn that said nothing does not call the hook, so a caller is never handed
// an empty answer to parse and report as a failure.
func TestAnswerHookStaysQuietForASilentTurn(t *testing.T) {
	f := newFakeDriver()
	s := newSession("silent", "/tmp/p", "", f)
	defer s.Close()

	var mu sync.Mutex
	calls := 0
	s.SetAnswerHook(func(string) { mu.Lock(); calls++; mu.Unlock() })

	if err := s.Prompt("go", nil, nil); err != nil {
		t.Fatal(err)
	}
	waitPrompts(t, f, 1)
	endTurn(f, 0.01) // no assistant message at all
	quiet()

	mu.Lock()
	defer mu.Unlock()
	if calls != 0 {
		t.Errorf("hook fired %d times for a turn that said nothing", calls)
	}
}
