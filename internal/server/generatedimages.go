package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hegade/kunai/internal/cliproxy/codex"
)

// Where a picture the agent drew lands.
//
// A Codex session can now make images (see internal/cliproxy/codex/imagetool.go
// for why that works on the subscription and needs no API key). The picture
// arrives inside the proxy, which is one process-wide server shared by every
// session on this machine and knows nothing about which session asked: the
// request carries a model and messages, not a session id.
//
// So the images go to one place kunai owns rather than into a session's working
// directory. That was the alternative and it was rejected twice over: the proxy
// would have to be taught session identity it currently has no way to learn, and
// a picture is not source, so writing one into somebody's repository makes their
// next `git status` a mess they did not ask for. Keeping them here means they
// are also still there tomorrow, when the session they came from is a transcript.
//
// The consequence is that this directory has to be servable, which is handled in
// sessionfile.go: it is added as a root alongside the session's own folders, and
// nothing else about that route changes. It stays owner-only, images-only, size
// capped and symlink-resolved, so the picture is reachable on exactly the terms
// a screenshot in the project already was.
const generatedImagesDir = "generated-images"

// imageKeep bounds the directory. Pictures are large (roughly 800KB each at the
// backend's default size) and nothing else ever deletes one, so without a bound
// an enthusiastic afternoon quietly fills a disk. Oldest goes first; a picture
// in an old conversation is the one least likely to be looked at again.
const imageKeep = 200

// imageSink writes generated images under the data dir. It satisfies
// codex.ImageSaver, which is the only thing the proxy knows about it.
type imageSink struct{ dir string }

// newImageSink prepares the directory. A nil sink (no data dir) means the proxy
// never offers the tool at all, so the capability is gated on being able to
// deliver it rather than on a flag that could disagree with reality.
func newImageSink(dataDir string) *imageSink {
	if dataDir == "" {
		return nil
	}
	dir := filepath.Join(dataDir, generatedImagesDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil
	}
	return &imageSink{dir: dir}
}

// SaveImage writes one picture and returns the absolute path it landed at.
func (s *imageSink) SaveImage(data []byte, info codex.ImageInfo) (string, error) {
	if s == nil {
		return "", fmt.Errorf("no data directory to save images in")
	}
	ext := "." + info.Format
	if info.Format == "" {
		ext = ".png"
	}
	// The extension is what decides whether the file route will serve it at all
	// (imageTypes in sessionfile.go), so an unknown one is corrected here rather
	// than written and then refused at read time.
	if _, ok := imageTypes[ext]; !ok {
		ext = ".png"
	}
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	// Time-prefixed so the directory sorts oldest-first for the sweep below, and
	// so a person looking in it can tell what is recent.
	name := time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(buf[:]) + ext
	path := filepath.Join(s.dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	s.sweep()
	return path, nil
}

// sweep drops the oldest pictures once there are too many. Best effort: a
// failure to tidy must never fail the picture that was just drawn.
func (s *imageSink) sweep() {
	entries, err := os.ReadDir(s.dir)
	if err != nil || len(entries) <= imageKeep {
		return
	}
	// ReadDir returns sorted by name, and names lead with a UTC timestamp, so
	// this is oldest-first without stat'ing anything.
	for _, e := range entries[:len(entries)-imageKeep] {
		if !e.IsDir() {
			_ = os.Remove(filepath.Join(s.dir, e.Name()))
		}
	}
}

// dir reports the directory, or "" when there is no sink. Used by the file route
// to add it as a servable root.
func (s *imageSink) path() string {
	if s == nil {
		return ""
	}
	return s.dir
}
