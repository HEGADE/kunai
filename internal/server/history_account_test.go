package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A minimal transcript probeTranscript can read a cwd and title out of.
func writeTranscript(t *testing.T, root, encodedCwd, id, cwd string) {
	t.Helper()
	dir := filepath.Join(root, encodedCwd)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","cwd":"` + cwd + `","message":{"role":"user","content":"hello"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The Recent list scans each account's own transcript folder and tags every entry
// with the account it belongs to, so a work session is listed and can be reopened
// on the work account.
func TestScanHistoryTagsEachAccount(t *testing.T) {
	personal := filepath.Join(t.TempDir(), "projects")
	work := filepath.Join(t.TempDir(), "projects")
	writeTranscript(t, personal, "-home-me-proj", "id-personal", "/home/me/proj")
	writeTranscript(t, work, "-home-me-work", "id-work", "/home/me/work")

	roots := []accountRoot{{name: "Claude", root: personal}, {name: "Claude Work", root: work}}
	got := scanHistory(map[string]bool{}, 25, roots, nil)

	byID := map[string]string{}
	for _, e := range got {
		byID[e.ID] = e.CLI
	}
	if byID["id-personal"] != "Claude" {
		t.Errorf("personal session tagged %q, want Claude", byID["id-personal"])
	}
	if byID["id-work"] != "Claude Work" {
		t.Errorf("work session tagged %q, want Claude Work", byID["id-work"])
	}
}

// A live session is excluded, and a session id is never listed twice even if it
// somehow appears under two roots (unique ids, first root wins).
func TestScanHistoryExcludesLiveAndDedupes(t *testing.T) {
	a := filepath.Join(t.TempDir(), "projects")
	b := filepath.Join(t.TempDir(), "projects")
	writeTranscript(t, a, "-x", "live-one", "/x")
	writeTranscript(t, a, "-x", "dup", "/x")
	writeTranscript(t, b, "-x", "dup", "/x") // same id under a second root

	roots := []accountRoot{{name: "A", root: a}, {name: "B", root: b}}
	got := scanHistory(map[string]bool{"live-one": true}, 25, roots, nil)

	ids := map[string]int{}
	for _, e := range got {
		ids[e.ID]++
	}
	if ids["live-one"] != 0 {
		t.Error("a live session leaked into Recent")
	}
	if ids["dup"] != 1 {
		t.Errorf("id listed %d times, want 1 (deduped)", ids["dup"])
	}
}

// The Dir shorthand becomes CLAUDE_CONFIG_DIR for the driver, or the CLI would
// auth as the default account; and configDir reads back from either place.
func TestProfileDirAndEnv(t *testing.T) {
	p := CLIProfile{Name: "Work", Bin: "claude", Dir: "/home/me/.claude-work"}
	if p.configDir() != "/home/me/.claude-work" {
		t.Fatalf("configDir = %q", p.configDir())
	}
	if p.effectiveEnv()["CLAUDE_CONFIG_DIR"] != "/home/me/.claude-work" {
		t.Fatalf("effectiveEnv did not set CLAUDE_CONFIG_DIR: %v", p.effectiveEnv())
	}
	// Env form works too.
	q := CLIProfile{Name: "Work", Bin: "claude", Env: map[string]string{"CLAUDE_CONFIG_DIR": "/w"}}
	if q.configDir() != "/w" {
		t.Fatalf("configDir from env = %q", q.configDir())
	}
	// Default account: no dir, empty env untouched.
	d := CLIProfile{Name: "Claude", Bin: "claude"}
	if d.configDir() != "" || d.effectiveEnv() != nil {
		t.Fatalf("default profile leaked a config dir: dir=%q env=%v", d.configDir(), d.effectiveEnv())
	}
}

// accountRoots gives one root per distinct config dir, and always covers the
// default even when every profile pins a custom dir.
func TestAccountRoots(t *testing.T) {
	s := &Server{clis: []CLIProfile{
		{Name: "Claude", Bin: "claude"},
		{Name: "Work", Bin: "claude", Dir: "/home/me/.claude-work"},
	}}
	roots := s.accountRoots()
	found := map[string]bool{}
	for _, r := range roots {
		found[r.name] = true
	}
	if !found["Claude"] || !found["Work"] {
		t.Fatalf("roots missing an account: %+v", roots)
	}
}

// touchTranscript sets a transcript's mtime, standing in for "this account is the
// one still writing to it".
func touchTranscript(t *testing.T, root, encodedCwd, id string, at time.Time) {
	t.Helper()
	p := filepath.Join(root, encodedCwd, id+".jsonl")
	if err := os.Chtimes(p, at, at); err != nil {
		t.Fatal(err)
	}
}

// Switching an account copies the transcript, so the same id exists under both.
// The session must be credited to the account still writing it (the newest copy),
// not to whichever account is listed first. Getting this wrong also reopened the
// session on the wrong account, because the client sends this tag back.
func TestScanHistoryCreditsTheAccountThatLastRanIt(t *testing.T) {
	personal := filepath.Join(t.TempDir(), "projects")
	work := filepath.Join(t.TempDir(), "projects")
	const id, slug = "switched", "-home-me-proj"
	writeTranscript(t, personal, slug, id, "/home/me/proj")
	writeTranscript(t, work, slug, id, "/home/me/proj")

	now := time.Now()
	touchTranscript(t, personal, slug, id, now.Add(-2*time.Hour)) // stale copy
	touchTranscript(t, work, slug, id, now)                       // the live one

	// Personal is listed first, exactly as the default account always is.
	roots := []accountRoot{{name: "Claude", root: personal}, {name: "Work", root: work}}
	got := scanHistory(map[string]bool{}, 25, roots, nil)
	if len(got) != 1 {
		t.Fatalf("got %d entries, want the id listed once", len(got))
	}
	if got[0].CLI != "Work" {
		t.Errorf("tagged %q, want Work (the account still writing it)", got[0].CLI)
	}
	// And it must sort by the live copy's time, not the stale one's.
	if got[0].Mtime.Before(now.Add(-time.Minute)) {
		t.Errorf("mtime = %v, want the live copy's time %v", got[0].Mtime, now)
	}
}

// The same, with the newer copy under the account listed FIRST, so the test
// cannot pass by simply preferring the last root.
func TestScanHistoryCreditsNewestWhicheverRootItIsIn(t *testing.T) {
	first := filepath.Join(t.TempDir(), "projects")
	second := filepath.Join(t.TempDir(), "projects")
	const id, slug = "sess", "-home-me-proj"
	writeTranscript(t, first, slug, id, "/home/me/proj")
	writeTranscript(t, second, slug, id, "/home/me/proj")

	now := time.Now()
	touchTranscript(t, first, slug, id, now)
	touchTranscript(t, second, slug, id, now.Add(-3*time.Hour))

	roots := []accountRoot{{name: "First", root: first}, {name: "Second", root: second}}
	got := scanHistory(map[string]bool{}, 25, roots, nil)
	if len(got) != 1 || got[0].CLI != "First" {
		t.Fatalf("got %+v, want one entry tagged First", got)
	}
}

// A truncated (zero byte) copy must never win the tag, or a session that lives
// intact under one account would be credited to the account holding the stub and
// then reopen from nothing.
func TestScanHistoryIgnoresTruncatedCopies(t *testing.T) {
	good := filepath.Join(t.TempDir(), "projects")
	stub := filepath.Join(t.TempDir(), "projects")
	const id, slug = "sess", "-home-me-proj"
	writeTranscript(t, good, slug, id, "/home/me/proj")
	if err := os.MkdirAll(filepath.Join(stub, slug), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stub, slug, id+".jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	touchTranscript(t, good, slug, id, now.Add(-time.Hour))
	touchTranscript(t, stub, slug, id, now) // newer, but empty

	roots := []accountRoot{{name: "Good", root: good}, {name: "Stub", root: stub}}
	got := scanHistory(map[string]bool{}, 25, roots, nil)
	if len(got) != 1 || got[0].CLI != "Good" {
		t.Fatalf("got %+v, want the intact copy under Good", got)
	}
}

// Equal timestamps must not reshuffle the list between polls.
func TestScanHistoryOrderIsDeterministic(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	at := time.Now()
	for _, id := range []string{"c", "a", "b"} {
		writeTranscript(t, root, "-home-me-proj", id, "/home/me/proj")
		touchTranscript(t, root, "-home-me-proj", id, at)
	}
	roots := []accountRoot{{name: "Claude", root: root}}
	var first []string
	for i := 0; i < 5; i++ {
		var ids []string
		for _, e := range scanHistory(map[string]bool{}, 25, roots, nil) {
			ids = append(ids, e.ID)
		}
		if i == 0 {
			first = ids
			continue
		}
		if strings.Join(ids, ",") != strings.Join(first, ",") {
			t.Fatalf("order changed between scans: %v then %v", first, ids)
		}
	}
}

// Two copies sharing a timestamp is a real case: the switch writes one and both
// are touched inside the same second. Transcripts only grow, so the longer copy
// is the one that ran further and must win.
func TestScanHistoryPrefersTheLongerCopyOnATimestampTie(t *testing.T) {
	short := filepath.Join(t.TempDir(), "projects")
	long := filepath.Join(t.TempDir(), "projects")
	const id, slug = "sess", "-home-me-proj"
	writeTranscript(t, short, slug, id, "/home/me/proj")
	writeTranscript(t, long, slug, id, "/home/me/proj")
	// Give the second copy more turns, as the account that kept running would.
	p := filepath.Join(long, slug, id+".jsonl")
	extra := strings.Repeat(`{"type":"user","cwd":"/home/me/proj","message":{"role":"user","content":"more"}}`+"\n", 40)
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(body, extra...), 0o644); err != nil {
		t.Fatal(err)
	}

	at := time.Now() // identical timestamps
	touchTranscript(t, short, slug, id, at)
	touchTranscript(t, long, slug, id, at)

	// The shorter copy is under the account listed first, as the default is.
	roots := []accountRoot{{name: "Short", root: short}, {name: "Long", root: long}}
	got := scanHistory(map[string]bool{}, 25, roots, nil)
	if len(got) != 1 || got[0].CLI != "Long" {
		t.Fatalf("got %+v, want the longer copy under Long", got)
	}
}
