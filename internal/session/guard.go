package session

import (
	"encoding/json"

	"github.com/hegade/kunai/internal/claude"
)

// The tool-call guard: the second half of confining a shared session, and the
// only half that can see what the agent is actually about to do.
//
// The first half is spawn-time. A shared session runs with --disallowedTools, so
// Bash and Task are not in the model's toolset at all. That is what makes this
// half possible: the tools that remain name the files they touch as arguments,
// and arguments can be read. A shell command cannot, which is why it is removed
// rather than inspected.
//
// This runs before the ask reaches pending or any client, so a call outside the
// boundary dies without anybody being asked about it, and the model is told why
// so it corrects itself rather than retrying.

// ToolGuard decides whether a tool call may proceed.
//
// Returning a non-empty reason denies the call, and the reason is sent to the
// model as the denial message. Returning ok=false with an empty reason means the
// guard has nothing to say and the ordinary permission flow applies.
type ToolGuard interface {
	// Check is given the tool and its arguments and reports why it may not run.
	// An empty string allows it.
	Check(toolName string, input json.RawMessage) string
	// AutoMode is the permission mode to apply to calls the guard cleared, so a
	// guest working while the owner is asleep is not stopped by every write.
	// Empty means the call goes through the normal permission flow.
	AutoMode() string
}

// SetToolGuard installs the guard for a shared session, or clears it with nil.
func (s *Session) SetToolGuard(g ToolGuard) {
	s.mu.Lock()
	s.guard = g
	s.mu.Unlock()
}

// Guarded reports whether this session is confined by a guard, which in practice
// means it is shared.
//
// Exported for the one caller that has to know BEFORE acting rather than after:
// entering Yolo mode replaces the process, and the new Session has no guard by
// construction, so a check that ran afterwards would always pass and the refusal
// would be exactly as good as not having one.
func (s *Session) Guarded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.guard != nil
}

// answerUnattended decides an ask for a session nobody is going to answer for.
//
// unattended is false for an ordinary session, and then the ask takes its normal
// route to a person. Otherwise the answer is here and immediate: a tool on the
// list runs, anything else is refused with a sentence the model can act on, and
// in neither case does the session stop.
//
// The refusal names the tool rather than apologising, because the model reads it
// and picks another way: a review told "Monitor is not available in a review" got
// its answer from Read and Grep, which is what it should have done first.
func (s *Session) answerUnattended(ask *claude.PermissionAsk) (allow bool, reason string, unattended bool) {
	s.mu.Lock()
	list := s.unattended
	s.mu.Unlock()
	if len(list) == 0 {
		return false, "", false
	}
	for _, t := range list {
		if t == ask.ToolName {
			return true, "", true
		}
	}
	return false, ask.ToolName + " is not available here: this session reads, and nobody is attached to approve anything else. Use the tools you have.", true
}

// guardVerdict is what the guard decided about an incoming ask.
type guardVerdict struct {
	denied  bool
	reason  string
	autoYes bool
}

// judge applies the guard to an ask. Called on the permission path with the lock
// NOT held, because the guard resolves symlinks and touches the filesystem.
func (s *Session) judge(ask *claude.PermissionAsk) guardVerdict {
	s.mu.Lock()
	g, from := s.guard, s.turnFrom
	s.mu.Unlock()
	// No guard, or the owner's own turn: nothing to confine. The guard exists to
	// bound what somebody else's prompt can reach, and the owner already has a
	// shell on this machine by other means.
	if g == nil || from == FromOwner {
		return guardVerdict{}
	}
	if reason := g.Check(ask.ToolName, ask.Input); reason != "" {
		return guardVerdict{denied: true, reason: reason}
	}
	return guardVerdict{autoYes: g.AutoMode() != ""}
}
