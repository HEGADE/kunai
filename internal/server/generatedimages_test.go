package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hegade/kunai/internal/cliproxy/codex"
)

func TestImageSinkWritesAServableFile(t *testing.T) {
	dir := t.TempDir()
	sink := newImageSink(dir)
	if sink == nil {
		t.Fatal("no sink for a real data dir")
	}
	path, err := sink.SaveImage([]byte("png-bytes"), codex.ImageInfo{Format: "png"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "png-bytes" {
		t.Errorf("file holds %q", got)
	}
	if !strings.HasPrefix(path, filepath.Join(dir, generatedImagesDir)) {
		t.Errorf("%s is not under the images dir", path)
	}
	// The extension is what decides whether the file route will serve it at all,
	// so it has to be one that route recognises.
	if _, ok := imageTypes[strings.ToLower(filepath.Ext(path))]; !ok {
		t.Errorf("%s has an extension the file route refuses", path)
	}
}

func TestImageSinkCorrectsAnUnservableFormat(t *testing.T) {
	sink := newImageSink(t.TempDir())
	// The backend naming a format the file route does not serve would produce a
	// file that exists and 415s on read, which looks like a broken feature.
	for _, format := range []string{"", "svg", "tiff"} {
		path, err := sink.SaveImage([]byte("x"), codex.ImageInfo{Format: format})
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Ext(path) != ".png" {
			t.Errorf("format %q produced %s, want a .png", format, path)
		}
	}
}

func TestImageSinkKeepsTheDirectoryBounded(t *testing.T) {
	sink := newImageSink(t.TempDir())
	// Written by hand rather than through SaveImage: the names carry a
	// one-second-resolution timestamp, so a loop of real saves would tie and the
	// sweep's oldest-first order would be untestable.
	for i := 0; i < imageKeep+10; i++ {
		name := fmt.Sprintf("2026%04d-000000-%08x.png", i, i)
		if err := os.WriteFile(filepath.Join(sink.dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sink.SaveImage([]byte("newest"), codex.ImageInfo{Format: "png"}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(sink.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) > imageKeep {
		t.Errorf("%d files left, want at most %d", len(entries), imageKeep)
	}
	// Oldest goes first: a picture in an old conversation is the one least likely
	// to be looked at again.
	if _, err := os.Stat(filepath.Join(sink.dir, "20260000-000000-00000000.png")); !os.IsNotExist(err) {
		t.Error("the oldest file survived the sweep")
	}
}

func TestImageSinkIsNilWithoutADataDir(t *testing.T) {
	// Nil means the proxy never offers the tool, so a run with nowhere to put a
	// picture cannot promise one.
	sink := newImageSink("")
	if sink != nil {
		t.Fatal("got a sink with no data dir")
	}
	if got := sink.path(); got != "" {
		t.Errorf("nil sink reported path %q", got)
	}
	if _, err := sink.SaveImage([]byte("x"), codex.ImageInfo{}); err == nil {
		t.Error("a nil sink saved something")
	}
}
