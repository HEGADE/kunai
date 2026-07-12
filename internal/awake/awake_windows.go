package awake

import (
	"runtime"
	"sync"
	"syscall"
	"time"
)

// Windows keeps the system awake with SetThreadExecutionState. The flag is
// thread-scoped, and Go migrates goroutines across OS threads, so a dedicated
// goroutine pins its thread (runtime.LockOSThread) for the lifetime of the hold
// and re-asserts periodically as belt-and-suspenders. Clearing must happen on
// that same pinned thread, so it is done there on stop.
const (
	esContinuous     = 0x80000000
	esSystemRequired = 0x00000001
	reassertEvery    = 30 * time.Second
)

type winKeeper struct {
	mu   sync.Mutex
	on   bool
	stop chan struct{}
}

func New() Keeper { return &winKeeper{} }

func (k *winKeeper) Supported() bool { return true }

func (k *winKeeper) Enabled() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.on
}

func (k *winKeeper) Set(on bool) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if on == k.on {
		return nil
	}
	if on {
		k.stop = make(chan struct{})
		go hold(k.stop)
	} else {
		close(k.stop)
		k.stop = nil
	}
	k.on = on
	return nil
}

func hold(stop chan struct{}) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("SetThreadExecutionState")
	set := func(flags uintptr) { _, _, _ = proc.Call(flags) }

	set(esContinuous | esSystemRequired)
	t := time.NewTicker(reassertEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			set(esContinuous) // clear, on the same pinned thread
			return
		case <-t.C:
			set(esContinuous | esSystemRequired)
		}
	}
}
