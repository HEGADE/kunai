package server

import (
	"os"
	"path/filepath"
	"testing"
)

// stubGrokToken makes the ~/.grok credential check report present/absent
// deterministically. The managers below use an empty dataDir, so tokenPath() skips
// the sidecar-dir glob and falls through to this stubbed home-dir check.
func stubGrokToken(t *testing.T, present bool) {
	t.Helper()
	orig := grokTokenPathFn
	grokTokenPathFn = func() (string, bool) {
		if present {
			return "/fake/.grok/auth.json", true
		}
		return "", false
	}
	t.Cleanup(func() { grokTokenPathFn = orig })
}

func TestNativeGrokRouting(t *testing.T) {
	grok := Provider{Name: "Grok", Models: map[string]string{"opus": "grok-4.5"}}
	codex := Provider{Name: "Codex", Models: map[string]string{"opus": "gpt-5.5"}}

	if !isGrokModel("grok-4.5") || isGrokModel("gpt-5.5") {
		t.Fatal("isGrokModel wrong")
	}

	// native grok on WITH a login: a Grok provider is native; all-Grok needs no sidecar.
	stubGrokToken(t, true)
	on := &Server{nativeGrok: newNativeGrokManager("")}
	on.providers = &providerStore{list: []Provider{grok}}
	if !on.providerUsesNativeGrok("Grok") {
		t.Error("Grok with a login should route to native grok")
	}
	if on.anyProviderNeedsSidecar() {
		t.Error("all-Grok native setup with a login should not need the sidecar")
	}
	// A Codex provider still needs the sidecar when only native grok is on.
	on.providers = &providerStore{list: []Provider{grok, codex}}
	if !on.anyProviderNeedsSidecar() {
		t.Error("Codex needs the sidecar when only native grok is enabled")
	}
}

// The load-bearing fix: with -native-grok on but NO grok CLI login, the provider
// must NOT be claimed by native (so the create path readies the sidecar instead of
// leaving the session with nothing to serve it -> empty replies).
func TestNativeGrokWithoutLoginFallsBack(t *testing.T) {
	stubGrokToken(t, false)
	s := &Server{nativeGrok: newNativeGrokManager("")}
	s.providers = &providerStore{list: []Provider{{Name: "Grok", Models: map[string]string{"opus": "grok-4.5"}}}}
	if s.providerUsesNativeGrok("Grok") {
		t.Error("Grok without a login must not be claimed by native")
	}
	if !s.anyProviderNeedsSidecar() {
		t.Error("Grok without a login must fall back to needing the sidecar")
	}
}

func TestNativeGrokOffNeverRoutes(t *testing.T) {
	stubGrokToken(t, true) // even with a login, an off manager routes nothing
	off := &Server{}
	off.providers = &providerStore{list: []Provider{{Name: "Grok", Models: map[string]string{"opus": "grok-4.5"}}}}
	if off.providerUsesNativeGrok("Grok") {
		t.Error("native off: Grok must not route native")
	}
}

// The app-login fix: a Grok credential written to the sidecar auth dir (the shape
// the in-app login produces) must be found by native grok's tokenPath, so the
// session routes native instead of falling back to the hanging sidecar.
func TestNativeGrokFindsSidecarAppLogin(t *testing.T) {
	dir := t.TempDir()
	authDir := filepath.Join(dir, "cliproxy", "auth")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(authDir, "xai-abc-user@example.com.json"), []byte(`{"access_token":"tok","type":"xai"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := newNativeGrokManager(dir)
	if p, ok := m.tokenPath(); !ok || p == "" {
		t.Error("native grok did not find the sidecar app-login credential")
	}
}
