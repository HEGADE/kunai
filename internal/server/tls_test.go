package server

import "testing"

func TestCertDomain(t *testing.T) {
	cases := []struct {
		publicURL, certFile, want string
	}{
		{"https://host.tailnet.ts.net:8443", "/x/host.tailnet.ts.net.crt", "host.tailnet.ts.net"},
		{"", "/x/host.tailnet.ts.net.crt", "host.tailnet.ts.net"}, // fall back to filename
		{"https://mac.example.ts.net:8443", "/data/tls/mac.example.ts.net.crt", "mac.example.ts.net"},
		{"not a url", "/x/fallback.crt", "fallback"},
	}
	for _, c := range cases {
		if got := certDomain(c.publicURL, c.certFile); got != c.want {
			t.Errorf("certDomain(%q, %q) = %q, want %q", c.publicURL, c.certFile, got, c.want)
		}
	}
}
