//go:build darwin || linux

package awake

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// Exercise the process-holding keeper with a harmless `sleep` child instead of
// the real caffeinate/systemd-inhibit, so the test has no system side effects.
func TestProcKeeperLifecycle(t *testing.T) {
	k := &procKeeper{supported: true, newCmd: func() *exec.Cmd { return exec.Command("sleep", "3600") }}

	if k.Enabled() {
		t.Fatal("should start disabled")
	}
	if err := k.Set(true); err != nil {
		t.Fatalf("Set(true): %v", err)
	}
	if !k.Enabled() {
		t.Fatal("should be enabled after Set(true)")
	}
	pid := k.cmd.Process.Pid

	// Idempotent: a second Set(true) must not spawn a second child.
	if err := k.Set(true); err != nil {
		t.Fatalf("Set(true) again: %v", err)
	}
	if k.cmd.Process.Pid != pid {
		t.Fatal("double Set(true) restarted the child")
	}

	if err := k.Set(false); err != nil {
		t.Fatalf("Set(false): %v", err)
	}
	if k.Enabled() {
		t.Fatal("should be disabled after Set(false)")
	}
	// The child should be reaped shortly.
	if !gone(pid, time.Second) {
		t.Fatal("child process still alive after release")
	}
	// Idempotent off.
	if err := k.Set(false); err != nil {
		t.Fatalf("Set(false) again: %v", err)
	}
}

// A no-op keeper (supported == false) must never spawn anything.
func TestProcKeeperUnsupported(t *testing.T) {
	k := &procKeeper{supported: false, newCmd: func() *exec.Cmd { return exec.Command("sleep", "3600") }}
	if err := k.Set(true); err != nil {
		t.Fatalf("Set(true): %v", err)
	}
	if k.Enabled() {
		t.Fatal("unsupported keeper must stay disabled")
	}
}

func gone(pid int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}
