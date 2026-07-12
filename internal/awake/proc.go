//go:build darwin || linux

package awake

import (
	"os/exec"
	"sync"
)

// procKeeper holds a power assertion by keeping a child process alive (macOS
// `caffeinate`, Linux `systemd-inhibit`). The child inherits the process group
// and is killed on release; if kunai dies the child dies with it, so the hold
// self-releases. newCmd builds a fresh command each acquire.
type procKeeper struct {
	mu        sync.Mutex
	cmd       *exec.Cmd
	supported bool
	newCmd    func() *exec.Cmd
}

func (k *procKeeper) Supported() bool { return k.supported }

func (k *procKeeper) Enabled() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.cmd != nil
}

func (k *procKeeper) Set(on bool) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.supported || on == (k.cmd != nil) {
		return nil // unsupported, or already in the requested state
	}
	if !on {
		cmd := k.cmd
		k.cmd = nil
		_ = cmd.Process.Kill()
		go cmd.Wait() // reap so it doesn't linger as a zombie
		return nil
	}
	cmd := k.newCmd()
	if err := cmd.Start(); err != nil {
		return err
	}
	k.cmd = cmd
	return nil
}
