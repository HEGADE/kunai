package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fakeRelease serves an asset (with the right sha256 in checksums.txt) so the
// download+verify+swap path can run end to end without touching the network.
// Set corrupt to make the served bytes disagree with the advertised checksum.
func fakeRelease(t *testing.T, asset string, content []byte, corrupt bool) {
	t.Helper()
	sum := sha256.Sum256(content)
	checksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), asset)
	served := content
	if corrupt {
		served = append([]byte("tampered"), content...)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch filepath.Base(r.URL.Path) {
		case "checksums.txt":
			_, _ = w.Write([]byte(checksums))
		case asset:
			_, _ = w.Write(served)
		default:
			http.NotFound(w, r)
		}
	}))
	orig := releaseBase
	releaseBase = srv.URL
	t.Cleanup(func() { releaseBase = orig; srv.Close() })
}

// applyUpdate must download, verify, and atomically swap the new bytes over the
// target binary.
func TestApplyUpdateSwaps(t *testing.T) {
	asset := fmt.Sprintf("kunai-%s-%s", runtime.GOOS, runtime.GOARCH)
	newBytes := []byte("#!/bin/sh\necho new-kunai\n")
	fakeRelease(t, asset, newBytes, false)

	self := filepath.Join(t.TempDir(), "kunai")
	if err := os.WriteFile(self, []byte("old-kunai"), 0o755); err != nil {
		t.Fatal(err)
	}
	var lastDone, lastTotal int64
	if err := applyUpdate(asset, self, func(done, total int64) { lastDone, lastTotal = done, total }); err != nil {
		t.Fatalf("applyUpdate: %v", err)
	}
	got, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(newBytes) {
		t.Fatalf("binary not swapped: got %q", got)
	}
	if lastDone != int64(len(newBytes)) || lastTotal != int64(len(newBytes)) {
		t.Fatalf("progress reported %d/%d, want %d/%d", lastDone, lastTotal, len(newBytes), len(newBytes))
	}
}

// A checksum mismatch must abort without corrupting the existing binary and must
// leave no temp files behind.
func TestApplyUpdateChecksumMismatchLeavesBinary(t *testing.T) {
	asset := fmt.Sprintf("kunai-%s-%s", runtime.GOOS, runtime.GOARCH)
	fakeRelease(t, asset, []byte("legit"), true)

	dir := t.TempDir()
	self := filepath.Join(dir, "kunai")
	if err := os.WriteFile(self, []byte("old-kunai"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := applyUpdate(asset, self, nil); err == nil {
		t.Fatal("expected a checksum-mismatch error")
	}
	got, _ := os.ReadFile(self)
	if string(got) != "old-kunai" {
		t.Fatalf("binary changed after a failed update: %q", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}

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
