package server

// Which public ports this machine funnelled itself.
//
// Recorded rather than inferred, and the difference is somebody else's tailnet.
// The unattended repoint (reopenPublicPortIfStale) decides what to adopt from
// staleLoopback, which is true of ANY funnel pointing at a loopback port nothing
// is listening on -- and an owner's own funnel to their own app looks exactly
// like that for as long as the app is stopped, restarting or being rebuilt. So a
// mapping the owner made for something else could be silently rewritten to the
// share gate, and would not come back when their service did.
//
// While the repoint only ever happened because a person clicked "make public"
// and could see the port list, that inference was a reasonable guess in front of
// somebody who could correct it. Making it automatic is what turns it into a
// change to a machine's public surface with nobody watching, so the guess is
// replaced by a fact: kunai writes down the ports it funnelled, and touches
// nothing else.
//
// One file beside the gate's port file, same atomic-write idiom. Losing it is
// safe in the direction that matters: an unrecorded port is simply left alone,
// so the worst case is a link that has to be made public by hand again.

import (
	"encoding/json"
	"os"
	"sort"
	"sync"
)

type funnelOurs struct {
	mu    sync.Mutex
	path  string
	ports map[int]bool
}

func newFunnelOurs(path string) *funnelOurs {
	f := &funnelOurs{path: path, ports: map[int]bool{}}
	if b, err := os.ReadFile(path); err == nil {
		var list []int
		if json.Unmarshal(b, &list) == nil {
			for _, p := range list {
				f.ports[p] = true
			}
		}
	}
	return f
}

// add records a port this machine funnelled at the share gate.
func (f *funnelOurs) add(port int) {
	if f == nil || port == 0 {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ports[port] {
		return
	}
	f.ports[port] = true
	f.saveLocked()
}

// drop forgets a port once it is closed again.
func (f *funnelOurs) drop(port int) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.ports[port] {
		return
	}
	delete(f.ports, port)
	f.saveLocked()
}

// has reports whether this machine is the one that funnelled a port.
func (f *funnelOurs) has(port int) bool {
	if f == nil {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ports[port]
}

func (f *funnelOurs) saveLocked() {
	if f.path == "" {
		return
	}
	list := make([]int, 0, len(f.ports))
	for p := range f.ports {
		list = append(list, p)
	}
	sort.Ints(list)
	b, err := json.Marshal(list)
	if err != nil {
		return
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, f.path)
}
