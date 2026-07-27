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
	"strings"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/hegade/kunai/internal/awake"
	"github.com/hegade/kunai/internal/fsbrowse"
	"github.com/hegade/kunai/internal/push"
	"github.com/hegade/kunai/internal/schedule"
	"github.com/hegade/kunai/internal/session"
	"github.com/hegade/kunai/internal/share"
	"github.com/hegade/kunai/internal/telegram"
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
	// Telegram bot. Empty token means no bot, which is the default: it is an
	// opt-in second interface, and it reaches a third party.
	TelegramToken   string
	TelegramAllowed []int64 // Telegram user ids permitted to drive kunai
	TelegramDetail  bool    // let tool inputs and outputs leave the machine
	// NativeCodex routes a Codex provider through kunai's own in-process proxy
	// (internal/cliproxy/codex) instead of the CLIProxyAPI sidecar. Opt-in
	// (KUNAI_NATIVE_CODEX=1) while it is proven out; login still uses the sidecar.
	NativeCodex bool
	// NativeGrok routes a Grok provider through kunai's own in-process proxy
	// (internal/cliproxy/grok), reading the grok CLI login. Opt-in (KUNAI_NATIVE_GROK=1).
	NativeGrok bool
}

// Server wires the manager, config, and embedded PWA into an http.Handler.
type Server struct {
	// telegram is the Telegram channel state (token, who may use it, pending
	// pairings). Nil until startTelegram runs.
	telegram      *telegram.Store
	cfg           Config
	mgr           *session.Manager
	pwa           fs.FS
	push          *push.Manager // optional; nil disables Web Push
	uploadsDir    string
	machines      *machineStore
	disco         discoveryCache
	awake         awake.Keeper             // opt-in keep-awake while locked/idle
	lid           lidKeeper                // opt-in, privileged: keep working with the lid shut
	sched         *schedule.Scheduler      // runs prompts at a time / after quota reset
	guardian      *guardian                // whole-machine thermal safety net
	clis          []CLIProfile             // named Claude CLIs (accounts) a session can run on
	clisMu        sync.RWMutex             // guards clis, which the Accounts settings edit live
	providers     *providerStore           // proxy-backed model sources (Codex/Grok/Kimi via CLIProxyAPI)
	cliproxy      *cliproxyManager         // the managed CLIProxyAPI sidecar (nil without a data dir)
	nativeCodex   *nativeCodexManager      // kunai's own in-process Codex proxy (opt-in, replaces the sidecar for Codex)
	nativeGrok    *nativeGrokManager       // kunai's own in-process Grok proxy (opt-in, replaces the sidecar for Grok)
	nativeLogin   *nativeCodexLoginManager // native Codex OAuth login (no sidecar), when NativeCodex is on
	cliproxyLogin *cliproxyLoginManager    // in-app provider (Codex/Grok/Kimi) login flows
	baseCtx       context.Context          // server lifetime, for starting the sidecar on a runtime provider add
	usage         *usageCache              // the default account's subscription quota windows
	codexUC       *codexUsageCache         // a Codex provider's ChatGPT quota (read from OpenAI's usage endpoint)
	grokUC        *grokUsageCache          // a Grok provider's xAI credit-billing quota
	sessionMeta   *sessionMetaStore        // per-session pins and renames (nil without a data dir)
	login         *loginManager            // in-app account login flows (nil without a data dir)
	checkpoints   *checkpointManager       // per-turn git snapshots for undo/revert
	failover      *failoverController      // opt-in: roll a walled session onto another account
	modelVers     modelVersionCache        // newest model version per family, read from the claude binary
	worktrees     *worktreeStore           // git worktrees, so several agents share one repo safely
	fleet         *fleetHub                // pushes the session list to clients instead of them polling for it
	// shares and gate are session sharing: a link worth one conversation, served
	// on a listener of its own so a guest can never reach the routes above. See
	// sharegate.go for why that is a separate mux rather than a middleware.
	shares     *share.Store
	gate       *shareGate
	funnelMu   sync.Mutex
	funnelAt   time.Time // when the funnel port was last read (it shells out)
	funnelPort int
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

	s := &Server{cfg: cfg, mgr: mgr, pwa: webui.FS(), uploadsDir: uploads, machines: machines, awake: awake.New(), lid: newLidKeeper(), usage: newUsageCache(), checkpoints: newCheckpointManager()}
	// Worktrees derive "who is using this one" from the live session list rather
	// than persisting it, since a persisted copy goes stale the moment a session
	// ends and the only thing it is used for is refusing to delete a worktree
	// somebody is working in.
	s.worktrees = newWorktreeStore(cfg.DataDir, mgr.List)
	// Anything that changes the session list wakes every connected client. Wired
	// before the first session can exist.
	s.fleet = newFleetHub()
	mgr.SetOnChange(s.fleet.notify)
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
	// Providers must exist before the first resolveCLI call below (the login
	// manager resolves the default binary), since resolveCLI now consults them.
	s.providers = newProviderStore(filepath.Join(cfg.DataDir, "providers.json"))
	s.cliproxy = newCLIProxyManager(cfg.DataDir)
	if cfg.NativeCodex {
		s.nativeCodex = newNativeCodexManager(cfg.DataDir)
		s.nativeLogin = newNativeCodexLoginManager(cfg.DataDir)
	}
	if cfg.NativeGrok {
		s.nativeGrok = newNativeGrokManager(cfg.DataDir)
	}
	s.cliproxyLogin = newCLIProxyLoginManager(s.cliproxy)
	s.codexUC = &codexUsageCache{}
	s.grokUC = &grokUsageCache{}
	s.failover = newFailoverController(s)
	s.failover.load()            // re-apply the persisted opt-in on boot (default off)
	go s.discoverModelVersions() // warm the model-version cache off the request path
	s.sched = schedule.New(filepath.Join(cfg.DataDir, "schedule.json"), s.fireJob)
	// Sharing needs somewhere to persist links, so like the other stores it is
	// only available with a data dir. The gate is constructed either way and simply
	// never started when there is nothing to serve.
	s.shares = share.NewStore(shareStorePath(cfg.DataDir))
	s.gate = newShareGate(s.shares, mgr, s.pwa)
	if cfg.DataDir != "" {
		s.sessionMeta = newSessionMetaStore(filepath.Join(cfg.DataDir, "sessionmeta.json"))
		// New accounts log in with the same binary as the default profile, into a
		// fresh config dir under the data dir. The register callback saves a
		// completed account, called once from the login's finalize, so it lands
		// whether the user pasted a code or the browser completed it directly.
		s.login = newLoginManager(s.resolveCLI("").Bin, cfg.DataDir, func(p CLIProfile) {
			s.saveCLIs(append(s.cliList(), p))
			forgetAuthStatus() // a brand-new login must show as signed in at once
		})
	}
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
	if s.checkpoints != nil {
		sess.SetCheckpointHook(func(seq uint64) { s.checkpoints.capture(sess.ID, sess.Cwd, seq) })
	}
	// One hook, because a session has one: failover reacts to a wall, and a
	// worktree still carrying a placeholder name takes one from what was asked
	// of it. Composed here rather than fought over.
	sess.SetTurnEndHook(func(rateLimited bool) {
		if s.worktrees != nil {
			s.worktrees.nameFromFirstPrompt(sess.Cwd, sess.LastPromptText())
		}
		if s.failover != nil {
			s.failover.onTurnEnd(sess, rateLimited)
		}
	})
}

// SetPush enables Web Push wake-ups.
func (s *Server) SetPush(p *push.Manager) { s.push = p }

// Handler builds the route mux.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/sessions", s.handleListSessions)
	mux.HandleFunc("POST /api/sessions", s.handleCreateSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.handleCloseSession)
	mux.HandleFunc("PATCH /api/sessions/{id}", s.handleUpdateSessionMeta)
	mux.HandleFunc("POST /api/sessions/{id}/effort", s.handleSetEffort)
	mux.HandleFunc("POST /api/sessions/{id}/account", s.handleSetAccount)
	mux.HandleFunc("GET /api/sessions/{id}/history", s.handleOlderTurns)
	mux.HandleFunc("GET /api/sessions/{id}/checkpoints", s.handleListCheckpoints)
	mux.HandleFunc("GET /api/sessions/{id}/revert", s.handleRevertPreview)
	mux.HandleFunc("POST /api/sessions/{id}/revert", s.handleRevert)
	mux.HandleFunc("GET /api/worktrees", s.handleWorktrees)
	mux.HandleFunc("POST /api/worktrees", s.handleCreateWorktree)
	mux.HandleFunc("DELETE /api/worktrees", s.handleDeleteWorktree)
	mux.HandleFunc("GET /api/worktrees/repo", s.handleIsRepo)
	mux.HandleFunc("GET /api/worktrees/setup", s.handleWorktreeSetup)
	mux.HandleFunc("GET /api/worktrees/branches", s.handleWorktreeBranches)
	mux.HandleFunc("POST /api/worktrees/merge", s.handleMergeWorktree)
	mux.HandleFunc("POST /api/worktrees/pr", s.handlePullRequestWorktree)
	mux.HandleFunc("GET /api/worktrees/settings", s.handleWorktreeSettings)
	mux.HandleFunc("POST /api/worktrees/settings", s.handleWorktreeSettings)
	mux.HandleFunc("GET /api/browse", s.handleBrowse)
	mux.HandleFunc("GET /api/history", s.handleHistory)
	mux.HandleFunc("DELETE /api/history/{id}", s.handleDeleteHistory)
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/usage", s.handleUsage)
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
	mux.HandleFunc("GET /api/failover", s.handleFailover)
	mux.HandleFunc("POST /api/failover", s.handleFailover)
	mux.HandleFunc("GET /api/clis", s.handleCLIs)
	mux.HandleFunc("POST /api/clis", s.handleCLIs)
	mux.HandleFunc("GET /api/providers", s.handleProviders)
	mux.HandleFunc("POST /api/providers", s.handleProviders)
	mux.HandleFunc("DELETE /api/providers/{name}", s.handleDeleteProvider)
	mux.HandleFunc("POST /api/sessions/{id}/provider-model", s.handleSetProviderModel)
	mux.HandleFunc("GET /api/providers/models", s.handleProviderModels)
	mux.HandleFunc("POST /api/providers/login/start", s.handleProviderLoginStart)
	mux.HandleFunc("POST /api/providers/login/finish", s.handleProviderLoginFinish)
	mux.HandleFunc("POST /api/providers/login/status", s.handleProviderLoginStatus)
	mux.HandleFunc("POST /api/providers/login/cancel", s.handleProviderLoginCancel)
	mux.HandleFunc("GET /api/channels", s.handleChannels)
	mux.HandleFunc("POST /api/channels/{id}", s.handleChannelUpdate)
	mux.HandleFunc("POST /api/channels/{id}/requests/{code}", s.handleChannelApprove)
	mux.HandleFunc("DELETE /api/channels/{id}/people/{person}", s.handleChannelRevoke)
	mux.HandleFunc("GET /api/accounts", s.handleAccounts)
	mux.HandleFunc("DELETE /api/accounts/{name}", s.handleAccountRemove)
	mux.HandleFunc("POST /api/accounts/login/start", s.handleAccountLoginStart)
	mux.HandleFunc("POST /api/accounts/login/finish", s.handleAccountLoginFinish)
	mux.HandleFunc("POST /api/accounts/login/status", s.handleAccountLoginStatus)
	mux.HandleFunc("POST /api/accounts/login/cancel", s.handleAccountLoginCancel)
	mux.HandleFunc("GET /api/schedule", s.handleScheduleList)
	mux.HandleFunc("POST /api/schedule", s.handleScheduleCreate)
	mux.HandleFunc("PUT /api/schedule/{id}", s.handleScheduleReplace)
	mux.HandleFunc("DELETE /api/schedule/{id}", s.handleScheduleDelete)
	// Sharing, owner side. The guest's own routes are NOT here: they live on a
	// separate mux on a separate listener (sharegate.go), which is what keeps a
	// public link from ever reaching the handlers above.
	mux.HandleFunc("GET /api/shares", s.handleListShares)
	mux.HandleFunc("POST /api/shares", s.handleCreateShare)
	mux.HandleFunc("GET /api/sessions/{id}/share", s.handleGetShare)
	mux.HandleFunc("POST /api/shares/{token}/approve/{code}", s.handleApproveShare)
	mux.HandleFunc("POST /api/shares/{token}/deny", s.handleDenyShare)
	mux.HandleFunc("DELETE /api/shares/{token}", s.handleRevokeShare)
	mux.HandleFunc("GET /api/funnel", s.handleFunnelStatus)
	mux.HandleFunc("POST /api/funnel", s.handleFunnelOn)
	mux.HandleFunc("DELETE /api/funnel", s.handleFunnelOff)

	mux.HandleFunc("GET /ws/app/{id}", s.handleWS)
	// Machine-level pushes: the session list when it changes, stats on a timer the
	// server owns. Replaces the client polling every machine for both.
	mux.HandleFunc("GET /ws/fleet", s.handleFleetWS)
	mux.Handle("GET /", s.spaHandler())
	return cors(logRequests(mux))
}

// Run starts the HTTP(S) server and blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	s.baseCtx = ctx // so a provider added at runtime can start the sidecar
	srv := &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	// Boot the managed CLIProxyAPI sidecar if any providers are configured, so
	// their sessions have a proxy to reach. Downloading/verifying happens inside.
	if s.cliproxy != nil && s.anyProviderNeedsSidecar() {
		go s.ensureCLIProxy()
	}
	// Start the native Codex proxy too when enabled, so a Codex provider session
	// created right after boot finds it ready.
	if s.nativeCodex != nil {
		go func() {
			if err := s.nativeCodex.start(s.baseCtx); err != nil {
				log.Printf("native codex: %v", err)
			}
		}()
	}
	if s.nativeGrok != nil {
		go func() {
			if err := s.nativeGrok.start(s.baseCtx); err != nil {
				log.Printf("native grok: %v", err)
			}
		}()
	}
	go s.sched.Run(ctx) // fire scheduled jobs while the server is up
	// The guardian wakes the phone the same way a finished turn does; push may
	// have been set after New, so wire the notifier here at launch.
	if s.push != nil || s.cfg.HubURL != "" {
		s.guardian.notify = s.pushNotifier()
	}
	go s.guardian.run(ctx)   // stop everything if the host overheats or runs too long
	go s.resumeLoops(ctx)    // restart any loop that was running when we last died
	go s.usagePollLoop(ctx)  // feed real window reset times to the scheduler, so reset jobs fire
	go s.loginSweepLoop(ctx) // kill abandoned account-login flows so they don't linger
	go s.discover(true)      // warm peer discovery so the first client load sees the fleet
	s.startTelegram(ctx)     // opt-in: drive a session from a Telegram chat
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
	metas := s.mgr.List()
	s.worktrees.tagRepos(metas)
	if s.sessionMeta != nil {
		mergeMeta(metas, s.sessionMeta.all())
	}
	writeJSON(w, http.StatusOK, metas)
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cwd    string `json:"cwd"`
		Title  string `json:"title"`
		Model  string `json:"model"`
		Effort string `json:"effort"`
		Resume string `json:"resume"`
		CLI    string `json:"cli"` // which Claude account; empty = the default
		// Worktree, when set, is the path of a worktree created earlier by
		// POST /api/worktrees. It becomes the session's cwd, which is the whole
		// isolation mechanism: nothing else about the session changes.
		Worktree string `json:"worktree"`
		// Mode is the permission mode to spawn in; empty takes the default. It
		// belongs at create rather than only on the live session because the CLI
		// applies it as a spawn flag: sent afterwards it arrives too late to
		// govern the first tool call, which for an unattended start is the one
		// that matters.
		Mode string `json:"mode"`
		// ProviderModel is the upstream model to run, when CLI names a provider
		// rather than a Claude account. It is separate from Model because the two
		// are different things: Model is a Claude tier the CLI resolves, while
		// this is a real upstream model id that has to be baked into the spawn env
		// before the process starts. Ignored for a Claude account, which has no
		// such mapping.
		ProviderModel string `json:"provider_model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Model == "" {
		req.Model = s.model()
	}
	if req.Effort == "" {
		req.Effort = s.effort()
	}
	// Session start blocks on the CLI init handshake; give it room.
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	// A provider session's base_url comes from the managed sidecar, which may
	// still be starting; wait for it to have a real address before we bake the
	// env, or claude would spawn with no proxy to reach.
	if s.isProviderName(req.CLI) && !s.providerUsesNative(req.CLI) && !s.providerUsesNativeGrok(req.CLI) {
		s.ensureCLIProxyReady()
	}
	// Pin the provider's model before the profile is compiled, since the model is
	// carried in the spawn env: chosen after resolveCLI it would be saved for next
	// time but not applied to the session that asked for it.
	if m := strings.TrimSpace(req.ProviderModel); m != "" {
		if p := s.providerNamed(req.CLI); p != nil {
			s.pinProviderModel(p, m)
		}
	}
	cli := s.resolveCLI(req.CLI)
	opts := session.CreateOptions{
		Cwd: req.Cwd, Title: req.Title, Model: req.Model, Effort: req.Effort, Resume: req.Resume,
		CLIName: cli.Name, Bin: cli.Bin, Env: cli.effectiveEnv(),
	}
	if req.Worktree != "" {
		if err := s.applyWorktree(ctx, &opts, req.Worktree); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if isProxyProfile(cli) {
		// Provider sessions deliberately use their own default. It currently matches
		// the normal Auto default, but keeping the branch means provider-specific
		// safety-mode policy stays one constant instead of leaking into create/resume.
		opts.Mode = session.ProviderPermissionMode
	}
	// An explicit choice beats both defaults. This is what lets a worktree be
	// started in acceptEdits from the app: an unattended agent that stops on its
	// first file write has not started at all. An unrecognised mode is ignored
	// rather than refused, so a newer client cannot fail a create on an older
	// server over a field that only ever chooses a default.
	if m := session.ValidPermissionMode(req.Mode); m != "" {
		opts.Mode = m
	}
	if req.Resume != "" {
		// Replay the prior conversation into the buffer so the client doesn't
		// open onto an empty transcript, and seed the context meter from the
		// transcript so it reflects the real fill before the next turn. Read from
		// the chosen account's config dir, so a work session seeds from its own
		// transcript, not the default account's.
		dir := cli.configDir()
		opts.Seed, opts.HistBefore = loadTranscriptSeed(dir, req.Resume)
		opts.ContextTokens, opts.Overhead = loadTranscriptContextTokens(dir, req.Resume)
	}
	sess, err := s.mgr.Create(ctx, opts)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.armSession(sess)
	writeJSON(w, http.StatusCreated, sess.Meta())
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
	id := r.PathValue("id")
	s.mgr.Close(id)
	if s.checkpoints != nil {
		s.checkpoints.forget(id) // the shadow refs remain for git GC; drop the tracking
	}
	// A share must not outlive the session it points at. Left behind, it is a
	// public link to a conversation that no longer exists, and worse, to an id
	// that could later be reused by another one.
	if s.shares != nil {
		if _, had := s.shares.RevokeSession(id); had {
			logShare("session %s ended, so its link was revoked", id)
		}
	}
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
