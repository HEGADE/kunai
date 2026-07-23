package codex

// Native Codex OAuth login: authorize a ChatGPT account into kunai without the
// CLIProxyAPI sidecar. Ported from CLIProxyAPI's internal/auth/codex OAuth flow
// (same client_id, PKCE S256, the auth.openai.com endpoints, and the fixed
// http://localhost:1455/auth/callback redirect the client is registered for).
//
// The owner authenticates in THEIR OWN browser; only the resulting code crosses to
// the machine running kunai. Two ways it completes:
//   - Browser on THIS machine: the redirect hits kunai's own localhost:1455
//     callback and the login finishes hands-free.
//   - Browser elsewhere (phone, another laptop): the redirect can't reach this
//     machine, so the owner pastes the code back and kunai exchanges it directly
//     (kunai holds the PKCE verifier, so no forwarding is needed).

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	oauthAuthorizeURL = "https://auth.openai.com/oauth/authorize"
	oauthRedirectURI  = "http://localhost:1455/auth/callback"
	oauthCallbackPort = 1455
)

// LoginFlow is one in-progress Codex OAuth login. Construct with StartLogin.
type LoginFlow struct {
	verifier string
	state    string
	saveDir  string

	ln  net.Listener // localhost:1455, nil if it could not be bound (paste-only then)
	srv *http.Server

	once   sync.Once
	mu     sync.Mutex
	done   bool
	err    error
	saved  string // path of the written token file
	waitCh chan struct{}
}

// StartLogin begins a Codex login: it generates PKCE + state, tries to bind the
// localhost callback for the hands-free case, and returns the authorize URL for the
// owner to open. saveDir is where the token file is written on success.
func StartLogin(saveDir string) (*LoginFlow, string, error) {
	verifier, challenge, err := genPKCE()
	if err != nil {
		return nil, "", err
	}
	state, err := randToken(16)
	if err != nil {
		return nil, "", err
	}
	f := &LoginFlow{verifier: verifier, state: state, saveDir: saveDir, waitCh: make(chan struct{})}

	// Best-effort hands-free callback. If 1455 is taken or unbindable, the paste
	// path still works (Finish exchanges directly), so this is never fatal.
	if ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", oauthCallbackPort)); err == nil {
		f.ln = ln
		mux := http.NewServeMux()
		mux.HandleFunc("/auth/callback", f.handleCallback)
		f.srv = &http.Server{Handler: mux}
		go func() { _ = f.srv.Serve(ln) }()
	}

	params := url.Values{
		"client_id":                  {oauthClientID},
		"response_type":              {"code"},
		"redirect_uri":               {oauthRedirectURI},
		"scope":                      {"openid email profile offline_access"},
		"state":                      {state},
		"code_challenge":             {challenge},
		"code_challenge_method":      {"S256"},
		"prompt":                     {"login"},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
	}
	return f, oauthAuthorizeURL + "?" + params.Encode(), nil
}

// handleCallback receives the OAuth redirect on this machine (local-browser case),
// verifies state, and exchanges the code hands-free.
func (f *LoginFlow) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if code := q.Get("code"); code != "" && q.Get("state") == f.state {
		go func() { _ = f.exchange(context.Background(), code) }()
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<html><body style="font-family:sans-serif;background:#111;color:#eee">`+
			`<h2>Codex account connected.</h2><p>You can close this tab and return to kunai.</p></body></html>`)
		return
	}
	http.Error(w, "invalid callback", http.StatusBadRequest)
}

// Finish exchanges a pasted code (or a pasted callback URL / code=&state= fragment)
// for tokens. Used when the browser was on another machine. Idempotent with the
// hands-free path: whichever arrives first wins.
func (f *LoginFlow) Finish(ctx context.Context, pasted string) error {
	code := codeFromPasted(pasted)
	if code == "" {
		return fmt.Errorf("no authorization code found in the pasted text")
	}
	return f.exchange(ctx, code)
}

// Wait blocks until the login completes (hands-free) or ctx is done.
func (f *LoginFlow) Wait(ctx context.Context) error {
	select {
	case <-f.waitCh:
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Status reports whether the login finished and any error.
func (f *LoginFlow) Status() (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.done, f.err
}

// Cancel stops the callback server and marks the flow abandoned.
func (f *LoginFlow) Cancel() {
	f.shutdown()
	f.settle(fmt.Errorf("login cancelled"), "")
}

func (f *LoginFlow) shutdown() {
	if f.srv != nil {
		_ = f.srv.Close()
	}
}

// exchange trades the code + PKCE verifier for tokens and writes the token file.
// Runs at most once per flow (hands-free callback and a paste can both fire).
func (f *LoginFlow) exchange(ctx context.Context, code string) error {
	var runErr error
	ran := false
	f.once.Do(func() {
		ran = true
		runErr = f.doExchange(ctx, code)
		f.shutdown()
		f.settle(runErr, f.saved)
	})
	if !ran {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.err
	}
	return runErr
}

func (f *LoginFlow) doExchange(ctx context.Context, code string) error {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {oauthClientID},
		"code":          {code},
		"redirect_uri":  {oauthRedirectURI},
		"code_verifier": {f.verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("codex token exchange: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("codex token exchange: HTTP %d: %s", resp.StatusCode, string(body))
	}
	var t struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &t); err != nil {
		return fmt.Errorf("codex token exchange: parse: %w", err)
	}
	if t.AccessToken == "" {
		return fmt.Errorf("codex token exchange: no access_token in response")
	}
	account := accountFromIDToken(t.IDToken)
	tok := TokenFile{
		AccessToken:  t.AccessToken,
		RefreshToken: t.RefreshToken,
		IDToken:      t.IDToken,
		AccountID:    account,
		Type:         "codex",
	}
	if t.ExpiresIn > 0 {
		tok.Expired = time.Now().Add(time.Duration(t.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
	}
	return f.save(tok, account)
}

// save writes the token file into saveDir as codex-<slug>.json, which is what the
// native proxy and the codex usage reader glob for.
func (f *LoginFlow) save(tok TokenFile, account string) error {
	if err := os.MkdirAll(f.saveDir, 0o700); err != nil {
		return err
	}
	slug := account
	if slug == "" {
		slug, _ = randToken(6)
	}
	slug = sanitizeSlug(slug)
	path := filepath.Join(f.saveDir, "codex-"+slug+".json")
	b, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return err
	}
	f.mu.Lock()
	f.saved = path
	f.mu.Unlock()
	return nil
}

func (f *LoginFlow) settle(err error, _ string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.done {
		return
	}
	f.done, f.err = true, err
	close(f.waitCh)
}

// --- helpers -----------------------------------------------------------------

func genPKCE() (verifier, challenge string, err error) {
	b := make([]byte, 64)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func randToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// codeFromPasted accepts a bare code, a "code=...&state=..." fragment, or a full
// callback URL (what a remote browser lands on), and returns the code.
func codeFromPasted(pasted string) string {
	s := strings.TrimSpace(pasted)
	if s == "" {
		return ""
	}
	// A full or partial URL / query string.
	if strings.Contains(s, "code=") {
		if i := strings.Index(s, "?"); i >= 0 {
			s = s[i+1:]
		}
		if q, err := url.ParseQuery(s); err == nil {
			if c := q.Get("code"); c != "" {
				return c
			}
		}
	}
	// A bare code (may still carry a trailing #... fragment).
	if i := strings.IndexAny(s, "#&? "); i >= 0 {
		s = s[:i]
	}
	return s
}

func sanitizeSlug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == '@' || r == '.':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "account"
	}
	if len(out) > 60 {
		out = out[:60]
	}
	return out
}
