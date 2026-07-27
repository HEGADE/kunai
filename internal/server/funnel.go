package server

// Turning the share gate into something the outside world can reach.
//
// Tailscale Funnel is the only option that keeps the project's promise. Its
// ingress servers route by SNI and forward the stream without decrypting it; TLS
// terminates on this machine, using this machine's own ts.net certificate. So
// Tailscale sees connection metadata and never content. That is a real widening
// of the trust model and the UI says so in a line, but it is not a relay in the
// sense kunai rejects, and nothing else gives a browser with no Tailscale on it a
// working HTTPS URL.
//
// Every command goes through the injectable execRun that guardian.go established,
// so a test asserts the exact argv instead of reshaping the machine's network.

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// funnelPorts are the only public ports Tailscale Funnel will serve on. Not a
// choice kunai makes; the list is fixed upstream, and a machine can easily have
// some of them already spoken for by an unrelated `tailscale serve`.
var funnelPorts = []int{443, 8443, 10000}

// funnelState is what the UI needs to decide what to offer.
type funnelState struct {
	// Available reports whether the tailscale CLI could be asked at all.
	Available bool `json:"available"`
	// Port is the public port currently forwarding to the share gate, 0 if none.
	Port int `json:"port,omitempty"`
	// Free lists the funnel ports not already serving something else.
	Free []int `json:"free"`
	// InUse maps a taken funnel port to what it currently points at, so the UI can
	// say "10000 is already serving 127.0.0.1:8080" instead of silently refusing.
	InUse map[int]string `json:"in_use,omitempty"`
	Error string         `json:"error,omitempty"`
}

// tailscaleServeStatus is the subset of `tailscale serve status --json` we read.
// Deliberately partial: the full shape is large and not a stable contract.
type tailscaleServeStatus struct {
	TCP map[string]struct {
		HTTPS bool `json:"HTTPS"`
	} `json:"TCP"`
	Web map[string]struct {
		Handlers map[string]struct {
			Proxy string `json:"Proxy"`
		} `json:"Handlers"`
	} `json:"Web"`
	AllowFunnel map[string]bool `json:"AllowFunnel"`
}

// funnelStatus reports what Funnel is currently doing, given the gate's port.
func (s *Server) funnelStatus(gatePort int) funnelState {
	out := funnelState{Free: nil, InUse: map[int]string{}}
	raw, err := execOut("tailscale", "serve", "status", "--json")
	if err != nil {
		out.Error = "tailscale is not available on this machine"
		return out
	}
	out.Available = true

	var st tailscaleServeStatus
	if json.Unmarshal([]byte(raw), &st) != nil {
		// An empty config prints something that is not the object above; that is
		// not an error, it just means nothing is served yet.
		out.Free = append(out.Free, funnelPorts...)
		return out
	}

	target := "http://127.0.0.1:" + strconv.Itoa(gatePort)
	for _, port := range funnelPorts {
		// A port something on this machine is ALREADY listening on is not free,
		// whatever tailscale thinks.
		//
		// tailscale only knows about what `tailscale serve` is configured to
		// forward. kunai binds its own port directly, so tailscale reports it as
		// available, and taking it with Funnel does not fail: it silently starts
		// intercepting that port at the tailnet level and hands it to whatever
		// Funnel was pointed at. The app that was there is still running and
		// perfectly healthy, and completely unreachable.
		//
		// This cost a real outage. A nightly install on 8444 offered 8443 as free,
		// because 8443 belonged to the STABLE install and the check only knew this
		// process's own -addr. Turning on public access took the stable instance
		// off the air, and it stayed off after the share expired, because the
		// Funnel mapping outlives the link. So the question has to be "is anything
		// here already on that port", not "is it me".
		if who := listenerOn(port); who != "" {
			out.InUse[port] = who
			continue
		}
		key := ":" + strconv.Itoa(port)
		web, served := st.Web[hostSuffix(st, key)]
		if !served {
			out.Free = append(out.Free, port)
			continue
		}
		proxy := ""
		for _, h := range web.Handlers {
			if h.Proxy != "" {
				proxy = h.Proxy
				break
			}
		}
		if gatePort != 0 && proxy == target {
			out.Port = port
			continue
		}
		out.InUse[port] = proxy
	}
	return out
}

// listenerOn reports what is already listening on a port, or "" when nothing is.
//
// Asked by trying to bind it, which is the only answer that does not depend on
// parsing another tool's output or on this process knowing what else is
// installed. A port that cannot be bound is a port in use, whoever holds it: the
// stable install, a nightly one, or something that has nothing to do with kunai.
//
// The wildcard address is what has to be tried, not loopback: kunai binds its
// tailnet IP, so 127.0.0.1:8443 is still free while 8443 is very much taken.
// Binding 0.0.0.0 conflicts with a listener on ANY address, which is the
// question being asked.
//
// Only EADDRINUSE counts. A low port like 443 fails with a permission error for
// an unprivileged service, and that is not a reason to call it taken: Funnel on
// 443 is tailscale binding it, not kunai.
//
// A var so the tests can answer without opening sockets.
var listenerOn = func(port int) string {
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err == nil {
		_ = ln.Close()
		return ""
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return "already in use on this machine"
	}
	return ""
}

// hostSuffix finds the Web key ending in the port we are asking about. The key is
// "<host>:<port>" and the host is the machine's own MagicDNS name, which we would
// otherwise have to look up separately.
func hostSuffix(st tailscaleServeStatus, portKey string) string {
	for k := range st.Web {
		if strings.HasSuffix(k, portKey) {
			return k
		}
	}
	return ""
}

// funnelPortFor reports the public port serving the gate, and whether Funnel is
// actually on. Cached briefly, because it shells out and the share views ask for
// it on every render.
func (s *Server) funnelPortFor(gatePort int) (int, bool) {
	if gatePort == 0 {
		return 0, false
	}
	s.funnelMu.Lock()
	fresh := time.Since(s.funnelAt) < 5*time.Second
	port := s.funnelPort
	s.funnelMu.Unlock()
	if fresh {
		return port, port != 0
	}
	st := s.funnelStatus(gatePort)
	s.funnelMu.Lock()
	s.funnelPort, s.funnelAt = st.Port, time.Now()
	s.funnelMu.Unlock()
	return st.Port, st.Port != 0
}

// handleFunnelStatus reports what the owner would be turning on.
func (s *Server) handleFunnelStatus(w http.ResponseWriter, r *http.Request) {
	st := s.funnelStatus(s.gate.Port())
	writeJSON(w, http.StatusOK, st)
}

// handleFunnelOn points a public port at the share gate.
//
// This is outward-facing and hard to take back by accident, so it never happens
// implicitly: creating a share does not call it, and the UI shows the exact
// command before offering the button.
func (s *Server) handleFunnelOn(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Port int `json:"port"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body)
	if !allowedFunnelPort(body.Port) {
		writeErr(w, http.StatusBadRequest, "Funnel only serves 443, 8443 or 10000")
		return
	}
	// Refuse a port something here is already serving, checked HERE and not only
	// when the list was built. The status is advisory and can be seconds old; this
	// is the request that actually takes the port, and taking one already in use
	// does not fail loudly, it quietly makes the app that was there unreachable.
	// That is how a nightly install knocked the stable one off the air.
	if who := listenerOn(body.Port); who != "" {
		writeErr(w, http.StatusConflict,
			"port "+strconv.Itoa(body.Port)+" is "+who+
				", and Funnel would take it over and make that unreachable")
		return
	}
	// The gate must be listening before anything is pointed at it, or the first
	// visitor gets a connection refused from a port that looks live.
	if err := s.gate.start(s.baseCtx); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	args := funnelOnArgs(body.Port, s.gate.Port())
	if out, err := execOut(args[0], args[1:]...); err != nil {
		writeErr(w, http.StatusInternalServerError, strings.TrimSpace(out+" "+err.Error()))
		return
	}
	s.forgetFunnel()
	writeJSON(w, http.StatusOK, s.funnelStatus(s.gate.Port()))
}

// handleFunnelOff closes the public port again.
func (s *Server) handleFunnelOff(w http.ResponseWriter, r *http.Request) {
	port, _ := strconv.Atoi(r.URL.Query().Get("port"))
	if !allowedFunnelPort(port) {
		writeErr(w, http.StatusBadRequest, "Funnel only serves 443, 8443 or 10000")
		return
	}
	args := funnelOffArgs(port)
	if out, err := execOut(args[0], args[1:]...); err != nil {
		writeErr(w, http.StatusInternalServerError, strings.TrimSpace(out+" "+err.Error()))
		return
	}
	s.forgetFunnel()
	writeJSON(w, http.StatusOK, s.funnelStatus(s.gate.Port()))
}

func (s *Server) forgetFunnel() {
	s.funnelMu.Lock()
	s.funnelAt = time.Time{}
	s.funnelMu.Unlock()
}

// funnelOnArgs and funnelOffArgs are separated out so a test can assert the exact
// command without a tailnet, and so the UI can show the user the same string it
// is about to run.
func funnelOnArgs(publicPort, localPort int) []string {
	return []string{"tailscale", "funnel", "--bg",
		"--https=" + strconv.Itoa(publicPort),
		"http://127.0.0.1:" + strconv.Itoa(localPort)}
}

func funnelOffArgs(publicPort int) []string {
	return []string{"tailscale", "funnel", "--https=" + strconv.Itoa(publicPort), "off"}
}

func allowedFunnelPort(p int) bool {
	for _, ok := range funnelPorts {
		if p == ok {
			return true
		}
	}
	return false
}

var errNoFunnelPort = errors.New("every Funnel port is already serving something else")
