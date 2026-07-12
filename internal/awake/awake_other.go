//go:build !darwin && !windows && !linux

package awake

// New is a no-op on platforms without a supported keep-awake mechanism.
func New() Keeper { return noopKeeper{} }

type noopKeeper struct{}

func (noopKeeper) Set(bool) error  { return nil }
func (noopKeeper) Enabled() bool   { return false }
func (noopKeeper) Supported() bool { return false }
