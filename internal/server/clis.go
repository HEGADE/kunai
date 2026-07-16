package server

import (
	"encoding/json"
	"net/http"
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
	// Dir is the account's Claude config dir, where it keeps its auth and its
	// session transcripts. It is what actually separates two accounts. Optional
	// shorthand for setting CLAUDE_CONFIG_DIR in Env; either one lets the Recent
	// list find this account's past sessions. Empty means the default (~/.claude).
	Dir string `json:"dir,omitempty"`
}

// configDir is where this account keeps its transcripts and auth: the explicit
// Dir, else a CLAUDE_CONFIG_DIR in Env, else "" for the default (~/.claude).
func (p CLIProfile) configDir() string {
	if p.Dir != "" {
		return p.Dir
	}
	return p.Env["CLAUDE_CONFIG_DIR"]
}

// effectiveEnv is the env handed to the driver. It folds the Dir shorthand into
// CLAUDE_CONFIG_DIR so the CLI actually runs against that account; without this a
// profile that used Dir would still auth as the default account.
func (p CLIProfile) effectiveEnv() map[string]string {
	if p.Dir == "" {
		return p.Env
	}
	env := make(map[string]string, len(p.Env)+1)
	for k, v := range p.Env {
		env[k] = v
	}
	env["CLAUDE_CONFIG_DIR"] = p.Dir
	return env
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

// cliList returns a snapshot of the accounts under the read lock, so the Accounts
// settings can edit the list live without racing a session start.
func (s *Server) cliList() []CLIProfile {
	s.clisMu.RLock()
	defer s.clisMu.RUnlock()
	return append([]CLIProfile(nil), s.clis...)
}

// resolveCLI returns the profile with the given name, or the default (first) when
// the name is empty or unknown. A session must always get a runnable binary, so
// this never fails.
func (s *Server) resolveCLI(name string) CLIProfile {
	list := s.cliList()
	for _, c := range list {
		if c.Name == name {
			return c
		}
	}
	return list[0]
}

// cliNames lists the profile names for the client's picker, in config order.
func (s *Server) cliNames() []string {
	list := s.cliList()
	names := make([]string, 0, len(list))
	for _, c := range list {
		names = append(names, c.Name)
	}
	return names
}

// saveCLIs replaces the account list live (no restart) and persists it. The list
// is sanitized the same way loadCLIs treats the file: entries need a name and a
// binary, and an empty result falls back to the single default.
func (s *Server) saveCLIs(clis []CLIProfile) []CLIProfile {
	clean := clis[:0]
	seen := map[string]bool{}
	for _, c := range clis {
		if c.Name == "" || c.Bin == "" || seen[c.Name] {
			continue
		}
		seen[c.Name] = true
		clean = append(clean, c)
	}
	if len(clean) == 0 {
		clean = defaultCLIs()
	}
	s.clisMu.Lock()
	s.clis = clean
	s.clisMu.Unlock()
	if s.cfg.DataDir != "" {
		if b, err := json.MarshalIndent(clean, "", "  "); err == nil {
			_ = os.WriteFile(filepath.Join(s.cfg.DataDir, "clis.json"), b, 0o600)
		}
	}
	return clean
}

// handleCLIs lists the accounts (GET) or replaces the whole list (POST). The list
// is machine-local, so each machine's Settings edits its own.
func (s *Server) handleCLIs(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, s.cliList())
		return
	}
	var clis []CLIProfile
	if err := json.NewDecoder(r.Body).Decode(&clis); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	writeJSON(w, http.StatusOK, s.saveCLIs(clis))
}

// accountRoot pairs an account name with the transcript folder to scan for it.
type accountRoot struct {
	name string
	root string
}

// accountRoots lists the transcript folders to scan for the Recent list, one per
// distinct account config dir, each tagged with the account that owns it. The
// default (~/.claude) is always covered, so the personal account is never lost
// even if every profile pins a custom dir.
func (s *Server) accountRoots() []accountRoot {
	seen := map[string]bool{}
	var roots []accountRoot
	for _, c := range s.cliList() {
		root := claudeRoot(c.configDir())
		if seen[root] {
			continue
		}
		seen[root] = true
		roots = append(roots, accountRoot{name: c.Name, root: root})
	}
	if def := claudeRoot(""); !seen[def] {
		roots = append(roots, accountRoot{name: "", root: def})
	}
	return roots
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
