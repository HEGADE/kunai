package server

import (
	"net/http"
	"strings"
)

// cors lets the PWA served from one machine (the hub) call the REST API of other
// machines directly over the tailnet — the basis of the multi-machine client.
//
// A wildcard origin is safe here: the tailnet is the entire auth perimeter
// (Tailscale ACLs decide who can reach the port) and the API uses no cookies or
// credentials, so there is nothing for a hostile origin to ride. WebSocket
// upgrades run their own origin check in ws.go and are left untouched.
//
// crossMachine is false in local mode, where that premise does not hold: nothing
// decides who reaches a loopback port, so every page in the browser can try. The
// wildcard is not merely unsafe there, it is pointless -- it exists so the hub's
// PWA can call its PEERS, and a machine on no tailnet has none. See localmode.go
// for the guard that does the actual refusing; this only stops us handing out
// permission nobody needs.
func cors(next http.Handler, crossMachine bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !crossMachine || strings.HasPrefix(r.URL.Path, "/ws/") {
			next.ServeHTTP(w, r)
			return
		}
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Add("Vary", "Origin")
		if r.Method == http.MethodOptions {
			// Preflight: our routes are method-scoped, so an OPTIONS reaches no
			// handler — answer it here.
			h.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Content-Type")
			h.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
