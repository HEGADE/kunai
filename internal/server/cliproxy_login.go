package server

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// In-app provider login: authorize a Codex/Grok/Kimi/Claude account into the
// managed sidecar without a terminal. CLIProxyAPI's login subcommands print the
// OAuth URL to stdout under --no-browser and (for the OAuth families) run a
// localhost callback server; kunai scrapes the URL, hands it to the app, and
// bridges the callback the same way it does for Claude account logins. The
// sidecar's file watcher picks up the new credential the moment the login
// process writes it, so nothing has to be restarted.

// providerLoginFlag maps a provider kind to CLIProxyAPI's login flag.
var providerLoginFlag = map[string]string{
	"codex":  "-codex-login",
	"xai":    "-xai-login", // Grok
	"kimi":   "-kimi-login",
	"claude": "-claude-login",
}

var cliproxyURLRe = regexp.MustCompile(`https://[^\s"']+`)

type cliproxyLogin struct {
	id       string
	provider string
	url      string             // scraped authorize URL
	base     string             // loopback callback base, "" for a paste flow
	state    string             // OAuth state the authorize URL carried
	stdin    io.WriteCloser     // for paste-style providers
	cancel   context.CancelFunc // kills the login process

	mu      sync.Mutex
	done    bool
	err     error
	waiters []chan struct{}
}

func (f *cliproxyLogin) settle(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.done {
		return
	}
	f.done, f.err = true, err
	for _, w := range f.waiters {
		close(w)
	}
	f.waiters = nil
}

func (f *cliproxyLogin) await(d time.Duration) error {
	f.mu.Lock()
	if f.done {
		err := f.err
		f.mu.Unlock()
		return err
	}
	ch := make(chan struct{})
	f.waiters = append(f.waiters, ch)
	f.mu.Unlock()
	select {
	case <-ch:
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.err
	case <-time.After(d):
		return fmt.Errorf("the login did not complete in time")
	}
}

type cliproxyLoginManager struct {
	m     *cliproxyManager
	mu    sync.Mutex
	flows map[string]*cliproxyLogin
}

func newCLIProxyLoginManager(m *cliproxyManager) *cliproxyLoginManager {
	return &cliproxyLoginManager{m: m, flows: map[string]*cliproxyLogin{}}
}

func randID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// start launches the provider's login, scrapes the OAuth URL, and returns it for
// the app to open. The login process stays alive waiting for its callback until
// finish (or the flow is cancelled / times out).
func (lm *cliproxyLoginManager) start(provider string) (id, authURL string, err error) {
	if lm == nil || lm.m == nil {
		return "", "", fmt.Errorf("this machine has no managed proxy")
	}
	flag, ok := providerLoginFlag[strings.ToLower(strings.TrimSpace(provider))]
	if !ok {
		return "", "", fmt.Errorf("unknown provider %q", provider)
	}
	// The login runs the sidecar binary against the sidecar's config, so it writes
	// into the same auth dir the running proxy watches.
	if err := lm.m.ensureBinary(context.Background()); err != nil {
		return "", "", err
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, lm.m.binPath(), flag, "--no-browser", "--config", lm.m.configPath())
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return "", "", err
	}
	cmd.Stderr = cmd.Stdout // the URL banner can land on either stream
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return "", "", err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return "", "", err
	}
	f := &cliproxyLogin{id: randID(), provider: provider, stdin: stdin, cancel: cancel}

	// Keep a bounded copy of what the login process printed, so a failure to
	// produce a URL can report why (e.g. macOS killing an unsigned binary) rather
	// than a bare timeout.
	var outMu sync.Mutex
	var out strings.Builder
	urlCh := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		sent := false
		for sc.Scan() {
			line := sc.Text()
			outMu.Lock()
			if out.Len() < 4096 {
				out.WriteString(line + "\n")
			}
			outMu.Unlock()
			if !sent {
				if u := cliproxyURLRe.FindString(line); u != "" {
					sent = true
					urlCh <- u
				}
			}
		}
	}()
	died := make(chan struct{})
	go func() { f.settle(cmd.Wait()); close(died) }() // login process exit settles the flow

	tail := func() string {
		outMu.Lock()
		defer outMu.Unlock()
		s := strings.TrimSpace(out.String())
		if s == "" {
			return "it printed nothing (the proxy binary may have failed to start)"
		}
		if len(s) > 400 {
			s = s[len(s)-400:]
		}
		return s
	}

	select {
	case u := <-urlCh:
		f.url = u
		f.base, f.state, _ = loopbackTarget(u)
		lm.mu.Lock()
		lm.flows[f.id] = f
		lm.mu.Unlock()
		return f.id, u, nil
	case <-died:
		return "", "", fmt.Errorf("the %s login exited before a sign-in URL: %s", provider, tail())
	case <-time.After(25 * time.Second):
		cancel()
		return "", "", fmt.Errorf("the %s login produced no sign-in URL in time: %s", provider, tail())
	}
}

// finish delivers the pasted callback (or bare code) to the waiting login: over
// HTTP to the CLI's loopback server for OAuth families, or into its stdin for a
// paste flow. It returns once the login process exits (the sidecar then loads the
// new credential on its own).
func (lm *cliproxyLoginManager) finish(id, pasted string) error {
	lm.mu.Lock()
	f := lm.flows[id]
	delete(lm.flows, id)
	lm.mu.Unlock()
	if f == nil {
		return fmt.Errorf("this login expired; start it again")
	}
	code, state := codeFromPaste(pasted, f.state)
	if f.base != "" {
		if err := forwardLoopback(f.base, code, state); err != nil {
			return fmt.Errorf("could not deliver the code to the local callback: %w", err)
		}
	} else if f.stdin != nil {
		fmt.Fprintln(f.stdin, pasted) // paste-style provider reads the callback from stdin
	}
	return f.await(60 * time.Second)
}

// status reports whether a login already finished on its own (a local browser
// hitting the callback directly, no paste needed), so the client can close the
// dialog hands-free.
func (lm *cliproxyLoginManager) status(id string) (done bool, err error) {
	lm.mu.Lock()
	f := lm.flows[id]
	lm.mu.Unlock()
	if f == nil {
		return true, nil // already consumed by finish, or never existed
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.done, f.err
}

func (lm *cliproxyLoginManager) cancel(id string) {
	lm.mu.Lock()
	f := lm.flows[id]
	delete(lm.flows, id)
	lm.mu.Unlock()
	if f != nil && f.cancel != nil {
		f.cancel()
	}
}

// --- HTTP handlers ------------------------------------------------------------

func (s *Server) handleProviderLoginStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	// A Codex login goes through kunai's own in-process OAuth flow when native Codex
	// is enabled, so no sidecar is needed to add a Codex account.
	if s.nativeLogin != nil && strings.EqualFold(strings.TrimSpace(req.Provider), "codex") {
		id, url, err := s.nativeLogin.start()
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"login_id": "native:" + id, "url": url})
		return
	}
	// The login runs the proxy binary against the sidecar's config, so the sidecar
	// (and thus its config.yaml + auth dir) must exist first. Starting it here also
	// means it is already watching the auth dir when the login writes the new
	// credential, so it is served with no restart. Safe to call repeatedly.
	s.ensureCLIProxy()
	id, url, err := s.cliproxyLogin.start(req.Provider)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"login_id": id, "url": url})
}

func (s *Server) handleProviderLoginFinish(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LoginID string `json:"login_id"`
		Code    string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if id, ok := strings.CutPrefix(req.LoginID, "native:"); ok {
		if s.nativeLogin == nil {
			writeErr(w, http.StatusBadRequest, "native login is not enabled")
			return
		}
		if err := s.nativeLogin.finish(id, req.Code); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	if err := s.cliproxyLogin.finish(req.LoginID, req.Code); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleProviderLoginStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LoginID string `json:"login_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	var done bool
	var err error
	if id, ok := strings.CutPrefix(req.LoginID, "native:"); ok && s.nativeLogin != nil {
		done, err = s.nativeLogin.status(id)
	} else {
		done, err = s.cliproxyLogin.status(req.LoginID)
	}
	out := map[string]any{"done": done}
	if err != nil {
		out["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleProviderLoginCancel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LoginID string `json:"login_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if id, ok := strings.CutPrefix(req.LoginID, "native:"); ok {
		if s.nativeLogin != nil {
			s.nativeLogin.cancel(id)
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.cliproxyLogin.cancel(req.LoginID)
	w.WriteHeader(http.StatusNoContent)
}

// handleProviderModels lists the models the managed sidecar can currently serve
// (one entry per authorized account's models), so the UI can offer real model
// strings after a login instead of making the owner type them.
func (s *Server) handleProviderModels(w http.ResponseWriter, r *http.Request) {
	// Models come from whichever proxy the named provider actually uses: its own
	// base_url when set, else the managed sidecar. Without a provider name, fall
	// back to the managed sidecar (the zero-config default).
	var base, key string
	if p := s.providerNamed(r.URL.Query().Get("cli")); p != nil {
		prof := s.providerProfile(*p)
		base, key = prof.Env["ANTHROPIC_BASE_URL"], prof.Env["ANTHROPIC_AUTH_TOKEN"]
	} else if s.cliproxy != nil {
		s.ensureCLIProxy()
		base, key = s.cliproxy.BaseURL(), s.cliproxy.APIKey()
	}
	if base == "" {
		writeJSON(w, http.StatusOK, []string{})
		return
	}
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, base+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		writeJSON(w, http.StatusOK, []string{})
		return
	}
	defer resp.Body.Close()
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	ids := make([]string, 0, len(body.Data))
	for _, m := range body.Data {
		ids = append(ids, m.ID)
	}
	writeJSON(w, http.StatusOK, ids)
}
