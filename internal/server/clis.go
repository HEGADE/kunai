package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// A CLI profile is a named Claude CLI: a display name, the binary to run, and any
// extra environment it needs. It exists so one machine can drive more than one
// Claude account. The account separation is the CLI's own concern (a different
// binary, or the same binary pointed at another auth via CLAUDE_CONFIG_DIR in
// Env); kunai just runs the profile you pick. The first profile is the default.
type CLIProfile struct {
	Name string            `json:"name"`
	Bin  string            `json:"bin"`
	Env  map[string]string `json:"env,omitempty"`
}

// defaultCLIs is what a machine has until clis.json says otherwise: the one
// ordinary `claude` on PATH.
func defaultCLIs() []CLIProfile {
	return []CLIProfile{{Name: "Claude", Bin: "claude"}}
}

// loadCLIs reads the profile list from clis.json in the data dir, writing a
// starter file the owner can edit if none exists. A missing or unreadable file,
// or an empty list, falls back to the single default so a session can always
// start.
func loadCLIs(dataDir string) []CLIProfile {
	if dataDir == "" {
		return defaultCLIs()
	}
	path := filepath.Join(dataDir, "clis.json")
	b, err := os.ReadFile(path)
	if err != nil {
		// First run: drop a template so the format is discoverable.
		if def, _ := json.MarshalIndent(defaultCLIs(), "", "  "); def != nil {
			_ = os.WriteFile(path, def, 0o600)
		}
		return defaultCLIs()
	}
	var clis []CLIProfile
	if json.Unmarshal(b, &clis) != nil {
		return defaultCLIs()
	}
	// Drop entries missing the essentials rather than trusting a hand-edited file.
	out := clis[:0]
	for _, c := range clis {
		if c.Name != "" && c.Bin != "" {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return defaultCLIs()
	}
	return out
}

// resolveCLI returns the profile with the given name, or the default (first) when
// the name is empty or unknown. A session must always get a runnable binary, so
// this never fails.
func (s *Server) resolveCLI(name string) CLIProfile {
	for _, c := range s.clis {
		if c.Name == name {
			return c
		}
	}
	return s.clis[0]
}

// cliNames lists the profile names for the client's picker, in config order.
func (s *Server) cliNames() []string {
	names := make([]string, 0, len(s.clis))
	for _, c := range s.clis {
		names = append(names, c.Name)
	}
	return names
}

// envSlice turns a profile's env map into the KEY=VALUE form exec wants, sorted
// so the process environment is deterministic.
func envSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}
