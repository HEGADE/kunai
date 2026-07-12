package awake

import (
	"os/exec"
	"syscall"
)

// New holds a `systemd-inhibit ... sleep infinity` child while enabled, blocking
// system sleep for as long as it runs. Pdeathsig=SIGKILL makes the child die
// with kunai, so the hold is released even on an uncaught kill. Best-effort:
// unsupported (a no-op the client hides) when systemd-inhibit is not on PATH,
// which is fine for the headless servers that never sleep anyway.
func New() Keeper {
	_, err := exec.LookPath("systemd-inhibit")
	return &procKeeper{
		supported: err == nil,
		newCmd: func() *exec.Cmd {
			cmd := exec.Command("systemd-inhibit",
				"--what=sleep", "--why=kunai keep-awake", "--mode=block",
				"sleep", "infinity")
			cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
			return cmd
		},
	}
}
