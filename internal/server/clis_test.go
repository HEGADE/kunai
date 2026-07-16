package server

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// With no clis.json, a machine has exactly one account (plain claude) and a
// starter file is written so the format is discoverable.
func TestLoadCLIsDefaultsAndWritesTemplate(t *testing.T) {
	dir := t.TempDir()
	clis := loadCLIs(dir)
	if len(clis) != 1 || clis[0].Name != "Claude" || clis[0].Bin != "claude" {
		t.Fatalf("default = %+v, want a single Claude/claude", clis)
	}
	if _, err := os.Stat(filepath.Join(dir, "clis.json")); err != nil {
		t.Fatalf("starter clis.json not written: %v", err)
	}
}

// A real config with two accounts is read in order; the first is the default.
func TestLoadCLIsReadsProfiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "clis.json"), []byte(`[
	  {"name":"Claude","bin":"claude"},
	  {"name":"Claude Work","bin":"claude","env":{"CLAUDE_CONFIG_DIR":"/home/me/.claude-work"}}
	]`), 0o600)

	clis := loadCLIs(dir)
	if len(clis) != 2 {
		t.Fatalf("got %d profiles, want 2", len(clis))
	}
	if clis[1].Name != "Claude Work" || clis[1].Env["CLAUDE_CONFIG_DIR"] != "/home/me/.claude-work" {
		t.Fatalf("second profile wrong: %+v", clis[1])
	}
}

// Entries missing a name or binary are dropped; an all-bad file falls back to the
// default so a session can always start.
func TestLoadCLIsDropsIncompleteAndFallsBack(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "clis.json"), []byte(`[{"name":"","bin":"x"},{"name":"y","bin":""}]`), 0o600)
	if clis := loadCLIs(dir); len(clis) != 1 || clis[0].Name != "Claude" {
		t.Fatalf("bad file did not fall back to default: %+v", clis)
	}
}

// Resolving is by name, and an empty or unknown name lands on the default, so a
// session never fails to get a runnable binary.
func TestResolveCLI(t *testing.T) {
	s := &Server{clis: []CLIProfile{{Name: "Claude", Bin: "claude"}, {Name: "Work", Bin: "claude-work"}}}
	if got := s.resolveCLI("Work"); got.Bin != "claude-work" {
		t.Fatalf(`resolveCLI("Work") = %+v`, got)
	}
	if got := s.resolveCLI(""); got.Name != "Claude" {
		t.Fatalf(`resolveCLI("") = %+v, want the default`, got)
	}
	if got := s.resolveCLI("nope"); got.Name != "Claude" {
		t.Fatalf(`resolveCLI("nope") = %+v, want the default`, got)
	}
	if got := s.cliNames(); !reflect.DeepEqual(got, []string{"Claude", "Work"}) {
		t.Fatalf("cliNames = %v", got)
	}
}

// The env map becomes a deterministic KEY=VALUE slice for exec.
func TestEnvSlice(t *testing.T) {
	got := envSlice(map[string]string{"B": "2", "A": "1"})
	if !reflect.DeepEqual(got, []string{"A=1", "B=2"}) {
		t.Fatalf("envSlice = %v, want sorted KEY=VALUE", got)
	}
	if envSlice(nil) != nil {
		t.Fatal("empty env should be nil")
	}
}

// saveCLIs updates the live list and the file, dropping incomplete or duplicate
// entries, and never leaves a machine with zero accounts.
func TestSaveCLIs(t *testing.T) {
	dir := t.TempDir()
	s := &Server{cfg: Config{DataDir: dir}, clis: defaultCLIs()}

	got := s.saveCLIs([]CLIProfile{
		{Name: "Claude", Bin: "claude"},
		{Name: "Work", Bin: "claude", Dir: "/w"},
		{Name: "", Bin: "x"},          // no name: dropped
		{Name: "Work", Bin: "claude"}, // dup name: dropped
	})
	if len(got) != 2 || got[1].Name != "Work" || got[1].Dir != "/w" {
		t.Fatalf("saveCLIs = %+v, want Claude + Work", got)
	}
	// Live list updated, and it survives a reload from the file.
	if len(s.cliList()) != 2 {
		t.Fatalf("live list not updated: %+v", s.cliList())
	}
	if reloaded := loadCLIs(dir); len(reloaded) != 2 || reloaded[1].Name != "Work" {
		t.Fatalf("not persisted: %+v", reloaded)
	}
	// An all-empty save falls back to the default, never zero.
	if got := s.saveCLIs(nil); len(got) != 1 || got[0].Name != "Claude" {
		t.Fatalf("empty save did not fall back to default: %+v", got)
	}
}
