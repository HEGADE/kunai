package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// One-click self-update. The client already knows every machine's version (from
// /api/stats) and compares it against GitHub's latest release tag client-side;
// when a machine is behind, the dashboard offers an Update button that POSTs
// here. The server then pulls the matching prebuilt binary from the latest
// GitHub release, verifies its sha256 against the release checksums.txt, swaps
// it over the running binary, and exits(0). The service manager (systemd
// Restart=always / launchd KeepAlive) brings it straight back on the new binary
// — the process never restarts itself. This is the only path where a machine
// reaches out to GitHub, and it fires only on an explicit user tap, so the
// relay-free promise holds. No content ever leaves the tailnet here either.

const updateTimeout = 90 * time.Second

// releaseBase is the latest-release asset directory; a var so tests can point it
// at a local server.
var releaseBase = "https://github.com/HEGADE/kunai/releases/latest/download"

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	self, err := os.Executable()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot locate own binary")
		return
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	// Refuse early with a clear message if we can't replace the binary in place
	// (e.g. root-owned install), rather than downloading and failing at the swap.
	if err := writableTarget(self); err != nil {
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}

	asset := fmt.Sprintf("kunai-%s-%s", runtime.GOOS, runtime.GOARCH)
	if err := applyUpdate(asset, self); err != nil {
		writeErr(w, http.StatusBadGateway, "update failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
	// Give the response time to flush, then exit so the service manager restarts
	// us on the new binary.
	go func() {
		time.Sleep(400 * time.Millisecond)
		log.Printf("update: swapped %s, exiting for service-manager restart", self)
		os.Exit(0)
	}()
}

// applyUpdate downloads the asset, verifies its sha256, and atomically swaps it
// over self. Everything but the process exit lives here so it is testable.
func applyUpdate(asset, self string) error {
	newBin, err := downloadAndVerify(asset, filepath.Dir(self))
	if err != nil {
		return err
	}
	if err := os.Chmod(newBin, 0o755); err != nil {
		_ = os.Remove(newBin)
		return fmt.Errorf("chmod: %w", err)
	}
	// Atomic on the same filesystem; replacing a running binary's file is allowed
	// on Linux and macOS (the running process keeps the old inode until it exits).
	if err := os.Rename(newBin, self); err != nil {
		_ = os.Remove(newBin)
		return fmt.Errorf("swap: %w", err)
	}
	return nil
}

// writableTarget reports whether we can atomically replace path — its directory
// must be writable (os.Rename needs to create + rename within it).
func writableTarget(path string) error {
	dir := filepath.Dir(path)
	probe := filepath.Join(dir, ".kunai-update-probe")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("cannot write to %s (update needs a writable install dir)", dir)
	}
	_ = f.Close()
	_ = os.Remove(probe)
	return nil
}

// downloadAndVerify fetches the release asset and checksums.txt, verifies the
// asset's sha256, and writes it to a temp file in dir. Returns the temp path.
func downloadAndVerify(asset, dir string) (string, error) {
	client := &http.Client{Timeout: updateTimeout}

	want, err := checksumFor(client, asset)
	if err != nil {
		return "", err
	}

	tmp, err := os.CreateTemp(dir, ".kunai-update-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(tmpPath) }

	resp, err := client.Get(releaseBase + "/" + asset)
	if err != nil {
		cleanup()
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		cleanup()
		return "", fmt.Errorf("download %s: HTTP %d", asset, resp.StatusCode)
	}

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		cleanup()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("checksum mismatch for %s", asset)
	}
	return tmpPath, nil
}

// checksumFor pulls checksums.txt from the release and returns the expected
// sha256 for asset. Lines are "<hash>  <filename>" (sha256sum format).
func checksumFor(client *http.Client, asset string) (string, error) {
	resp, err := client.Get(releaseBase + "/checksums.txt")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checksums.txt: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && filepath.Base(fields[1]) == asset {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("no checksum for %s", asset)
}
