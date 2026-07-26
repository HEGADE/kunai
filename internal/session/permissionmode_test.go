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
	for _, bad := range []string{"", "AcceptEdits", "accept_edits", "yolo", "bypassPermissions"} {
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
