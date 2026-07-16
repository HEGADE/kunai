package server

import "os/exec"

// execRun runs a command and waits for it. It is a package var, not a direct
// exec call, so tests of the privileged paths (poweroff, pmset lid-hold) can
// substitute a recorder and assert exactly what WOULD run without running it.
// Every privileged Phase 2 action goes through here for that reason.
var execRun = func(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}
