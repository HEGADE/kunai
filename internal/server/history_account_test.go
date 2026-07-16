package server

import (
	"os"
	"path/filepath"
	"testing"
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
	got := scanHistory(map[string]bool{}, 25, roots)

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
	got := scanHistory(map[string]bool{"live-one": true}, 25, roots)

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
