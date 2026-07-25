//go:build unix

package worktree

import (
	"os/exec"
	"syscall"
)

// isolate makes the setup command killable as a whole.
//
// This is not a detail. `sh -c "npm ci"` forks rather than execs (measured: the
// installer's PPID is the shell, so the shell is still there), which means
// killing the process exec.CommandContext knows about leaves the actual work
// running. Two things then go wrong: the install keeps consuming the machine
// after kunai gave up on it, and cmd.Wait blocks until it finishes anyway,
// because the orphan still holds the write end of the output pipe. A timeout
// that waits for the thing it timed out on is not a timeout.
//
// So the command gets its own process group, and cancelling kills the group.
func isolate(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative pid means the process group. SIGKILL rather than SIGTERM: this
		// only runs once the deadline has already passed, and a package manager
		// that ignores SIGTERM would hold the timeout open.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
