package server

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hegade/kunai/internal/claude"
	"github.com/hegade/kunai/internal/session"
)

// Attachment ids must have the shape hexID mints, so the tests use real ones
// rather than "img1". A short label would now be refused before it is read, which
// is the point of stagedID.
// fakePNG is the PNG signature and nothing else. It has to be the real eight
// bytes: buildContent now decides from the CONTENT rather than from the media
// type on the record, because a label that said image/png over HEIC bytes is
// exactly what the API kept refusing.
var fakePNG = []byte("\x89PNG\r\n\x1a\n" + "fake")

const (
	testID1       = "0f1e2d3c4b5a69788796a5b4c3d2e1f0"
	testIDMissing = "ffffffffffffffffffffffffffffffff"
)

// An image sent with NO message must not produce an empty text block: the API
// rejects one and the turn then hangs on "Working…" forever, which is exactly what
// sending a screenshot with no words did.
func TestBuildContent_ImageWithNoText(t *testing.T) {
	dir := t.TempDir()
	s := &Server{uploadsDir: dir}
	os.WriteFile(filepath.Join(dir, testID1), fakePNG, 0o600)

	got := s.buildContent(t.TempDir(), "", []session.Attachment{{ID: testID1, Name: "shot.png", MediaType: "image/png"}})
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
	if blocks[0].Source == nil || blocks[0].Source.Data != base64.StdEncoding.EncodeToString(fakePNG) {
		t.Error("image data not inlined correctly")
	}
}

// With text AND an image, the text block is kept and leads.
func TestBuildContent_ImageWithText(t *testing.T) {
	dir := t.TempDir()
	s := &Server{uploadsDir: dir}
	os.WriteFile(filepath.Join(dir, testID1), fakePNG, 0o600)

	got := s.buildContent(t.TempDir(), "what is this?", []session.Attachment{{ID: testID1, MediaType: "image/png"}})
	blocks := got.([]claude.ContentBlock)
	if len(blocks) != 2 || blocks[0].Type != "text" || blocks[0].Text != "what is this?" {
		t.Fatalf("text should lead, got %+v", blocks)
	}
}

// The attachment id is joined onto the uploads dir, and filepath.Join CLEANS the
// result, so an id full of ".." resolves clean out of it. This is the whole
// exploit, run end to end: a secret outside the uploads dir, referenced by a
// traversing id with a non-image media type, which is the branch that COPIES the
// bytes into the session's own folder and tells the model the path. Nothing about
// the agent's tools can stop that, because kunai does the read itself before the
// model runs, and the file it lands is then legitimately inside the folder.
func TestBuildContent_RefusesATraversingID(t *testing.T) {
	root := t.TempDir()
	uploads := filepath.Join(root, "data", "uploads")
	if err := os.MkdirAll(uploads, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "secret.txt")
	const contents = "ANTHROPIC_API_KEY=sk-should-never-be-read"
	if err := os.WriteFile(secret, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	s := &Server{uploadsDir: uploads}

	// "../../secret.txt" from <root>/data/uploads resolves to <root>/secret.txt.
	got := s.buildContent(cwd, "read this", []session.Attachment{
		{ID: "../../secret.txt", Name: "notes.txt", MediaType: "text/plain"},
	})

	if str, ok := got.(string); ok && strings.Contains(str, contents) {
		t.Fatal("the secret was inlined into the prompt")
	}
	if str, ok := got.(string); ok && strings.Contains(str, "Attached file:") {
		t.Fatalf("the secret was copied into the session folder and pointed at: %q", str)
	}
	// And nothing was written into the folder the agent works in.
	if entries, err := os.ReadDir(filepath.Join(cwd, ".kunai-uploads")); err == nil && len(entries) > 0 {
		t.Fatalf("wrote %d file(s) into the session folder from a traversing id", len(entries))
	}
	if _, err := os.Stat(secret); err != nil {
		t.Fatalf("the original secret was disturbed: %v", err)
	}
}

// Every id shape that is not exactly what hexID mints is refused. The read and
// the write derive from caller-supplied strings, so the check is on the shape
// rather than on a list of bad sequences.
func TestStagedIDAcceptsOnlyMintedIDs(t *testing.T) {
	good := hexID()
	if !stagedID(good) {
		t.Fatalf("a freshly minted id was refused: %q", good)
	}
	bad := []string{
		"", "..", "../../etc/passwd", "/etc/passwd",
		"0f1e2d3c4b5a69788796a5b4c3d2e1f",    // one short
		"0f1e2d3c4b5a69788796a5b4c3d2e1f00",  // one long
		"0F1E2D3C4B5A69788796A5B4C3D2E1F0",   // upper case is not what hexID emits
		"0f1e2d3c4b5a69788796a5b4c3d2e1g0",   // not hex
		"0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1", // dashes
	}
	for _, id := range bad {
		if stagedID(id) {
			t.Errorf("accepted %q", id)
		}
	}
}

// safeName's fallback must never be the caller's id, which is how the write
// destination used to escape.
func TestSafeNameFallsBackToAConstant(t *testing.T) {
	for _, name := range []string{"", "  ", ".", "..", "../../etc/passwd", `a\b`} {
		got := safeName(name)
		if strings.ContainsAny(got, `/\`) || got == ".." || got == "." {
			t.Errorf("safeName(%q) = %q, which can leave the directory", name, got)
		}
	}
	if got := safeName("notes.txt"); got != "notes.txt" {
		t.Errorf("a legitimate name was mangled: %q", got)
	}
}

// Attachments that cannot be read must still yield a usable prompt, never "".
func TestBuildContent_UnreadableAttachmentStillPrompts(t *testing.T) {
	s := &Server{uploadsDir: t.TempDir()}
	got := s.buildContent(t.TempDir(), "", []session.Attachment{{ID: testIDMissing, MediaType: "image/png"}})
	if str, ok := got.(string); !ok || str == "" {
		t.Fatalf("want a non-empty fallback prompt, got %#v", got)
	}
}
