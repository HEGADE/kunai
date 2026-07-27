package session

import (
	"encoding/json"
	"testing"

	"github.com/hegade/kunai/internal/claude"
)

// fakeGuard denies whatever it is told to and reports a fixed standing mode.
type fakeGuard struct {
	deny string
	mode string
}

func (g fakeGuard) Check(tool string, _ json.RawMessage) string {
	if g.deny != "" && tool == g.deny {
		return "denied: " + tool + " is outside the boundary"
	}
	return ""
}
func (g fakeGuard) AutoMode() string { return g.mode }

// ask feeds a permission request as the driver would.
func ask(s *Session, tool string) {
	s.onPermission(&claude.PermissionAsk{
		RequestID: "req-" + tool,
		ToolName:  tool,
		ToolUseID: "tu-" + tool,
		Input:     json.RawMessage(`{"file_path":"/etc/passwd"}`),
	})
}

// The guard must answer a refused call itself, before anybody is asked. Showing
// it to the owner would be worse than useless: they see a tool name on a lock
// screen, and every ask they are shown is one they might approve by reflex.
func TestGuardDeniedCallNeverReachesAHuman(t *testing.T) {
	drv := newFakeDriver()
	s := newSession("s", t.TempDir(), "", drv)
	s.SetToolGuard(fakeGuard{deny: "Read"})

	// A guest's turn is what the guard exists for.
	s.mu.Lock()
	s.turnFrom = FromGuest
	s.mu.Unlock()

	ask(s, "Read")

	drv.mu.Lock()
	got, answered := drv.resolved["req-Read"]
	drv.mu.Unlock()
	if !answered {
		t.Fatal("the guard did not answer the call at all, so the turn would hang")
	}
	if got.Behavior != "deny" {
		t.Errorf("behavior = %q, want deny", got.Behavior)
	}
	if got.Message == "" {
		t.Error("the denial carried no reason, so the model cannot correct itself")
	}

	s.mu.Lock()
	pending, state := len(s.pending), s.state
	s.mu.Unlock()
	if pending != 0 {
		t.Error("a guard-denied call was recorded as pending, so the owner would be asked about it")
	}
	if state == StateAwaiting {
		t.Error("the session went to awaiting_permission for a call the guard already settled")
	}
}

// A call the guard cleared may proceed on its own when the share granted a
// standing yes, which is what lets a guest work while the owner is asleep.
func TestGuardClearedCallCanProceedOnAStandingYes(t *testing.T) {
	drv := newFakeDriver()
	s := newSession("s", t.TempDir(), "", drv)
	s.SetToolGuard(fakeGuard{deny: "Write", mode: "acceptEdits"})
	s.mu.Lock()
	s.turnFrom = FromGuest
	s.mu.Unlock()

	ask(s, "Read") // cleared by the guard

	drv.mu.Lock()
	got := drv.resolved["req-Read"]
	drv.mu.Unlock()
	if got.Behavior != "allow" {
		t.Fatalf("behavior = %q, want allow", got.Behavior)
	}
	// An allow that omits updatedInput makes the CLI run the tool with empty
	// input, which is a long-standing invariant of this codebase.
	if len(got.UpdatedInput) == 0 {
		t.Error("the allow dropped updatedInput, so the tool would run with nothing")
	}
}

// Without a standing yes, a cleared call still stops for the owner. The guard
// bounds what can be asked, it does not answer on the owner's behalf.
func TestGuardWithoutAStandingYesStillAsks(t *testing.T) {
	drv := newFakeDriver()
	s := newSession("s", t.TempDir(), "", drv)
	s.SetToolGuard(fakeGuard{deny: "Write"}) // no mode
	s.mu.Lock()
	s.turnFrom = FromGuest
	s.mu.Unlock()

	ask(s, "Read")

	drv.mu.Lock()
	_, answered := drv.resolved["req-Read"]
	drv.mu.Unlock()
	if answered {
		t.Fatal("a cleared call was auto-answered without the share asking for it")
	}
	s.mu.Lock()
	pending := len(s.pending)
	s.mu.Unlock()
	if pending != 1 {
		t.Errorf("the ask did not reach the owner: %d pending", pending)
	}
}

// The owner's own turns are never guarded. The guard bounds what somebody ELSE's
// prompt can reach; the owner has a shell on this machine by other means, and
// guarding them would break their session the moment they shared it.
func TestTheOwnersOwnTurnIsNotGuarded(t *testing.T) {
	drv := newFakeDriver()
	s := newSession("s", t.TempDir(), "", drv)
	s.SetToolGuard(fakeGuard{deny: "Read", mode: "acceptEdits"})
	// turnFrom is FromOwner by default.

	ask(s, "Read")

	drv.mu.Lock()
	_, answered := drv.resolved["req-Read"]
	drv.mu.Unlock()
	if answered {
		t.Fatal("the owner's own tool call was answered by the guest guard")
	}
	s.mu.Lock()
	pending := len(s.pending)
	s.mu.Unlock()
	if pending != 1 {
		t.Errorf("the owner's ask did not reach them: %d pending", pending)
	}
}

// An unshared session behaves exactly as it always did.
func TestNoGuardMeansNoChange(t *testing.T) {
	drv := newFakeDriver()
	s := newSession("s", t.TempDir(), "", drv)
	s.mu.Lock()
	s.turnFrom = FromGuest
	s.mu.Unlock()

	ask(s, "Read")

	drv.mu.Lock()
	_, answered := drv.resolved["req-Read"]
	drv.mu.Unlock()
	if answered {
		t.Fatal("a session with no guard answered a permission ask by itself")
	}
}

// A permission ask carries who caused it, so an owner approving from a lock
// screen can tell their own work from a visitor's.
func TestPermissionAsksSayWhoCausedThem(t *testing.T) {
	drv := newFakeDriver()
	s := newSession("s", t.TempDir(), "", drv)
	s.mu.Lock()
	s.turnFrom = FromGuest
	s.mu.Unlock()

	ask(s, "Read")

	s.mu.Lock()
	ev := s.pending["req-Read"]
	s.mu.Unlock()
	if ev.From != string(FromGuest) {
		t.Errorf("the ask says From=%q, want %q: an owner cannot tell whose request this is",
			ev.From, FromGuest)
	}
}
