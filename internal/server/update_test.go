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
	"strings"
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
	// Served the way GitHub does: a release describing its assets, each fetched
	// by ID rather than by name. Assets are addressed by id precisely so a
	// re-upload cannot be served from a cache under the old name.
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release":
			_, _ = fmt.Fprintf(w, `{"tag_name":"nightly","assets":[
				{"id":1,"name":"checksums.txt","url":"%s/assets/1"},
				{"id":2,"name":%q,"url":"%s/assets/2"}]}`, srv.URL, asset, srv.URL)
		case "/assets/1":
			_, _ = w.Write([]byte(checksums))
		case "/assets/2":
			_, _ = w.Write(served)
		default:
			http.NotFound(w, r)
		}
	}))
	orig := releaseAPI
	releaseAPI = srv.URL + "/release"
	t.Cleanup(func() { releaseAPI = orig; srv.Close() })
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
	noRetryWait(t)
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
func TestChecksumFrom(t *testing.T) {
	const body = "aaa111  kunai-linux-amd64\n" +
		"bbb222  kunai-darwin-arm64\n" +
		"ccc333  kunai-linux-arm64\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	rel := release{Assets: []releaseAsset{{ID: 1, Name: "checksums.txt", URL: srv.URL}}}

	got, err := checksumFrom(srv.Client(), rel, "kunai-darwin-arm64")
	if err != nil {
		t.Fatalf("checksumFrom: %v", err)
	}
	if got != "bbb222" {
		t.Fatalf("got %q, want bbb222", got)
	}

	if _, err := checksumFrom(srv.Client(), rel, "kunai-windows-amd64"); err == nil {
		t.Fatal("expected error for a missing asset")
	}
}

// The bug this whole change exists for. GitHub redirects a name-based asset URL
// to a CDN that caches by URL, so a re-uploaded nightly served the PREVIOUS
// build for a window after publishing. Two updates in a row installed the build
// already running, and the checksum did not catch it because checksums.txt was
// fetched by name too: a stale binary and a stale checksum are consistent with
// each other.
//
// Resolving through the release means every asset is addressed by an id that
// changes on re-upload, and both come from ONE read, so they cannot be from
// different generations.
func TestAssetsComeFromOneReadOfTheRelease(t *testing.T) {
	var reads int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/release" {
			reads++
			_, _ = fmt.Fprintf(w, `{"tag_name":"nightly","assets":[
				{"id":7,"name":"checksums.txt","url":"%s/assets/7"}]}`, "http://example.invalid")
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	orig := releaseAPI
	releaseAPI = srv.URL + "/release"
	defer func() { releaseAPI = orig }()

	rel, err := fetchRelease(srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if reads != 1 {
		t.Errorf("read the release %d times, want exactly one", reads)
	}
	// Every asset is addressed by its id, which is what a re-upload changes and
	// therefore what a CDN cannot serve stale under an old name.
	a, err := rel.find("checksums.txt")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != 7 || !strings.Contains(a.URL, "/assets/7") {
		t.Errorf("asset = %+v, want it addressed by id", a)
	}
	// A release that does not carry what we need says so by name, rather than
	// failing later with a 404 nobody can act on.
	if _, err := rel.find("kunai-linux-amd64"); err == nil ||
		!strings.Contains(err.Error(), "checksums.txt") {
		t.Errorf("a missing asset should name what the release does have, got %v", err)
	}
}

// noRetryWait makes the mid-publish retry loop run without sleeping.
func noRetryWait(t *testing.T) {
	t.Helper()
	orig := updateRetryDelay
	updateRetryDelay = 0
	t.Cleanup(func() { updateRetryDelay = orig })
}

// The nightly release is recreated on every push, so a download begun during the
// upload window can see a binary that disagrees with checksums.txt. The updater
// must ride that out: a mismatch on the first attempt and a clean asset on the
// retry is a successful update, not an error surfaced to the user.
func TestApplyUpdateRetriesThroughPublishWindow(t *testing.T) {
	asset := fmt.Sprintf("kunai-%s-%s", runtime.GOOS, runtime.GOARCH)
	content := []byte("#!/bin/sh\necho healed\n")
	sum := sha256.Sum256(content)
	checksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), asset)

	var assetHits int
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release":
			_, _ = fmt.Fprintf(w, `{"tag_name":"nightly","assets":[
				{"id":1,"name":"checksums.txt","url":"%s/assets/1"},
				{"id":2,"name":%q,"url":"%s/assets/2"}]}`, srv.URL, asset, srv.URL)
		case "/assets/1":
			_, _ = w.Write([]byte(checksums))
		case "/assets/2":
			assetHits++
			if assetHits == 1 {
				_, _ = w.Write(append([]byte("mid-publish"), content...)) // stale bytes
				return
			}
			_, _ = w.Write(content)
		default:
			http.NotFound(w, r)
		}
	}))
	orig := releaseAPI
	releaseAPI = srv.URL + "/release"
	t.Cleanup(func() { releaseAPI = orig; srv.Close() })

	self := filepath.Join(t.TempDir(), "kunai")
	if err := os.WriteFile(self, []byte("old-kunai"), 0o755); err != nil {
		t.Fatal(err)
	}
	noRetryWait(t)
	if err := applyUpdate(asset, self, nil); err != nil {
		t.Fatalf("update should heal through the publish window, got: %v", err)
	}
	if got, _ := os.ReadFile(self); string(got) != string(content) {
		t.Fatalf("binary not swapped to the healed content: %q", got)
	}
	if assetHits != 2 {
		t.Fatalf("expected exactly one retry (2 hits), got %d", assetHits)
	}
}
