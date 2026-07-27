package server

import "os/exec"

// execRun runs a command and waits for it. It is a package var, not a direct
// exec call, so tests of the privileged paths (poweroff, pmset lid-hold) can
// substitute a recorder and assert exactly what WOULD run without running it.
// Every privileged Phase 2 action goes through here for that reason.
var execRun = func(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

// execOut is execRun for commands whose OUTPUT is the point rather than their
// exit code: reading `tailscale serve status --json`, and reporting what
// tailscale said when turning Funnel on fails. Same reason for being a var, and
// the funnel tests substitute it to assert the exact argv without touching the
// machine's network configuration.
//
// Combined output, because tailscale explains a refusal on stderr and that
// explanation is the most useful thing to show the person who asked.
var execOut = func(name string, args ...string) (string, error) {
	b, err := exec.Command(name, args...).CombinedOutput()
	return string(b), err
}
