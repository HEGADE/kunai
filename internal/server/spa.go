package server

import (
	"io/fs"
	"net/http"
	"strings"
)

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
			// Long-cache fingerprinted assets; keep HTML fresh.
			if !strings.HasSuffix(p, ".html") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		s.serveIndex(w, r)
	})
}

func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	index, err := fs.ReadFile(s.pwa, "index.html")
	if err != nil {
		http.Error(w, "PWA not built", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(index)
}
