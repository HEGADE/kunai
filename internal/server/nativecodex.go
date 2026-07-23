package server

// nativeCodexManager runs kunai's own in-process Codex proxy (internal/cliproxy/codex)
// on a localhost port, as a drop-in for the CLIProxyAPI sidecar for a Codex provider.
// It exposes the same BaseURL()/APIKey() shape providerProfile already consumes, so
// wiring it in is a one-line swap. Gated by cfg.NativeCodex (KUNAI_NATIVE_CODEX=1):
// off by default until it fully replaces the sidecar (login still runs through the
// sidecar). Proven end to end against real Codex (internal/cliproxy/codex live tests).

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/hegade/kunai/internal/cliproxy/codex"
)

type nativeCodexManager struct {
	dataDir string

	mu      sync.Mutex
	port    int
	started bool
	srv     *http.Server
}

func newNativeCodexManager(dataDir string) *nativeCodexManager {
	return &nativeCodexManager{dataDir: dataDir}
}

// providerUsesNative reports whether the named provider is a Codex provider that
// the native proxy handles, so the create/switch paths can skip the sidecar.
func (s *Server) providerUsesNative(name string) bool {
	if s.nativeCodex == nil {
		return false
	}
	p := s.providerNamed(name)
	return p != nil && p.BaseURL == "" && isCodexModel(providerDisplayModel(*p))
}

// anyProviderNeedsSidecar reports whether at least one configured provider relies
// on the managed CLIProxyAPI sidecar (i.e. is not handled by the native proxy and
// has no external base URL of its own). When false, the 40MB sidecar is never
// downloaded — the whole point of the native path.
func (s *Server) anyProviderNeedsSidecar() bool {
	for _, p := range s.providerList() {
		if p.BaseURL != "" {
			continue // points at its own proxy, not ours
		}
		if s.nativeCodex != nil && isCodexModel(providerDisplayModel(p)) {
			continue // native handles it
		}
		return true
	}
	return false
}

// codexTokenPath finds the Codex OAuth token: the sidecar's auth dir (where the
// in-app login writes it) first, then ~/.codex/auth.json.
func (m *nativeCodexManager) codexTokenPath() (path string, owns bool, ok bool) {
	if m.dataDir != "" {
		if matches, _ := filepath.Glob(filepath.Join(m.dataDir, "cliproxy", "auth", "codex-*.json")); len(matches) > 0 {
			return matches[0], true, true
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".codex", "auth.json")
		if _, err := os.Stat(p); err == nil {
			return p, false, true
		}
	}
	return "", false, false
}

// BaseURL is the origin a Codex provider points ANTHROPIC_BASE_URL at, "" until started.
func (m *nativeCodexManager) BaseURL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.port == 0 {
		return ""
	}
	return "http://127.0.0.1:" + strconv.Itoa(m.port)
}

// APIKey is a placeholder token; the native proxy authenticates to Codex itself and
// ignores the client token, but claude requires ANTHROPIC_AUTH_TOKEN to be non-empty.
func (m *nativeCodexManager) APIKey() string { return "kunai-native" }

// start binds a localhost port and serves the native Codex proxy until ctx is done.
// Idempotent. Returns an error if no Codex token is available yet.
func (m *nativeCodexManager) start(ctx context.Context) error {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return nil
	}
	tokenPath, owns, ok := m.codexTokenPath()
	if !ok {
		m.mu.Unlock()
		return errNoCodexToken
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		m.mu.Unlock()
		return err
	}
	proxy := codex.NewProxy(tokenPath, owns)
	srv := &http.Server{Handler: proxy.Handler()}
	m.port = ln.Addr().(*net.TCPAddr).Port
	m.srv = srv
	m.started = true
	m.mu.Unlock()

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("native codex proxy: %v", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	return nil
}

// --- native login -------------------------------------------------------------

// nativeCodexLoginManager runs Codex OAuth logins in-process (internal/cliproxy/codex),
// writing the token into the same auth dir the native proxy reads, so no sidecar is
// needed to add a Codex account. Mirrors cliproxyLoginManager's start/finish/status/
// cancel shape so the HTTP handlers can branch to it.
type nativeCodexLoginManager struct {
	authDir string
	mu      sync.Mutex
	flows   map[string]*codex.LoginFlow
}

func newNativeCodexLoginManager(dataDir string) *nativeCodexLoginManager {
	return &nativeCodexLoginManager{
		authDir: filepath.Join(dataDir, "cliproxy", "auth"),
		flows:   map[string]*codex.LoginFlow{},
	}
}

func (lm *nativeCodexLoginManager) start() (id, authURL string, err error) {
	// Only one Codex login runs at a time (you are adding one account), and an
	// abandoned flow holds the localhost:1455 callback, so reclaim it: cancel any
	// prior flow before starting a new one, freeing the port to rebind.
	lm.mu.Lock()
	prior := lm.flows
	lm.flows = map[string]*codex.LoginFlow{}
	lm.mu.Unlock()
	for _, f := range prior {
		f.Cancel()
	}
	f, url, err := codex.StartLogin(lm.authDir)
	if err != nil {
		return "", "", err
	}
	id = randID()
	lm.mu.Lock()
	lm.flows[id] = f
	lm.mu.Unlock()
	return id, url, nil
}

func (lm *nativeCodexLoginManager) finish(id, code string) error {
	lm.mu.Lock()
	f := lm.flows[id]
	lm.mu.Unlock()
	if f == nil {
		return fmt.Errorf("this login expired; start it again")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	err := f.Finish(ctx, code)
	if err == nil {
		lm.forget(id)
	}
	return err
}

func (lm *nativeCodexLoginManager) status(id string) (bool, error) {
	lm.mu.Lock()
	f := lm.flows[id]
	lm.mu.Unlock()
	if f == nil {
		return true, nil
	}
	done, err := f.Status()
	if done {
		lm.forget(id)
	}
	return done, err
}

func (lm *nativeCodexLoginManager) cancel(id string) {
	lm.mu.Lock()
	f := lm.flows[id]
	delete(lm.flows, id)
	lm.mu.Unlock()
	if f != nil {
		f.Cancel()
	}
}

func (lm *nativeCodexLoginManager) forget(id string) {
	lm.mu.Lock()
	delete(lm.flows, id)
	lm.mu.Unlock()
}

var errNoCodexToken = &codexTokenErr{}

type codexTokenErr struct{}

func (*codexTokenErr) Error() string {
	return "native codex: no Codex token found (log in a Codex provider first)"
}
