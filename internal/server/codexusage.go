package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
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

// codexCreds finds a Codex OAuth token: the managed sidecar's auth dir first (the
// account added to kunai, kept fresh by the sidecar), then ~/.codex/auth.json (the
// codex CLI login) as a fallback.
func codexCreds(dataDir string) (token, account string, ok bool) {
	var files []string
	if dataDir != "" {
		m, _ := filepath.Glob(filepath.Join(dataDir, "cliproxy", "auth", "codex-*.json"))
		files = append(files, m...)
	}
	if home, err := os.UserHomeDir(); err == nil {
		files = append(files, filepath.Join(home, ".codex", "auth.json"))
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var a codexAuthFile
		if json.Unmarshal(b, &a) != nil {
			continue
		}
		if t, acct := a.creds(); t != "" {
			return t, acct, true
		}
	}
	return "", "", false
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
		return nil, fmt.Errorf("codex usage: HTTP %d", resp.StatusCode)
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
		return nil, fmt.Errorf("codex usage: no windows in response")
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
// network round trip, and the numbers move slowly).
type codexUsageCache struct {
	mu  sync.Mutex
	u   *Usage
	exp time.Time
}

func (c *codexUsageCache) get(ctx context.Context, dataDir string) *Usage {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.u != nil && time.Now().Before(c.exp) {
		return c.u
	}
	token, account, ok := codexCreds(dataDir)
	if !ok {
		log.Printf("codex usage: no token found (checked managed sidecar auth dir and ~/.codex)")
		return nil
	}
	u, err := fetchCodexUsage(ctx, token, account)
	if err != nil {
		log.Printf("codex usage: %v (account=%q)", err, account)
		return nil
	}
	c.u, c.exp = u, time.Now().Add(60*time.Second)
	return u
}
