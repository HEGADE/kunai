package server

import (
	"bytes"
	"io/fs"
	"net/http"
	"strings"
)

// stableThemeColor is the theme-color the built shell and manifest carry: the
// app canvas, matching --bg.
const stableThemeColor = "#0b0b0c"

// nightlyThemeColor is what a nightly build serves instead, so the browser's own
// chrome matches the night-sky header rather than sitting as a black band above
// it. It is the average of that header's top edge, sampled from the rendered
// gradient; web/src/lib/themeColor.ts carries the same value and the comment
// explaining why an average is the honest choice.
const nightlyThemeColor = "#2a234e"

// themed rewrites the theme-color a document carries when this binary is a
// nightly build.
//
// The client sets the tag from JavaScript too, and that is what makes it follow
// the view: on a phone an open session covers the sidebar, so the purple has to
// go and come back. But a mobile browser paints its chrome from the value present
// when the page is parsed and does not reliably re-read it afterwards. Measured
// on a real iPhone: the tag had been updated to #2a234e and the status bar was
// still black above a purple header. So the first value has to be right, and only
// the server can do that, because only the server knows its channel.
func themed(doc []byte) []byte {
	if buildChannel != "nightly" {
		return doc
	}
	return bytes.ReplaceAll(doc, []byte(stableThemeColor), []byte(nightlyThemeColor))
}

// carriesThemeColor reports whether a path is one of the two documents a browser
// reads a theme colour from: the HTML shell, and the manifest an installed PWA
// uses for its first paint. Leaving the manifest alone would mean a nightly
// install flashed the stable colour every launch.
func carriesThemeColor(p string) bool {
	return p == "index.html" || strings.HasSuffix(p, ".webmanifest")
}

// spaHandler serves the embedded PWA. Real files are served directly; any
// unknown path falls back to index.html so client-side routing works. API and
// WebSocket routes are registered separately and never reach this handler.
func (s *Server) spaHandler() http.Handler {
	fileServer := http.FileServer(http.FS(s.pwa))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if f, err := s.pwa.Open(p); err == nil {
			_ = f.Close()
			if carriesThemeColor(p) {
				s.serveThemed(w, r, p)
				return
			}
			// Only Vite's content-hashed files (under assets/) are safe to cache
			// forever. Everything else — the service worker, its registration shim,
			// the web manifest, icons, and the HTML shell — must stay revalidated so
			// a new deploy is picked up. Caching sw.js immutably strands clients on
			// the old worker (and thus the old cached UI) no matter how often they
			// reload.
			if strings.HasPrefix(p, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		s.serveIndex(w, r)
	})
}

// serveThemed serves one of the documents carrying a theme colour, with this
// channel's value substituted in. Never cached, like the shell and manifest were
// before: a build that changes the colour has to be able to change it.
func (s *Server) serveThemed(w http.ResponseWriter, r *http.Request, p string) {
	doc, err := fs.ReadFile(s.pwa, p)
	if err != nil {
		s.serveIndex(w, r)
		return
	}
	ctype := "text/html; charset=utf-8"
	if p != "index.html" {
		ctype = "application/manifest+json"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(themed(doc))
}

func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	index, err := fs.ReadFile(s.pwa, "index.html")
	if err != nil {
		http.Error(w, "PWA not built", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(themed(index))
}
