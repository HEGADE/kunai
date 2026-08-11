package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// A picture the conversation drew, shown to the person watching it.
//
// A guest saw a broken frame for every image, and that was correct rather than
// faulty: `GET /api/sessions/{id}/file` is owner-only and deliberately never on
// this gate, because it reads files inside the session's FOLDERS and a share
// link is a public URL. That invariant is unchanged, and its test still pins it.
//
// This is a different route with a different root. It serves only
// <dataDir>/generated-images, which holds nothing but pictures the model drew in
// a conversation, and it is reached only with a live share token. So it leaks
// nothing a guest cannot already read: they are watching the very turn that
// produced the image, and the caption describing it is already on their screen.
// Nothing under the session's own directories is reachable here at any spelling
// of the path.
//
// Before images existed nothing in a shared conversation needed a file at all,
// which is why the gap only appeared once the agent could draw.

// handleSharedImage serves one generated image to a guest holding a live token.
func (g *shareGate) handleSharedImage(w http.ResponseWriter, r *http.Request) {
	if _, err := g.shares.Get(r.PathValue("token")); err != nil {
		http.NotFound(w, r) // dead, expired or invented link
		return
	}
	if g.imagesDir == "" {
		http.NotFound(w, r)
		return
	}
	// The client sends the absolute path it found in the reply, exactly as it
	// does for the owner's route, so the two render paths stay identical. Only
	// the base name of it is used: a generated image lives in one directory and
	// nowhere else, so the directory a guest names is discarded rather than
	// checked. That makes traversal inexpressible instead of merely refused.
	name := filepath.Base(r.URL.Query().Get("path"))
	if name == "" || name == "." || name == string(filepath.Separator) || strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}
	ct, ok := imageTypes[strings.ToLower(filepath.Ext(name))]
	if !ok {
		// Same rule as the owner's route: raster images only, so SVG (a scriptable
		// document) is never served from an origin the app runs on.
		writeErr(w, http.StatusUnsupportedMediaType, "only images can be shown in the conversation")
		return
	}

	path := filepath.Join(g.imagesDir, name)
	// Resolved before the containment check, so a symlink planted in the
	// directory cannot be followed out of it. The directory is kunai's own, but
	// the check costs nothing and the rule should not depend on that staying true.
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	root, err := filepath.EvalSymlinks(g.imagesDir)
	if err != nil || !withinRoot(real, root) {
		http.NotFound(w, r)
		return
	}
	fi, err := os.Stat(real)
	if err != nil || !fi.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	if fi.Size() > maxSessionFile {
		writeErr(w, http.StatusRequestEntityTooLarge, "that image is too large to show")
		return
	}
	f, err := os.Open(real)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	// A generated image never changes once written (the name carries a timestamp
	// and random suffix), so unlike the owner's route this one may be cached.
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, name, fi.ModTime(), f)
}

// withinRoot compares as path segments, so a sibling directory whose name merely
// starts with the root's cannot pass.
func withinRoot(path, root string) bool {
	path, root = strings.TrimRight(path, "/"), strings.TrimRight(root, "/")
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}
