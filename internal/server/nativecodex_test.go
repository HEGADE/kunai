package server

import (
	"os"
	"path/filepath"
	"testing"
)

// codexMgrWithToken builds a native codex manager whose data dir already holds a
// codex-*.json, so its credential check passes deterministically.
func codexMgrWithToken(t *testing.T) *nativeCodexManager {
	t.Helper()
	dir := t.TempDir()
	authDir := filepath.Join(dir, "cliproxy", "auth")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(authDir, "codex-test.json"), []byte(`{"access_token":"t"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return newNativeCodexManager(dir)
}

// The routing logic that decides sidecar vs native for a provider, unit-tested
// without spawning anything. The live path (real Codex) is exercised by the
// internal/cliproxy/codex live tests.
func TestNativeCodexRouting(t *testing.T) {
	// Isolate HOME so a real ~/.codex on the dev box never leaks into the check.
	t.Setenv("HOME", t.TempDir())

	codex := Provider{Name: "Codex", Models: map[string]string{"opus": "gpt-5.5"}}
	grok := Provider{Name: "Grok", Models: map[string]string{"opus": "grok-4.5"}}
	external := Provider{Name: "Ext", BaseURL: "http://127.0.0.1:9999", Models: map[string]string{"opus": "gpt-5.5"}}

	// With native OFF, nothing is native and Codex needs the sidecar.
	off := &Server{}
	off.providers = &providerStore{list: []Provider{codex}}
	if off.providerUsesNative("Codex") {
		t.Error("native off: Codex should not use native")
	}
	if !off.anyProviderNeedsSidecar() {
		t.Error("native off: a Codex provider should need the sidecar")
	}

	// With native ON and a token, a Codex provider is native; a Grok provider still
	// needs the sidecar.
	on := &Server{nativeCodex: codexMgrWithToken(t)}
	on.providers = &providerStore{list: []Provider{codex}}
	if !on.providerUsesNative("Codex") {
		t.Error("native on + token: Codex should use native")
	}
	if on.anyProviderNeedsSidecar() {
		t.Error("native on + token: an all-Codex setup should NOT need the sidecar (the 40MB is the point)")
	}

	on.providers = &providerStore{list: []Provider{codex, grok}}
	if !on.anyProviderNeedsSidecar() {
		t.Error("native on + Grok: Grok still needs the sidecar")
	}
	if on.providerUsesNative("Grok") {
		t.Error("Grok is not a Codex model; must not route to native")
	}

	// An external-base provider never needs our sidecar and isn't native.
	on.providers = &providerStore{list: []Provider{external}}
	if on.anyProviderNeedsSidecar() {
		t.Error("external-base provider should not need our sidecar")
	}
	if on.providerUsesNative("Ext") {
		t.Error("external-base provider should not route to native (it has its own base)")
	}
}

// The fix: native codex ON but NO codex token must fall back to the sidecar, not
// claim the session and leave it unserved.
func TestNativeCodexWithoutTokenFallsBack(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no ~/.codex either
	s := &Server{nativeCodex: newNativeCodexManager(t.TempDir())}
	s.providers = &providerStore{list: []Provider{{Name: "Codex", Models: map[string]string{"opus": "gpt-5.5"}}}}
	if s.providerUsesNative("Codex") {
		t.Error("Codex without a token must not be claimed by native")
	}
	if !s.anyProviderNeedsSidecar() {
		t.Error("Codex without a token must fall back to needing the sidecar")
	}
}

// providerProfile bakes the native proxy's base URL for a Codex provider once the
// native proxy has a bound port.
func TestProviderProfileUsesNativeWhenStarted(t *testing.T) {
	s := &Server{nativeCodex: newNativeCodexManager("")}
	s.providers = &providerStore{}
	// Simulate a started native proxy by giving it a port directly.
	s.nativeCodex.port = 12345
	s.nativeCodex.started = true

	p := Provider{Name: "Codex", Models: map[string]string{"opus": "gpt-5.5"}}
	prof := s.providerProfile(p)
	if got := prof.Env["ANTHROPIC_BASE_URL"]; got != "http://127.0.0.1:12345" {
		t.Errorf("ANTHROPIC_BASE_URL = %q, want the native proxy", got)
	}
	if prof.Env["ANTHROPIC_AUTH_TOKEN"] == "" {
		t.Error("expected a non-empty auth token for claude")
	}
}
