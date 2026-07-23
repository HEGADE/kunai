package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// baseURLIsLocalOrEmpty reports whether a provider base_url is unset or points at a
// loopback address (the managed sidecar). Both mean the native proxy may take over;
// a non-loopback host is treated as a deliberate external override and left alone.
func baseURLIsLocalOrEmpty(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch u.Hostname() {
	case "127.0.0.1", "localhost", "::1", "[::1]":
		return true
	}
	return false
}

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

// handleSetProviderModel changes which upstream model a provider session runs on.
// The model is baked into the spawn env (the slot mapping), so this updates the
// provider's saved mapping and respawns the session under it -- the conversation
// resumes from the transcript, and future sessions on the provider use the new
// model too. Only valid for a session currently on a provider.
func (s *Server) handleSetProviderModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, ok := s.mgr.Get(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	var body struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	model := strings.TrimSpace(body.Model)
	if model == "" {
		writeErr(w, http.StatusBadRequest, "model is required")
		return
	}
	p := s.providerNamed(sess.Meta().CLI)
	if p == nil {
		writeErr(w, http.StatusBadRequest, "this session is not on a provider")
		return
	}
	// Map every slot to the new model, so whatever slot the CLI spawns under lands
	// on it, and persist so the provider's next session uses it too.
	p.Models = map[string]string{"opus": model, "sonnet": model, "haiku": model}
	s.providers.save(*p)
	s.ensureCLIProxyReady()
	prof := s.providerProfile(*p)
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	restarted, err := s.mgr.RestartWithAccount(ctx, id, prof.Name, prof.Bin, prof.effectiveEnv(), loadTranscriptTurns)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.armSession(restarted)
	writeJSON(w, http.StatusOK, restarted.Meta())
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
	return s.providerNamed(name) != nil
}

// providerNamed returns the provider with this name, or nil.
func (s *Server) providerNamed(name string) *Provider {
	for _, p := range s.providerList() {
		if strings.EqualFold(p.Name, name) {
			p := p
			return &p
		}
	}
	return nil
}

// providerProfile compiles a provider to a runnable profile, defaulting the proxy
// address and token to the managed sidecar when the provider left them blank
// (the zero-config path: the owner picks only a model, kunai supplies the proxy).
func (s *Server) providerProfile(p Provider) CLIProfile {
	ctx := s.baseCtx
	if ctx == nil {
		ctx = context.Background()
	}
	model := providerDisplayModel(p)
	// The native in-process proxy takes precedence over an empty OR loopback
	// base_url: the CLIProxyAPI sidecar is the legacy fallback, and it hangs for
	// minutes on a 429 with a cryptic "credentials cooling down" message, so a
	// native-capable model must go native whenever its login is present -- even if a
	// stale sidecar base_url was saved on the provider (which used to route around
	// native entirely). A genuine external base_url is honored as an explicit
	// override. Native still needs a bound port before its URL is baked, or the
	// session spawns pointing nowhere.
	if s.nativeCodex != nil && isCodexModel(model) && baseURLIsLocalOrEmpty(p.BaseURL) {
		if err := s.nativeCodex.start(ctx); err != nil {
			log.Printf("native codex: %v (falling back to sidecar)", err)
		} else if base := s.nativeCodex.BaseURL(); base != "" {
			p.BaseURL = base
			p.Token = s.nativeCodex.APIKey()
			log.Printf("provider %q -> native codex proxy (%s)", p.Name, model)
			return p.profile(s.cfg.DataDir)
		}
	}
	if s.nativeGrok != nil && isGrokModel(model) && baseURLIsLocalOrEmpty(p.BaseURL) {
		if err := s.nativeGrok.start(ctx); err != nil {
			log.Printf("native grok: %v (falling back to sidecar)", err)
		} else if base := s.nativeGrok.BaseURL(); base != "" {
			p.BaseURL = base
			p.Token = s.nativeGrok.APIKey()
			log.Printf("provider %q -> native grok proxy (%s)", p.Name, model)
			return p.profile(s.cfg.DataDir)
		}
	}
	if p.BaseURL == "" && s.cliproxy != nil {
		log.Printf("provider %q -> CLIProxyAPI sidecar (%s)", p.Name, model)
		// Reaching here means the sidecar is the proxy for this session (either a
		// plain sidecar provider, or a native provider whose login was missing so it
		// degraded). Make sure the sidecar has a bound port before baking its URL, or
		// the session would spawn with an empty base URL and reply with nothing.
		s.ensureCLIProxyReady()
		p.BaseURL = s.cliproxy.BaseURL()
	}
	if p.Token == "" && s.cliproxy != nil {
		p.Token = s.cliproxy.APIKey()
	}
	return p.profile(s.cfg.DataDir)
}
