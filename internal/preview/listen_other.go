//go:build !linux

package preview

// No /proc, so no native reader: macOS keeps the lsof path. Returning ok=false
// (rather than an empty list) is what tells the caller to fall back, since "I
// cannot look" and "nothing is listening" must never be the same answer.
func Listeners() ([]Server, bool) { return nil, false }
