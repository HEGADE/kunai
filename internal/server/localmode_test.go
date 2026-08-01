package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoopbackBindDecidesLocalMode(t *testing.T) {
	for _, c := range []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8443", true},
		{"localhost:8443", true},
		{"[::1]:8443", true},
		{"127.0.0.2:8443", true}, // the whole 127/8 block is this machine
		{"100.90.239.81:8443", false},
		{"192.168.1.10:8443", false},
		// An empty host is EVERY interface. Reading that as local would loosen the
		// rules on a machine that is genuinely exposed, which is the one direction
		// a wrong guess must never go.
		{":8443", false},
		{"0.0.0.0:8443", false},
	} {
		if got := loopbackBind(c.addr); got != c.want {
			t.Errorf("loopbackBind(%q) = %v, want %v", c.addr, got, c.want)
		}
	}
}

// The guard is the entire perimeter in local mode, so each way past it is worth
// a case. Every one of these is a request a hostile page can make from a tab the
// owner has open, against a server whose port is "only" on localhost.
func TestLocalGuardRefusesAnythingThatIsNotThisMachine(t *testing.T) {
	guarded := localGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot) // proof the request reached the app
	}))

	for _, c := range []struct {
		name   string
		host   string
		origin string
		want   int
	}{
		{"the app itself", "localhost:8443", "http://localhost:8443", http.StatusTeapot},
		{"the app on the IP", "127.0.0.1:8443", "http://127.0.0.1:8443", http.StatusTeapot},
		{"IPv6 loopback", "[::1]:8443", "http://[::1]:8443", http.StatusTeapot},
		{
			// curl, a script, the handoff command. Not a browser, so not the threat:
			// anything that can run a command here can run claude without us.
			name: "no origin at all", host: "localhost:8443", origin: "", want: http.StatusTeapot,
		},
		{
			// The plain cross-site case: a page on the internet fetching localhost.
			name: "a hostile page", host: "localhost:8443",
			origin: "https://evil.example", want: http.StatusForbidden,
		},
		{
			// DNS rebinding. The attacker's name resolves to 127.0.0.1, so the
			// browser thinks this is same-origin and sends no cross-site Origin --
			// the Origin check cannot see this one at all. What they cannot forge is
			// the name in the request.
			name: "rebinding onto loopback", host: "evil.example:8443",
			origin: "http://evil.example:8443", want: http.StatusForbidden,
		},
		{
			// Same attack, with the Origin omitted entirely to slip past that check.
			name: "rebinding with no origin", host: "evil.example:8443",
			origin: "", want: http.StatusForbidden,
		},
		{
			// A sandboxed iframe or a file:// page. "Unknown" is not "harmless".
			name: "null origin", host: "localhost:8443",
			origin: "null", want: http.StatusForbidden,
		},
		{
			// Reads as local right up to the last character. Substring matching on
			// "localhost" is the obvious wrong implementation of this check.
			name: "a lookalike hostname", host: "localhost.evil.example:8443",
			origin: "", want: http.StatusForbidden,
		},
		{
			name: "a lookalike origin", host: "localhost:8443",
			origin: "https://localhost.evil.example", want: http.StatusForbidden,
		},
	} {
		r := httptest.NewRequest(http.MethodPost, "/api/sessions", nil)
		r.Host = c.host
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		w := httptest.NewRecorder()
		guarded.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("%s: status %d, want %d", c.name, w.Code, c.want)
		}
	}
}

// The guard has to cover websockets too. A handshake is an ordinary request
// until it upgrades, and ws.go accepts any origin on the stated grounds that the
// tailnet is the perimeter -- which in local mode is no longer true. Wrapping
// outside everything is what lets that line stay as it is.
func TestLocalGuardCoversWebsocketRoutes(t *testing.T) {
	reached := false
	guarded := localGuard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))

	r := httptest.NewRequest(http.MethodGet, "/ws/app/abc", nil)
	r.Host = "localhost:8443"
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	guarded.ServeHTTP(w, r)

	if reached {
		t.Fatal("a websocket handshake from a hostile origin reached the handler")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403", w.Code)
	}
}

// The wildcard exists so the hub's PWA can call its PEERS. A local machine has
// none, so handing it out buys nothing and would let a hostile page read every
// answer it managed to provoke.
func TestNoWildcardOriginInLocalMode(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	r := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	w := httptest.NewRecorder()
	cors(ok, false).ServeHTTP(w, r)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("local mode advertised Access-Control-Allow-Origin: %q", got)
	}

	// And the multi-machine case is untouched, or every peer stops answering.
	w = httptest.NewRecorder()
	cors(ok, true).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/stats", nil))
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("tailnet mode lost its wildcard: %q", got)
	}
}
