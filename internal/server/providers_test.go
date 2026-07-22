package server

import (
	"path/filepath"
	"testing"
)

// A provider compiles to a claude profile whose env points the agent at the
// proxy and maps every model slot, so any in-app model pick lands on it.
func TestProviderProfileEnv(t *testing.T) {
	p := Provider{
		Name:    "Kimi K3",
		BaseURL: "http://127.0.0.1:8317",
		Token:   "sk-dummy",
		Models:  map[string]string{"opus": "kimi-k3", "sonnet": "kimi-k3", "haiku": "kimi-k3"},
	}
	prof := p.profile("/data")
	if prof.Bin != "claude" {
		t.Fatalf("bin = %q, want claude", prof.Bin)
	}
	want := map[string]string{
		"ANTHROPIC_BASE_URL":             "http://127.0.0.1:8317",
		"ANTHROPIC_AUTH_TOKEN":           "sk-dummy",
		"ANTHROPIC_DEFAULT_OPUS_MODEL":   "kimi-k3",
		"ANTHROPIC_DEFAULT_SONNET_MODEL": "kimi-k3",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":  "kimi-k3",
	}
	for k, v := range want {
		if prof.Env[k] != v {
			t.Errorf("env[%s] = %q, want %q", k, prof.Env[k], v)
		}
	}
	// Its own config dir keeps transcripts separate from the real account.
	if got, want := prof.configDir(), filepath.Join("/data", "providers", "kimi-k3"); got != want {
		t.Errorf("configDir = %q, want %q", got, want)
	}
	// effectiveEnv must fold that dir into CLAUDE_CONFIG_DIR (the whole point of Dir).
	if prof.effectiveEnv()["CLAUDE_CONFIG_DIR"] != prof.Dir {
		t.Errorf("effectiveEnv missing CLAUDE_CONFIG_DIR=%q", prof.Dir)
	}
	// And it must read as proxy-backed, which gates the authOK/usage skips.
	if !isProxyProfile(prof) {
		t.Error("compiled provider not detected as proxy-backed")
	}
}

// An empty model slot is skipped, and a real Claude account is never proxy-backed.
func TestProviderProfileSkipsAndDetection(t *testing.T) {
	p := Provider{Name: "Codex", BaseURL: "http://127.0.0.1:8317", Models: map[string]string{"opus": "gpt-5-codex(high)", "sonnet": ""}}
	prof := p.profile("/data")
	if _, ok := prof.Env["ANTHROPIC_DEFAULT_SONNET_MODEL"]; ok {
		t.Error("empty sonnet slot should not set an env var")
	}
	if prof.Env["ANTHROPIC_DEFAULT_OPUS_MODEL"] != "gpt-5-codex(high)" {
		t.Errorf("opus slot = %q", prof.Env["ANTHROPIC_DEFAULT_OPUS_MODEL"])
	}
	if isProxyProfile(CLIProfile{Name: "Claude", Bin: "claude"}) {
		t.Error("a real account must not be detected as proxy-backed")
	}
}

// The store upserts by name (case-insensitive), removes, and round-trips to disk.
func TestProviderStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "providers.json")
	st := newProviderStore(path)
	st.save(Provider{Name: "Grok", BaseURL: "http://127.0.0.1:8317", Models: map[string]string{"opus": "grok-4.5"}})
	st.save(Provider{Name: "grok", BaseURL: "http://127.0.0.1:9999"}) // same name, different case
	if all := st.all(); len(all) != 1 {
		t.Fatalf("want 1 after case-insensitive upsert, got %d", len(all))
	}
	if st.all()[0].BaseURL != "http://127.0.0.1:9999" {
		t.Errorf("upsert did not replace: %q", st.all()[0].BaseURL)
	}
	// Reload from disk sees the persisted entry.
	if reloaded := newProviderStore(path); len(reloaded.all()) != 1 {
		t.Fatalf("persisted list not reloaded: %d", len(reloaded.all()))
	}
	st.remove("GROK")
	if len(st.all()) != 0 {
		t.Errorf("remove (case-insensitive) left %d", len(st.all()))
	}
}

// providerSlug produces filesystem-safe, non-empty folder names.
func TestProviderSlug(t *testing.T) {
	cases := map[string]string{
		"Kimi K3":  "kimi-k3",
		"GPT-5.6":  "gpt-5-6", // '.' folds to '-' for a safe folder name
		"  Grok  ": "grok",
		"!!!":      "provider",
	}
	for in, want := range cases {
		if got := providerSlug(in); got != want {
			t.Errorf("providerSlug(%q) = %q, want %q", in, got, want)
		}
	}
}
