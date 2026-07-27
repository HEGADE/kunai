package server

// The guard a shared session runs under: what a guest's prompt is allowed to
// reach on this machine.

import (
	"encoding/json"
	"strings"

	"github.com/hegade/kunai/internal/pathguard"
	"github.com/hegade/kunai/internal/share"
)

// shareGuard confines a guest-prompted tool call to the folders the share froze
// when it was created.
//
// It is deliberately a small, dumb object: a list of roots and a mode. It holds
// no reference to the store, so it cannot be widened later by anything that
// happens to the share. Ending or changing the share replaces the guard.
type shareGuard struct {
	roots []string
	mode  string
}

func newShareGuard(sh *share.Share) *shareGuard {
	return &shareGuard{roots: append([]string(nil), sh.Roots...), mode: sh.Mode}
}

// AutoMode is the standing permission for calls this guard cleared.
func (g *shareGuard) AutoMode() string {
	if g.mode == "acceptEdits" || g.mode == "auto" {
		return g.mode
	}
	return "" // anything else means "still ask the owner"
}

// Check reports why a tool call may not run, or "" to allow it.
//
// The default is refusal. A tool this package does not recognise is refused
// rather than allowed, because "we found no paths in it" and "it has no paths"
// are indistinguishable from here, and only one of them is safe. That matters
// most for a tool added to the CLI after this was written: it arrives denied,
// and somebody has to look at it before a guest can use it.
func (g *shareGuard) Check(toolName string, input json.RawMessage) string {
	if len(g.roots) == 0 {
		return "This session is shared and has no folder set, so it cannot run tools."
	}
	if !pathguard.GuardedTools[toolName] {
		return "The " + toolName + " tool is not available while this session is shared."
	}
	for _, p := range pathguard.ToolPaths(input) {
		if !pathguard.Inside(g.roots, p) {
			// Worded for the model, which reads this and tries something else. It
			// says what the boundary is so the next attempt can be inside it,
			// without naming paths the guest has no business learning.
			return "Denied: " + p + " is outside the folder this shared session may touch. " +
				"Work only inside " + strings.Join(g.roots, ", ") + "."
		}
	}
	return ""
}
