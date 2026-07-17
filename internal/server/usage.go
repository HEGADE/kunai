package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Subscription usage: the same two numbers `claude`'s /usage prints, for the
// default account, on the dashboard. A `rate_limit_info` frame only ever tells
// us a window's reset time and whether the last turn was rejected, so the "how
// full is it" half has to come from here.
//
// Both endpoints below are the CLI's own and are UNDOCUMENTED, exactly like the
// stream-json control protocol in internal/claude. They are quarantined in this
// one file for the same reason protocol.go is: when the CLI changes, this is the
// only place to fix. Nothing else in the server may reach for them.
const (
	oauthClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	oauthBeta     = "oauth-2025-04-20"

	// The endpoint is the account's, not ours: poll it about as often as a
	// window can meaningfully move, never once per dashboard paint.
	usageTTL = 60 * time.Second
	// Refresh a little before the wall so a fetch never races the expiry.
	tokenSkew = 2 * time.Minute
	usageHTTP = 10 * time.Second
)

// Injectable so a test can point them at an httptest server, the same reason
// guardian.go routes privileged commands through execRun. Without this a test
// of the refresh would talk to the real endpoint with a junk token.
var (
	usageURL = "https://api.anthropic.com/api/oauth/usage"
	tokenURL = "https://platform.claude.com/v1/oauth/token"
)

// UsageWindow is one quota window's fill. Mirrors web/src/lib/types.ts.
type UsageWindow struct {
	Percent  float64 `json:"percent"`             // 0-100
	ResetsAt int64   `json:"resets_at,omitempty"` // unix seconds; 0 = unknown
}

// Usage is the account's quota picture. A nil window means the account has no
// such limit (or the API stopped reporting it), which the UI shows as absent
// rather than as zero: an empty meter and an unknown one are not the same claim.
type Usage struct {
	Session   *UsageWindow `json:"session,omitempty"` // rolling 5-hour
	Weekly    *UsageWindow `json:"weekly,omitempty"`  // rolling 7-day
	Plan      string       `json:"plan,omitempty"`    // e.g. "max"
	FetchedAt int64        `json:"fetched_at"`        // unix seconds
}

// usageResponse is the wire shape of GET /api/oauth/usage (undocumented). It
// carries far more (per-model windows, overage, spend); we decode only the two
// windows the dashboard shows, so new fields never break the parse.
type usageResponse struct {
	FiveHour *usageEntry `json:"five_hour"`
	SevenDay *usageEntry `json:"seven_day"`
}

type usageEntry struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"` // RFC3339
}

func (e *usageEntry) window() *UsageWindow {
	if e == nil {
		return nil
	}
	w := &UsageWindow{Percent: e.Utilization}
	if t, err := time.Parse(time.RFC3339, e.ResetsAt); err == nil {
		w.ResetsAt = t.Unix()
	}
	return w
}

// oauthCreds is the subset of <configDir>/.credentials.json we read. The file
// holds more (scopes, tier), so it is only ever rewritten field-by-field into
// the decoded original: see writeAccessToken.
type oauthCreds struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresAt    int64  `json:"expiresAt"` // unix MILLIseconds
	Subscription string `json:"subscriptionType"`
}

func (c oauthCreds) expired() bool {
	if c.ExpiresAt == 0 {
		return false // no expiry recorded: let the API be the judge
	}
	return time.Now().Add(tokenSkew).After(time.UnixMilli(c.ExpiresAt))
}

// credsPath mirrors claudeRoot's default: "" is the default account (~/.claude).
//
// Linux keeps the tokens in this file. macOS keeps them in the Keychain instead
// (the CLI shells `security find-generic-password`), so there is no file to read
// and usage reports unavailable there: the tile is simply absent, nothing breaks.
// Wiring the Keychain up needs a real Mac to verify, and it needs an answer for
// writing a refreshed token back first: a read-only store that we refresh on
// every poll would churn the account's refresh token, which is worse than no
// tile. Same "prove it on the hardware" rule as the thermal Phase 2 work.
func credsPath(configDir string) string {
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		configDir = filepath.Join(home, ".claude")
	}
	return filepath.Join(configDir, ".credentials.json")
}

// readCreds returns the account's tokens plus the whole decoded file, so a write
// can put the new token back without dropping any field we do not model.
func readCreds(configDir string) (oauthCreds, map[string]any, error) {
	var c oauthCreds
	p := credsPath(configDir)
	if p == "" {
		return c, nil, errors.New("no config dir")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return c, nil, err
	}
	var whole map[string]any
	if err := json.Unmarshal(b, &whole); err != nil {
		return c, nil, err
	}
	sub, ok := whole["claudeAiOauth"]
	if !ok {
		return c, nil, errors.New("not logged in (no claudeAiOauth)")
	}
	raw, err := json.Marshal(sub)
	if err != nil {
		return c, nil, err
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, nil, err
	}
	return c, whole, nil
}

// writeAccessToken puts a refreshed token back, preserving every other field in
// the file. It rewrites the decoded original rather than a struct of our own, so
// a key we never modelled (scopes, tier, a future one) survives the round trip.
// The write is atomic (temp + rename, 0600) because this file is the account's
// login: a half-written one logs you out.
func writeAccessToken(configDir string, whole map[string]any, c oauthCreds) error {
	sub, _ := whole["claudeAiOauth"].(map[string]any)
	if sub == nil {
		return errors.New("malformed credentials")
	}
	sub["accessToken"] = c.AccessToken
	sub["expiresAt"] = c.ExpiresAt
	if c.RefreshToken != "" {
		sub["refreshToken"] = c.RefreshToken
	}
	whole["claudeAiOauth"] = sub

	b, err := json.MarshalIndent(whole, "", "  ")
	if err != nil {
		return err
	}
	p := credsPath(configDir)
	tmp, err := os.CreateTemp(filepath.Dir(p), ".credentials-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// tokenResponse is the wire shape of POST /v1/oauth/token (undocumented).
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"` // seconds
}

// refreshToken swaps a refresh token for a fresh access token, the way the CLI
// does. Returns the new credentials; the caller persists them.
func refreshToken(ctx context.Context, cl *http.Client, refresh string) (oauthCreds, error) {
	var out oauthCreds
	body, err := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refresh,
		"client_id":     oauthClientID,
	})
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := cl.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return out, fmt.Errorf("token refresh: HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(b))
	}
	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return out, err
	}
	if tr.AccessToken == "" {
		return out, errors.New("token refresh: no access_token")
	}
	out.AccessToken = tr.AccessToken
	out.RefreshToken = tr.RefreshToken // "" keeps the existing one (see writeAccessToken)
	out.ExpiresAt = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second).UnixMilli()
	return out, nil
}

// usageCache serves the dashboard without hammering the account's endpoint, and
// serialises the refresh so this process can never race itself.
type usageCache struct {
	mu   sync.Mutex
	at   time.Time
	val  *Usage
	err  error
	http *http.Client
}

func newUsageCache() *usageCache {
	return &usageCache{http: &http.Client{Timeout: usageHTTP}}
}

// token returns a usable access token for the account, refreshing only when the
// CLI's has actually expired.
//
// Reading the file every time (never caching the token in memory) is the
// load-bearing part. kunai is not this file's only writer: every `claude` it
// spawns refreshes the same token. Taking whatever is on disk means a CLI that
// just refreshed hands us its token for free, and we only ever refresh when
// nobody else has — which is what keeps the two from rotating the token out from
// under each other. Callers hold u.mu, so this process never races itself.
// Failing to refresh is never fatal: the usage tile goes quiet, the account
// keeps working.
func (u *usageCache) token(ctx context.Context, configDir string) (oauthCreds, error) {
	c, whole, err := readCreds(configDir)
	if err != nil {
		return c, err
	}
	if !c.expired() {
		return c, nil
	}
	if c.RefreshToken == "" {
		return c, errors.New("token expired and no refresh token")
	}
	fresh, err := refreshToken(ctx, u.http, c.RefreshToken)
	if err != nil {
		return c, err
	}
	c.AccessToken, c.ExpiresAt = fresh.AccessToken, fresh.ExpiresAt
	if fresh.RefreshToken != "" {
		c.RefreshToken = fresh.RefreshToken
	}
	if err := writeAccessToken(configDir, whole, c); err != nil {
		// The token is good even if we could not save it; report and use it.
		return c, nil
	}
	return c, nil
}

// fetch gets the account's usage, refreshing the token first if needed.
func (u *usageCache) fetch(ctx context.Context, configDir string) (*Usage, error) {
	c, err := u.token(ctx, configDir)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, usageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-beta", oauthBeta)
	resp, err := u.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("usage: HTTP %d", resp.StatusCode)
	}
	var ur usageResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&ur); err != nil {
		return nil, err
	}
	return &Usage{
		Session:   ur.FiveHour.window(),
		Weekly:    ur.SevenDay.window(),
		Plan:      c.Subscription,
		FetchedAt: time.Now().Unix(),
	}, nil
}

// get returns the cached usage, refetching at most once per usageTTL. A failure
// is cached too, so a logged-out or offline machine does not retry every paint.
func (u *usageCache) get(ctx context.Context, configDir string) (*Usage, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if time.Since(u.at) < usageTTL {
		return u.val, u.err
	}
	u.val, u.err = u.fetch(ctx, configDir)
	u.at = time.Now()
	return u.val, u.err
}

// handleUsage serves the default account's quota windows. Usage is per-account
// and the dashboard shows one machine's default account, so this deliberately
// does not take an account parameter.
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	usage, err := s.usage.get(r.Context(), s.resolveCLI("").configDir())
	if err != nil {
		// Not an error the client can act on: the account may simply be logged
		// out, or offline. Say "no usage", not "500".
		writeJSON(w, http.StatusOK, map[string]any{"unavailable": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, usage)
}
