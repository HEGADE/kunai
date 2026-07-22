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
	// CLI names the account these numbers belong to, so a client showing more
	// than one cannot mislabel them. Stamped per response, not cached.
	CLI string `json:"cli,omitempty"`
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
//
// Entries are per account. A quota belongs to the login in a config dir, so a
// single shared slot would make two accounts serve each other's numbers and evict
// one another on every poll. Each entry carries its own lock, so a slow poll for
// one account never blocks the dashboard reading another.
type usageCache struct {
	mu      sync.Mutex
	entries map[string]*usageEntry
}

// usageEntry is one account's cached answer.
type usageEntry struct {
	mu  sync.Mutex
	at  time.Time
	val *Usage
	err error
}

func newUsageCache() *usageCache { return &usageCache{entries: map[string]*usageEntry{}} }

// usageKey identifies the account a quota belongs to. The subscription follows
// the login in the config dir rather than the label, so two profiles pointing at
// one dir correctly share an entry, and renaming a profile does not strand its
// cached answer.
func usageKey(p CLIProfile) string { return p.Bin + "\x00" + p.configDir() }

// entryFor returns the account's cache slot, creating it on first use.
func (u *usageCache) entryFor(p CLIProfile) *usageEntry {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.entries == nil {
		u.entries = map[string]*usageEntry{}
	}
	key := usageKey(p)
	e, ok := u.entries[key]
	if !ok {
		e = &usageEntry{}
		u.entries[key] = e
	}
	return e
}

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
	e := u.entryFor(p)
	e.mu.Lock()
	defer e.mu.Unlock()
	ttl := usageTTL
	if e.err != nil {
		ttl = usageFailTTL
	}
	if time.Since(e.at) < ttl {
		return e.val, e.err
	}
	e.val, e.err = u.fetch(ctx, p, cwd)
	e.at = time.Now()
	return e.val, e.err
}

// usagePollLoop keeps the scheduler's window reset times fresh from /usage, the
// same source the dashboard shows. Without it a reset job could only learn a
// reset from a live session's rate_limit frame, which is rare and lost on every
// restart, so a "fire after reset" job often armed late (to the next window) or
// never armed to the reset the user actually set it for. /usage knows the real
// reset every minute regardless of any session, so feeding it here is what makes
// reset jobs reliable.
//
// It runs only while a reset job exists: with none, there is nothing to feed and
// no reason to shell the CLI. First poll is soon after boot so a job that was
// waiting when we last died re-learns its reset quickly; after that the cadence
// is slow, because reset times move on the scale of hours.
func (s *Server) usagePollLoop(ctx context.Context) {
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		s.feedSchedulerResets(ctx)
		timer.Reset(5 * time.Minute)
	}
}

// feedSchedulerResets hands the current window reset times to the scheduler, so
// a reset-triggered job arms to the real reset. Does nothing (and skips the CLI)
// when no reset job is waiting. The window keys are the scheduler's own,
// "five_hour" for the session window and "seven_day" for the weekly one.
func (s *Server) feedSchedulerResets(ctx context.Context) {
	if s.sched == nil || !s.sched.HasResetJobs() {
		return
	}
	u, err := s.usage.get(ctx, s.resolveCLI(""), s.cfg.DataDir)
	if err != nil || u == nil {
		return
	}
	if u.Session != nil && u.Session.ResetsAt > 0 {
		s.sched.NoteReset("five_hour", u.Session.ResetsAt)
	}
	if u.Weekly != nil && u.Weekly.ResetsAt > 0 {
		s.sched.NoteReset("seven_day", u.Weekly.ResetsAt)
	}
}

// handleUsage serves one account's quota windows. ?cli=<name> picks the account;
// an omitted or unknown name means the machine's default, which is what a
// single-account machine and any older client ask for. Quota is per-account, and
// a machine can now run several, so "which account is this" has to be a parameter
// rather than an assumption: switching a session to the work account is only a
// real choice if you can see whether the work account has room.
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	p := s.resolveCLI(r.URL.Query().Get("cli"))
	// A proxy provider has no Anthropic subscription window, and shelling `/usage`
	// against the proxy would only burn ~2s and leave a stray transcript. For a
	// Codex provider, though, the ChatGPT account has real quota windows we can
	// read from OpenAI's usage endpoint; other providers stay unavailable.
	if isProxyProfile(p) {
		if prov := s.providerNamed(p.Name); prov != nil && isCodexModel(providerDisplayModel(*prov)) {
			if u := s.codexUC.get(r.Context(), s.cfg.DataDir); u != nil {
				out := *u
				out.CLI = p.Name
				writeJSON(w, http.StatusOK, out)
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"unavailable": "usage not available for this provider", "cli": p.Name})
		return
	}
	usage, err := s.usage.get(r.Context(), p, s.cfg.DataDir)
	if err != nil || usage == nil {
		// Nothing the client can act on: the account may be logged out, on an
		// API key, or the CLI may be missing. Say "no usage", not "500".
		msg := "no usage reported"
		if err != nil {
			msg = err.Error()
		}
		writeJSON(w, http.StatusOK, map[string]any{"unavailable": msg, "cli": p.Name})
		return
	}
	// Stamp the account on a copy: the cached value is shared, and two profiles
	// may point at one config dir under different names.
	out := *usage
	out.CLI = p.Name
	writeJSON(w, http.StatusOK, out)
}
