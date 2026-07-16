// Package server exposes the session manager over HTTP: a small JSON REST API,
// the /ws/app WebSocket bridge to the phone, and the embedded PWA. It binds to a
// tailnet address and (in production) terminates TLS with a `tailscale cert`
// certificate so the PWA runs in a secure context.
package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/hegade/kunai/internal/awake"
	"github.com/hegade/kunai/internal/fsbrowse"
	"github.com/hegade/kunai/internal/push"
	"github.com/hegade/kunai/internal/schedule"
	"github.com/hegade/kunai/internal/session"
	"github.com/hegade/kunai/internal/webui"
)

// When neither the request nor the machine config names one, every session
// falls back to these — so the composer always shows a real model and effort
// (never a blank "Model"/"Effort"), and resumed sessions (which don't carry a
// model/effort) default to Opus at high effort.
const (
	defaultModel  = "opus"
	defaultEffort = "high"
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
	// Thermal guard defaults, seeded from flags/env. A persisted thermal.json
	// overrides these on boot; the Settings toggle overrides at runtime.
	ThermalGuard    bool    // enable the guardian by default
	ThermalSoftC    float64 // trip temperature in Celsius (0 = no temp check)
	ThermalMaxHours float64 // wall-clock cap on an unattended awake hold (0 = none)
	ThermalHardC    float64 // Phase 2 poweroff ceiling (0 = never)
	ThermalAction   string  // "sleep" (default) or "poweroff"
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
	awake      awake.Keeper        // opt-in keep-awake while locked/idle
	lid        lidKeeper           // opt-in, privileged: keep working with the lid shut
	sched      *schedule.Scheduler // runs prompts at a time / after quota reset
	guardian   *guardian           // whole-machine thermal safety net
	clis       []CLIProfile        // named Claude CLIs (accounts) a session can run on
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

	s := &Server{cfg: cfg, mgr: mgr, pwa: webui.FS(), uploadsDir: uploads, machines: machines, awake: awake.New(), lid: newLidKeeper()}
	s.loadAwake() // re-apply a persisted keep-awake preference on boot
	s.loadLid()   // re-apply a persisted lid-closed preference (after boot-time unstick)
	s.guardian = newGuardian(mgr, s.awake, clampGuardConfig(guardConfig{
		Enabled:  cfg.ThermalGuard,
		SoftC:    cfg.ThermalSoftC,
		MaxHours: cfg.ThermalMaxHours,
		HardC:    cfg.ThermalHardC,
		Action:   cfg.ThermalAction,
	}))
	// On a trip the guard also drops the lid hold, so a closed-lid Mac can sleep.
	s.guardian.releaseLid = func() { _ = s.lid.Set(false) }
	s.loadThermal() // a persisted policy overrides the flag defaults
	s.clis = loadCLIs(cfg.DataDir)
	s.sched = schedule.New(filepath.Join(cfg.DataDir, "schedule.json"), s.fireJob)
	return s
}

// armSession attaches the push notifier and the scheduler's rate-limit handler
// to a freshly created session (both live and scheduler-started ones).
func (s *Server) armSession(sess *session.Session) {
	if s.push != nil || s.cfg.HubURL != "" {
		sess.SetNotifier(s.pushNotifier())
	}
	if s.sched != nil {
		sess.SetRateLimitHandler(s.sched.NoteReset)
	}
	sess.SetLoopPersister(s.loopPersister()) // save a running loop so it survives a restart
}

// SetPush enables Web Push wake-ups.
func (s *Server) SetPush(p *push.Manager) { s.push = p }

// Handler builds the route mux.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/sessions", s.handleListSessions)
	mux.HandleFunc("POST /api/sessions", s.handleCreateSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.handleCloseSession)
	mux.HandleFunc("POST /api/sessions/{id}/effort", s.handleSetEffort)
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
	mux.HandleFunc("POST /api/update", s.handleUpdate)
	mux.HandleFunc("POST /api/awake", s.handleAwake)
	mux.HandleFunc("POST /api/lid", s.handleLid)
	mux.HandleFunc("GET /api/thermal", s.handleThermal)
	mux.HandleFunc("POST /api/thermal", s.handleThermal)
	mux.HandleFunc("GET /api/schedule", s.handleScheduleList)
	mux.HandleFunc("POST /api/schedule", s.handleScheduleCreate)
	mux.HandleFunc("PUT /api/schedule/{id}", s.handleScheduleReplace)
	mux.HandleFunc("DELETE /api/schedule/{id}", s.handleScheduleDelete)
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
	go s.sched.Run(ctx) // fire scheduled jobs while the server is up
	// The guardian wakes the phone the same way a finished turn does; push may
	// have been set after New, so wire the notifier here at launch.
	if s.push != nil || s.cfg.HubURL != "" {
		s.guardian.notify = s.pushNotifier()
	}
	go s.guardian.run(ctx) // stop everything if the host overheats or runs too long
	go s.resumeLoops(ctx)  // restart any loop that was running when we last died
	go func() {
		<-ctx.Done()
		_ = s.awake.Set(false) // release the keep-awake hold on graceful shutdown
		_ = s.lid.Set(false)   // and drop the sticky lid hold, so nothing is stranded
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	if s.cfg.TLSCert != "" && s.cfg.TLSKey != "" {
		keeper := newCertKeeper(s.cfg.TLSCert, s.cfg.TLSKey, s.cfg.PublicURL)
		if _, err := keeper.GetCertificate(nil); err != nil {
			return fmt.Errorf("load TLS cert: %w", err)
		}
		srv.TLSConfig = &tls.Config{GetCertificate: keeper.GetCertificate}
		go keeper.renewLoop(ctx) // auto-renew via `tailscale cert` before expiry
		log.Printf("kunai listening on https://%s", s.cfg.Addr)
		// Certs are served from TLSConfig.GetCertificate, so the file args are empty.
		return srv.ListenAndServeTLS("", "")
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
		CLI    string `json:"cli"` // which Claude account; empty = the default
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Model == "" {
		req.Model = s.cfg.DefaultModel
	}
	if req.Model == "" {
		req.Model = defaultModel
	}
	if req.Effort == "" {
		req.Effort = s.cfg.DefaultEffort
	}
	if req.Effort == "" {
		req.Effort = defaultEffort
	}
	// Session start blocks on the CLI init handshake; give it room.
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	cli := s.resolveCLI(req.CLI)
	opts := session.CreateOptions{
		Cwd: req.Cwd, Title: req.Title, Model: req.Model, Effort: req.Effort, Resume: req.Resume,
		CLIName: cli.Name, Bin: cli.Bin, Env: cli.effectiveEnv(),
	}
	if req.Resume != "" {
		// Replay the prior conversation into the buffer so the client doesn't
		// open onto an empty transcript, and seed the context meter from the
		// transcript so it reflects the real fill before the next turn. Read from
		// the chosen account's config dir, so a work session seeds from its own
		// transcript, not the default account's.
		dir := cli.configDir()
		opts.Seed = loadTranscriptTurns(dir, req.Resume)
		opts.ContextTokens = loadTranscriptContextTokens(dir, req.Resume)
	}
	sess, err := s.mgr.Create(ctx, opts)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.armSession(sess)
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
	case "loop":
		return "Kunai", "A loop finished"
	case "thermal":
		return "Kunai", "Stopped everything: the host got too hot"
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

// handleSetEffort relaunches a live session at a new reasoning effort. Effort is
// a spawn-time CLI flag, so the session is closed and re-created with --resume;
// the conversation is replayed from the transcript. The id is unchanged.
func (s *Server) handleSetEffort(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Effort string `json:"effort"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	sess, err := s.mgr.RestartWithEffort(ctx, r.PathValue("id"), req.Effort, loadTranscriptTurns)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.armSession(sess)
	writeJSON(w, http.StatusOK, sess.Meta())
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
