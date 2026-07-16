//go:build linux

package server

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// On Linux the lid hold is a systemd-inhibit block lock on handle-lid-switch (plus
// sleep and idle for good measure). Unlike the awake package's sleep-only lock,
// this one vetoes the lid-close action itself, and unlike a sleep lock it is
// PRIVILEGED: logind gates a block inhibitor on handle-lid-switch behind the
// polkit action org.freedesktop.login1.inhibit-handle-lid-switch, which an
// ordinary user is denied (verified: "Failed to inhibit: Access denied"). The
// installer grants that action; without the grant Set reports the failure rather
// than pretending to hold, because a lid hold you believe is up while the machine
// is free to sleep is worse than one that plainly refused.
//
// The lock is held by a child process that dies with kunai (Pdeathsig), so
// nothing sticky survives a crash: the machine goes back to its normal lid
// behavior.
type lidProc struct {
	mu        sync.Mutex
	cmd       *exec.Cmd
	supported bool
}

func newLidKeeper() lidKeeper {
	_, err := exec.LookPath("systemd-inhibit")
	return &lidProc{supported: err == nil}
}

func (k *lidProc) Supported() bool { return k.supported }

func (k *lidProc) Enabled() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.cmd != nil
}

func (k *lidProc) Set(on bool) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.supported || on == (k.cmd != nil) {
		return nil
	}
	if !on {
		cmd := k.cmd
		k.cmd = nil
		_ = cmd.Process.Kill() // the Wait goroutine started at acquire reaps it
		return nil
	}
	cmd := exec.Command("systemd-inhibit",
		"--what=handle-lid-switch:sleep:idle", "--why=kunai lid-closed work", "--mode=block",
		"sleep", "infinity")
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	// The block inhibitor is privileged, and systemd-inhibit exits at once when
	// denied. Watch for that: if the child is gone within the grace window the hold
	// never took, so surface the reason instead of recording a phantom hold.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("lid hold refused (grant the install-time privilege): %s", msg)
		}
		return fmt.Errorf("lid hold refused (grant the install-time privilege): %v", err)
	case <-time.After(400 * time.Millisecond):
		k.cmd = cmd // still running: the hold took
		return nil
	}
}
