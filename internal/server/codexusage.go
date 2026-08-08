package server

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

	"github.com/hegade/kunai/internal/cliproxy/codex"
)

// Codex (ChatGPT) subscription quota, so a Codex provider's dashboard shows the
// same two numbers Claude does. The proxy exposes no rate-limit info and there is
// no `codex /usage` to shell, so the only source is ChatGPT's own usage endpoint
// (the one the Codex apps and CodexBar read): the "wham/usage" backend endpoint
// with the account's OAuth token. That means kunai has to READ the token here,
// which it otherwise avoids -- but read-only, only to show a number, and the
// token it prefers is the managed sidecar's own (kunai wrote it), refreshed by
// the sidecar, so kunai never rotates or drops it.

// codexUsageURL is the ChatGPT backend usage endpoint (the "wham" one the Codex
// apps and CodexBar read for an OAuth account). A var so a test can point it at a
// local server.
var codexUsageURL = "https://chatgpt.com/backend-api/wham/usage"

// codexAuthFile is the shape of both the managed sidecar's codex-*.json and the
// codex CLI's ~/.codex/auth.json: the token lives under "tokens" (older files put
// it at the top level too), the account id in one of a few spellings.
type codexAuthFile struct {
	AccessToken string `json:"access_token"`
	AccountID   string `json:"account_id"`
	ChatGPTAcct string `json:"chatgpt_account_id"`
	Tokens      struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	} `json:"tokens"`
}

func (a codexAuthFile) creds() (token, account string) {
	token = firstNonEmpty(a.Tokens.AccessToken, a.AccessToken)
	account = firstNonEmpty(a.Tokens.AccountID, a.AccountID, a.ChatGPTAcct)
	return
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

// codexTokenFile finds the Codex login to read: the managed sidecar's auth dir
// first (the account added to kunai, which kunai wrote and may rewrite), then
// ~/.codex/auth.json (the codex CLI's own login, which it may not).
func codexTokenFile(dataDir string) (path string, owns, ok bool) {
	if dataDir != "" {
		if m, _ := filepath.Glob(filepath.Join(dataDir, "cliproxy", "auth", "codex-*.json")); len(m) > 0 {
			return m[0], true, true
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

// codexCreds returns a token that works NOW, refreshing an expired one first.
//
// It used to read the file and hand back whatever was in it, which is fine right
// up until the access token lapses -- and then this posts a dead token every
// minute forever while the refresh token needed to fix it sits in the same file
// it just read. That is exactly what happened here: the access token expired on
// a Saturday, the file had not been rewritten in two weeks, and the dashboard
// said "Codex: no quota" from then on. The proxy already knew how to refresh;
// only this path did not, so it now shares that code
// (internal/cliproxy/codex.Credentials) rather than keeping a second, dumber
// reader of the same file.
func codexCreds(ctx context.Context, dataDir string) (token, account string, err error) {
	path, owns, ok := codexTokenFile(dataDir)
	if !ok {
		return "", "", errNoCodexLogin
	}
	return codex.Credentials(ctx, path, owns)
}

var errNoCodexLogin = errors.New("no Codex login found (checked kunai's accounts and ~/.codex/auth.json)")

// codexReason turns a failure into the sentence a person can act on.
//
// A 401 here means one thing and has one fix, and saying "HTTP 401" instead
// leaves the reader to work that out from a status code. The dashboard shows
// whatever this returns, so it has to name the thing to do rather than the thing
// that happened.
func codexReason(err error) string {
	if err == nil {
		return ""
	}
	if strings.Contains(err.Error(), "HTTP 401") || strings.Contains(err.Error(), "HTTP 403") {
		return "the Codex login has expired; sign in to Codex again"
	}
	return err.Error()
}

// codexUsageResp mirrors the wham/usage response: up to two rolling windows, each
// a used-percent, a reset time, and its length (which varies by plan).
type codexUsageResp struct {
	RateLimit struct {
		Primary   *codexWindow `json:"primary_window"`
		Secondary *codexWindow `json:"secondary_window"`
	} `json:"rate_limit"`
}
type codexWindow struct {
	UsedPercent  float64 `json:"used_percent"`
	ResetAt      int64   `json:"reset_at"`
	WindowSecond int64   `json:"limit_window_seconds"` // the window length; varies by plan
}

func fetchCodexUsage(ctx context.Context, token, account string) (*Usage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, codexUsageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "kunai")
	req.Header.Set("OpenAI-Beta", "codex-1")
	if account != "" {
		req.Header.Set("ChatGPT-Account-Id", account)
	}
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var r codexUsageResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	// The two windows the response carries aren't fixed to 5h/7d: a ChatGPT Go
	// plan, for instance, has a single ~30-day window. Place each by its actual
	// length -- a short window (under a day) is the "session" row, a longer one the
	// "weekly" row -- so the reset time the client shows is always honest.
	u := &Usage{FetchedAt: time.Now().Unix()}
	for _, w := range []*codexWindow{r.RateLimit.Primary, r.RateLimit.Secondary} {
		if w == nil {
			continue
		}
		uw := &UsageWindow{Percent: w.UsedPercent, ResetsAt: w.ResetAt}
		if w.WindowSecond > 0 && w.WindowSecond < 24*60*60 {
			if u.Session == nil {
				u.Session = uw
			}
		} else if u.Weekly == nil {
			u.Weekly = uw
		}
	}
	if u.Session == nil && u.Weekly == nil {
		return nil, fmt.Errorf("no windows in the response")
	}
	return u, nil
}

// isCodexModel reports whether a provider's model is a ChatGPT/Codex one, so
// codex usage is only fetched for a Codex provider (not Grok/Kimi, which would
// otherwise show the codex account's numbers from the ~/.codex fallback).
func isCodexModel(model string) bool {
	m := strings.ToLower(model)
	for _, p := range []string{"gpt", "codex", "o1", "o3", "o4", "chatgpt"} {
		if strings.HasPrefix(m, p) {
			return true
		}
	}
	return false
}

// codexUsageCache serves the Codex quota with a short TTL (the endpoint is a real
// network round trip, and the numbers move slowly), and remembers a failure for
// the same period so a stale login is asked about once a minute rather than once
// per caller. See usagefail.go.
type codexUsageCache struct {
	mu   sync.Mutex
	u    *Usage
	exp  time.Time
	fail usageFailure
}

func (c *codexUsageCache) get(ctx context.Context, dataDir string) *Usage {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if c.u != nil && now.Before(c.exp) {
		return c.u
	}
	if c.fail.holding(now) {
		return nil // already asked recently and could not get an answer
	}
	token, account, err := codexCreds(ctx, dataDir)
	if err != nil {
		c.fail.report(now, providerUsageFailTTL, "codex quota", codexReason(err))
		return nil
	}
	u, err := fetchCodexUsage(ctx, token, account)
	if err != nil {
		c.fail.report(now, providerUsageFailTTL, "codex quota", codexReason(err)+" (account "+account+")")
		return nil
	}
	c.fail.clear()
	c.u, c.exp = u, now.Add(usageTTL)
	return u
}

// reason is why the last poll could not answer, for the client to show. Empty
// when nothing has failed.
func (c *codexUsageCache) reason() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fail.reason()
}
