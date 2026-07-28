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
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"slices"
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
	// Resolved, not assumed. A macOS service started by launchd gets a minimal
	// PATH with no /usr/local/bin in it, and the GUI client keeps its CLI inside
	// the app bundle, so a bare "tailscale" is not found on exactly the machine
	// most likely to be running the GUI client. discover.go already learned this;
	// calling the binary by name here meant Funnel looked unavailable on a Mac
	// where it was installed and working.
	bin := tailscaleBin()
	if bin == "" {
		out.Error = "the tailscale command was not found on this machine"
		return out
	}
	raw, err := execOut(bin, "serve", "status", "--json")
	if err != nil {
		// Say what it said. "Not available" covered a missing binary, a stopped
		// daemon and a logged-out tailnet as if they were one thing, and none of
		// them is fixed the same way.
		out.Error = whyTailscaleRefused(raw, "could not ask tailscale about its serve config")
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
		// What tailscale itself says about the port is read FIRST, and the order
		// matters twice over.
		//
		// tailscaled BINDS a port it serves. So the bind probe below reports every
		// served port as "in use" -- including one already Funnelled to our own
		// gate. Running the probe first therefore hid the one answer that matters:
		// kunai could never see that Funnel was already on, so `reachable` stayed
		// false forever, the dialog kept saying nobody outside could open the link,
		// and the URL kept the tailnet port even after public access was working.
		//
		// And when the port belongs to something else, tailscale knows WHAT it is
		// pointed at, which is what somebody needs in order to free it. The probe
		// can only ever say "something". Asking the informed source first is how
		// "443 already in use on this machine" becomes "443 is serving
		// http://127.0.0.1:8501".
		key := ":" + strconv.Itoa(port)
		proxy := ""
		web, served := st.Web[hostSuffix(st, key)]
		if served {
			for _, h := range web.Handlers {
				if h.Proxy != "" {
					proxy = h.Proxy
					break
				}
			}
		}
		switch {
		case served && gatePort != 0 && proxy == target:
			out.Port = port // already serving this machine's share gate
		case served && staleLoopback(proxy):
			// Pointed at a loopback port with nothing behind it: a mapping kunai
			// made for a share gate that has since gone. Offered back rather than
			// reported busy, because otherwise every restart burned one of the three
			// Funnel ports permanently and the third attempt left nothing to share
			// with at all. Turning it on again simply repoints it.
			out.Free = append(out.Free, port)
		case served && proxy != "":
			out.InUse[port] = "tailscale is serving " + proxy + " here"
		case served:
			out.InUse[port] = "tailscale is already serving this port"
		case port == s.ownPort():
			out.InUse[port] = "kunai itself is serving on this port"
		default:
			// Anything tailscale does not know about: another kunai, or an app that
			// has nothing to do with either. Only a bind can see those.
			if who := listenerOn(port); who != "" {
				out.InUse[port] = who
			} else {
				out.Free = append(out.Free, port)
			}
		}
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
	// SO_REUSEADDR is turned OFF for this probe, and that is the whole reason it
	// works on a Mac.
	//
	// Go sets SO_REUSEADDR on every listener it opens. On Linux that still refuses
	// a wildcard bind while another socket holds the port on a specific address,
	// so the probe answered correctly. On BSD and macOS the same option PERMITS
	// exactly that bind -- so the probe succeeded, reported the port free, and the
	// dialog offered 8443: the port kunai itself is serving on. Turning Funnel on
	// there hands kunai's own port to the share gate and takes the machine off its
	// tailnet, which is the outage this function exists to prevent.
	lc := net.ListenConfig{
		Control: func(_, _ string, c syscall.RawConn) error {
			var serr error
			if err := c.Control(func(fd uintptr) {
				serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 0)
			}); err != nil {
				return err
			}
			return serr
		},
	}
	ln, err := lc.Listen(context.Background(), "tcp", ":"+strconv.Itoa(port))
	if err == nil {
		_ = ln.Close()
		return ""
	}
	// Only EADDRINUSE. EADDRNOTAVAIL was briefly accepted here too, on no evidence
	// at all, and a probe that guesses wrong in this direction reports every port
	// taken and leaves somebody with no way to share anything.
	if errors.Is(err, syscall.EADDRINUSE) {
		return "already in use on this machine"
	}
	return ""
}

// ownPort is the port kunai itself is serving on, or 0 if it cannot be read.
//
// Belt and braces beside the bind probe above, and deliberately not a
// replacement for it: this one needs no reasoning about socket options on any
// platform. Whatever a probe concludes, the port this process is listening on is
// never free, and pointing Funnel at it is the one mistake that costs the
// machine its tailnet.
func (s *Server) ownPort() int {
	_, p, err := net.SplitHostPort(s.cfg.Addr)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		return 0
	}
	return n
}

// whyTailscaleRefused is the command's own complaint, trimmed to something a
// dialog can hold. tailscale explains itself on stderr and that sentence is the
// most useful thing anybody can be shown; the fallback covers it saying nothing.
func whyTailscaleRefused(out, fallback string) string {
	for _, ln := range strings.Split(out, "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			if len(ln) > 160 {
				ln = ln[:160] + "…"
			}
			return ln
		}
	}
	return fallback
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
	// The gate must be listening before anything is pointed at it, or the first
	// visitor gets a connection refused from a port that looks live. It also has
	// to come first because the check below is computed against its port.
	if err := s.gate.start(s.baseCtx); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Whether a port may be taken is asked of funnelStatus, the same function
	// that decided what to OFFER, rather than re-derived here.
	//
	// It was re-derived, with a raw bind probe, and the two answers diverged the
	// moment funnelStatus learned to reclaim a mapping left pointing at a share
	// gate that no longer exists: the dialog offered 443 and this handler refused
	// it in the same breath, because tailscaled binds a port it serves and the
	// probe could only see that. A button that is offered and then rejected is
	// worse than one that was never offered, and any check written twice will
	// eventually disagree with itself.
	//
	// Freshly computed, not cached, because this is the request that actually
	// takes the port: the list the dialog was drawn from can be seconds old, and
	// taking a port already in use does not fail loudly, it quietly makes
	// whatever was there unreachable.
	st := s.funnelStatus(s.gate.Port())
	if st.Port == body.Port {
		writeJSON(w, http.StatusOK, st) // already serving our gate; nothing to do
		return
	}
	if !slices.Contains(st.Free, body.Port) {
		why := st.InUse[body.Port]
		if why == "" {
			why = "it is not available on this machine"
		}
		writeErr(w, http.StatusConflict,
			"port "+strconv.Itoa(body.Port)+" cannot be used: "+why)
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
//
// The binary is resolved rather than named, for the same reason funnelStatus
// resolves it: on a Mac under launchd there is no "tailscale" on PATH. The
// displayed command keeps the bare name, because that is what a person would
// type in their own shell.
func funnelOnArgs(publicPort, localPort int) []string {
	return []string{tailscaleOr("tailscale"), "funnel", "--bg",
		"--https=" + strconv.Itoa(publicPort),
		"http://127.0.0.1:" + strconv.Itoa(localPort)}
}

func funnelOffArgs(publicPort int) []string {
	return []string{tailscaleOr("tailscale"), "funnel", "--https=" + strconv.Itoa(publicPort), "off"}
}

// tailscaleOr resolves the CLI, falling back to the bare name so the argv stays
// readable in a test and in a log.
func tailscaleOr(fallback string) string {
	if bin := tailscaleBin(); bin != "" {
		return bin
	}
	return fallback
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

// staleLoopback reports whether a serve target points at a port on this machine
// that nothing is listening on any more.
//
// That is the signature of a mapping left behind by an earlier share gate: the
// gate used to take a fresh ephemeral port on every start, so each restart
// orphaned the mapping made for the previous one. Only loopback counts -- a
// target on another host is somebody else's business and this cannot tell
// whether it is up.
func staleLoopback(proxy string) bool {
	u, err := url.Parse(proxy)
	if err != nil {
		return false
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		return false
	}
	switch host {
	case "127.0.0.1", "localhost", "::1", "[::1]":
	default:
		return false
	}
	// Reachable means something is serving it; a refused connection on loopback
	// means the thing that mapping was made for is gone.
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", port), 400*time.Millisecond)
	if err != nil {
		return true
	}
	_ = c.Close()
	return false
}
