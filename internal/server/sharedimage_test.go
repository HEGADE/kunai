package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hegade/kunai/internal/share"
)

// gateWithImages stands up a gate serving one generated image, and returns the
// live token for it.
func gateWithImages(t *testing.T) (*shareGate, string, string) {
	t.Helper()
	dir := t.TempDir()
	imgs := filepath.Join(dir, "generated-images")
	if err := os.MkdirAll(imgs, 0o700); err != nil {
		t.Fatal(err)
	}
	png := filepath.Join(imgs, "20260811-000000-abc.png")
	if err := os.WriteFile(png, []byte("\x89PNG\r\n\x1a\npretend"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := share.NewStore(filepath.Join(dir, "shares.json"))
	sh, err := store.Create(share.Share{SessionID: "sess-1", Tier: share.TierWork}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return newShareGate(store, noSessions{}, testPWA{}, "", imgs, nil), sh.Token, png
}

func getGate(g *shareGate, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	g.mux().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestAGuestSeesAPictureTheConversationDrew(t *testing.T) {
	g, token, png := gateWithImages(t)
	w := getGate(g, "/api/share/"+token+"/image?path="+png)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("content-type %q", ct)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("nosniff missing on a route that answers the public internet")
	}
}

// The whole point of the route being separate: it serves ONE directory, and the
// directory a guest names is discarded rather than checked, so traversal is
// inexpressible rather than merely refused.
func TestTheGuestImageRouteReachesNothingElse(t *testing.T) {
	g, token, _ := gateWithImages(t)
	outside := filepath.Join(t.TempDir(), "secret.png")
	if err := os.WriteFile(outside, []byte("\x89PNG\r\n\x1a\nsecret"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		outside,                        // an absolute path somewhere else entirely
		"/etc/passwd.png",              // a system file wearing the right extension
		"../../../../etc/hosts.png",    // climbing out
		"/home/someone/project/ui.png", // an image in a project, which is the case
	} { //                                 the owner-only route exists to protect
		w := getGate(g, "/api/share/"+token+"/image?path="+path)
		if w.Code == http.StatusOK {
			t.Errorf("%s was served to a guest", path)
		}
	}
}

func TestTheGuestImageRouteRefusesSVGAndNonImages(t *testing.T) {
	g, token, _ := gateWithImages(t)
	for _, name := range []string{"map.svg", "notes.txt", "run.sh"} {
		w := getGate(g, "/api/share/"+token+"/image?path=/x/"+name)
		if w.Code == http.StatusOK {
			t.Errorf("%s was served", name)
		}
	}
}

func TestTheGuestImageRouteNeedsALiveToken(t *testing.T) {
	g, _, png := gateWithImages(t)
	for _, token := range []string{"", "not-a-token", "deadbeef"} {
		w := getGate(g, "/api/share/"+token+"/image?path="+png)
		if w.Code == http.StatusOK {
			t.Errorf("token %q was accepted", token)
		}
	}
}

// A gate with no images directory serves none, rather than falling back to
// anything: an install with no data dir must not become a file server.
func TestAGateWithNoImagesDirectoryServesNoImages(t *testing.T) {
	dir := t.TempDir()
	store := share.NewStore(filepath.Join(dir, "shares.json"))
	sh, err := store.Create(share.Share{SessionID: "sess-1", Tier: share.TierWork}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	g := newShareGate(store, noSessions{}, testPWA{}, "", "", nil)
	if w := getGate(g, "/api/share/"+sh.Token+"/image?path=/anything.png"); w.Code == http.StatusOK {
		t.Errorf("status %d, want a refusal", w.Code)
	}
}
