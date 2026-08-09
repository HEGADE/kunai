package session

import "testing"

// A mode off the wire decides how a session is spawned, so the check has to
// separate three cases that look alike from the caller's side: a real mode, no
// mode at all, and a mode the CLI would reject. Only the last is a mistake, and
// conflating it with the second is what would let a typo spawn a session in a
// mode nobody chose.
func TestValidPermissionMode(t *testing.T) {
	for _, mode := range PermissionModes {
		if got := ValidPermissionMode(mode); got != mode {
			t.Errorf("ValidPermissionMode(%q) = %q, want it kept", mode, got)
		}
	}

	// Not choosing is not choosing wrongly: both give "", and the caller reads
	// that as "keep the default", which is right for either.
	for _, bad := range []string{"", "AcceptEdits", "accept_edits", "yolo", "bypass"} {
		if got := ValidPermissionMode(bad); got != "" {
			t.Errorf("ValidPermissionMode(%q) = %q, want \"\"", bad, got)
		}
	}

	// The defaults have to be modes the CLI actually takes, or every session
	// spawns with a flag it rejects.
	if ValidPermissionMode(DefaultPermissionMode) == "" {
		t.Errorf("DefaultPermissionMode %q is not in PermissionModes", DefaultPermissionMode)
	}
	if ValidPermissionMode(ProviderPermissionMode) == "" {
		t.Errorf("ProviderPermissionMode %q is not in PermissionModes", ProviderPermissionMode)
	}
	// A loop borrows a mode and hands it back, so its mode must be real too.
	if ValidPermissionMode(LoopPermissionMode) == "" {
		t.Errorf("LoopPermissionMode %q is not in PermissionModes", LoopPermissionMode)
	}
}

// The one mode a guest's work may never run under, whatever route it arrives by.
//
// This is not about trusting the guest less than the owner. The share guard is
// implemented AS a can_use_tool handler, and bypass stops the CLI sending
// can_use_tool at all, so a shared session in this mode has a folder boundary
// that is never consulted -- the guard object is still attached, the tier is
// still recorded, and none of it runs. Pinned because the failure is silent
// from every side: nothing errors, nothing logs, and the share looks correct.
func TestGuestModeRefusesBypass(t *testing.T) {
	if got := ValidGuestMode(BypassPermissionMode); got != "" {
		t.Errorf("ValidGuestMode(%q) = %q, want \"\"", BypassPermissionMode, got)
	}
	// Everything else a share may legitimately ask for still passes, or the guard
	// would be closed by making the feature useless rather than by being right.
	for _, ok := range []string{"default", "auto", "acceptEdits", "plan"} {
		if got := ValidGuestMode(ok); got != ok {
			t.Errorf("ValidGuestMode(%q) = %q, want it kept", ok, got)
		}
	}
	// And the ordinary validator must still accept it, or the owner cannot use it
	// on their own session either.
	if ValidPermissionMode(BypassPermissionMode) != BypassPermissionMode {
		t.Errorf("ValidPermissionMode(%q) rejected it; the owner's own mode must work", BypassPermissionMode)
	}
}

// A loop makes a session more autonomous, never less.
//
// The borrow exists so an unattended run does not stall on the first file write.
// Applied blindly to a session already running without prompts it would do the
// opposite: acceptEdits still stops for a risky Bash call, so the loop would
// start hanging overnight on exactly the thing the owner had arranged not to be
// asked about.
func TestLoopKeepsBypassAndBorrowsOtherwise(t *testing.T) {
	if got := loopModeFor(BypassPermissionMode); got != BypassPermissionMode {
		t.Errorf("loopModeFor(bypass) = %q, want the session to keep bypass", got)
	}
	for _, m := range []string{"default", "auto", "acceptEdits", "plan", ""} {
		if got := loopModeFor(m); got != LoopPermissionMode {
			t.Errorf("loopModeFor(%q) = %q, want %q", m, got, LoopPermissionMode)
		}
	}
}
