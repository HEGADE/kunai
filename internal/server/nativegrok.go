package server

// nativeGrokManager runs kunai's own in-process Grok proxy (internal/cliproxy/grok)
// on a localhost port, a drop-in for the CLIProxyAPI sidecar for a Grok provider.
// Like the Codex one it exposes BaseURL()/APIKey() so providerProfile can point a
// Grok provider at it. Grok's login is the grok CLI's own (~/.grok/auth.json), so
// there is no separate login flow here yet. Gated by cfg.NativeGrok
// (KUNAI_NATIVE_GROK=1). Proven end to end against real Grok (grok live tests).

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hegade/kunai/internal/cliproxy/grok"
)

type nativeGrokManager struct {
	mu      sync.Mutex
	port    int
	started bool
	srv     *http.Server
	proxy   *grok.Proxy // kept so the dashboard can read the free-tier quota it captured
}

// freeQuotaUsage returns the free-tier token quota the proxy captured from a 429, as
// a Usage window, or nil if none seen (or it is too stale to trust). This is the only
// source of the free tier's 1M/24h limit; paid accounts use the billing endpoint.
func (m *nativeGrokManager) freeQuotaUsage() *Usage {
	m.mu.Lock()
	p := m.proxy
	m.mu.Unlock()
	if p == nil {
		return nil
	}
	used, limit, at, ok := p.FreeQuota()
	if !ok || limit <= 0 {
		return nil
	}
	// A capture older than the rolling window is meaningless; drop it.
	if time.Since(at) > 24*time.Hour {
		return nil
	}
	pct := float64(used) / float64(limit) * 100
	if pct > 100 {
		pct = 100
	}
	// A 24h rolling window is a short window -> the session row. No precise reset time
	// is available from the 429, so ResetsAt stays 0.
	return &Usage{Session: &UsageWindow{Percent: pct}, FetchedAt: at.Unix()}
}

func newNativeGrokManager() *nativeGrokManager { return &nativeGrokManager{} }

// grokTokenPath returns the grok CLI's login file, or ok=false if it is not there.
// A var so a test can stub the credential check deterministically.
var grokTokenPath = func() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	p := filepath.Join(home, ".grok", "auth.json")
	if _, err := os.Stat(p); err != nil {
		return "", false
	}
	return p, true
}

func (m *nativeGrokManager) BaseURL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.port == 0 {
		return ""
	}
	return "http://127.0.0.1:" + strconv.Itoa(m.port)
}

func (m *nativeGrokManager) APIKey() string { return "kunai-native" }

func (m *nativeGrokManager) start(ctx context.Context) error {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return nil
	}
	tokenPath, ok := grokTokenPath()
	if !ok {
		m.mu.Unlock()
		return errNoGrokToken
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		m.mu.Unlock()
		return err
	}
	proxy := grok.NewProxy(tokenPath)
	srv := &http.Server{Handler: proxy.Handler()}
	m.port = ln.Addr().(*net.TCPAddr).Port
	m.srv = srv
	m.proxy = proxy
	m.started = true
	m.mu.Unlock()

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("native grok proxy: %v", err)
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

// isGrokModel reports whether a provider's model is a Grok one.
func isGrokModel(model string) bool {
	return strings.HasPrefix(strings.ToLower(model), "grok")
}

// providerUsesNativeGrok reports whether the named provider is a Grok provider the
// native proxy can actually serve: native grok enabled, a Grok model, AND a grok CLI
// login present. The credential check is load-bearing -- without it, a machine with
// -native-grok but no grok CLI would skip the sidecar and then have nothing to serve
// the session, producing empty replies. When there is no ~/.grok login this returns
// false, so the create path readies the sidecar (where the in-app Grok login writes)
// and the session works.
func (s *Server) providerUsesNativeGrok(name string) bool {
	if s.nativeGrok == nil {
		return false
	}
	p := s.providerNamed(name)
	if p == nil || p.BaseURL != "" || !isGrokModel(providerDisplayModel(*p)) {
		return false
	}
	_, ok := grokTokenPath()
	return ok
}

var errNoGrokToken = &grokTokenErr{}

type grokTokenErr struct{}

func (*grokTokenErr) Error() string {
	return "native grok: no grok CLI login found (~/.grok/auth.json); run `grok` to log in"
}
