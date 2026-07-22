package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// A Provider is a proxy-backed model source: another model (Codex, Grok, Kimi,
// ...) reached by pointing the ordinary `claude` agent at a local CLIProxyAPI
// (https://github.com/router-for-me/CLIProxyAPI) instead of Anthropic. The agent
// itself is unchanged -- tools, permissions, file edits and bash all still run
// through the CLI -- only the model behind it changes. A Provider compiles down
// to a CLIProfile whose Env carries the proxy address, token, and per-slot model
// mapping, so every existing session / switch / loop path runs it unmodified.
// The only special-casing is skipping the OAuth sign-in and the /usage poll,
// which only mean something for a real Anthropic subscription.
type Provider struct {
	Name    string `json:"name"`     // display name and picker label, e.g. "Kimi K3"
	BaseURL string `json:"base_url"` // ANTHROPIC_BASE_URL, e.g. http://127.0.0.1:8317
	Token   string `json:"token"`    // ANTHROPIC_AUTH_TOKEN (the proxy's key; often a dummy)
	// Models maps a Claude model slot (opus|sonnet|haiku) to the upstream model
	// string the proxy routes to. kunai always spawns with --model <slot>, and the
	// CLI resolves that slot through these env vars, so mapping all three to the
	// same model makes every in-app model pick land on this provider's model.
	Models map[string]string `json:"models,omitempty"`
}

// providerSlotEnv names the env vars claude 2.x reads to map its model slots to
// upstream models (the 1.x ANTHROPIC_MODEL/ANTHROPIC_SMALL_FAST_MODEL pair is not
// used; the installed CLI is 2.x).
var providerSlotEnv = map[string]string{
	"opus":   "ANTHROPIC_DEFAULT_OPUS_MODEL",
	"sonnet": "ANTHROPIC_DEFAULT_SONNET_MODEL",
	"haiku":  "ANTHROPIC_DEFAULT_HAIKU_MODEL",
}

// profile compiles the provider into a runnable CLIProfile: the ordinary `claude`
// binary, the proxy env, and its own config dir so its transcripts (and the
// Recent list) stay separate from the real Claude account.
func (p Provider) profile(dataDir string) CLIProfile {
	env := map[string]string{}
	if p.BaseURL != "" {
		env["ANTHROPIC_BASE_URL"] = p.BaseURL
	}
	if p.Token != "" {
		env["ANTHROPIC_AUTH_TOKEN"] = p.Token
	}
	for slot, model := range p.Models {
		if model == "" {
			continue
		}
		if key := providerSlotEnv[slot]; key != "" {
			env[key] = model
		}
	}
	dir := ""
	if dataDir != "" {
		dir = filepath.Join(dataDir, "providers", providerSlug(p.Name))
	}
	return CLIProfile{Name: p.Name, Bin: "claude", Env: env, Dir: dir}
}

// providerDisplayModel is the model to show for a provider session: the opus
// slot (kunai's default spawn slot) if set, else any mapped model, else "".
func providerDisplayModel(p Provider) string {
	if m := p.Models["opus"]; m != "" {
		return m
	}
	for _, slot := range []string{"sonnet", "haiku"} {
		if m := p.Models[slot]; m != "" {
			return m
		}
	}
	return ""
}

// isProxyProfile reports whether a resolved profile is proxy-backed, true exactly
// when it carries a base-URL override. Proxy profiles skip the OAuth sign-in
// preflight and the /usage poll, both of which assume a real Anthropic account.
func isProxyProfile(p CLIProfile) bool {
	return p.Env["ANTHROPIC_BASE_URL"] != ""
}

// providerSlug is a filesystem-safe folder name for a provider's config dir.
func providerSlug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_' || r == '.':
			b.WriteRune('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		s = "provider"
	}
	return s
}

// providerStore persists the provider list to providers.json, mirroring
// machineStore. It is kept separate from clis.json so the raw account editor
// never clobbers a provider and vice versa.
type providerStore struct {
	mu   sync.Mutex
	path string
	list []Provider
}

func newProviderStore(path string) *providerStore {
	s := &providerStore{path: path}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &s.list)
	}
	return s
}

func (s *providerStore) all() []Provider {
	s.mu.Lock()
	defer s.mu.Unlock()
	// A non-nil slice so the GET handler serializes [] (not null) for the client.
	out := make([]Provider, len(s.list))
	copy(out, s.list)
	return out
}

// save upserts a provider by name (case-insensitive) and persists.
func (s *providerStore) save(p Provider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, x := range s.list {
		if strings.EqualFold(x.Name, p.Name) {
			s.list[i] = p
			s.saveLocked()
			return
		}
	}
	s.list = append(s.list, p)
	s.saveLocked()
}

func (s *providerStore) remove(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.list[:0]
	for _, x := range s.list {
		if !strings.EqualFold(x.Name, name) {
			out = append(out, x)
		}
	}
	s.list = out
	s.saveLocked()
}

func (s *providerStore) saveLocked() {
	if s.path == "" {
		return
	}
	if b, err := json.MarshalIndent(s.list, "", "  "); err == nil {
		_ = os.WriteFile(s.path, b, 0o600)
	}
}

// --- handlers ----------------------------------------------------------------

// handleProviders lists providers (GET) or upserts one (POST). Machine-local,
// like clis. A POST creates the provider's config dir so the CLI has somewhere to
// write, and refuses a name that collides with a real account (which would shadow
// it in resolveCLI, since accounts are matched first).
func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, s.providers.all())
		return
	}
	var p Provider
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	p.BaseURL = strings.TrimSpace(p.BaseURL) // blank = use the managed sidecar
	if p.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	for _, c := range s.cliList() {
		if strings.EqualFold(c.Name, p.Name) {
			writeErr(w, http.StatusConflict, "that name is already used by an account")
			return
		}
	}
	if dir := s.providerProfile(p).configDir(); dir != "" {
		_ = os.MkdirAll(dir, 0o700)
	}
	s.providers.save(p)
	// A provider that relies on the managed sidecar needs it running.
	if p.BaseURL == "" {
		go s.ensureCLIProxy()
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	s.providers.remove(r.PathValue("name"))
	w.WriteHeader(http.StatusNoContent)
}

// providerList is the nil-safe snapshot the resolve/list paths use, so a Server
// built without newProviderStore (some tests) does not panic.
func (s *Server) providerList() []Provider {
	if s.providers == nil {
		return nil
	}
	return s.providers.all()
}

// isProviderName reports whether the given CLI name is a proxy provider (as
// opposed to a real Claude account), so the create/switch paths know to make
// the sidecar ready first.
func (s *Server) isProviderName(name string) bool {
	for _, p := range s.providerList() {
		if p.Name == name {
			return true
		}
	}
	return false
}

// providerProfile compiles a provider to a runnable profile, defaulting the proxy
// address and token to the managed sidecar when the provider left them blank
// (the zero-config path: the owner picks only a model, kunai supplies the proxy).
func (s *Server) providerProfile(p Provider) CLIProfile {
	if p.BaseURL == "" && s.cliproxy != nil {
		p.BaseURL = s.cliproxy.BaseURL()
	}
	if p.Token == "" && s.cliproxy != nil {
		p.Token = s.cliproxy.APIKey()
	}
	return p.profile(s.cfg.DataDir)
}
