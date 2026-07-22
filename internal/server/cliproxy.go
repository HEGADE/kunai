package server

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// ensureCLIProxy starts the managed sidecar if it is not already running. Safe to
// call from boot and from a runtime provider add (start is idempotent). A failed
// download is logged so a provider that cannot reach its proxy is diagnosable.
func (s *Server) ensureCLIProxy() {
	if s.cliproxy == nil {
		return
	}
	ctx := s.baseCtx
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.cliproxy.start(ctx); err != nil {
		log.Printf("cliproxy: %v", err)
	}
}

// ensureCLIProxyReady starts the sidecar if needed and blocks (bounded) until it
// has a bound port, so a provider session compiled right after gets a real
// base_url rather than the empty string a still-starting sidecar returns. The
// started flag is set before the port is assigned, so we poll BaseURL() rather
// than trust it. Called synchronously on the provider paths that bake the env.
func (s *Server) ensureCLIProxyReady() {
	if s.cliproxy == nil {
		return
	}
	ctx := s.baseCtx
	if ctx == nil {
		ctx = context.Background()
	}
	go func() { _ = s.cliproxy.start(ctx) }() // ensure a start is in flight (idempotent)
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		if s.cliproxy.BaseURL() != "" {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// The managed CLIProxyAPI sidecar. Providers (Codex/Grok/Kimi) work by pointing
// the claude agent at a local CLIProxyAPI; rather than make the owner install and
// run that themselves, kunai fetches a pinned release, verifies its checksum,
// writes a localhost-only config, and supervises the process for the machine's
// lifetime. This is the "install kunai and providers just work" half; the other
// half is the in-app login that authorizes each provider into the sidecar.
//
// The download is pinned by version AND sha256 (below), so a compromised or
// swapped release asset is refused. Later we intend to fold the proxy's logic
// into kunai directly; until then it runs as a supervised child.

const cliproxyVersion = "7.2.95"

// cliproxyAsset is the release tarball for a platform plus its pinned sha256.
type cliproxyAsset struct {
	name   string
	sha256 string
}

// cliproxyAssets pins the release per GOOS_GOARCH. Checksums are from the v7.2.95
// checksums.txt (the linux/amd64 one re-verified against a locally downloaded
// tarball). aarch64 is the release's spelling of arm64.
var cliproxyAssets = map[string]cliproxyAsset{
	"linux_amd64":  {"CLIProxyAPI_" + cliproxyVersion + "_linux_amd64.tar.gz", "826604e2dbf11913b0f373047f7bca1829eb2bab8a45d3a1916cc2534c7a9fd5"},
	"linux_arm64":  {"CLIProxyAPI_" + cliproxyVersion + "_linux_aarch64.tar.gz", "acc1173c73db2a2ee203438bac9a956491855d4955c5175855abc62d12ae0184"},
	"darwin_amd64": {"CLIProxyAPI_" + cliproxyVersion + "_darwin_amd64.tar.gz", "fbee90c29ee1047a8b3041d736500422bea22cd2ebb306782efcd74c0a10939c"},
	"darwin_arm64": {"CLIProxyAPI_" + cliproxyVersion + "_darwin_aarch64.tar.gz", "c7ccc28b7db5d1799999a9e22725ccc6bd0e36d9aa023da6b52b7c1a71aad978"},
}

// assetFor returns the pinned asset for the current platform, ok=false when
// unsupported (the feature then stays off rather than guessing a binary).
func assetFor(goos, goarch string) (cliproxyAsset, bool) {
	a, ok := cliproxyAssets[goos+"_"+goarch]
	return a, ok
}

// cliproxyManager owns the sidecar's files and process. Zero value is unusable;
// build with newCLIProxyManager.
type cliproxyManager struct {
	dir    string // <dataDir>/cliproxy
	apiKey string // the key providers authenticate to the sidecar with

	mu      sync.Mutex
	port    int
	running bool
	started bool // supervision launched once; start() is idempotent after
	cmd     *exec.Cmd
}

func newCLIProxyManager(dataDir string) *cliproxyManager {
	if dataDir == "" {
		return nil // no data dir: no managed sidecar (dev/ephemeral)
	}
	dir := filepath.Join(dataDir, "cliproxy")
	m := &cliproxyManager{dir: dir}
	m.apiKey = m.loadOrMakeKey()
	return m
}

func (m *cliproxyManager) binPath() string    { return filepath.Join(m.dir, "cli-proxy-api") }
func (m *cliproxyManager) configPath() string { return filepath.Join(m.dir, "config.yaml") }
func (m *cliproxyManager) authDir() string    { return filepath.Join(m.dir, "auth") }
func (m *cliproxyManager) logPath() string    { return filepath.Join(m.dir, "cliproxy.log") }

// loadOrMakeKey returns a stable per-install api key for the sidecar, generating
// and persisting one on first use so it survives restarts.
func (m *cliproxyManager) loadOrMakeKey() string {
	p := filepath.Join(m.dir, "apikey")
	if b, err := os.ReadFile(p); err == nil && len(b) >= 16 {
		return string(b)
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "kunai-cliproxy-key" // deterministic fallback; still localhost-only
	}
	key := "kn-" + hex.EncodeToString(buf)
	_ = os.MkdirAll(m.dir, 0o700)
	_ = os.WriteFile(p, []byte(key), 0o600)
	return key
}

// BaseURL is the sidecar origin providers point at, "" until it has a port.
func (m *cliproxyManager) BaseURL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.port == 0 {
		return ""
	}
	return fmt.Sprintf("http://127.0.0.1:%d", m.port)
}

func (m *cliproxyManager) APIKey() string { return m.apiKey }

func (m *cliproxyManager) isRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// ensureBinary downloads and verifies the pinned release into dir if the binary
// is not already present. It is safe to call repeatedly.
func (m *cliproxyManager) ensureBinary(ctx context.Context) error {
	if _, err := os.Stat(m.binPath()); err == nil {
		return m.hardenBinary() // already downloaded; still make sure it can run
	}
	asset, ok := assetFor(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return fmt.Errorf("no pinned CLIProxyAPI build for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return err
	}
	url := fmt.Sprintf("https://github.com/router-for-me/CLIProxyAPI/releases/download/v%s/%s", cliproxyVersion, asset.name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download CLIProxyAPI: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download CLIProxyAPI: HTTP %d", resp.StatusCode)
	}
	// Buffer to disk while hashing, so we verify before trusting the archive.
	tmp, err := os.CreateTemp(m.dir, "dl-*.tar.gz")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	if got := hex.EncodeToString(h.Sum(nil)); got != asset.sha256 {
		return fmt.Errorf("CLIProxyAPI checksum mismatch: got %s want %s", got, asset.sha256)
	}
	if err := extractBinary(tmp.Name(), "cli-proxy-api", m.binPath()); err != nil {
		return err
	}
	return m.hardenBinary()
}

// hardenBinary makes the downloaded proxy runnable. On macOS an unsigned or
// quarantined binary is killed on exec (Apple Silicon requires at least an
// ad-hoc signature), and we fetched this over HTTP rather than a browser, so
// strip any quarantine flag and ad-hoc sign it. Best-effort: a Mac without
// codesign is rare, and a failure then surfaces through the login diagnostics
// rather than as a silent hang. Runs whether the binary was just downloaded or
// left over from a prior (unsigned) run.
func (m *cliproxyManager) hardenBinary() error {
	if err := os.Chmod(m.binPath(), 0o755); err != nil {
		return err
	}
	if runtime.GOOS == "darwin" {
		_ = exec.Command("/usr/bin/xattr", "-dr", "com.apple.quarantine", m.binPath()).Run()
		_ = exec.Command("/usr/bin/codesign", "--force", "--sign", "-", m.binPath()).Run()
	}
	return nil
}

// extractBinary pulls one file (by base name) out of a .tar.gz into dst.
func extractBinary(archive, base, dst string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("%s not found in archive", base)
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != base {
			continue
		}
		out, err := os.Create(dst)
		if err != nil {
			return err
		}
		defer out.Close()
		if _, err := io.Copy(out, tr); err != nil { //nolint:gosec // size bounded by a pinned release
			return err
		}
		return nil
	}
}

// writeConfig writes a localhost-only config with the chosen port, the sidecar's
// own auth dir, and the api key providers use. Written as a small template so we
// carry no YAML dependency.
func (m *cliproxyManager) writeConfig(port int) error {
	if err := os.MkdirAll(m.authDir(), 0o700); err != nil {
		return err
	}
	cfg := fmt.Sprintf(`port: %d
auth-dir: %q
remote-management:
  allow-remote: false
  management-key: %q
api-keys:
  - %q
`, port, m.authDir(), m.apiKey+"-mgmt", m.apiKey)
	return os.WriteFile(m.configPath(), []byte(cfg), 0o600)
}

// freePort grabs an ephemeral localhost port and releases it, so the sidecar
// binds a port kunai knows is free (avoids clashing with a hand-run proxy).
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// start ensures the binary, writes config on a free port, and supervises the
// process until ctx is done (restarting on a crash with backoff). It returns
// once the sidecar answers or the wait times out; supervision continues in the
// background. Safe to call once at boot.
func (m *cliproxyManager) start(ctx context.Context) error {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return nil // already supervising; nothing to do
	}
	m.started = true
	m.mu.Unlock()
	if err := m.ensureBinary(ctx); err != nil {
		m.mu.Lock()
		m.started = false // let a later add retry the download
		m.mu.Unlock()
		return err
	}
	port, err := freePort()
	if err != nil {
		return err
	}
	if err := m.writeConfig(port); err != nil {
		return err
	}
	m.mu.Lock()
	m.port = port
	m.mu.Unlock()
	go m.supervise(ctx)
	return m.waitHealthy(ctx, 20*time.Second)
}

func (m *cliproxyManager) supervise(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		logf, _ := os.OpenFile(m.logPath(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		cmd := exec.CommandContext(ctx, m.binPath(), "--config", m.configPath())
		if logf != nil {
			cmd.Stdout, cmd.Stderr = logf, logf
		}
		if err := cmd.Start(); err != nil {
			if logf != nil {
				logf.Close()
			}
			return // binary vanished or unrunnable; nothing to supervise
		}
		m.mu.Lock()
		m.cmd, m.running = cmd, true
		m.mu.Unlock()
		_ = cmd.Wait()
		if logf != nil {
			logf.Close()
		}
		m.mu.Lock()
		m.running = false
		m.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		// Crashed; back off (capped) and respawn.
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// waitHealthy blocks until the sidecar answers on its port or the deadline hits.
func (m *cliproxyManager) waitHealthy(ctx context.Context, d time.Duration) error {
	deadline := time.Now().Add(d)
	base := m.BaseURL()
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/models", nil)
		req.Header.Set("Authorization", "Bearer "+m.apiKey)
		if resp, err := client.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("CLIProxyAPI did not become healthy in %s", d)
}
