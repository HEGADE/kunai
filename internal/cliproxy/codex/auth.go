package codex

// OAuth token handling for a Codex (ChatGPT) subscription account. kunai reads the
// token the managed sidecar's login wrote (or the codex CLI's ~/.codex/auth.json)
// and refreshes it against the OpenAI OAuth endpoint when it is near expiry. This
// mirrors what CLIProxyAPI's internal/auth/codex does; the constants and grant
// shape are taken from there (client_id app_EMoamEEZ..., refresh_token grant,
// scope "openid profile email").
//
// Read-only against the account otherwise: kunai never mints a login here, only
// refreshes an existing one so an in-flight request does not 401 mid-turn.

import (
	"context"
	"encoding/base64"
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
	oauthClientID  = "app_EMoamEEZ73f0CkXaXp7hrann"
	refreshLeadway = 5 * time.Minute // refresh this long before the token actually expires
)

// oauthTokenURL is the OAuth token endpoint (refresh and code exchange). A var so a
// test can point it at a mock server.
var oauthTokenURL = "https://auth.openai.com/oauth/token"

// TokenFile is the on-disk shape the sidecar login writes (flat fields). The codex
// CLI instead nests these under a "tokens" object; readFileLocked accepts both.
// Only the fields kunai needs are decoded.
type TokenFile struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	AccountID    string `json:"account_id"`
	Expired      string `json:"expired"` // RFC3339; sidecar's spelling
	Type         string `json:"type"`
}

// tokenManager loads a Codex token from a file and keeps it fresh in memory,
// refreshing under a lock so concurrent requests share one refresh. The file is
// the source of truth on load; a refresh updates the in-memory copy (writing it
// back is the sidecar's job when it owns the file, so kunai only persists when it
// owns the path).
type tokenManager struct {
	path  string
	owns  bool // whether kunai may write the refreshed token back to path
	httpc *http.Client

	mu   sync.Mutex
	tok  TokenFile
	exp  time.Time
	load bool
}

func newTokenManager(path string, owns bool) *tokenManager {
	return &tokenManager{path: path, owns: owns, httpc: &http.Client{Timeout: 30 * time.Second}}
}

// creds returns a valid access token and the account id, refreshing if the current
// token is missing or within refreshLeadway of expiry.
func (m *tokenManager) creds(ctx context.Context) (access, account string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.load {
		if err = m.readFileLocked(); err != nil {
			return "", "", err
		}
		m.load = true
	}
	if m.tok.AccessToken != "" && time.Now().Before(m.exp.Add(-refreshLeadway)) {
		return m.tok.AccessToken, m.tok.AccountID, nil
	}
	if m.tok.RefreshToken == "" {
		// No way to refresh; hand back whatever we have (may 401, surfaced upstream).
		if m.tok.AccessToken == "" {
			return "", "", fmt.Errorf("codex token: no access or refresh token in %s", m.path)
		}
		return m.tok.AccessToken, m.tok.AccountID, nil
	}
	if err = m.refreshLocked(ctx); err != nil {
		// A failed refresh with a still-present access token is worth trying; the
		// upstream 401 is a clearer signal than blocking here.
		if m.tok.AccessToken != "" {
			return m.tok.AccessToken, m.tok.AccountID, nil
		}
		return "", "", err
	}
	return m.tok.AccessToken, m.tok.AccountID, nil
}

func (m *tokenManager) readFileLocked() error {
	b, err := os.ReadFile(m.path)
	if err != nil {
		return fmt.Errorf("codex token: read %s: %w", m.path, err)
	}
	var t TokenFile
	if err := json.Unmarshal(b, &t); err != nil {
		return fmt.Errorf("codex token: parse %s: %w", m.path, err)
	}
	// The codex CLI writes the token nested under "tokens" (with a sibling
	// "last_refresh"), while the sidecar login writes the fields flat. Accept both
	// so pointing a Codex provider at a real ~/.codex/auth.json works instead of
	// failing with "no access or refresh token".
	if t.AccessToken == "" {
		var nested struct {
			Tokens TokenFile `json:"tokens"`
		}
		if json.Unmarshal(b, &nested) == nil && nested.Tokens.AccessToken != "" {
			expired := t.Expired // preserve a flat expiry if one was also present
			t = nested.Tokens
			if expired != "" {
				t.Expired = expired
			}
		}
	}
	m.tok = t
	m.exp = parseExpiry(t.Expired)
	// The nested format carries no "expired" field, so fall back to the access
	// token's own JWT exp claim -- the real expiry, and better than a fixed TTL.
	if m.exp.IsZero() {
		if e := expiryFromJWT(t.AccessToken); !e.IsZero() {
			m.exp = e
		}
	}
	if m.tok.AccountID == "" {
		m.tok.AccountID = accountFromIDToken(t.IDToken)
	}
	return nil
}

// expiryFromJWT reads the exp claim from a JWT access token, so a token file that
// records no explicit expiry still refreshes at the right time. Best-effort: a
// parse failure returns the zero time (treated as expired, forcing a refresh).
func expiryFromJWT(accessToken string) time.Time {
	parts := strings.Split(accessToken, ".")
	if len(parts) < 2 {
		return time.Time{}
	}
	claims, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return time.Time{}
	}
	var c struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(claims, &c) != nil || c.Exp == 0 {
		return time.Time{}
	}
	return time.Unix(c.Exp, 0)
}

// refreshLocked exchanges the refresh token for a new access token. Caller holds mu.
func (m *tokenManager) refreshLocked(ctx context.Context) error {
	form := url.Values{
		"client_id":     {oauthClientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {m.tok.RefreshToken},
		"scope":         {"openid profile email"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := m.httpc.Do(req)
	if err != nil {
		return fmt.Errorf("codex token refresh: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("codex token refresh: HTTP %d: %s", resp.StatusCode, string(body))
	}
	var r struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return fmt.Errorf("codex token refresh: parse: %w", err)
	}
	m.tok.AccessToken = r.AccessToken
	if r.RefreshToken != "" {
		m.tok.RefreshToken = r.RefreshToken
	}
	if r.IDToken != "" {
		m.tok.IDToken = r.IDToken
		if acct := accountFromIDToken(r.IDToken); acct != "" {
			m.tok.AccountID = acct
		}
	}
	if r.ExpiresIn > 0 {
		m.exp = time.Now().Add(time.Duration(r.ExpiresIn) * time.Second)
	} else {
		m.exp = time.Now().Add(time.Hour)
	}
	if m.owns {
		m.tok.Expired = m.exp.UTC().Format(time.RFC3339)
		if b, err := json.Marshal(m.tok); err == nil {
			_ = os.WriteFile(m.path, b, 0o600)
		}
	}
	return nil
}

// accountFromIDToken extracts the ChatGPT account id from an OAuth id_token's
// claims (the "https://api.openai.com/auth".chatgpt_account_id field), so a token
// file that omits a top-level account_id still yields one. Best-effort: a parse
// failure returns "" and the caller falls back to whatever the file carried.
func accountFromIDToken(idToken string) string {
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return ""
	}
	claims, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var c struct {
		Auth struct {
			ChatGPTAccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if json.Unmarshal(claims, &c) != nil {
		return ""
	}
	return c.Auth.ChatGPTAccountID
}

func parseExpiry(s string) time.Time {
	if s == "" {
		return time.Time{} // zero -> treated as expired, forces a refresh
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
