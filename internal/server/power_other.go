//go:build !linux && !darwin

package server

import "errors"

// hostPowerOff is unsupported off Linux and macOS.
func hostPowerOff() error {
	return errors.New("poweroff is not supported on this platform")
}
