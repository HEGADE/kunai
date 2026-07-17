package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
