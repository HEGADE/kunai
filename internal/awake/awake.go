// Package awake prevents the host from idle-sleeping while enabled, so a locked
// or idle machine keeps its claude sessions alive and reachable over Tailscale.
//
// The hold is an in-process power assertion: it exists only while enabled and is
// released the moment it is turned off OR the process exits. Nothing global or
// sticky is written to the OS, so a crash can never leave a machine stuck awake.
// This deliberately does NOT keep a laptop awake with the lid closed (macOS
// force-sleeps on lid close and only root can override that).
package awake

// Keeper prevents idle system sleep while enabled. Set(true) acquires the hold,
// Set(false) or process exit releases it. Set is idempotent and safe to call
// from multiple goroutines.
type Keeper interface {
	Set(on bool) error
	Enabled() bool
	// Supported reports whether this platform/host can hold the assertion; the
	// client hides the toggle when false.
	Supported() bool
}
