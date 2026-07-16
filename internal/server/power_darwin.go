//go:build darwin

package server

// hostPowerOff powers the machine off. A user LaunchAgent has no elevated rights,
// so shutdown needs sudo; the install script adds a sudoers NOPASSWD entry for
// exactly this command. Absolute paths because launchd's minimal PATH lacks the
// system dirs (the same reason stats_darwin.go uses /usr/sbin paths).
func hostPowerOff() error {
	return execRun("/usr/bin/sudo", "-n", "/sbin/shutdown", "-h", "now")
}
