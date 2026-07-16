//go:build !linux && !darwin

package server

// Off Linux and macOS the lid hold is unsupported; the client hides the toggle.
type lidNoop struct{}

func newLidKeeper() lidKeeper     { return lidNoop{} }
func (lidNoop) Set(on bool) error { return nil }
func (lidNoop) Enabled() bool     { return false }
func (lidNoop) Supported() bool   { return false }
