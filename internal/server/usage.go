package server

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Subscription usage: the same two numbers `claude`'s /usage prints, for the
// machine's default account, on the dashboard. A `rate_limit_info` frame only
// carries a window's reset time and whether a turn was rejected, so the "how
// full is it" half has to come from the account.
//
// We ask the CLI rather than the account's HTTP endpoint, and the reason is
// credentials: the CLI already knows how to read its own login, which on macOS
// lives in the Keychain rather than a file. Shelling out means kunai never
// touches that login at all, so it can never rotate a token out from under a
// running session or drop a field and log the account out. That safety is worth
// the costs, which are real: a couple of seconds per poll, and prose to parse
// instead of JSON. `/usage` is free (no model call, no tokens).
const (
	// The CLI is the slow part, and a window moves slowly: poll about as often
	// as the number can meaningfully change, never once per dashboard paint.
	usageTTL = 60 * time.Second
	// A cold CLI start is ~2s; leave room without hanging the dashboard.
	usageTimeout = 30 * time.Second
	// A failure is cached far more briefly than an answer. Holding a transient
	// error for a full minute means a blank meter for a minute, which reads as
	// broken; retrying soon costs one cheap CLI run.
	usageFailTTL = 10 * time.Second
)

// UsageWindow is one quota window's fill. Mirrors web/src/lib/types.ts.
type UsageWindow struct {
	Percent  float64 `json:"percent"`             // 0-100
	ResetsAt int64   `json:"resets_at,omitempty"` // unix seconds; 0 = unknown
}

// Usage is the account's quota picture. A nil window means the CLI did not
// report that limit, which the UI shows as absent rather than as zero: an empty
// meter and an unknown one are not the same claim.
type Usage struct {
	Session   *UsageWindow `json:"session,omitempty"` // rolling 5-hour
	Weekly    *UsageWindow `json:"weekly,omitempty"`  // rolling 7-day
	FetchedAt int64        `json:"fetched_at"`        // unix seconds
}

// usageRun shells the CLI. Injectable for the same reason guardian.go routes
// privileged commands through execRun: a test asserts the exact command instead
// of spawning a real claude.
var usageRun = func(ctx context.Context, bin string, env []string, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Dir = dir
	return cmd.Output()
}

// What `/usage` prints. Only these two lines matter; the CLI also prints a
// per-model week and a local-activity breakdown, which we ignore. The separator
// is a middle dot, and the reset half is absent on a window that never resets.
var (
	reSession = regexp.MustCompile(`(?m)^Current session:\s*([\d.]+)%\s*used(?:\s*·\s*resets\s+(.+?))?\s*$`)
	reWeekly  = regexp.MustCompile(`(?m)^Current week \(all models\):\s*([\d.]+)%\s*used(?:\s*·\s*resets\s+(.+?))?\s*$`)
)

// parseResetAt reads the CLI's "Jul 17, 10:29pm (Asia/Kolkata)". Returns 0 when
// it cannot be read, which the UI shows as an unknown reset rather than a wrong
// one.
//
// The CLI prints no year. A reset is always ahead of now (five hours or seven
// days), so the year that puts it there is the right one; the only ambiguity is
// a window spanning New Year, which this resolves the same way.
func parseResetAt(s string, now time.Time) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	loc := time.Local
	if i := strings.LastIndex(s, "("); i >= 0 && strings.HasSuffix(s, ")") {
		if l, err := time.LoadLocation(strings.TrimSpace(s[i+1 : len(s)-1])); err == nil {
			loc = l
		}
		s = strings.TrimSpace(s[:i])
	}
	t, err := time.ParseInLocation("Jan 2, 3:04pm", s, loc)
	if err != nil {
		return 0
	}
	t = time.Date(now.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, loc)
	if t.Before(now.Add(-24 * time.Hour)) {
		t = t.AddDate(1, 0, 0)
	}
	return t.Unix()
}

func matchWindow(re *regexp.Regexp, out string, now time.Time) *UsageWindow {
	m := re.FindStringSubmatch(out)
	if m == nil {
		return nil
	}
	pct, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return nil
	}
	return &UsageWindow{Percent: pct, ResetsAt: parseResetAt(m[2], now)}
}

// parseUsage reads `/usage` output. An account that is not on a subscription
// (an API key, say) prints something else entirely and yields no windows, which
// is a quiet absent tile rather than an error.
func parseUsage(out string, now time.Time) *Usage {
	u := &Usage{
		Session:   matchWindow(reSession, out, now),
		Weekly:    matchWindow(reWeekly, out, now),
		FetchedAt: now.Unix(),
	}
	if u.Session == nil && u.Weekly == nil {
		return nil
	}
	return u
}

// newSessionID is a v4 UUID for --session-id. We pass our own so we know exactly
// which transcript the poll created, and can remove precisely that one.
func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // v4
	b[8] = (b[8] & 0x3f) | 0x80 // variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// projectSlug is how the CLI names a cwd's transcript folder: the path with its
// separators turned into dashes, so /tmp becomes -tmp.
func projectSlug(dir string) string {
	return strings.ReplaceAll(dir, string(filepath.Separator), "-")
}

// dropTranscript removes the transcript a usage poll just wrote. Every `-p` run
// records one, so without this a 60s poll would bury the Recent list in ~1400
// "/usage" sessions a day. It only ever deletes the id this poll generated: the
// direct path first, then a search, because the folder name follows the CLI's
// slug rule rather than ours.
func dropTranscript(configDir, cwd, id string) {
	root := claudeRoot(configDir)
	if root == "" || id == "" {
		return
	}
	if err := os.Remove(filepath.Join(root, projectSlug(cwd), id+".jsonl")); err == nil {
		return
	}
	matches, _ := filepath.Glob(filepath.Join(root, "*", id+".jsonl"))
	for _, m := range matches {
		os.Remove(m)
	}
}

// usageCache serves the dashboard without shelling the CLI on every paint, and
// serialises the poll so a slow CLI start can never pile up.
type usageCache struct {
	mu  sync.Mutex
	at  time.Time
	val *Usage
	err error
}

func newUsageCache() *usageCache { return &usageCache{} }

// fetch shells `claude -p /usage` as the given account and reads the answer.
// cwd is kunai's own data dir: somewhere stable that is ours, so the transcript
// this creates never lands in a project the Recent list cares about.
func (u *usageCache) fetch(ctx context.Context, p CLIProfile, cwd string) (*Usage, error) {
	id, err := newSessionID()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, usageTimeout)
	defer cancel()

	// Ours to clean up whether the run worked or not.
	defer dropTranscript(p.configDir(), cwd, id)

	out, err := usageRun(ctx, p.Bin, envSlice(p.effectiveEnv()), cwd, "-p", "--session-id", id, "/usage")
	if err != nil {
		return nil, fmt.Errorf("claude -p /usage: %w", err)
	}
	usage := parseUsage(string(out), time.Now())
	if usage == nil {
		return nil, fmt.Errorf("no usage in CLI output")
	}
	return usage, nil
}

// get returns the cached usage, refetching at most once per usageTTL. A failure
// is held only briefly (usageFailTTL) so a blip clears itself, while still not
// re-shelling the CLI on every paint.
func (u *usageCache) get(ctx context.Context, p CLIProfile, cwd string) (*Usage, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	ttl := usageTTL
	if u.err != nil {
		ttl = usageFailTTL
	}
	if time.Since(u.at) < ttl {
		return u.val, u.err
	}
	u.val, u.err = u.fetch(ctx, p, cwd)
	u.at = time.Now()
	return u.val, u.err
}

// handleUsage serves the default account's quota windows. Quota is per-account
// and the dashboard shows one machine's default account, so this deliberately
// takes no account parameter.
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	usage, err := s.usage.get(r.Context(), s.resolveCLI(""), s.cfg.DataDir)
	if err != nil {
		// Nothing the client can act on: the account may be logged out, on an
		// API key, or the CLI may be missing. Say "no usage", not "500".
		writeJSON(w, http.StatusOK, map[string]any{"unavailable": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, usage)
}
