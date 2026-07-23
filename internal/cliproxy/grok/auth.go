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

	mu   sync.Mutex
	tok  authEntry
	exp  time.Time
	load bool
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
		if m.tok.Key != "" {
			return m.tok.Key, nil // no way to refresh; try what we have
		}
		return "", fmt.Errorf("grok token: no key or refresh token in %s", m.path)
	}
	if err := m.refreshLocked(ctx); err != nil {
		if m.tok.Key != "" {
			return m.tok.Key, nil
		}
		return "", err
	}
	return m.tok.Key, nil
}

func (m *tokenManager) readLocked() error {
	b, err := os.ReadFile(m.path)
	if err != nil {
		return fmt.Errorf("grok token: read %s: %w", m.path, err)
	}
	var raw map[string]authEntry
	if err := json.Unmarshal(b, &raw); err != nil {
		return fmt.Errorf("grok token: parse %s: %w", m.path, err)
	}
	// Pick the entry with a key (there is normally exactly one).
	for _, e := range raw {
		if e.Key != "" {
			m.tok = e
			m.exp = parseTime(e.ExpiresAt)
			return nil
		}
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
	return nil
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
