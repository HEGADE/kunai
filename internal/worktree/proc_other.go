//go:build !unix

package worktree

import "os/exec"

// isolate is a no-op where process groups are not available; exec's own
// cancellation kills the direct child, and WaitDelay bounds the wait. A setup
// command's grandchildren may survive a timeout on such a platform, which is
// worth knowing but not worth emulating job objects for: kunai ships on Linux
// and macOS.
func isolate(cmd *exec.Cmd) {}
