package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hegade/kunai/internal/webui"
)

// A mobile browser paints its own chrome from the theme colour present when the
// page is parsed, and does not reliably re-read it when a script changes it
// afterwards. Measured on a real iPhone: the tag said #2a234e and the status bar
// was still black above a purple header. So the shell has to leave the server
// with the right value already in it.
func TestNightlyServesItsOwnThemeColour(t *testing.T) {
	restore := buildChannel
	t.Cleanup(func() { buildChannel = restore })

	s := &Server{pwa: webui.FS()}

	buildChannel = "stable"
	body := get(t, s, "/")
	if !strings.Contains(body, stableThemeColor) {
		t.Errorf("a stable build should serve %s:\n%s", stableThemeColor, head(body))
	}
	if strings.Contains(body, nightlyThemeColor) {
		t.Error("a stable build served the nightly colour")
	}

	buildChannel = "nightly"
	body = get(t, s, "/")
	if !strings.Contains(body, nightlyThemeColor) {
		t.Errorf("a nightly build should serve %s:\n%s", nightlyThemeColor, head(body))
	}
	if strings.Contains(body, stableThemeColor) {
		t.Error("the stable colour survived in a nightly shell")
	}
}

// An installed PWA takes its first paint from the manifest, so leaving that at
// the stable colour means a nightly install flashes black on every launch.
func TestNightlyManifestCarriesTheSameColour(t *testing.T) {
	restore := buildChannel
	t.Cleanup(func() { buildChannel = restore })

	s := &Server{pwa: webui.FS()}
	buildChannel = "nightly"

	body := get(t, s, "/manifest.webmanifest")
	if !strings.Contains(body, nightlyThemeColor) {
		t.Errorf("the manifest should carry %s:\n%s", nightlyThemeColor, head(body))
	}
	if strings.Contains(body, stableThemeColor) {
		t.Error("the manifest still carries the stable colour")
	}
}

// The rewrite must not touch anything else it happens to serve, and a stable
// build must be byte-identical to what it was before this existed.
func TestTheRewriteOnlyTouchesTheColour(t *testing.T) {
	restore := buildChannel
	t.Cleanup(func() { buildChannel = restore })

	doc := []byte(`<meta name="theme-color" content="#0b0b0c" /><p>#0b0b0cish</p>`)

	buildChannel = "stable"
	if got := string(themed(doc)); got != string(doc) {
		t.Errorf("a stable build rewrote its own shell:\n%s", got)
	}

	buildChannel = "nightly"
	got := string(themed(doc))
	if strings.Count(got, nightlyThemeColor) != 2 {
		// Both occurrences are the literal colour, so both are replaced; this
		// records that the substitution is textual rather than parsed, so a future
		// document must not use the colour string for something that is not a
		// theme colour.
		t.Errorf("expected every occurrence replaced, got:\n%s", got)
	}
	if !strings.Contains(got, `name="theme-color"`) {
		t.Errorf("the tag itself was damaged:\n%s", got)
	}
}

// Assets stay immutable-cacheable; the shell and manifest must not, or a build
// that changes the colour could never change it on a client that has been.
func TestCachingIsUnchangedByTheming(t *testing.T) {
	s := &Server{pwa: webui.FS()}
	for path, want := range map[string]string{
		"/":                     "no-cache",
		"/manifest.webmanifest": "no-cache",
	} {
		rec := httptest.NewRecorder()
		s.spaHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if got := rec.Header().Get("Cache-Control"); got != want {
			t.Errorf("%s: Cache-Control = %q, want %q", path, got, want)
		}
	}
}

func get(t *testing.T, s *Server, path string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	s.spaHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status %d", path, rec.Code)
	}
	return rec.Body.String()
}

func head(s string) string {
	if len(s) > 400 {
		return s[:400]
	}
	return s
}
