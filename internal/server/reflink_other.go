//go:build !linux

package server

import (
	"errors"
	"os"
)

// errNoReflink means this platform has no clone call wired up, so the caller
// falls back to a byte copy. macOS/APFS does support cloning (clonefile(2)) and
// could be added here; until then it takes the copy path, which is correct, just
// not free.
var errNoReflink = errors.New("reflink unsupported on this platform")

func reflinkFile(src, dst *os.File) error { return errNoReflink }
