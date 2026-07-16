//go:build darwin

package server

import (
	"os"
	"sync"
)

// On macOS the only way to keep a closed lid awake is `pmset -a disablesleep 1`,
// which is root-only and STICKY: it is global system state that outlives the
// process. That is the whole risk the awake package refused. The mitigations:
//
//   - it goes through sudo, which the installer grants NOPASSWD for this exact
//     command, so it works only on a machine deliberately set up for it;
//   - newLidKeeper clears the setting at construction (boot-time unstick), so a
//     crash that left disablesleep on is undone the next time kunai starts;
//   - the server also clears it on graceful shutdown.
//
// Absolute paths for launchd's minimal PATH, matching stats_darwin.go.
type lidPmset struct {
	mu        sync.Mutex
	on        bool
	supported bool
}

func newLidKeeper() lidKeeper {
	_, err := os.Stat("/usr/bin/pmset")
	k := &lidPmset{supported: err == nil}
	if k.supported {
		// Unstick: undo any disablesleep a previous run may have left on. Ignoring
		// the error is deliberate; if we lack the privilege there is nothing we
		// could have set either, so there is nothing to clear.
		_ = execRun("/usr/bin/sudo", "-n", "/usr/bin/pmset", "-a", "disablesleep", "0")
	}
	return k
}

func (k *lidPmset) Supported() bool { return k.supported }

func (k *lidPmset) Enabled() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.on
}

func (k *lidPmset) Set(on bool) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.supported || on == k.on {
		return nil
	}
	v := "0"
	if on {
		v = "1"
	}
	if err := execRun("/usr/bin/sudo", "-n", "/usr/bin/pmset", "-a", "disablesleep", v); err != nil {
		return err
	}
	k.on = on
	return nil
}
