package server

// Serving an image the agent produced, so a screenshot it took or a chart it
// rendered can appear in the conversation instead of as a path only the machine
// can open.
//
// The gap this closes: kunai answers every unmatched path with the app shell, so
// an agent writing ![shot](/tmp/x.png) produced an <img> that fetched HTML and
// showed a broken icon. Not a "wrong machine" problem -- it failed the same way
// in a browser on the machine itself, because nothing here ever served a file
// from disk.
//
// Two decisions bound it, and both are deliberate:
//
//   - OWNER ONLY. This handler is registered on the main mux and must never be
//     added to the share gate, at any tier. A shared link is a public URL, and a
//     route that reads files inside the session's folders would hand whoever
//     holds it every image in the project. sharegate_test.go pins the 404.
//
//   - IMAGES ONLY. The response goes into an <img> in the owner's own app, so
//     the safe set is the formats a browser decodes as pixels. SVG is refused
//     despite being an image: it is a document that can carry script, and this
//     serves it from kunai's own origin where that script would run with the
//     app's privileges.

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/hegade/kunai/internal/pathguard"
)

// maxSessionFile bounds what will be read into a response. Generous for a
// screenshot, small enough that a stray pointer at a video file is refused
// rather than streamed.
const maxSessionFile = 16 << 20

// imageTypes are the extensions served, mapped to the type sent with them. The
// type is never sniffed from the bytes: a mismatch between what the extension
// claims and what the file contains should fail, not be resolved in the file's
// favour.
//
// SVG is absent on purpose. See the header.
var imageTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".avif": "image/avif",
	".bmp":  "image/bmp",
	".ico":  "image/x-icon",
}

// handleSessionFile serves an image from inside the session's own folders.
func (s *Server) handleSessionFile(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.mgr.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	rel := r.URL.Query().Get("path")
	if rel == "" {
		writeErr(w, http.StatusBadRequest, "path is required")
		return
	}
	ct, ok := imageTypes[strings.ToLower(filepath.Ext(rel))]
	if !ok {
		// Named before the path is resolved, so the answer does not depend on
		// whether the file happens to exist: this is about what may be served at
		// all, not about what is there.
		writeErr(w, http.StatusUnsupportedMediaType, "only images can be shown in the conversation")
		return
	}

	// The same confinement a shared session's tool calls get, and for the same
	// reason: symlinks resolved BEFORE the containment check, so a link inside the
	// folder pointing at /etc cannot be followed out of it.
	abs, err := pathguard.ResolveAny(s.shareRoots(sess), rel)
	if err != nil {
		if errors.Is(err, pathguard.ErrOutside) {
			writeErr(w, http.StatusForbidden, "that file is outside this session's folders")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	fi, err := os.Stat(abs)
	if err != nil || !fi.Mode().IsRegular() {
		writeErr(w, http.StatusNotFound, "no such file")
		return
	}
	if fi.Size() > maxSessionFile {
		writeErr(w, http.StatusRequestEntityTooLarge, "that image is too large to show")
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such file")
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", ct)
	// nosniff so the browser cannot be talked into treating these bytes as
	// something executable, and no-store because the agent overwrites a screenshot
	// at the same path constantly and a cached one would show yesterday's run.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	http.ServeContent(w, r, filepath.Base(abs), fi.ModTime(), f)
}
