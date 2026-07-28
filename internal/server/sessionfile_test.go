package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hegade/kunai/internal/session"
)

// The route reads files off the owner's disk on request, so what it refuses is
// the whole of its safety.
func TestSessionFileServesOnlyImagesInsideTheSession(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "shot.png"), []byte("\x89PNG\r\n\x1a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "map.svg"), []byte("<svg onload=alert(1)>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.png"), []byte("\x89PNG"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A symlink INSIDE the session pointing out of it: the case a string compare
	// would wave through and pathguard exists to catch.
	_ = os.Symlink(filepath.Join(outside, "secret.png"), filepath.Join(root, "link.png"))

	mgr := session.NewManager()
	sess, err := mgr.Create(t.Context(), session.CreateOptions{Cwd: root, Bin: "/bin/true"})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	s := &Server{mgr: mgr}
	get := func(q string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/sessions/"+sess.ID+"/file?path="+q, nil)
		req.SetPathValue("id", sess.ID)
		s.handleSessionFile(rec, req)
		return rec.Code
	}

	if code := get("shot.png"); code != http.StatusOK {
		t.Errorf("an image inside the session returned %d, want 200", code)
	}
	for _, c := range []struct {
		what string
		q    string
	}{
		{"a text file", "notes.txt"},
		{"an SVG, which is a scriptable document served from kunai's own origin", "map.svg"},
		{"a file outside the session", filepath.Join(outside, "secret.png")},
		{"a traversal out of the session", "../" + filepath.Base(outside) + "/secret.png"},
		{"a symlink pointing out of the session", "link.png"},
		{"nothing at all", ""},
	} {
		if code := get(c.q); code == http.StatusOK {
			t.Errorf("%s was served (200); it must be refused", c.what)
		}
	}
}
