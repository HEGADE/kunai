package server

// The guard a shared session runs under: what a guest's prompt is allowed to
// reach on this machine.

import (
	"encoding/json"

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
//
// Only acceptEdits means "yes without asking". "auto" used to be accepted here
// too, which quietly made the two identical: everywhere else in kunai auto means
// the session still stops for anything risky, so an owner choosing it on the
// share dialog was picking the cautious-sounding option and getting the
// permissive one. Auto now falls through to the ordinary permission flow, which
// is what it means everywhere else.
func (g *shareGuard) AutoMode() string {
	if g.mode == "acceptEdits" {
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
	if !pathguard.Guarded(toolName) {
		return "The " + toolName + " tool is not available while this session is shared."
	}
	paths, ok := pathguard.ToolPaths(toolName, input)
	if !ok {
		// "We found no paths" and "there are no paths" are different answers, and
		// only one of them is safe. A tool that always names a file, called in a
		// shape this build does not recognise, is refused rather than waved through
		// on the strength of having nothing to check. That is what makes a renamed
		// CLI field a broken tool call instead of a silent hole in the boundary.
		return "Denied: this " + toolName + " call is in a form a shared session cannot check. " +
			"Name the file directly."
	}
	for _, p := range paths {
		if !pathguard.Inside(g.roots, p) {
			// Worded for the model, which reads this and tries something else.
			//
			// It deliberately does NOT list the roots. An earlier version did, two
			// lines under a comment promising it would not: the model reads the
			// denial and repeats it, so every refusal handed the guest the owner's
			// absolute directory layout. The model already knows where it is
			// working, so naming the boundary adds nothing it needs.
			return "Denied: " + p + " is outside the folders this shared session may touch. " +
				"Work only inside the session's own project folders."
		}
	}
	return ""
}
