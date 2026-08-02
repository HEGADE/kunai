package server

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func splitHostPortForTest(addr string) (string, string, error) { return net.SplitHostPort(addr) }
func parseIPForTest(host string) net.IP                        { return net.ParseIP(host) }

// A LAN bind hands the app to every device on the network, so the perimeter is
// the whole feature. Each case below is a request a hostile page can make from a
// tab somebody has open on the same wifi.
func TestLANGuardRefusesAnythingButThisNetwork(t *testing.T) {
	guarded := lanGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	for _, c := range []struct {
		name   string
		host   string
		origin string
		want   int
	}{
		{"the app on the LAN", "192.168.0.6:8443", "http://192.168.0.6:8443", http.StatusTeapot},
		{"another private range", "10.1.2.3:8443", "http://10.1.2.3:8443", http.StatusTeapot},
		{"loopback still works", "localhost:8443", "http://localhost:8443", http.StatusTeapot},
		{"a .localhost name", "kunai.localhost:8443", "", http.StatusTeapot},
		{
			// The plain cross-site case: a page on the internet, open in a browser
			// on this wifi, reaching in.
			name: "a hostile page reaching in", host: "192.168.0.6:8443",
			origin: "https://evil.example", want: http.StatusForbidden,
		},
		{
			// DNS rebinding: the attacker's own name resolves to the LAN address, so
			// the browser thinks it is same-origin and sends no cross-site Origin.
			// Only the Host betrays it, which is why names are refused outright.
			name: "rebinding onto the LAN address", host: "evil.example:8443",
			origin: "", want: http.StatusForbidden,
		},
		{
			// A PUBLIC address is not ours either. Nothing legitimate reaches this
			// listener by one.
			name: "a public address as Host", host: "93.184.216.34:8443",
			origin: "", want: http.StatusForbidden,
		},
		{
			name: "null origin", host: "192.168.0.6:8443",
			origin: "null", want: http.StatusForbidden,
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

func TestPrivateHostAcceptsOnlyPrivateLiterals(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"192.168.0.6:8443", true},
		{"10.0.0.1:8443", true},
		{"172.16.4.2:8443", true},
		{"169.254.10.1:8443", true}, // link-local, e.g. a direct cable
		{"192.168.0.6", true},       // no port
		{"8.8.8.8:8443", false},     // public
		{"example.com:8443", false}, // a name, never
		// The rebinding shape: a name that WOULD resolve to a private address.
		// Refusing every name is what makes that attack inexpressible.
		{"kunai.example.com:8443", false},
		{"", false},
	} {
		if got := privateHost(c.in); got != c.want {
			t.Errorf("privateHost(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// The tailnet address is already served by the main listener, so binding it again
// would fail; and loopback is the local listener's job.
func TestLANAddrsSkipTailnetAndLoopback(t *testing.T) {
	for _, addr := range lanAddrs("8443") {
		host, _, _ := splitHostPortForTest(addr)
		if !privateHost(host) {
			t.Errorf("lanAddrs returned a non-private address: %s", addr)
		}
		if tailscaleCGNAT.Contains(parseIPForTest(host)) {
			t.Errorf("lanAddrs returned the tailnet address %s, which the main listener already has", addr)
		}
		if parseIPForTest(host).IsLoopback() {
			t.Errorf("lanAddrs returned loopback %s, which the local listener already has", addr)
		}
	}
}
