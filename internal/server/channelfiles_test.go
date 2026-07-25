package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hegade/kunai/internal/telegram"
)

// A file from a chat must land in the SAME uploads dir an app upload uses, so the
// two paths cannot drift and buildContent can inline it the same way.
func TestChannelSendFilesStoresIntoUploads(t *testing.T) {
	dir := t.TempDir()
	srv := &Server{uploadsDir: dir}
	c := channelSessions{srv: srv}

	atts, text := c.stageFiles("look at this", []telegram.InboundFile{
		{Name: "shot.png", MediaType: "image/png", Data: []byte("PNGBYTES")},
	})
	if len(atts) != 1 {
		t.Fatalf("want one attachment, got %d", len(atts))
	}
	if atts[0].Name != "shot.png" || atts[0].MediaType != "image/png" {
		t.Errorf("attachment metadata lost: %+v", atts[0])
	}
	got, err := os.ReadFile(filepath.Join(dir, atts[0].ID))
	if err != nil {
		t.Fatalf("bytes not stored in uploads: %v", err)
	}
	if string(got) != "PNGBYTES" {
		t.Errorf("stored %q, want PNGBYTES", got)
	}
	if text != "look at this" {
		t.Errorf("caption should be the prompt, got %q", text)
	}
}

// A bare screenshot with no caption is a clear request, but an empty prompt is
// rejected by the API and strands the turn on "Working...", so the words are
// supplied.
func TestChannelSendFilesSuppliesWordsForBareFile(t *testing.T) {
	srv := &Server{uploadsDir: t.TempDir()}
	c := channelSessions{srv: srv}
	_, text := c.stageFiles("   ", []telegram.InboundFile{{Name: "a.png", MediaType: "image/png", Data: []byte("x")}})
	if text == "" {
		t.Fatal("a caption-less file must still carry a prompt")
	}
}

// A file with no declared type still needs one, or buildContent cannot decide
// whether to inline it or drop it in the project.
func TestChannelSendFilesDefaultsMediaType(t *testing.T) {
	srv := &Server{uploadsDir: t.TempDir()}
	c := channelSessions{srv: srv}
	atts, _ := c.stageFiles("hi", []telegram.InboundFile{{Name: "blob", Data: []byte("x")}})
	if len(atts) != 1 || atts[0].MediaType == "" {
		t.Fatalf("want a default media type, got %+v", atts)
	}
}
