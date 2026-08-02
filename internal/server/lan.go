package server

// Reaching kunai from another device on the same wifi, with no Tailscale.
//
// Opt-in, and it has to be: it puts the app in front of every device on the
// network, and kunai has no login. See the warning in lanGuard.
//
// What you get is the web app and nothing else. A LAN address is not a secure
// context, so the browser withholds service workers (hence no PWA install, no
// offline shell, no auto-update) and Web Push. Measured, not assumed: the app
// renders, the websocket streams, sessions work. That is the whole trade, and it
// is a reasonable one for "open the laptop's kunai on the tablet".
//
// A separate listener rather than widening the main one, for the same reason the
// loopback listener is separate: the perimeter is different here, and the way to
// keep two perimeters straight is two handlers, not a conditional inside one.

import (
	"context"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// tailscaleCGNAT is the 100.64.0.0/10 range Tailscale assigns. Addresses in it
// are skipped: the tailnet address is what the MAIN listener already serves, and
// binding it twice would just fail.
var tailscaleCGNAT = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// lanAddrs lists the private IPv4 addresses on this host worth serving, each
// joined to port.
//
// Specific addresses rather than 0.0.0.0, because the main listener is already
// holding a specific address on that port and a wildcard bind over the top of it
// fails outright (the bug that taught us this is in localAddr). Binding each
// interface separately also means a machine on two networks is reachable on both
// without the ambiguity of a wildcard.
func lanAddrs(port string) []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil || !ip.IsPrivate() || tailscaleCGNAT.Contains(ip) {
				continue
			}
			out = append(out, net.JoinHostPort(ip.String(), port))
		}
	}
	return out
}

// privateHost reports whether an authority names a private address literal.
//
// Deliberately IP literals only, no names. A DNS rebinding attack needs a NAME
// that resolves here, and refusing every name is the cheapest complete answer to
// it: an attacker cannot express the attack at all. The cost is that you reach
// the machine at 192.168.x.y rather than a friendly name, which for a link you
// paste into a tablet once is no cost.
func privateHost(authority string) bool {
	host := authority
	if h, _, err := net.SplitHostPort(authority); err == nil {
		host = h
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && (ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

// lanGuard is the perimeter for the LAN listener.
//
// The main listener's rules cannot be reused. Its CORS wildcard is safe only
// because "the tailnet is the entire auth perimeter, Tailscale ACLs decide who
// can reach the port" -- on a LAN address what decides that is every browser tab
// on the network, and POST /api/sessions takes any cwd and spawns a CLI in it.
//
// Two checks, the same pair loopback uses and for the same reasons:
//
//   - The Host must be a private address literal. This is what stops DNS
//     rebinding, where a hostile page points its own name at 192.168.x.y; the
//     browser then treats the request as same-origin and sends no cross-site
//     Origin, so the Origin check below cannot see it at all.
//   - A cross-site Origin is refused outright rather than merely denied a CORS
//     header. Withholding the header stops a hostile page READING the answer,
//     which is no help when the damage is done by the request arriving.
//
// What this does NOT do, and what you are agreeing to by turning it on: kunai
// has no login, so any device on the network that can talk to the port can drive
// the agent. The guard stops hostile WEB PAGES; it cannot stop a machine on your
// wifi making the request directly. Turning this on means trusting the network.
func lanGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackAuthority(r.Host) && !privateHost(r.Host) {
			http.Error(w, "kunai answers on its own address only", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !localOrigin(origin) && !privateOrigin(origin) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// privateOrigin reports whether an Origin header names a page this server served
// over the LAN.
func privateOrigin(origin string) bool {
	if origin == "" || origin == "null" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return privateHost(u.Host)
}

// serveLAN starts a listener on every private address, plain HTTP.
//
// Failures are logged and survived: a machine with several interfaces should not
// lose the ones that work because one did not, and none of this is the address
// kunai is primarily reachable at.
func (s *Server) serveLAN(ctx context.Context, port string) {
	addrs := lanAddrs(port)
	if len(addrs) == 0 {
		log.Printf("lan: no private network address found; nothing extra to serve")
		return
	}
	handler := lanGuard(cors(logRequests(s.routes()), false))
	for _, addr := range addrs {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			log.Printf("lan: cannot serve %s: %v", addr, err)
			continue
		}
		srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
		go func() {
			<-ctx.Done()
			shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutCtx)
		}()
		go func(l net.Listener) {
			if err := srv.Serve(l); err != nil && err != http.ErrServerClosed {
				log.Printf("lan listener: %v", err)
			}
		}(ln)
		log.Printf("kunai also on http://%s (this network; no app install or notifications here)", addr)
	}
}
