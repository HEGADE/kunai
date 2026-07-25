package server

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/hegade/kunai/internal/claude"
	"github.com/hegade/kunai/internal/session"
)

// An image sent with NO message must not produce an empty text block: the API
// rejects one and the turn then hangs on "Working…" forever, which is exactly what
// sending a screenshot with no words did.
func TestBuildContent_ImageWithNoText(t *testing.T) {
	dir := t.TempDir()
	s := &Server{uploadsDir: dir}
	os.WriteFile(filepath.Join(dir, "img1"), []byte("\x89PNG fake"), 0o600)

	got := s.buildContent(t.TempDir(), "", []session.Attachment{{ID: "img1", Name: "shot.png", MediaType: "image/png"}})
	blocks, ok := got.([]claude.ContentBlock)
	if !ok {
		t.Fatalf("want content blocks, got %T (%v)", got, got)
	}
	for _, b := range blocks {
		if b.Type == "text" && b.Text == "" {
			t.Fatal("an empty text block was sent; the turn would hang forever")
		}
	}
	if len(blocks) != 1 || blocks[0].Type != "image" {
		t.Fatalf("image-only message should be just the image block, got %+v", blocks)
	}
	if blocks[0].Source == nil || blocks[0].Source.Data != base64.StdEncoding.EncodeToString([]byte("\x89PNG fake")) {
		t.Error("image data not inlined correctly")
	}
}

// With text AND an image, the text block is kept and leads.
func TestBuildContent_ImageWithText(t *testing.T) {
	dir := t.TempDir()
	s := &Server{uploadsDir: dir}
	os.WriteFile(filepath.Join(dir, "img1"), []byte("png"), 0o600)

	got := s.buildContent(t.TempDir(), "what is this?", []session.Attachment{{ID: "img1", MediaType: "image/png"}})
	blocks := got.([]claude.ContentBlock)
	if len(blocks) != 2 || blocks[0].Type != "text" || blocks[0].Text != "what is this?" {
		t.Fatalf("text should lead, got %+v", blocks)
	}
}

// Attachments that cannot be read must still yield a usable prompt, never "".
func TestBuildContent_UnreadableAttachmentStillPrompts(t *testing.T) {
	s := &Server{uploadsDir: t.TempDir()}
	got := s.buildContent(t.TempDir(), "", []session.Attachment{{ID: "missing", MediaType: "image/png"}})
	if str, ok := got.(string); !ok || str == "" {
		t.Fatalf("want a non-empty fallback prompt, got %#v", got)
	}
}
