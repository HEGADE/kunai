package server

// Grok (xAI) subscription quota on the dashboard, the same two numbers Claude and
// Codex show. Two sources, because xAI splits it:
//   - Paid / SuperGrok: monthly CREDIT billing (cents) at
//     cli-chat-proxy.grok.com/v1/billing, read with the grok CLI token. Real usage.
//   - Free tier: a 1M-token / 24h rolling limit that NO proactive endpoint exposes;
//     it appears only in a 429 body, which the grok proxy captures
//     (grok.Proxy.FreeQuota) and the usage handler surfaces here.
// This is read-only against the account, only to show a number (like codexusage.go).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hegade/kunai/internal/cliproxy/grok"
)

// grokBillingURL is xAI's CLI chat-proxy billing endpoint. A var so a test can point
// it at a local server.
var grokBillingURL = "https://cli-chat-proxy.grok.com/v1/billing"

// grokBilling is the subset of /v1/billing we use: a monthly credit limit and used
// amount (in cents), plus the billing period end for the reset time.
type grokBilling struct {
	Config struct {
		MonthlyLimit     grokCent `json:"monthlyLimit"`
		Used             grokCent `json:"used"`
		BillingPeriodEnd string   `json:"billingPeriodEnd"`
	} `json:"config"`
}
type grokCent struct {
	Val int64 `json:"val"`
}

// grokToken returns a grok session token that works NOW, refreshing an expired
// one first.
//
// It used to read ~/.grok/auth.json and send whatever key was in it, which meant
// that once the token lapsed this posted a dead one every minute forever. That
// is worse here than for Codex: xAI rotates refresh tokens and revokes the old
// one on each refresh, so the token in that file goes stale on its own and only
// a refresh that PERSISTS the rotated one keeps it alive. The proxy already does
// exactly that (internal/cliproxy/grok), so this now shares it rather than
// keeping a second, read-only view of the same file.
func grokToken(ctx context.Context) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(home, ".grok", "auth.json")
	if _, err := os.Stat(path); err != nil {
		return "", errNoGrokLogin
	}
	return grok.Token(ctx, path)
}

var errNoGrokLogin = errors.New("no Grok login found (~/.grok/auth.json); run `grok` to sign in")

// grokReason turns a failure into the sentence a person can act on. The token
// manager already produces an actionable message for a login that cannot be
// refreshed; this only has to cover a bare status code from the billing call.
func grokReason(err error) string {
	if err == nil {
		return ""
	}
	if msg := err.Error(); strings.Contains(msg, "HTTP 401") || strings.Contains(msg, "HTTP 403") {
		return "the Grok login has expired; run `grok` to sign in again"
	}
	return err.Error()
}

// fetchGrokBilling reads the credit billing and maps it to a Usage window, or nil
// when there is no monthly credit limit (the free tier, whose real limit is the
// token one the proxy captures instead).
func fetchGrokBilling(ctx context.Context, token string) (*Usage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, grokBillingURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-XAI-Token-Auth", "xai-grok-cli")
	req.Header.Set("x-grok-client-version", grokClientVersionForUsage)
	req.Header.Set("User-Agent", "xai-grok-workspace/"+grokClientVersionForUsage)
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// No "grok billing:" prefix here. The caller names what failed when it
		// logs, and carrying it in the error too produced the doubled
		// "grok billing: grok billing: HTTP 401" that ran down the journal.
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, fmt.Errorf("HTTP %d, the grok login is no longer valid (run `grok` to sign in again)", resp.StatusCode)
		}
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var b grokBilling
	if err := json.NewDecoder(resp.Body).Decode(&b); err != nil {
		return nil, err
	}
	limit := b.Config.MonthlyLimit.Val
	if limit <= 0 {
		return nil, nil // free tier: no credit billing to show
	}
	pct := float64(b.Config.Used.Val) / float64(limit) * 100
	if pct > 100 {
		pct = 100
	}
	var reset int64
	if t, err := time.Parse(time.RFC3339, b.Config.BillingPeriodEnd); err == nil {
		reset = t.Unix()
	}
	// A monthly credit period is a long window, so it lands in the weekly row (as a
	// Codex Go plan's ~30-day window does).
	return &Usage{Weekly: &UsageWindow{Percent: pct, ResetsAt: reset}, FetchedAt: time.Now().Unix()}, nil
}

const grokClientVersionForUsage = "0.2.111"

// grokUsageCache serves the Grok billing quota with a short TTL, and remembers a
// failure for the same period so a dead login is asked about once a minute rather
// than once per caller. See usagefail.go.
type grokUsageCache struct {
	mu   sync.Mutex
	u    *Usage
	exp  time.Time
	fail usageFailure
	// free records that the last poll SUCCEEDED and found no credit limit, which
	// is the free tier rather than a fault. The two produce the same empty result
	// and must not produce the same sentence: one is "sign in again", the other is
	// "there is nothing to show until a request is refused".
	free bool
}

func (c *grokUsageCache) get(ctx context.Context) *Usage {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if c.u != nil && now.Before(c.exp) {
		return c.u
	}
	if c.fail.holding(now) {
		return nil // already asked recently and was refused
	}
	token, err := grokToken(ctx)
	if err != nil {
		c.fail.report(now, providerUsageFailTTL, "grok quota", grokReason(err))
		return nil
	}
	u, err := fetchGrokBilling(ctx, token)
	if err != nil {
		c.fail.report(now, providerUsageFailTTL, "grok quota", grokReason(err))
		return nil
	}
	c.fail.clear()
	if u == nil {
		c.free = true
		return nil // free tier; caller falls back to the captured token quota
	}
	c.free = false
	c.u, c.exp = u, now.Add(usageTTL)
	return u
}

// reason is why there are no numbers, for the client to show. Empty when there
// is nothing to explain.
//
// A failure comes first, then the free tier -- which is not a failure and must
// not be dressed as one. xAI publishes no proactive endpoint for the free
// allowance; it appears only in the body of a 429, which the proxy captures when
// one arrives.
func (c *grokUsageCache) reason() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r := c.fail.reason(); r != "" {
		return r
	}
	if c.free {
		return "the Grok free tier publishes no quota; it appears only once a request is refused"
	}
	return ""
}
