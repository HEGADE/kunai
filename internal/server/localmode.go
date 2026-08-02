package server

// Local mode: kunai bound to the loopback interface, reachable only from the
// machine it runs on.
//
// This is what you get with no Tailscale. It has always worked -- the binary
// defaults to 127.0.0.1 and a loopback origin is a secure context, so the PWA
// and its service worker install without a certificate -- but it was never a
// supported way to install, so nothing had thought about who is allowed to talk
// to it.
//
// That question has a different answer here than on a tailnet, and cors.go is
// explicit about why:
//
//	A wildcard origin is safe here: the tailnet is the entire auth perimeter
//	(Tailscale ACLs decide who can reach the port)...
//
// The reasoning was never "no credentials, no risk". It was "Tailscale decides
// who reaches the port". On loopback, what decides who reaches the port is every
// page open in your browser: a hostile site can POST /api/sessions, which takes
// any cwd and spawns a CLI in it. Binding to localhost sounds like the safest
// possible choice and is the one that removes the perimeter.
//
// So local mode brings its own, and it is two checks rather than a permission
// model, because there is genuinely only one legitimate caller: the app kunai
// served, on this machine.
//
//   - The Host must be a loopback name. A DNS rebinding attack resolves an
//     attacker's domain to 127.0.0.1, which makes the browser treat the request
//     as same-origin and hands over the response; the Origin check below cannot
//     see it, because as far as the browser is concerned nothing is crossed.
//     What the attacker cannot change is that the request still arrives asking
//     for THEIR hostname.
//   - A cross-site Origin is refused outright rather than merely having its
//     response withheld. Dropping the CORS header stops a hostile page READING
//     the answer, which is no comfort at all when the damage is done by the
//     request arriving: a POST that starts a session has already started it.
//
// Requests carrying no Origin at all are allowed: curl and the like are not the
// threat, since anything that can run curl on this machine can run claude
// directly and does not need to go through us to do it.

import (
	"context"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// localAddr is the loopback address the app is ALSO served on when the main
// listener is bound to a network address.
//
// This is the point of the whole file, and it was the thing originally missed.
// Using kunai on the machine it runs on should not involve the tailnet at all.
// The tailnet URL does work here -- MagicDNS resolves it to this machine's own
// Tailscale interface -- but it makes your own laptop depend on tailscaled being
// up, MagicDNS resolving, and the certificate being valid, to reach a program
// running a few millimetres away. Log out of Tailscale and the app on your own
// machine dies with it.
//
// The port is the SAME one, which is free to take: the main listener binds a
// specific address (the tailnet IP), not every interface, so 127.0.0.1 on that
// port is a different socket and nobody is on it. So the local link is the port
// you already know, with no second number to remember.
//
// Empty when the main listener is already loopback, since there is nothing to
// add: it is the local listener.
func localAddr(mainAddr string) string {
	if mainAddr == "" || loopbackBind(mainAddr) {
		return ""
	}
	host, port, err := net.SplitHostPort(mainAddr)
	if err != nil || port == "" {
		return ""
	}
	// A wildcard bind (0.0.0.0, ::, or no host at all) is already listening on
	// loopback, and taking 127.0.0.1 on that port first makes the MAIN listener
	// fail with "address already in use" -- the server loses the address it exists
	// to serve, to gain one it already had. Found by running it, not by reading it.
	if host == "" {
		return ""
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil && ip.IsUnspecified() {
		return ""
	}
	return net.JoinHostPort("127.0.0.1", port)
}

// serveLocal starts the loopback listener beside the main one, plain HTTP.
//
// Plain HTTP is not a downgrade here: a loopback origin is trusted by browsers
// without a certificate, so the app, its service worker and Web Push all work.
// TLS would in fact be worse, since the tailnet certificate names a ts.net host
// and a browser asked for https://localhost would refuse it.
//
// A failure is logged and survived, never fatal. This listener is a convenience
// on top of a machine that is already reachable; taking the whole server down
// because a spare port was busy would trade a working install for a tidy one.
func (s *Server) serveLocal(ctx context.Context, addr string) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("local listener on %s unavailable (%v); this machine can still use its main address", addr, err)
		return
	}
	srv := &http.Server{Handler: s.localHandler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("local listener: %v", err)
		}
	}()
	log.Printf("kunai also on http://localhost:%s (this machine, no tailnet needed)", portOf(addr))
}

// portOf is the port half of a host:port, or the whole string if it has none.
func portOf(addr string) string {
	if _, port, err := net.SplitHostPort(addr); err == nil {
		return port
	}
	return addr
}

// loopbackBind reports whether addr binds the loopback interface only, which is
// what puts the server in local mode.
//
// An empty host (":8443") is every interface, and so explicitly NOT local mode:
// that binding is reachable from the network and must keep the tailnet's rules,
// or a guess here would quietly loosen a machine that is actually exposed.
func loopbackBind(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	return loopbackHost(host)
}

// loopbackHost reports whether a bare hostname (no port) names this machine.
//
// Anything under .localhost counts, which is what lets the app be reached at a
// name like http://kunai.localhost:8443 rather than a bare port. That costs
// nothing to support: RFC 6761 reserves the whole .localhost TLD for loopback, so
// it cannot be registered by anybody, and browsers resolve it themselves without
// consulting DNS at all.
//
// It does not weaken the rebinding check, which turns on an attacker getting a
// name they control to resolve here. They cannot have a .localhost name, and if
// they could serve a page from one they would already be running code on this
// machine. Note the suffix requires the dot, so localhost.evil.example is still
// refused -- that is the lookalike the check exists for.
func loopbackHost(host string) bool {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// loopbackAuthority reports whether a "host" or "host:port" authority is local.
func loopbackAuthority(authority string) bool {
	if host, _, err := net.SplitHostPort(authority); err == nil {
		return loopbackHost(host)
	}
	return loopbackHost(authority)
}

// localOrigin reports whether an Origin header names a page this server served.
//
// "null" is refused. It is what a sandboxed iframe or a file:// page sends, and
// neither is the app; treating it as unknown-therefore-fine would be a hole
// shaped exactly like the one the check exists to close.
func localOrigin(origin string) bool {
	if origin == "" || origin == "null" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return loopbackAuthority(u.Host)
}

// localGuard confines a loopback-bound server to callers on this machine.
//
// Wrapped OUTSIDE everything, websockets included, so there is one place that
// decides and no route can be added that forgets to ask. A websocket handshake
// is an ordinary HTTP request until it is upgraded, so the same two checks cover
// it, which is why ws.go can go on accepting any origin: by the time it runs,
// this has already turned away anything that is not us.
func localGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackAuthority(r.Host) {
			// Deliberately terse. Whoever sent this is either confused or probing,
			// and neither needs to be told how the check works.
			http.Error(w, "kunai is running in local mode: reach it at localhost", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !localOrigin(origin) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
