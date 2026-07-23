package server

import "testing"

func TestBaseURLIsLocalOrEmpty(t *testing.T) {
	local := []string{"", "  ", "http://127.0.0.1:8317", "http://localhost:9000", "http://[::1]:1234"}
	for _, u := range local {
		if !baseURLIsLocalOrEmpty(u) {
			t.Errorf("%q should be local/empty (native may take over)", u)
		}
	}
	external := []string{"https://my-proxy.example.com", "http://10.0.0.5:8080"}
	for _, u := range external {
		if baseURLIsLocalOrEmpty(u) {
			t.Errorf("%q is an external override and should be honored", u)
		}
	}
}
