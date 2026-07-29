package session

import "testing"

// TurnEndedAt is what lets a client mark a session Done-and-unread ("the agent
// finished while you were away"), so it must be stamped by a turn that actually
// ran and by nothing else.
func TestTurnEndStampsMetaForARealTurnOnly(t *testing.T) {
	f := newFakeDriver()
	s := newSession("done", "/tmp/p", "", f)
	defer s.Close()

	if s.Meta().TurnEndedAt != 0 {
		t.Fatal("a fresh session claims a finished turn")
	}

	if err := s.Prompt("go", nil, nil); err != nil {
		t.Fatal(err)
	}
	waitPrompts(t, f, 1)
	endTurn(f, 0.01)
	quiet()

	if s.Meta().TurnEndedAt == 0 {
		t.Fatal("a completed turn did not stamp TurnEndedAt")
	}
}

// A prompt the CLI refused never started a turn, so it must not read as one that
// finished: abandonTurn zeroes the clock before setting idle precisely so the
// stamp in setState cannot fire.
func TestARefusedPromptDoesNotClaimAFinishedTurn(t *testing.T) {
	f := newFakeDriver()
	s := newSession("refused", "/tmp/p", "", f)

	f.Close() // the process is gone; the prompt below is refused
	quiet()
	if err := s.Prompt("are you there", nil, nil); err == nil {
		t.Fatal("a prompt to a closed driver reported success")
	}
	quiet()

	if got := s.Meta().TurnEndedAt; got != 0 {
		t.Fatalf("TurnEndedAt = %d for a turn that never left the machine", got)
	}
}
