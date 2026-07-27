package session

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/hegade/kunai/internal/claude"
)

// endTurnErr ends a turn the way the CLI ends one it could not run, which for a
// spent subscription window is an ordinary error result carrying the reason as
// text. This is the shape that produced the reported bug: the chat showed
// "Rate-limited" (the client parses this same text) while auto-failover, the
// loop's stop condition and the scheduler's reset pin all sat out, because
// nothing server-side read it.
func endTurnErr(f *fakeDriver, text string) {
	raw, _ := json.Marshal(map[string]any{
		"subtype": "error_during_execution", "is_error": true, "result": text,
	})
	f.events <- claude.Event{Kind: claude.EventResult, Raw: json.RawMessage(raw)}
}

func TestParseWall(t *testing.T) {
	cases := []struct {
		text  string
		want  bool
		reset int64
	}{
		{"Claude AI usage limit reached|1753849800", true, 1753849800},
		{"Claude AI usage limit reached", true, 0},
		{"5-hour limit reached ∙ usage limit reached", true, 0},
		{"error_during_execution API Error: 400 quota exceeded", true, 0},
		// Everything else is an ordinary failure. Failover acts on this, so a
		// false positive spends another account's quota on a healthy session.
		{"", false, 0},
		{"error_during_execution", false, 0},
		{"Error: ENOENT no such file or directory", false, 0},
		{"the tool call was interrupted by the user", false, 0},
		{"I hit my daily step limit on my fitness tracker", false, 0},
	}
	for _, c := range cases {
		reset, ok := parseWall(c.text)
		if ok != c.want || reset != c.reset {
			t.Errorf("parseWall(%q) = (%d, %v), want (%d, %v)", c.text, reset, ok, c.reset, c.want)
		}
	}
}

func TestResultTextSurvivesANonStringResult(t *testing.T) {
	// A failed turn does not promise `result` is a string. Decoding must not throw
	// the frame away, or the wall goes unread exactly when it matters.
	raw := json.RawMessage(`{"subtype":"error","result":{"code":429},"error":"usage limit reached|1753849800"}`)
	reset, ok := parseWall(resultText(raw))
	if !ok || reset != 1753849800 {
		t.Fatalf("got (%d, %v), want the wall read off the error field", reset, ok)
	}
}

// The bug itself: a turn that ends against the wall as error text must reach the
// turn-end hook as rateLimited, which is the single thing auto-failover acts on.
func TestWallInErrorTextReachesTheTurnEndHook(t *testing.T) {
	f := newFakeDriver()
	s := newSession("wall", "/tmp/p", "", f)
	defer s.Close()

	var mu sync.Mutex
	var calls []bool
	s.SetTurnEndHook(func(rl, _ bool) { mu.Lock(); calls = append(calls, rl); mu.Unlock() })

	if err := s.Prompt("do the thing", nil, nil); err != nil {
		t.Fatal(err)
	}
	waitPrompts(t, f, 1)
	endTurnErr(f, "Claude AI usage limit reached|1753849800")
	quiet()

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 || !calls[0] {
		t.Fatalf("hook calls=%v, want exactly one reporting rateLimited", calls)
	}
	if _, reset := s.LastLimit(); reset != 1753849800 {
		t.Fatalf("LastLimit reset=%d, want the epoch the message carried", reset)
	}
}

// And the other half: an ordinary failed turn is not a wall. Without this, every
// failure would roll the session onto another account.
func TestAnOrdinaryFailedTurnIsNotAWall(t *testing.T) {
	f := newFakeDriver()
	s := newSession("nowall", "/tmp/p", "", f)
	defer s.Close()

	var mu sync.Mutex
	var calls []bool
	s.SetTurnEndHook(func(rl, _ bool) { mu.Lock(); calls = append(calls, rl); mu.Unlock() })

	if err := s.Prompt("do the thing", nil, nil); err != nil {
		t.Fatal(err)
	}
	waitPrompts(t, f, 1)
	endTurnErr(f, "Error: ENOENT no such file or directory")
	quiet()

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 || calls[0] {
		t.Fatalf("hook calls=%v, want exactly one reporting NOT rateLimited", calls)
	}
}

// A window that reset must let go. The text latch has nothing to retract it, so
// a completed turn is what clears it; otherwise the next healthy turn after a
// reset would fail over.
func TestASuccessfulTurnClearsATextWall(t *testing.T) {
	f := newFakeDriver()
	s := newSession("relatch", "/tmp/p", "", f)
	defer s.Close()

	var mu sync.Mutex
	var calls []bool
	s.SetTurnEndHook(func(rl, _ bool) { mu.Lock(); calls = append(calls, rl); mu.Unlock() })

	if err := s.Prompt("first", nil, nil); err != nil {
		t.Fatal(err)
	}
	waitPrompts(t, f, 1)
	endTurnErr(f, "Claude AI usage limit reached|1753849800")
	quiet()

	if err := s.Prompt("after the reset", nil, nil); err != nil {
		t.Fatal(err)
	}
	waitPrompts(t, f, 2)
	endTurn(f, 0.02)
	quiet()

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("hook fired %d times, want two", len(calls))
	}
	if !calls[0] || calls[1] {
		t.Fatalf("hook calls=%v, want the wall then a clean turn", calls)
	}
}

// A control frame is the CLI's own answer and must not be undone by a turn that
// happened to succeed, which is the pre-existing behaviour this change had to
// leave alone.
func TestASuccessfulTurnDoesNotClearARejectedControlFrame(t *testing.T) {
	f := newFakeDriver()
	s := newSession("ctrl", "/tmp/p", "", f)
	defer s.Close()

	var mu sync.Mutex
	var calls []bool
	s.SetTurnEndHook(func(rl, _ bool) { mu.Lock(); calls = append(calls, rl); mu.Unlock() })

	if err := s.Prompt("go", nil, nil); err != nil {
		t.Fatal(err)
	}
	waitPrompts(t, f, 1)
	f.events <- claude.Event{Kind: claude.EventRateLimit, Window: "seven_day", ResetsAt: 1, LimitStatus: "rejected"}
	endTurn(f, 0.01)
	quiet()

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 || !calls[0] {
		t.Fatalf("hook calls=%v, want the rejected frame to win", calls)
	}
}
