package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Real `claude -p /usage` output, verbatim. The CLI prints a per-model week and
// a local-activity breakdown too; the parse must take the two windows and ignore
// the rest, so new prose below never breaks it.
const usageOut = `You are currently using your subscription to power your Claude Code usage

Current session: 21% used · resets Jul 17, 10:29pm (Asia/Kolkata)
Current week (all models): 17% used · resets Jul 18, 2:29pm (Asia/Kolkata)
Current week (Fable): 0% used

What's contributing to your limits usage?
Approximate, based on local sessions on this machine — does not include other devices or claude.ai.

Last 24h · 656 requests · 4 sessions
  95% of your usage was at >150k context
  Top skills: /claude-api 8%
`

func TestParseUsage(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	u := parseUsage(usageOut, now)
	if u == nil {
		t.Fatal("no usage parsed")
	}
	if u.Session == nil || u.Session.Percent != 21 {
		t.Fatalf("session = %+v, want 21%%", u.Session)
	}
	if u.Weekly == nil || u.Weekly.Percent != 17 {
		t.Fatalf("weekly = %+v, want 17%%", u.Weekly)
	}
	// Jul 17 10:29pm in Asia/Kolkata is 16:59 UTC.
	kolkata, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Skip("no tzdata")
	}
	want := time.Date(2026, 7, 17, 22, 29, 0, 0, kolkata).Unix()
	if u.Session.ResetsAt != want {
		t.Errorf("session resets_at = %d (%v), want %d",
			u.Session.ResetsAt, time.Unix(u.Session.ResetsAt, 0).UTC(), want)
	}
}

// The per-model week ("Current week (Fable)") must not be mistaken for the
// all-models one: they are different limits and only one belongs on the tile.
func TestParseUsageIgnoresScopedWeek(t *testing.T) {
	out := "Current session: 5% used\nCurrent week (Fable): 99% used\n"
	u := parseUsage(out, time.Now())
	if u == nil || u.Session == nil || u.Session.Percent != 5 {
		t.Fatalf("session = %+v", u)
	}
	if u.Weekly != nil {
		t.Errorf("weekly = %+v, want nil: a per-model week is not the all-models week", u.Weekly)
	}
}

// A window with no reset half still reports its fill; the reset reads as unknown
// rather than as epoch.
func TestParseUsageWindowWithoutReset(t *testing.T) {
	u := parseUsage("Current session: 8% used\n", time.Now())
	if u == nil || u.Session == nil || u.Session.Percent != 8 {
		t.Fatalf("session = %+v", u)
	}
	if u.Session.ResetsAt != 0 {
		t.Errorf("resets_at = %d, want 0 (unknown)", u.Session.ResetsAt)
	}
}

// An account that is not on a subscription prints something else entirely: that
// is an absent tile, not a wrong one.
func TestParseUsageOnNonSubscription(t *testing.T) {
	if u := parseUsage("Claude Code is using an API key.\n", time.Now()); u != nil {
		t.Fatalf("want nil for output with no windows, got %+v", u)
	}
}

// The CLI prints no year, so the parse infers the one that puts the reset ahead
// of now. A window that spans New Year is the case that has to work.
func TestParseResetAtInfersYearAcrossNewYear(t *testing.T) {
	now := time.Date(2026, 12, 31, 23, 0, 0, 0, time.UTC)
	got := parseResetAt("Jan 1, 2:00am (UTC)", now)
	want := time.Date(2027, 1, 1, 2, 0, 0, 0, time.UTC).Unix()
	if got != want {
		t.Errorf("resets_at = %v, want %v (next year, not the one just gone)",
			time.Unix(got, 0).UTC(), time.Unix(want, 0).UTC())
	}
}

func TestParseResetAtUnreadable(t *testing.T) {
	if got := parseResetAt("some day next week", time.Now()); got != 0 {
		t.Errorf("resets_at = %d, want 0: an unreadable reset is unknown, not wrong", got)
	}
}

func TestNewSessionIDIsV4(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id, err := newSessionID()
		if err != nil {
			t.Fatal(err)
		}
		if len(id) != 36 || id[14] != '4' {
			t.Fatalf("id = %q, want a v4 uuid", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q: every poll must get its own", id)
		}
		seen[id] = true
	}
}

// Each -p run records a transcript, so the poll must remove the one it made or a
// 60s cadence buries the Recent list in ~1400 "/usage" sessions a day.
func TestFetchDropsItsTranscript(t *testing.T) {
	cfg := t.TempDir()
	cwd := t.TempDir()
	proj := filepath.Join(claudeRoot(cfg), projectSlug(cwd))
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}

	var gotArgs []string
	defer swapRun(func(ctx context.Context, bin string, env []string, dir string, args ...string) ([]byte, error) {
		gotArgs = args
		// Stand in for the CLI: writing the transcript is what it really does.
		id := args[2]
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return []byte(usageOut), nil
	})()

	u := newUsageCache()
	got, err := u.fetch(context.Background(), CLIProfile{Name: "Claude", Bin: "claude", Dir: cfg}, cwd)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got.Session == nil || got.Session.Percent != 21 {
		t.Fatalf("session = %+v", got.Session)
	}
	if len(gotArgs) != 4 || gotArgs[0] != "-p" || gotArgs[1] != "--session-id" || gotArgs[3] != "/usage" {
		t.Fatalf("args = %v, want -p --session-id <uuid> /usage", gotArgs)
	}
	left, _ := filepath.Glob(filepath.Join(proj, "*.jsonl"))
	if len(left) != 0 {
		t.Errorf("poll left %d transcript(s) behind: %v", len(left), left)
	}
}

// A failed run must still clean up after itself, and must not take the machine
// down: no usage is a quiet tile.
func TestFetchDropsTranscriptOnFailure(t *testing.T) {
	cfg := t.TempDir()
	cwd := t.TempDir()
	proj := filepath.Join(claudeRoot(cfg), projectSlug(cwd))
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	defer swapRun(func(ctx context.Context, bin string, env []string, dir string, args ...string) ([]byte, error) {
		os.WriteFile(filepath.Join(proj, args[2]+".jsonl"), []byte("{}\n"), 0o600)
		return nil, errors.New("boom")
	})()

	u := newUsageCache()
	if _, err := u.fetch(context.Background(), CLIProfile{Bin: "claude", Dir: cfg}, cwd); err == nil {
		t.Fatal("want an error from a failed run")
	}
	if left, _ := filepath.Glob(filepath.Join(proj, "*.jsonl")); len(left) != 0 {
		t.Errorf("failed poll left a transcript behind: %v", left)
	}
}

// The account's env must reach the CLI, or a second account's usage would be the
// default account's.
func TestFetchRunsAsTheAccount(t *testing.T) {
	var gotEnv []string
	var gotBin, gotDir string
	defer swapRun(func(ctx context.Context, bin string, env []string, dir string, args ...string) ([]byte, error) {
		gotBin, gotEnv, gotDir = bin, env, dir
		return []byte(usageOut), nil
	})()

	u := newUsageCache()
	p := CLIProfile{Name: "Work", Bin: "claude-work", Dir: "/w"}
	if _, err := u.fetch(context.Background(), p, "/data"); err != nil {
		t.Fatal(err)
	}
	if gotBin != "claude-work" || gotDir != "/data" {
		t.Fatalf("ran %q in %q", gotBin, gotDir)
	}
	var found bool
	for _, e := range gotEnv {
		if e == "CLAUDE_CONFIG_DIR=/w" {
			found = true
		}
	}
	if !found {
		t.Errorf("env = %v, want the account's CLAUDE_CONFIG_DIR", gotEnv)
	}
}

// The CLI is the slow part: a second look inside the TTL must not shell it again.
func TestGetCachesWithinTTL(t *testing.T) {
	runs := 0
	defer swapRun(func(ctx context.Context, bin string, env []string, dir string, args ...string) ([]byte, error) {
		runs++
		return []byte(usageOut), nil
	})()

	u := newUsageCache()
	p := CLIProfile{Bin: "claude", Dir: t.TempDir()}
	for i := 0; i < 3; i++ {
		if _, err := u.get(context.Background(), p, t.TempDir()); err != nil {
			t.Fatal(err)
		}
	}
	if runs != 1 {
		t.Errorf("shelled the CLI %d times, want 1 inside the TTL", runs)
	}
}

// swapRun points the CLI shell at a stub and returns the undo, so a test never
// spawns a real claude.
func swapRun(fn func(context.Context, string, []string, string, ...string) ([]byte, error)) func() {
	old := usageRun
	usageRun = fn
	return func() { usageRun = old }
}

// --- per-account usage ---

// The whole point of multi-account is choosing where a session runs, so each
// account's quota must be its own. One shared cache slot would make the second
// account serve the first's numbers and evict it on every poll.
func TestGetCachesPerAccount(t *testing.T) {
	byDir := map[string]string{
		"/personal": "Current session: 10% used\nCurrent week (all models): 20% used\n",
		"/work":     "Current session: 80% used\nCurrent week (all models): 90% used\n",
	}
	runs := map[string]int{}
	defer swapRun(func(ctx context.Context, bin string, env []string, dir string, args ...string) ([]byte, error) {
		cfg := ""
		for _, e := range env {
			if strings.HasPrefix(e, "CLAUDE_CONFIG_DIR=") {
				cfg = strings.TrimPrefix(e, "CLAUDE_CONFIG_DIR=")
			}
		}
		runs[cfg]++
		return []byte(byDir[cfg]), nil
	})()

	u := newUsageCache()
	personal := CLIProfile{Name: "Claude", Bin: "claude", Dir: "/personal"}
	work := CLIProfile{Name: "Work", Bin: "claude", Dir: "/work"}

	// Read each twice: both must keep their own numbers and shell the CLI once.
	for i := 0; i < 2; i++ {
		got, err := u.get(context.Background(), personal, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if got.Session.Percent != 10 {
			t.Fatalf("personal session = %v, want 10 (served another account's numbers?)", got.Session.Percent)
		}
		got, err = u.get(context.Background(), work, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if got.Session.Percent != 80 {
			t.Fatalf("work session = %v, want 80", got.Session.Percent)
		}
	}
	if runs["/personal"] != 1 || runs["/work"] != 1 {
		t.Errorf("CLI runs = %v, want exactly one per account", runs)
	}
}

// A failure for one account must not blank out another's good answer.
func TestOneAccountFailingLeavesTheOtherIntact(t *testing.T) {
	defer swapRun(func(ctx context.Context, bin string, env []string, dir string, args ...string) ([]byte, error) {
		for _, e := range env {
			if e == "CLAUDE_CONFIG_DIR=/broken" {
				return nil, fmt.Errorf("logged out")
			}
		}
		return []byte(usageOut), nil
	})()

	u := newUsageCache()
	good := CLIProfile{Name: "Good", Bin: "claude", Dir: "/good"}
	broken := CLIProfile{Name: "Broken", Bin: "claude", Dir: "/broken"}

	if _, err := u.get(context.Background(), good, t.TempDir()); err != nil {
		t.Fatalf("good account: %v", err)
	}
	if _, err := u.get(context.Background(), broken, t.TempDir()); err == nil {
		t.Fatal("broken account should report an error")
	}
	got, err := u.get(context.Background(), good, t.TempDir())
	if err != nil || got == nil {
		t.Fatalf("good account after the other failed: %v", err)
	}
}

// Profiles differing only by label share a login, so they must share an entry
// rather than each paying for its own CLI run.
func TestSameConfigDirSharesOneEntry(t *testing.T) {
	runs := 0
	defer swapRun(func(ctx context.Context, bin string, env []string, dir string, args ...string) ([]byte, error) {
		runs++
		return []byte(usageOut), nil
	})()

	u := newUsageCache()
	a := CLIProfile{Name: "Personal", Bin: "claude", Dir: "/same"}
	b := CLIProfile{Name: "Renamed", Bin: "claude", Dir: "/same"}
	if _, err := u.get(context.Background(), a, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := u.get(context.Background(), b, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Errorf("shelled the CLI %d times for one login, want 1", runs)
	}
}

// Concurrent reads of different accounts must not deadlock or serialise behind
// one lock; concurrent reads of ONE account must still collapse to a single run.
func TestConcurrentGetsAcrossAccounts(t *testing.T) {
	var mu sync.Mutex
	runs := map[string]int{}
	defer swapRun(func(ctx context.Context, bin string, env []string, dir string, args ...string) ([]byte, error) {
		cfg := ""
		for _, e := range env {
			if strings.HasPrefix(e, "CLAUDE_CONFIG_DIR=") {
				cfg = strings.TrimPrefix(e, "CLAUDE_CONFIG_DIR=")
			}
		}
		mu.Lock()
		runs[cfg]++
		mu.Unlock()
		return []byte(usageOut), nil
	})()

	u := newUsageCache()
	profiles := []CLIProfile{
		{Name: "A", Bin: "claude", Dir: "/a"},
		{Name: "B", Bin: "claude", Dir: "/b"},
		{Name: "C", Bin: "claude", Dir: "/c"},
	}
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(p CLIProfile) {
			defer wg.Done()
			if _, err := u.get(context.Background(), p, "/tmp"); err != nil {
				t.Error(err)
			}
		}(profiles[i%len(profiles)])
	}
	wg.Wait()
	for _, p := range profiles {
		if runs[p.Dir] != 1 {
			t.Errorf("account %s shelled %d times, want 1", p.Name, runs[p.Dir])
		}
	}
}

// The default account keeps working with no parameter, which is what a
// single-account machine and any older client send.
func TestHandleUsageDefaultsToTheDefaultAccount(t *testing.T) {
	var gotCfg string
	defer swapRun(func(ctx context.Context, bin string, env []string, dir string, args ...string) ([]byte, error) {
		gotCfg = ""
		for _, e := range env {
			if strings.HasPrefix(e, "CLAUDE_CONFIG_DIR=") {
				gotCfg = strings.TrimPrefix(e, "CLAUDE_CONFIG_DIR=")
			}
		}
		return []byte(usageOut), nil
	})()

	s := usageTestServer(t)
	got := doUsage(t, s, "")
	if gotCfg != "" {
		t.Errorf("ran against %q, want the default account (no CLAUDE_CONFIG_DIR)", gotCfg)
	}
	if got.CLI != "Claude" {
		t.Errorf("cli = %q, want Claude", got.CLI)
	}
}

// ?cli=<name> must poll that account, and label the answer as that account.
func TestHandleUsageSelectsTheNamedAccount(t *testing.T) {
	var gotCfg string
	defer swapRun(func(ctx context.Context, bin string, env []string, dir string, args ...string) ([]byte, error) {
		for _, e := range env {
			if strings.HasPrefix(e, "CLAUDE_CONFIG_DIR=") {
				gotCfg = strings.TrimPrefix(e, "CLAUDE_CONFIG_DIR=")
			}
		}
		return []byte(usageOut), nil
	})()

	s := usageTestServer(t)
	got := doUsage(t, s, "Work")
	if gotCfg != "/work" {
		t.Errorf("ran against %q, want /work", gotCfg)
	}
	if got.CLI != "Work" {
		t.Errorf("cli = %q, want Work", got.CLI)
	}
}

// An unknown account falls back to the default rather than erroring, matching
// resolveCLI everywhere else: a client must always get a usable answer.
func TestHandleUsageUnknownAccountFallsBackToDefault(t *testing.T) {
	defer swapRun(func(ctx context.Context, bin string, env []string, dir string, args ...string) ([]byte, error) {
		return []byte(usageOut), nil
	})()
	s := usageTestServer(t)
	if got := doUsage(t, s, "nope"); got.CLI != "Claude" {
		t.Errorf("cli = %q, want the default Claude", got.CLI)
	}
}

// A logged-out or API-key account is reported as unavailable with the account
// named, never as a 500: the dashboard shows an absent tile, not an error page.
func TestHandleUsageUnavailableNamesTheAccount(t *testing.T) {
	defer swapRun(func(ctx context.Context, bin string, env []string, dir string, args ...string) ([]byte, error) {
		return nil, fmt.Errorf("not logged in")
	})()
	s := usageTestServer(t)

	rec := httptest.NewRecorder()
	s.handleUsage(rec, httptest.NewRequest("GET", "/api/usage?cli=Work", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["unavailable"] == nil {
		t.Fatalf("body = %v, want an unavailable reason", body)
	}
	if body["cli"] != "Work" {
		t.Errorf("cli = %v, want Work", body["cli"])
	}
}

// usageTestServer is a two-account machine: the default plus a work account.
func usageTestServer(t *testing.T) *Server {
	t.Helper()
	return &Server{
		cfg:   Config{DataDir: t.TempDir()},
		usage: newUsageCache(),
		clis: []CLIProfile{
			{Name: "Claude", Bin: "claude"},
			{Name: "Work", Bin: "claude", Dir: "/work"},
		},
	}
}

// doUsage calls the handler and decodes a successful Usage body.
func doUsage(t *testing.T, s *Server, cli string) Usage {
	t.Helper()
	url := "/api/usage"
	if cli != "" {
		url += "?cli=" + cli
	}
	rec := httptest.NewRecorder()
	s.handleUsage(rec, httptest.NewRequest("GET", url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var u Usage
	if err := json.Unmarshal(rec.Body.Bytes(), &u); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return u
}
