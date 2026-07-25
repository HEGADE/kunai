package telegram

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A chat is authorised to drive the agent, but /get must still not become a way to
// read the whole filesystem: the path is confined to the session's own folder. These
// are the escapes worth being explicit about.
func TestResolveInsideRefusesEscapes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "chart.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, ok := range []string{"chart.png", "./chart.png", "sub"} {
		if _, err := resolveInside(root, ok); err != nil {
			t.Errorf("%q should be allowed: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"../outside.txt",
		"../../etc/passwd",
		"sub/../../escape",
		"/etc/passwd",
		filepath.Join(filepath.Dir(root), "sibling"),
	} {
		if _, err := resolveInside(root, bad); err == nil {
			t.Errorf("%q escaped the session folder and was allowed", bad)
		}
	}
}

// A symlink pointing out of the project must not be a way around the confinement:
// resolving it before the check is what closes that door.
func TestResolveInsideFollowsSymlinksBeforeChecking(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := resolveInside(root, "link.txt"); err == nil {
		t.Error("a symlink out of the project was followed and allowed")
	}
}

// A session with no folder cannot serve files, and must say so rather than reading
// from wherever the process happens to be.
func TestResolveInsideNeedsARoot(t *testing.T) {
	if _, err := resolveInside("", "anything"); err == nil {
		t.Error("expected a refusal when the session has no folder")
	}
}

// The happy path returns an absolute path inside the root, so the caller can stat
// and read it directly.
func TestResolveInsideReturnsAbsolutePath(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "a.txt")
	if err := os.WriteFile(p, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := resolveInside(root, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got) || !strings.HasSuffix(got, "a.txt") {
		t.Errorf("got %q, want an absolute path ending in a.txt", got)
	}
}

// SendDocument must post real multipart with the bytes intact -- the point of using
// sendDocument rather than sendPhoto is that nothing is recompressed on the way.
func TestSendDocumentPostsMultipart(t *testing.T) {
	var gotName, gotCaption string
	var gotBytes []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/sendDocument") {
			t.Errorf("wrong method called: %s", r.URL.Path)
		}
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			t.Fatalf("not multipart: %v", err)
		}
		gotCaption = r.FormValue("caption")
		f, hdr, err := r.FormFile("document")
		if err != nil {
			t.Fatalf("no document part: %v", err)
		}
		defer f.Close()
		gotName = hdr.Filename
		gotBytes, _ = io.ReadAll(f)
		_, _ = io.WriteString(w, `{"ok":true,"result":{}}`)
	}))
	old := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = old; srv.Close() })

	payload := []byte{0x89, 'P', 'N', 'G', 0x00, 0xff}
	if err := NewClient("tok").SendDocument(context.Background(), 42, "chart.png", payload, "out/chart.png"); err != nil {
		t.Fatalf("SendDocument: %v", err)
	}
	if gotName != "chart.png" {
		t.Errorf("filename = %q", gotName)
	}
	if gotCaption != "out/chart.png" {
		t.Errorf("caption = %q", gotCaption)
	}
	if !bytes.Equal(gotBytes, payload) {
		t.Errorf("bytes altered in transit: got %v want %v", gotBytes, payload)
	}
}

// Telegram reporting ok:false is a failure the chat must hear about, not a silent
// no-op that looks like the file was sent.
func TestSendDocumentSurfacesRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"ok":false,"description":"file is too big"}`)
	}))
	old := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = old; srv.Close() })

	err := NewClient("tok").SendDocument(context.Background(), 42, "a.bin", []byte("x"), "")
	if err == nil || !strings.Contains(err.Error(), "too big") {
		t.Fatalf("want the refusal surfaced, got %v", err)
	}
}
