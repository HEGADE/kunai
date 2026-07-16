//go:build linux

package server

// hostPowerOff powers the machine off. As a systemd --user service kunai cannot
// do this on its own: logind gates org.freedesktop.login1.power-off behind
// polkit, which denies a daemon with no seat. The install script drops a polkit
// rule granting this one action to the service user, so this succeeds only on a
// machine the owner deliberately set up for it; elsewhere it returns the polkit
// error and the soft trip (already done) remains the whole of the protection.
func hostPowerOff() error {
	return execRun("systemctl", "poweroff")
}
