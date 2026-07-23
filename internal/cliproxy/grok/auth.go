// Package grok is kunai's native proxy for a Grok (xAI) provider: it accepts the
// Anthropic Messages API the claude CLI speaks, translates to xAI's /responses
// format (the same OpenAI-Responses shape Codex uses, so the translation is reused
// from internal/cliproxy/codex), and calls xAI's CLI chat-proxy with the grok CLI's
// own session token. Like the Codex proxy, this lets kunai drive Grok without the
// CLIProxyAPI sidecar.
package grok

// Auth: the grok CLI (`grok`, xai-grok-workspace) stores its login in
// ~/.grok/auth.json under a "<issuer>::<id>" key, with the session token in "key"
// and an OIDC refresh_token/expires_at. kunai reads that token at runtime and
// refreshes it against the OIDC issuer when near expiry. Read-only otherwise.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// errGrokReauth is the error returned when the grok login is dead (expired with no
// working refresh). Its message tells the user the one thing that fixes it, so the
// app surfaces "run `grok` to sign in again" instead of a bare upstream 401.
func errGrokReauth(detail string) error {
	return fmt.Errorf("%s -- run `grok` on this machine to sign in again", detail)
}

// refreshReason pulls the human reason out of an OIDC refresh error (e.g. the
// error_description "Refresh token has been revoked") for a clearer message.
func refreshReason(err error) string {
	s := err.Error()
	if i := strings.Index(s, `"error_description":"`); i >= 0 {
		rest := s[i+len(`"error_description":"`):]
		if j := strings.IndexByte(rest, '"'); j >= 0 {
			return rest[:j]
		}
	}
	return "token refresh failed"
}

const (
	oidcTokenPath  = "/oauth2/token" // relative to the issuer
	refreshLeadway = 5 * time.Minute
)

// authEntry is the value under the single "<issuer>::<id>" key in ~/.grok/auth.json.
type authEntry struct {
	Key          string `json:"key"` // the session access token used as Bearer
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    string `json:"expires_at"`
	OIDCIssuer   string `json:"oidc_issuer"`
	OIDCClientID string `json:"oidc_client_id"`
}

// tokenManager loads the grok CLI token and keeps it fresh in memory.
type tokenManager struct {
	path  string
	httpc *http.Client

	mu      sync.Mutex
	tok     authEntry
	exp     time.Time
	load    bool
	fileKey string // the "<issuer>::<id>" map key the token lives under, for write-back
}

func newTokenManager(path string) *tokenManager {
	return &tokenManager{path: path, httpc: &http.Client{Timeout: 30 * time.Second}}
}

// token returns a valid Bearer token, refreshing if near expiry.
func (m *tokenManager) token(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.load {
		if err := m.readLocked(); err != nil {
			return "", err
		}
		m.load = true
	}
	if m.tok.Key != "" && time.Now().Before(m.exp.Add(-refreshLeadway)) {
		return m.tok.Key, nil
	}
	if m.tok.RefreshToken == "" || m.tok.OIDCIssuer == "" {
		if m.tok.Key != "" && time.Now().Before(m.exp) {
			return m.tok.Key, nil // can't refresh, but the key has not expired yet
		}
		return "", errGrokReauth("grok login expired and there is no refresh token")
	}
	if err := m.refreshLocked(ctx); err != nil {
		// A stale key that is still within its lifetime is worth a try; a dead login
		// is not, and the user needs a clear "sign in again" instead of a cryptic
		// upstream 401 (or the sidecar hanging on the retry).
		if m.tok.Key != "" && time.Now().Before(m.exp) {
			return m.tok.Key, nil
		}
		return "", errGrokReauth("grok login could not be refreshed (" + refreshReason(err) + ")")
	}
	return m.tok.Key, nil
}

func (m *tokenManager) readLocked() error {
	b, err := os.ReadFile(m.path)
	if err != nil {
		return fmt.Errorf("grok token: read %s: %w", m.path, err)
	}
	// The grok CLI writes a map of "<issuer>::<id>" -> entry; pick the one with a key.
	var raw map[string]authEntry
	if err := json.Unmarshal(b, &raw); err == nil {
		for k, e := range raw {
			if e.Key != "" {
				m.tok = e
				m.fileKey = k
				m.exp = parseTime(e.ExpiresAt)
				return nil
			}
		}
	}
	// The kunai app's in-app login writes the sidecar's flat CLIProxyAPI shape
	// instead ({access_token, refresh_token, expired, type:"xai"}). Accept it so a
	// provider logged in through the app routes native rather than to the hanging
	// sidecar. There is no OIDC issuer in this shape, so the access token is used as
	// is until it expires (kunai does not refresh it; the sidecar/app owns that).
	var flat struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		Expired      string `json:"expired"`
	}
	if json.Unmarshal(b, &flat) == nil && flat.AccessToken != "" {
		m.tok = authEntry{Key: flat.AccessToken, RefreshToken: flat.RefreshToken}
		m.exp = parseTime(flat.Expired)
		m.fileKey = "" // no map wrapper to write back into
		return nil
	}
	return fmt.Errorf("grok token: no session key in %s", m.path)
}

func (m *tokenManager) refreshLocked(ctx context.Context) error {
	tokenURL := strings.TrimRight(m.tok.OIDCIssuer, "/") + oidcTokenPath
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {m.tok.RefreshToken},
		"client_id":     {m.tok.OIDCClientID},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := m.httpc.Do(req)
	if err != nil {
		return fmt.Errorf("grok token refresh: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("grok token refresh: HTTP %d: %s", resp.StatusCode, string(body))
	}
	var r struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return fmt.Errorf("grok token refresh: parse: %w", err)
	}
	if r.AccessToken != "" {
		m.tok.Key = r.AccessToken
	}
	if r.RefreshToken != "" {
		m.tok.RefreshToken = r.RefreshToken
	}
	if r.ExpiresIn > 0 {
		m.exp = time.Now().Add(time.Duration(r.ExpiresIn) * time.Second)
	} else {
		m.exp = time.Now().Add(time.Hour)
	}
	// xAI rotates refresh tokens (each refresh revokes the old one), so the new
	// token MUST be written back or the next process reads the now-revoked token and
	// every grok session 401s. This is why the login broke: the rotated token was
	// only kept in memory.
	m.persistLocked()
	return nil
}

// persistLocked writes the refreshed key/refresh_token/expires_at back into the
// grok auth file, preserving every other entry and every other field of this entry
// (read-modify-write), so a rotated refresh token survives a restart. Best-effort:
// a write failure is not fatal (the in-memory token still works this run).
func (m *tokenManager) persistLocked() {
	if m.path == "" || m.fileKey == "" {
		return
	}
	b, err := os.ReadFile(m.path)
	if err != nil {
		return
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(b, &raw) != nil {
		return
	}
	entRaw, ok := raw[m.fileKey]
	if !ok {
		return
	}
	var ent map[string]any
	if json.Unmarshal(entRaw, &ent) != nil {
		return
	}
	ent["key"] = m.tok.Key
	ent["refresh_token"] = m.tok.RefreshToken
	ent["expires_at"] = m.exp.UTC().Format(time.RFC3339Nano)
	nb, err := json.Marshal(ent)
	if err != nil {
		return
	}
	raw[m.fileKey] = nb
	if fb, err := json.MarshalIndent(raw, "", "  "); err == nil {
		_ = os.WriteFile(m.path, fb, 0o600)
	}
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
