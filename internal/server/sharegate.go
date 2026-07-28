package server

// The public surface of a shared session, and the reason it is a separate
// listener rather than a route on the main one.
//
// Funnelling kunai's own port would publish GET /api/browse, which lists any
// directory on the machine with no root restriction at all, and POST
// /api/sessions, which starts a session with any cwd and is therefore a complete
// escape in one request. The share token would be the only thing standing in
// front of the whole fleet, and one routing mistake would end it.
//
// So the guest never reaches that mux. This one is built from scratch, binds
// 127.0.0.1 on a port of its own, and carries exactly the handlers below.
// Unmatched paths 404 instead of falling through to a SPA handler, which is the
// difference between a second listener and a second door into the same house.
// The test that asserts /api/sessions is 404 here is the load-bearing claim of
// the whole feature, so it is pinned rather than left to the absence of a route.

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hegade/kunai/internal/session"
	"github.com/hegade/kunai/internal/share"
)

// shareGate serves the guest-facing routes on their own listener.
type shareGate struct {
	shares *share.Store
	// sessions is deliberately a narrow interface rather than *Server: the gate
	// needs to look a session up and nothing else, and taking the whole server
	// would make "what can a guest reach" a question about every field on it.
	sessions sessionLookup
	pwa      fs.FS
	// portFile remembers the port to bind, so a Funnel mapping made once keeps
	// working across restarts. Empty means "do not remember" (tests).
	portFile string

	mu      sync.Mutex
	port    int
	started bool
	srv     *http.Server
	// guests counts open guest sockets. A share link is a public URL, and every
	// socket on it attaches a subscriber the session fans every event out to, so
	// without a ceiling one link posted anywhere is an unbounded fan-out on the
	// owner's machine. Generous, because the legitimate case is a handful of
	// people watching one conversation.
	guests int
}

// maxGuestSockets is the ceiling on concurrent viewers of all shares on this
// machine. Beyond it the gate refuses politely rather than accepting work it
// cannot bound.
const maxGuestSockets = 32

// sessionLookup is all the gate may do to the rest of kunai. *session.Manager
// satisfies it as-is; the interface exists so the gate can be stood up in a test
// without one, and so this stays the complete list of what a guest's request can
// reach.
type sessionLookup interface {
	Get(id string) (*session.Session, bool)
}

func newShareGate(shares *share.Store, sessions sessionLookup, pwa fs.FS, portFile string) *shareGate {
	return &shareGate{shares: shares, sessions: sessions, pwa: pwa, portFile: portFile}
}

// Port is the localhost port the gate listens on, 0 until started. This is what
// `tailscale funnel` is pointed at.
func (g *shareGate) Port() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.port
}

// start binds a localhost port and serves until ctx is done. Idempotent, so the
// create path can call it without tracking whether it already ran.
//
// Localhost only, never the tailnet address: the only thing that should be able
// to reach this is the Funnel proxy running on this same machine. If Funnel is
// off, the gate is bound and simply unreachable from anywhere, which is the
// right resting state.
func (g *shareGate) start(ctx context.Context) error {
	g.mu.Lock()
	if g.started {
		g.mu.Unlock()
		return nil
	}
	// The same port every time, remembered on disk.
	//
	// This used to be a bare 127.0.0.1:0, a fresh ephemeral port on every start.
	// A Funnel mapping is permanent and points at a NUMBER, so each restart left
	// the previous mapping aimed at a port that no longer existed: the link
	// stopped working, kunai no longer recognised the mapping as its own, and the
	// port was reported busy. Turning public access on again just consumed the
	// next Funnel port, and there are only three. That is how all three came to be
	// pointed at dead gates on one machine.
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(g.rememberedPort()))
	if err != nil {
		// Something else took it while we were away; fall back to any free port and
		// remember the new one. A stale mapping is repointed by funnelStatus, which
		// treats a mapping at a dead loopback port as reclaimable.
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		g.mu.Unlock()
		return err
	}
	srv := &http.Server{
		Handler: g.mux(),
		// A public listener needs its own timeouts. The main server sits behind the
		// tailnet where every client is one of yours; this one answers anybody.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	g.port = ln.Addr().(*net.TCPAddr).Port
	g.srv = srv
	g.started = true
	g.mu.Unlock()
	g.rememberPort(g.Port())

	log.Printf("share gate listening on 127.0.0.1:%d", g.Port())
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("share gate: %v", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		g.mu.Lock()
		g.started, g.port = false, 0
		g.mu.Unlock()
	}()
	return nil
}

// rememberedPort is the port this gate last served on, or 0 for "any free one".
// Kept beside the shares it serves, because a Funnel mapping outlives the
// process and points at a number.
func (g *shareGate) rememberedPort() int {
	if g.portFile == "" {
		return 0
	}
	b, err := os.ReadFile(g.portFile)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || n < 1024 || n > 65535 {
		return 0
	}
	return n
}

func (g *shareGate) rememberPort(port int) {
	if g.portFile == "" || port == 0 {
		return
	}
	_ = os.WriteFile(g.portFile, []byte(strconv.Itoa(port)), 0o600)
}

// stop shuts the listener down and forgets its port, so start can bind a fresh
// one later. Called when the last share goes: a listener with nothing to serve
// is a port open for no reason, and leaving it up is what let the public Funnel
// mapping outlive every share it was opened for.
func (g *shareGate) stop() {
	g.mu.Lock()
	srv, was := g.srv, g.started
	g.srv, g.started, g.port = nil, false, 0
	g.mu.Unlock()
	if !was || srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Print("share gate closed; nothing is shared any more")
}

// enterGuest reserves a slot for one guest socket, or reports that the machine
// is already carrying as many as it will.
func (g *shareGate) enterGuest() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.guests >= maxGuestSockets {
		return false
	}
	g.guests++
	return true
}

func (g *shareGate) leaveGuest() {
	g.mu.Lock()
	if g.guests > 0 {
		g.guests--
	}
	g.mu.Unlock()
}

// mux is the entire public surface. Every route a guest can reach is on this
// page; there is no catch-all and no fallthrough.
func (g *shareGate) mux() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /s/{token}", g.handlePage)
	m.HandleFunc("GET /api/share/{token}", g.handleHello)
	m.HandleFunc("POST /api/share/{token}/pair", g.handlePair)
	m.HandleFunc("GET /api/share/{token}/pair", g.handlePairStatus)
	m.HandleFunc("GET /ws/share/{token}", g.handleWS)
	// Only the fingerprinted bundle, and only as files: no directory listing, and
	// no reaching the rest of the embedded tree.
	m.Handle("GET /assets/", g.assets())
	return m
}

// assets serves the built bundle and nothing else. The guest page is a separate
// Vite entry, but the chunks it shares with the owner's app live under the same
// prefix, so the prefix is what is exposed rather than a list of filenames.
func (g *shareGate) assets() http.Handler {
	files := http.FileServer(http.FS(g.pwa))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "..") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		files.ServeHTTP(w, r)
	})
}

// handlePage serves the guest app shell. The token stays in the URL and is read
// by the client; it is never set as a cookie, because a cookie would let any page
// in the guest's browser drive the session, and because cors.go grants a wildcard
// origin on the stated premise that kunai's API carries no credentials a browser
// attaches on its own.
func (g *shareGate) handlePage(w http.ResponseWriter, r *http.Request) {
	// Resolve first so a dead link is a clean 404 rather than an app shell that
	// loads and then reports failure.
	if _, err := g.shares.Get(r.PathValue("token")); err != nil {
		http.NotFound(w, r)
		return
	}
	b, err := fs.ReadFile(g.pwa, "share.html")
	if err != nil {
		// The guest bundle is not built yet; say so rather than serving the owner's
		// app, which would be a far worse thing to hand a stranger.
		http.Error(w, "the shared view is unavailable on this machine", http.StatusNotFound)
		return
	}
	b = withoutServiceWorker(b)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	// A shared conversation should not be indexed or previewed by anything that
	// happens to see the link.
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_, _ = w.Write(b)
}

// swTag matches the service-worker registration vite-plugin-pwa injects into
// every HTML entry it builds.
var swTag = regexp.MustCompile(`\s*<script[^>]*registerSW\.js[^>]*></script>`)

// withoutServiceWorker strips the PWA registration from the guest page.
//
// The PWA is the owner's app: it is scoped to "/", it precaches their bundle, and
// it is what a long-open window uses to update itself. A visitor holding a
// temporary link should install none of that, and a service worker they did
// install would outlive the share, keeping a cached copy of somebody else's app
// on their machine after the link had expired.
//
// This happens here rather than in the build because vite-plugin-pwa injects the
// tag after transformIndexHtml runs, so a build-time strip would depend on
// matching that plugin's internal ordering. The gate is the only thing that ever
// serves this page, so removing it at the point of serving is both simpler and a
// guarantee rather than a hope.
func withoutServiceWorker(html []byte) []byte {
	return swTag.ReplaceAll(html, nil)
}

// shareHello is what the guest page needs to render before it opens a socket.
// Deliberately thin: no cwd, no machine name, no account, nothing about the rest
// of the fleet.
type shareHello struct {
	Title     string `json:"title"`
	Tier      string `json:"tier"`
	ExpiresAt int64  `json:"expires_at"`
	// Paired reports whether THIS device is the approved guest.
	Paired bool `json:"paired"`
	// Taken reports that somebody else already holds the prompting slot, so the
	// page can say the link is spent instead of offering a pairing that cannot
	// succeed.
	Taken     bool   `json:"taken"`
	PairCode  string `json:"pair_code,omitempty"` // this device's outstanding request
	TurnsLeft int    `json:"turns_left,omitempty"`
	Capped    bool   `json:"capped,omitempty"`
	Live      bool   `json:"live"` // the session still exists
}

func (g *shareGate) handleHello(w http.ResponseWriter, r *http.Request) {
	sh, err := g.shares.Get(r.PathValue("token"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	device := deviceOf(r)
	left, capped := sh.TurnsLeft()
	_, live := g.sessions.Get(sh.SessionID)
	out := shareHello{
		Title:     sh.Title,
		Tier:      string(sh.Tier),
		ExpiresAt: sh.ExpiresAt,
		Paired:    sh.Paired(device),
		Taken:     sh.Guest != nil && !sh.Paired(device),
		TurnsLeft: left,
		Capped:    capped,
		Live:      live,
	}
	if sh.Pending != nil && sh.Pending.Device == device {
		out.PairCode = sh.Pending.Code
	}
	writeGuestJSON(w, out)
}

// handlePair records this device's request to drive the session and returns the
// code the owner approves. It does not grant anything.
func (g *shareGate) handlePair(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Device string `json:"device"`
		Name   string `json:"name"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body) != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	code, err := g.shares.Ask(r.PathValue("token"), body.Device, trimName(body.Name))
	if err != nil {
		if errors.Is(err, share.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeGuestJSON(w, map[string]string{"code": code})
}

// handlePairStatus lets the guest page poll for approval without reloading.
func (g *shareGate) handlePairStatus(w http.ResponseWriter, r *http.Request) {
	sh, err := g.shares.Get(r.PathValue("token"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	writeGuestJSON(w, map[string]bool{"paired": sh.Paired(deviceOf(r))})
}

// deviceOf reads the guest's device key from a header. Not a cookie: a cookie is
// attached by the browser to every request from any page on this origin, which is
// exactly the property that would let a hostile page drive the session.
//
// Header only. The query string used to be accepted here as well, which put the
// credential into a place that ends up in referrers, history and any proxy log
// along the way, on routes that never needed it.
func deviceOf(r *http.Request) string {
	return boundDevice(r.Header.Get("X-Kunai-Device"))
}

// deviceOfSocket also accepts the key from the query string, which the websocket
// has no way to avoid: a browser cannot set headers on a WebSocket handshake.
// Kept separate from deviceOf so the widening applies to exactly the one route
// that is stuck with it.
func deviceOfSocket(r *http.Request) string {
	if d := deviceOf(r); d != "" {
		return d
	}
	return boundDevice(r.URL.Query().Get("device"))
}

// maxDeviceLen bounds the key a guest may claim. The client generates 16 random
// bytes base64'd, so anything near this is already far larger than legitimate;
// the bound exists because the value is persisted to shares.json and a stranger
// should not choose how much of it to fill.
const maxDeviceLen = 128

func boundDevice(d string) string {
	if len(d) > maxDeviceLen {
		return ""
	}
	return d
}

// trimName bounds what a stranger can write into the owner's approval prompt.
// It is displayed next to an Approve button, so it must not be able to run to a
// paragraph or carry newlines that reshape the dialog.
func trimName(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, s))
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

// writeGuestJSON is the gate's own responder. Separate from the owner-side
// writeJSON only to add no-store: a guest's answers carry a pairing code and a
// live expiry, and neither should sit in a shared cache on the way out.
func writeGuestJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

// shareURL builds the link handed to a guest: the machine's own public origin,
// but on the port Funnel is serving rather than the tailnet port kunai itself
// listens on. Those are different numbers and the guest can only reach the
// former.
//
// The origin's own port is always dropped first. Leaving it and appending was a
// bug on 443, where there is nothing to append and the tailnet's :8443 stayed
// behind, producing a link that resolved to a port no outsider can open.
func shareURL(origin string, funnelPort int, token string) string {
	origin = strings.TrimSuffix(origin, "/")
	if funnelPort != 0 {
		// Only a port after the host, never the "https:" colon, and never a colon
		// inside a bracketed IPv6 literal: "https://[fd7a::1]" has three of those
		// and taking the last one would cut the address in half.
		if i := strings.LastIndex(origin, ":"); i > strings.Index(origin, "//") && i > strings.LastIndex(origin, "]") {
			origin = origin[:i]
		}
		// 443 is implicit in an https URL, and writing it out makes a link people
		// assume is wrong.
		if funnelPort != 443 {
			origin += ":" + strconv.Itoa(funnelPort)
		}
	}
	return origin + "/s/" + token
}

// gatePortFile is where the share gate's port is remembered. Empty data dir
// means an ephemeral port and no memory, which is right for a dev run.
func gatePortFile(dataDir string) string {
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, "gate-port")
}
