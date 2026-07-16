package session

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hegade/kunai/internal/claude"
)

// These tests are all about one thing: a loop runs while nobody is watching, so
// every way it can end has to be proven, not assumed.

// fastLoop removes the inter-iteration pause so a test need not sleep for it.
func fastLoop(t *testing.T) {
	t.Helper()
	prev := loopCooldown
	loopCooldown = time.Millisecond
	t.Cleanup(func() { loopCooldown = prev })
}

// endTurn feeds the result frame that finishes the current turn.
func endTurn(f *fakeDriver, totalCostUSD float64) {
	raw, _ := json.Marshal(map[string]any{"subtype": "success", "total_cost_usd": totalCostUSD})
	f.events <- claude.Event{Kind: claude.EventResult, Raw: json.RawMessage(raw)}
}

// says makes the model speak, which is where a completion promise has to appear.
func says(f *fakeDriver, text string) {
	f.events <- claude.Event{Kind: claude.EventAssistant, Assistant: &claude.AssistantMessage{
		Content: []claude.AssistantContentBlock{{Type: "text", Text: text}},
	}}
}

// loopStatus reads the loop as a freshly attached client would see it, which is
// also the path a phone takes when it wakes up.
func loopStatus(s *Session) *LoopStatus {
	hello, _, _ := s.Attach(0)
	return hello.Loop
}

// quiet gives the loop a moment to do anything it was going to do.
func quiet() { time.Sleep(60 * time.Millisecond) }

// The iteration cap is the backstop that works even when cost reporting doesn't.
func TestLoopStopsAtMaxIterations(t *testing.T) {
	fastLoop(t)
	f := newFakeDriver()
	s := newSession("l1", "/tmp/p", "", f)
	defer s.Close()

	if err := s.StartLoop(LoopConfig{Prompt: "keep going", MaxIters: 3, MaxUSD: 100}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		waitPrompts(t, f, i)
		endTurn(f, 0.01*float64(i))
	}
	quiet()

	if got := len(f.sentPrompts()); got != 3 {
		t.Fatalf("ran %d iterations, want exactly 3", got)
	}
	st := loopStatus(s)
	if st.State != LoopExhausted {
		t.Fatalf("state = %q (%s), want exhausted", st.State, st.Reason)
	}
}

// The budget is the guard that matters at 3am: it must bind even when the
// iteration cap is nowhere near.
func TestLoopStopsWhenTheBudgetRunsOut(t *testing.T) {
	fastLoop(t)
	f := newFakeDriver()
	s := newSession("l2", "/tmp/p", "", f)
	defer s.Close()

	if err := s.StartLoop(LoopConfig{Prompt: "burn", MaxIters: 100, MaxUSD: 0.05}); err != nil {
		t.Fatal(err)
	}
	// total_cost_usd is a session running total, so 0.02 a turn.
	for i := 1; i <= 3; i++ {
		waitPrompts(t, f, i)
		endTurn(f, 0.02*float64(i))
	}
	quiet()

	if got := len(f.sentPrompts()); got != 3 {
		t.Fatalf("ran %d iterations, want 3 (0.06 crosses the 0.05 budget)", got)
	}
	st := loopStatus(s)
	if st.State != LoopExhausted {
		t.Fatalf("state = %q (%s), want exhausted", st.State, st.Reason)
	}
	if st.SpentUSD < 0.059 || st.SpentUSD > 0.061 {
		t.Errorf("spent = %v, want ~0.06", st.SpentUSD)
	}
}

// A loop started mid-conversation must be billed for its own turns only, or it
// would inherit the whole session's spend and stop on the first iteration.
func TestLoopBudgetCountsOnlyItsOwnTurns(t *testing.T) {
	fastLoop(t)
	f := newFakeDriver()
	s := newSession("l3", "/tmp/p", "", f)
	defer s.Close()

	// A long conversation happened before the loop was ever started.
	endTurn(f, 5.00)
	quiet()

	if err := s.StartLoop(LoopConfig{Prompt: "go", MaxIters: 2, MaxUSD: 1.00}); err != nil {
		t.Fatal(err)
	}
	waitPrompts(t, f, 1)
	endTurn(f, 5.10) // this turn cost 0.10, not 5.10
	quiet()

	st := loopStatus(s)
	if st.SpentUSD < 0.09 || st.SpentUSD > 0.11 {
		t.Fatalf("spent = %v, want ~0.10 (the loop's own turn, not the session total)", st.SpentUSD)
	}
	if st.State != LoopRunning {
		t.Fatalf("state = %q, want still running: 0.10 is nowhere near the 1.00 budget", st.State)
	}
}

// The whole point of the promise: the model gets to end the loop early by
// finishing the job.
func TestLoopStopsWhenTheModelKeepsItsPromise(t *testing.T) {
	fastLoop(t)
	f := newFakeDriver()
	s := newSession("l4", "/tmp/p", "", f)
	defer s.Close()

	if err := s.StartLoop(LoopConfig{Prompt: "do it", Promise: "DONE", MaxIters: 50, MaxUSD: 100}); err != nil {
		t.Fatal(err)
	}
	waitPrompts(t, f, 1)
	says(f, "All the tests pass now.\n\n<promise>DONE</promise>")
	endTurn(f, 0.01)
	quiet()

	if got := len(f.sentPrompts()); got != 1 {
		t.Fatalf("ran %d iterations, want 1: the promise ends it", got)
	}
	if st := loopStatus(s); st.State != LoopDone {
		t.Fatalf("state = %q, want done", st.State)
	}
}

// Merely discussing the phrase is not a promise; only the tag ends the loop. A
// model that writes "I will output DONE when finished" must not stop the loop.
func TestLoopIgnoresThePhraseOutsideTheTag(t *testing.T) {
	fastLoop(t)
	f := newFakeDriver()
	s := newSession("l5", "/tmp/p", "", f)
	defer s.Close()

	if err := s.StartLoop(LoopConfig{Prompt: "do it", Promise: "DONE", MaxIters: 3, MaxUSD: 100}); err != nil {
		t.Fatal(err)
	}
	waitPrompts(t, f, 1)
	says(f, "I am not finished. I will say DONE when the tests pass.")
	endTurn(f, 0.01)
	waitPrompts(t, f, 2) // it kept going, which is the point

	if st := loopStatus(s); st.State != LoopRunning {
		t.Fatalf("state = %q, want still running", st.State)
	}
}

// The promise is matched leniently, because a miss keeps spending money while a
// false match merely stops early and says so.
func TestLoopPromiseMatchingIsLenient(t *testing.T) {
	for _, tc := range []struct{ said, promise string }{
		{"<promise>done</promise>", "DONE"},
		{"<PROMISE>Done</PROMISE>", "done"},
		{"<promise>  ALL   GREEN  </promise>", "all green"},
		{"text before <promise>DONE</promise> text after", "DONE"},
	} {
		if !saidPromise(tc.said, tc.promise) {
			t.Errorf("saidPromise(%q, %q) = false, want true", tc.said, tc.promise)
		}
	}
	for _, tc := range []struct{ said, promise string }{
		{"I will say DONE later", "DONE"},
		{"<promise>NOT YET</promise>", "DONE"},
		{"", "DONE"},
	} {
		if saidPromise(tc.said, tc.promise) {
			t.Errorf("saidPromise(%q, %q) = true, want false", tc.said, tc.promise)
		}
	}
}

// Hitting the usage wall must end the loop, not have it hammer the wall all night.
func TestLoopStopsWhenRateLimited(t *testing.T) {
	fastLoop(t)
	f := newFakeDriver()
	s := newSession("l6", "/tmp/p", "", f)
	defer s.Close()

	if err := s.StartLoop(LoopConfig{Prompt: "go", MaxIters: 50, MaxUSD: 100}); err != nil {
		t.Fatal(err)
	}
	waitPrompts(t, f, 1)
	f.events <- claude.Event{Kind: claude.EventRateLimit, Window: "five_hour", ResetsAt: 1, LimitStatus: "rejected"}
	endTurn(f, 0.01)
	quiet()

	if got := len(f.sentPrompts()); got != 1 {
		t.Fatalf("ran %d iterations, want 1: a spent usage window ends it", got)
	}
	if st := loopStatus(s); st.State != LoopStopped {
		t.Fatalf("state = %q, want stopped", st.State)
	}
}

// Stop has to mean stop. Without this the loop starts the next iteration a
// moment later and the button looks broken.
func TestInterruptEndsTheLoop(t *testing.T) {
	fastLoop(t)
	f := newFakeDriver()
	s := newSession("l7", "/tmp/p", "", f)
	defer s.Close()

	if err := s.StartLoop(LoopConfig{Prompt: "go", MaxIters: 50, MaxUSD: 100}); err != nil {
		t.Fatal(err)
	}
	waitPrompts(t, f, 1)
	if err := s.Interrupt(); err != nil {
		t.Fatal(err)
	}
	endTurn(f, 0.01) // the interrupted turn still reports a result
	quiet()

	if got := len(f.sentPrompts()); got != 1 {
		t.Fatalf("ran %d iterations after Stop, want 1", got)
	}
	if st := loopStatus(s); st.State != LoopStopped {
		t.Fatalf("state = %q, want stopped", st.State)
	}
}

// A turn that breaks ends the loop rather than retrying the same failure until
// the budget is gone.
func TestLoopStopsOnAFailedTurn(t *testing.T) {
	fastLoop(t)
	f := newFakeDriver()
	s := newSession("l8", "/tmp/p", "", f)
	defer s.Close()

	if err := s.StartLoop(LoopConfig{Prompt: "go", MaxIters: 50, MaxUSD: 100}); err != nil {
		t.Fatal(err)
	}
	waitPrompts(t, f, 1)
	f.events <- claude.Event{Kind: claude.EventResult, Raw: json.RawMessage(`{"subtype":"error","is_error":true,"total_cost_usd":0.01}`)}
	quiet()

	if got := len(f.sentPrompts()); got != 1 {
		t.Fatalf("ran %d iterations, want 1: a failed turn ends it", got)
	}
	if st := loopStatus(s); st.State != LoopFailed {
		t.Fatalf("state = %q, want failed", st.State)
	}
}

// Limits are clamped rather than rejected: whoever started this has walked away,
// and a silent tightening beats an error they never read.
func TestLoopLimitsAreClamped(t *testing.T) {
	fastLoop(t)
	f := newFakeDriver()
	s := newSession("l9", "/tmp/p", "", f)
	defer s.Close()

	// Zero means "unset", and an unset budget is the thing we refuse to have.
	if err := s.StartLoop(LoopConfig{Prompt: "go", MaxIters: 0, MaxUSD: 0}); err != nil {
		t.Fatal(err)
	}
	st := loopStatus(s)
	if st.MaxIters != loopDefaultIters || st.MaxUSD != loopDefaultUSD {
		t.Fatalf("defaults = %d/%v, want %d/%v", st.MaxIters, st.MaxUSD, loopDefaultIters, loopDefaultUSD)
	}
	s.StopLoop("test")

	// And nothing may exceed the hard ceilings, however it is asked.
	if err := s.StartLoop(LoopConfig{Prompt: "go", MaxIters: 1 << 20, MaxUSD: 1e6}); err != nil {
		t.Fatal(err)
	}
	st = loopStatus(s)
	if st.MaxIters != loopHardIters || st.MaxUSD != loopHardUSD {
		t.Fatalf("ceilings = %d/%v, want %d/%v", st.MaxIters, st.MaxUSD, loopHardIters, loopHardUSD)
	}
}

// An empty task is the one input worth refusing outright: there is nothing to
// repeat, so the loop would just burn the budget asking the model to guess.
func TestLoopNeedsATask(t *testing.T) {
	f := newFakeDriver()
	s := newSession("l10", "/tmp/p", "", f)
	defer s.Close()

	if err := s.StartLoop(LoopConfig{Prompt: "   "}); err == nil {
		t.Fatal("started a loop with no task")
	}
	if loopStatus(s) != nil {
		t.Fatal("a rejected loop must leave no trace")
	}
}

// A prompt someone actually typed outranks the loop, and the loop resumes after
// it rather than racing it into the CLI.
func TestTypedPromptOutranksTheLoop(t *testing.T) {
	fastLoop(t)
	f := newFakeDriver()
	s := newSession("l11", "/tmp/p", "", f)
	defer s.Close()

	if err := s.StartLoop(LoopConfig{Prompt: "loop task", MaxIters: 50, MaxUSD: 100}); err != nil {
		t.Fatal(err)
	}
	waitPrompts(t, f, 1)
	// Someone wakes up and types while iteration 1 is still running.
	if err := s.Prompt("what are you doing?", nil, nil); err != nil {
		t.Fatal(err)
	}
	endTurn(f, 0.01)

	got := waitPrompts(t, f, 2)
	if got[1] != "what are you doing?" {
		t.Fatalf("second prompt = %q, want the typed one to go first", got[1])
	}
	// And once that turn ends, the loop carries on.
	endTurn(f, 0.02)
	got = waitPrompts(t, f, 3)
	if got[2] == "what are you doing?" {
		t.Fatal("the loop did not resume after the typed prompt")
	}
}

// A loop runs unattended, so it must not sit at a permission prompt until
// morning: it borrows the autonomous mode the way a scheduled job does. A real
// end-to-end run proved this the hard way, stalling at iteration 1 on the first
// file write with the session parked in awaiting_permission.
func TestLoopTakesTheAutonomousModeAndGivesItBack(t *testing.T) {
	fastLoop(t)
	f := newFakeDriver()
	s := newSession("l12", "/tmp/p", "", f)
	defer s.Close()
	s.SetPermissionMode("auto")

	if err := s.StartLoop(LoopConfig{Prompt: "go", MaxIters: 1, MaxUSD: 100}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	got := s.mode
	s.mu.Unlock()
	if got != LoopPermissionMode {
		t.Fatalf("mode during the loop = %q, want %q", got, LoopPermissionMode)
	}

	waitPrompts(t, f, 1)
	endTurn(f, 0.01) // the single iteration ends, so the loop does too
	quiet()

	s.mu.Lock()
	got = s.mode
	s.mu.Unlock()
	if got != "auto" {
		t.Fatalf("mode after the loop = %q, want the session's own mode back (auto)", got)
	}
}

// A mode change has to reach attached clients, not just the CLI. The loop borrows
// acceptEdits on its own, and without this the composer went on claiming "Auto"
// while the session was actually accepting every edit.
func TestModeChangeReachesClients(t *testing.T) {
	fastLoop(t)
	f := newFakeDriver()
	s := newSession("l13", "/tmp/p", "", f)
	defer s.Close()
	s.SetPermissionMode("auto")

	_, _, sub := s.Attach(0)
	if err := s.StartLoop(LoopConfig{Prompt: "go", MaxIters: 1, MaxUSD: 100}); err != nil {
		t.Fatal(err)
	}
	waitPrompts(t, f, 1)
	endTurn(f, 0.01) // the only iteration ends, so the loop does, restoring the mode

	var modes []string
	deadline := time.After(2 * time.Second)
	for len(modes) < 2 {
		select {
		case ev := <-sub.Events():
			if ev.T == EvMode {
				modes = append(modes, ev.Mode)
			}
		case <-deadline:
			t.Fatalf("saw mode events %v, want the borrow and the hand-back", modes)
		}
	}
	if modes[0] != LoopPermissionMode || modes[1] != "auto" {
		t.Fatalf("mode events = %v, want [%s auto]", modes, LoopPermissionMode)
	}
}
