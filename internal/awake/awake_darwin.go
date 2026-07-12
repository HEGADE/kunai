package awake

import (
	"os"
	"os/exec"
	"strconv"
)

// New holds a `caffeinate -i -w <pid>` child while enabled. -i asserts
// PreventUserIdleSystemSleep (blocks idle system sleep on AC and battery; lid
// close is intentionally left to sleep, which needs root to prevent). -w makes
// caffeinate exit when kunai exits, so the hold is released even if kunai is
// killed and never runs its own cleanup.
func New() Keeper {
	_, err := exec.LookPath("caffeinate")
	pid := strconv.Itoa(os.Getpid())
	return &procKeeper{
		supported: err == nil,
		newCmd:    func() *exec.Cmd { return exec.Command("caffeinate", "-i", "-w", pid) },
	}
}
