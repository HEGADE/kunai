package server

import (
	"os"
	"strings"
	"testing"
)

// Every platform kunai targets must have a pinned asset with a full sha256, and
// an unsupported platform must report ok=false rather than guess a binary.
func TestCLIProxyAssetsPinned(t *testing.T) {
	for _, plat := range []struct{ goos, goarch string }{
		{"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "amd64"}, {"darwin", "arm64"},
	} {
		a, ok := assetFor(plat.goos, plat.goarch)
		if !ok {
			t.Errorf("%s/%s: no pinned asset", plat.goos, plat.goarch)
			continue
		}
		if !strings.Contains(a.name, cliproxyVersion) || !strings.HasSuffix(a.name, ".tar.gz") {
			t.Errorf("%s/%s: bad asset name %q", plat.goos, plat.goarch, a.name)
		}
		if len(a.sha256) != 64 {
			t.Errorf("%s/%s: sha256 must be 64 hex chars, got %d", plat.goos, plat.goarch, len(a.sha256))
		}
	}
	if _, ok := assetFor("plan9", "mips"); ok {
		t.Error("unsupported platform should be ok=false")
	}
}

// The config the sidecar runs from must bind the chosen port, point at the
// manager's own auth dir, and carry its api key (so providers can authenticate).
func TestCLIProxyWriteConfig(t *testing.T) {
	m := newCLIProxyManager(t.TempDir())
	if m == nil {
		t.Fatal("manager nil for a real data dir")
	}
	if err := m.writeConfig(54321); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(m.configPath())
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(b)
	for _, want := range []string{"port: 54321", m.authDir(), m.APIKey()} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config missing %q\n---\n%s", want, cfg)
		}
	}
	// The auth dir must exist after writeConfig (the CLI needs somewhere to write).
	if fi, err := os.Stat(m.authDir()); err != nil || !fi.IsDir() {
		t.Errorf("auth dir not created: %v", err)
	}
}

// The api key is stable across manager instances on the same data dir, so a
// restart keeps talking to the same sidecar auth.
func TestCLIProxyKeyStable(t *testing.T) {
	dir := t.TempDir()
	k1 := newCLIProxyManager(dir).APIKey()
	k2 := newCLIProxyManager(dir).APIKey()
	if k1 == "" || k1 != k2 {
		t.Errorf("api key not stable: %q vs %q", k1, k2)
	}
}

// No data dir means no managed sidecar (dev/ephemeral), not a crash.
func TestCLIProxyNilWithoutDataDir(t *testing.T) {
	if newCLIProxyManager("") != nil {
		t.Error("expected nil manager without a data dir")
	}
}
