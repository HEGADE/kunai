//go:build !linux && !darwin

package server

import "errors"

// errNoReflink means this platform has no clone call wired up, so the caller
// falls back to a byte copy: correct, just not free.
var errNoReflink = errors.New("reflink unsupported on this platform")

func cloneFile(src, dst string) error { return errNoReflink }
