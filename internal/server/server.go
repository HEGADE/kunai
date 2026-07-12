// Package server exposes the session manager over HTTP: a small JSON REST API,
// the /ws/app WebSocket bridge to the phone, and the embedded PWA. It binds to a
// tailnet address and (in production) terminates TLS with a `tailscale cert`
// certificate so the PWA runs in a secure context.
package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/hegade/kunai/internal/fsbrowse"
	"github.com/hegade/kunai/internal/push"
	"github.com/hegade/kunai/internal/session"
	"github.com/hegade/kunai/internal/webui"
)

// Config holds server settings (populated from flags/env in cmd/kunai).
type Config struct {
	Addr          string // bind address, e.g. "100.x.y.z:8443" (tailnet IP)
	TLSCert       string // path to tailscale cert (empty = plain HTTP, dev only)
	TLSKey        string // path to tailscale key
	DefaultModel  string // optional default model for new sessions
	DefaultEffort string // optional default reasoning effort for new sessions
	DataDir       string // dir for uploads (and, via push, VAPID keys/subs)
	PublicURL     string // this machine's own tailnet origin, e.g. https://host.tailnet.ts.net:8443
	HubURL        string // if set, this is a peer that forwards push wake-ups to the hub at this URL
}

// Server wires the manager, config, and embedded PWA into an http.Handler.
type Server struct {
	cfg        Config
	mgr        *session.Manager
	pwa        fs.FS
	push       *push.Manager // optional; nil disables Web Push
	uploadsDir string
	machines   *machineStore
	disco      discoveryCache
}

func New(cfg Config, mgr *session.Manager) *Server {
	// Go's mime table doesn't know these; without them the PWA manifest and
	// service worker can be served with a type some browsers reject.
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
	_ = mime.AddExtensionType(".js", "text/javascript")

	uploads := cfg.DataDir
	if uploads == "" {
		uploads = os.TempDir()
	}
	uploads = filepath.Join(uploads, "uploads")
	_ = os.MkdirAll(uploads, 0o700)

	machines := newMachineStore(filepath.Join(cfg.DataDir, "machines.json"))

	return &Server{cfg: cfg, mgr: mgr, pwa: webui.FS(), uploadsDir: uploads, machines: machines}
}

// SetPush enables Web Push wake-ups.
func (s *Server) SetPush(p *push.Manager) { s.push = p }

// Handler builds the route mux.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/sessions", s.handleListSessions)
	mux.HandleFunc("POST /api/sessions", s.handleCreateSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.handleCloseSession)
	mux.HandleFunc("GET /api/browse", s.handleBrowse)
	mux.HandleFunc("GET /api/history", s.handleHistory)
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/push/pubkey", s.handlePushKey)
	mux.HandleFunc("POST /api/push/subscribe", s.handlePushSubscribe)
	mux.HandleFunc("POST /api/push/unsubscribe", s.handlePushUnsubscribe)
	mux.HandleFunc("POST /api/push/relay", s.handlePushRelay)
	mux.HandleFunc("POST /api/upload", s.handleUpload)
	mux.HandleFunc("GET /api/machines", s.handleMachines)
	mux.HandleFunc("POST /api/machines", s.handleAddMachine)
	mux.HandleFunc("DELETE /api/machines/{id}", s.handleDeleteMachine)
	mux.HandleFunc("GET /api/machines/discover", s.handleDiscover)
	mux.HandleFunc("GET /ws/app/{id}", s.handleWS)
	mux.Handle("GET /", s.spaHandler())
	return cors(logRequests(mux))
}

// Run starts the HTTP(S) server and blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	if s.cfg.TLSCert != "" && s.cfg.TLSKey != "" {
		log.Printf("kunai listening on https://%s", s.cfg.Addr)
		return srv.ListenAndServeTLS(s.cfg.TLSCert, s.cfg.TLSKey)
	}
	log.Printf("kunai listening on http://%s (no TLS — dev only; PWA/push need HTTPS)", s.cfg.Addr)
	return srv.ListenAndServe()
}

// --- REST handlers ---

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.mgr.List())
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cwd    string `json:"cwd"`
		Title  string `json:"title"`
		Model  string `json:"model"`
		Effort string `json:"effort"`
		Resume string `json:"resume"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Model == "" {
		req.Model = s.cfg.DefaultModel
	}
	if req.Effort == "" {
		req.Effort = s.cfg.DefaultEffort
	}
	// Session start blocks on the CLI init handshake; give it room.
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	opts := session.CreateOptions{Cwd: req.Cwd, Title: req.Title, Model: req.Model, Effort: req.Effort, Resume: req.Resume}
	if req.Resume != "" {
		// Replay the prior conversation into the buffer so the client doesn't
		// open onto an empty transcript.
		opts.Seed = loadTranscriptTurns(req.Resume)
	}
	sess, err := s.mgr.Create(ctx, opts)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// A wake-up can go out either locally (this machine owns push) or, on a peer,
	// by forwarding to the hub — so arm the notifier when either is possible.
	if s.push != nil || s.cfg.HubURL != "" {
		sess.SetNotifier(s.pushNotifier())
	}
	writeJSON(w, http.StatusCreated, sess.Meta())
}

// pushNotifier returns a callback that sends a generic wake-up — never content.
// On a peer (HubURL set) it forwards to the hub; on the hub (or a standalone
// machine) it sends the push directly.
func (s *Server) pushNotifier() func(kind, detail string) {
	return func(kind, detail string) {
		title, body := wakeupText(kind)
		if s.cfg.HubURL != "" {
			s.forwardWake(title, body)
			return
		}
		if s.push != nil {
			s.push.Notify(title, body)
		}
	}
}

func wakeupText(kind string) (title, body string) {
	switch kind {
	case "permission":
		return "Kunai", "A session needs your approval"
	case "done":
		return "Kunai", "A task finished"
	default:
		return "Kunai", "A session needs your attention"
	}
}

func (s *Server) handlePushKey(w http.ResponseWriter, r *http.Request) {
	if s.push == nil {
		writeErr(w, http.StatusNotFound, "push disabled")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"key": s.push.PublicKey()})
}

func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	if s.push == nil {
		writeErr(w, http.StatusNotFound, "push disabled")
		return
	}
	var sub webpush.Subscription
	if err := json.NewDecoder(r.Body).Decode(&sub); err != nil || sub.Endpoint == "" {
		writeErr(w, http.StatusBadRequest, "invalid subscription")
		return
	}
	s.push.Subscribe(&sub)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if s.push == nil {
		writeErr(w, http.StatusNotFound, "push disabled")
		return
	}
	var body struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Endpoint == "" {
		writeErr(w, http.StatusBadRequest, "invalid endpoint")
		return
	}
	s.push.Unsubscribe(body.Endpoint)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCloseSession(w http.ResponseWriter, r *http.Request) {
	s.mgr.Close(r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	listing, err := fsbrowse.List(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, listing)
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func parseSince(r *http.Request) uint64 {
	n, _ := strconv.ParseUint(r.URL.Query().Get("since"), 10, 64)
	return n
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}
