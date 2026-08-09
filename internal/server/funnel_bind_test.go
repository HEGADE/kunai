package server

import (
	"errors"
	"strings"
	"testing"
)

// A bind failure on kunai's own port has to say who took it.
//
// The state this recovers from is one kunai cannot get out of on its own: a
// Funnel mapping holds the port, kunai cannot bind, launchd restarts it, and it
// is never up long enough to clear the mapping. Seen on a real Mac, restarting
// every ten seconds against 8443 funnelled to a gate port from days earlier. The
// bind error alone is true and useless; the fix is one command nobody derives
// from "address already in use".
func TestBindConflictNamesTheFunnelHoldingThePort(t *testing.T) {
	s := &Server{cfg: Config{Addr: "100.89.93.86:8443"}}
	s.funnelStatusFn = func(int) funnelState {
		return funnelState{Available: true, InUse: map[int]string{8443: "http://127.0.0.1:59100"}}
	}

	why := s.diagnoseBindConflict(errors.New("listen tcp 100.89.93.86:8443: bind: address already in use"))
	if !strings.Contains(why, "127.0.0.1:59100") {
		t.Errorf("diagnosis does not name what holds the port: %q", why)
	}
	if !strings.Contains(why, "tailscale funnel --https=8443 off") {
		t.Errorf("diagnosis does not give the command that fixes it: %q", why)
	}

	// A port held by something that is not a Funnel mapping gets no invented
	// explanation: the bind error is genuinely all that is known.
	s.funnelStatusFn = func(int) funnelState { return funnelState{Available: true, InUse: map[int]string{}} }
	if why := s.diagnoseBindConflict(errors.New("listen tcp 100.89.93.86:8443: bind: address already in use")); why != "" {
		t.Errorf("invented an explanation for an unrelated conflict: %q", why)
	}

	// And any other failure is passed through untouched.
	if why := s.diagnoseBindConflict(errors.New("permission denied")); why != "" {
		t.Errorf("explained a failure that is not a port conflict: %q", why)
	}
}
