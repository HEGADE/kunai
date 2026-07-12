package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// checksumFor must pick the right hash out of an sha256sum-format checksums.txt
// (two-space separated, one line per asset) and error when the asset is absent.
func TestChecksumFor(t *testing.T) {
	const body = "aaa111  kunai-linux-amd64\n" +
		"bbb222  kunai-darwin-arm64\n" +
		"ccc333  kunai-linux-arm64\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/checksums.txt" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	orig := releaseBase
	releaseBase = srv.URL
	defer func() { releaseBase = orig }()

	got, err := checksumFor(srv.Client(), "kunai-darwin-arm64")
	if err != nil {
		t.Fatalf("checksumFor: %v", err)
	}
	if got != "bbb222" {
		t.Fatalf("got %q, want bbb222", got)
	}

	if _, err := checksumFor(srv.Client(), "kunai-windows-amd64"); err == nil {
		t.Fatal("expected error for a missing asset")
	}
}
