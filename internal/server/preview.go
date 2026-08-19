package server

// Seeing what the agent built.
//
// kunai could always tell you what an agent WROTE and never what it MADE. The
// agent ends a task by running the thing -- `npm run dev`, a test UI, a docs
// server -- and that is a port on a machine you are not sitting at. Finding it
// meant reading the transcript for a number and then working out how to reach a
// loopback socket from a phone, which is to say: you did not.
//
// Two halves. internal/preview answers which ports are listening and whose they
// are (by process ancestry, which is a fact rather than a guess). This file
// answers the second half: making one reachable, on request.

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"time"

	"github.com/hegade/kunai/internal/preview"
)

// scanTimeout bounds the two commands a scan shells out to. Generous for a
// loaded machine, short enough that a wedged lsof cannot hold a request open.
const scanTimeout = 6 * time.Second

// previewScan is injectable so tests assert against captured output instead of
// whatever happens to be listening on the machine running them. Same reason
// guardian.go has execRun.
var previewScan = func(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err
}

func (s *Server) previewRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sessions/{id}/previews", s.handleListPreviews)
	mux.HandleFunc("POST /api/sessions/{id}/previews/{port}", s.handleOpenPreview)
	mux.HandleFunc("DELETE /api/sessions/{id}/previews/{port}", s.handleClosePreview)
	mux.HandleFunc("PATCH /api/sessions/{id}/previews/{port}", s.handleHidePreview)
}

// previewView is one server, plus what kunai can do about it.
type previewView struct {
	preview.Server
	// URL is where to open it, once kunai is forwarding (or if it never needed
	// forwarding). Empty means "not reachable yet, ask to open it".
	URL string `json:"url,omitempty"`
	// Forwarding is true when kunai is holding a listener for this port.
	Forwarding bool `json:"forwarding"`
	// Hidden is true when this row has been dismissed for this session. Sent
	// rather than filtered out, because a dismissal has to be reversible: the
	// client shows the count it is holding back and can ask for them, which is
	// the same rule the sidebar's quiet folders follow. A card that silently
	// swallowed a row would leave a shared port with nothing to turn it off.
	Hidden bool `json:"hidden,omitempty"`
}

// sessionServers finds the listening ports belonging to one session.
func (s *Server) sessionServers(id string) ([]preview.Server, error) {
	sess, ok := s.mgr.Get(id)
	if !ok {
		return nil, errNoSession
	}
	root := sess.PID()
	if root == 0 {
		return nil, nil // not started yet, or already gone: it owns nothing
	}

	// The kernel first, lsof only if it cannot be asked.
	//
	// lsof was the original source and it silently went blind here: a real
	// next-server holding *:3000, owned by kunai's own user, visible to `ss` and
	// present in /proc/net/tcp6, produced zero lines from `lsof -p <pid>` while
	// lsof listed kunai's own sockets in the same run. The preview card was empty
	// for precisely the case the feature exists for. See listen_linux.go.
	all, ok := preview.Listeners()
	if !ok {
		lsof, err := previewScan("lsof", "-iTCP", "-sTCP:LISTEN", "-P", "-n", "-F", "pcn")
		if err != nil && lsof == "" {
			// lsof exits non-zero when it finds nothing, so only an EMPTY result is a
			// real failure. Treating a normal "nothing listening" as an error would put
			// a permanent red state on a machine that is simply idle.
			return nil, errNoLSOF
		}
		all = preview.ParseLSOF(lsof)
	}
	ps, err := previewScan("ps", "-Ao", "pid=,ppid=")
	if err != nil && ps == "" {
		return nil, err
	}

	pids := make([]int, 0, len(all))
	for _, srv := range all {
		pids = append(pids, srv.PID)
	}
	// Ancestry AND working directory, because a backgrounded dev server is
	// orphaned to init the moment the shell that started it exits, which severs
	// its chain to the session. See preview.OwnedBy.
	owned := preview.OwnedBy(all, preview.ParseProcessTree(ps), root, cwdsFor(pids), sess.Cwd)
	// kunai's own listeners are never a preview. They cannot be, since they are
	// not descendants of a session, but a session that somehow spawned one would
	// otherwise be offered a link back to kunai itself.
	out := owned[:0]
	for _, srv := range owned {
		if !s.servesPort(srv.Port) {
			out = append(out, srv)
		}
	}
	return out, nil
}

// servesPort reports whether a port is kunai's own service port.
//
// It deliberately does NOT count a port kunai is forwarding. It used to, and
// that made Stop unreachable: sharing a preview put kunai on the same port, the
// row was then filtered out as "kunai's own", and it disappeared from the card
// the moment you shared it -- still forwarded, with nothing left to turn it off.
// A forwarded preview is the most a preview has ever been, not the least.
// kunai's real listeners are excluded by pid instead (preview.selfPID), which
// covers this port too and is exact where a port number is a guess.
func (s *Server) servesPort(port int) bool {
	p := portOf(s.cfg.Addr)
	if p == "" {
		return false
	}
	n, err := strconv.Atoi(p)
	return err == nil && n == port
}

func (s *Server) handleListPreviews(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	servers, err := s.sessionServers(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.decorate(id, servers))
}

// decorate says, for each server, whether it is already reachable and where.
func (s *Server) decorate(id string, servers []preview.Server) []previewView {
	var hidden sessionMeta
	if s.sessionMeta != nil {
		hidden = s.sessionMeta.get(id)
	}
	out := make([]previewView, 0, len(servers))
	for _, srv := range servers {
		v := previewView{Server: srv, Hidden: hidden.hidesPreview(srv.Port)}
		switch {
		case s.previews != nil && s.previews.forwarding(id, srv.Port):
			v.Forwarding = true
			v.URL = s.previewURL(srv.Port)
		case !srv.Local:
			// Already bound to a routable address, so there is nothing to forward:
			// the machine's own name and that port is the answer.
			v.URL = s.previewURL(srv.Port)
		}
		out = append(out, v)
	}
	return out
}

func (s *Server) handleOpenPreview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	port, err := strconv.Atoi(r.PathValue("port"))
	if err != nil || port <= 0 || port > 65535 {
		writeErr(w, http.StatusBadRequest, "bad port")
		return
	}
	// Only a port this session actually owns. Without this check the endpoint is
	// a way to forward ANY loopback service on the machine -- a database, another
	// user's app -- onto the tailnet, which is a much bigger thing than showing
	// somebody their own dev server.
	servers, err := s.sessionServers(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	var found *preview.Server
	for i := range servers {
		if servers[i].Port == port {
			found = &servers[i]
			break
		}
	}
	if found == nil {
		writeErr(w, http.StatusNotFound, "this session is not serving that port")
		return
	}
	if !found.Local {
		// Nothing to do: it is already reachable.
		writeJSON(w, http.StatusOK, previewView{Server: *found, URL: s.previewURL(port)})
		return
	}
	if s.previews == nil {
		writeErr(w, http.StatusServiceUnavailable, "previews need a network address to forward to")
		return
	}
	if err := s.previews.open(id, port); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, previewView{Server: *found, URL: s.previewURL(port), Forwarding: true})
}

func (s *Server) handleClosePreview(w http.ResponseWriter, r *http.Request) {
	port, _ := strconv.Atoi(r.PathValue("port"))
	if s.previews != nil {
		s.previews.close(r.PathValue("id"), port)
	}
	writeJSON(w, http.StatusOK, map[string]any{"forwarding": false})
}

// handleHidePreview dismisses a discovered server from this session's card, or
// brings it back. Body: {"hidden": true}.
//
// Finding a port is a fact about processes and says nothing about whether
// anybody wants to look at it: a language server, a database, a dev server whose
// address you already know are all correctly attributed and all noise. So the
// card needs a way to be told, and it has to be a dismissal rather than a
// filter, because kunai cannot know which of a session's servers is the one you
// meant.
//
// Hiding a SHARED port stops the forwarding first, and that is the load-bearing
// half. The row is the only thing that can turn a forward off (Stop lives on
// it), so hiding one while kunai still held the listener would leave a port
// published on the tailnet with nothing on screen that could take it back --
// exactly the failure servesPort's comment records from the other direction.
func (s *Server) handleHidePreview(w http.ResponseWriter, r *http.Request) {
	if s.sessionMeta == nil {
		writeErr(w, http.StatusServiceUnavailable, "no data dir configured")
		return
	}
	id := r.PathValue("id")
	port, err := strconv.Atoi(r.PathValue("port"))
	if err != nil || port <= 0 || port > 65535 {
		writeErr(w, http.StatusBadRequest, "bad port")
		return
	}
	var req struct {
		Hidden bool `json:"hidden"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Hidden && s.previews != nil {
		s.previews.close(id, port)
	}
	s.sessionMeta.setPreviewHidden(id, port, req.Hidden)
	writeJSON(w, http.StatusOK, map[string]any{"port": port, "hidden": req.Hidden})
}

// previewURL is where a preview answers: this machine's hostname with the port
// swapped in.
//
// The scheme is ALWAYS http, and taking it from -public-url instead was a real
// bug: that origin is https because kunai terminates TLS on its OWN port with a
// tailscale cert, and none of that is true one port over. A dev server speaks
// plain HTTP, and when kunai forwards one it is a raw TCP splice
// (previewforward.go) that adds no TLS of its own -- so the link came out as
// https://host:3000 and the browser met a plain HTTP server behind a TLS
// handshake: ERR_SSL_PROTOCOL_ERROR, reproduced here as OpenSSL's "wrong version
// number". The hostname is still worth taking from the origin, since it is a
// name the other device can actually resolve.
//
// The residue: a dev server that serves TLS itself (vite --https) gets an http
// link that will not load. That is rarer than the case this fixes, and guessing
// by probing the port would trade a wrong link for a slow one.
func (s *Server) previewURL(port int) string {
	host := "localhost"
	if origin := normalizeOrigin(s.cfg.PublicURL); origin != "" {
		u, err := url.Parse(origin)
		if err != nil {
			return ""
		}
		host = u.Hostname()
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port))
}

var (
	errNoSession = errors.New("no such session")
	errNoLSOF    = errors.New("cannot list local servers on this machine (lsof is missing)")
)
