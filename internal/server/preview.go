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
}

// previewView is one server, plus what kunai can do about it.
type previewView struct {
	preview.Server
	// URL is where to open it, once kunai is forwarding (or if it never needed
	// forwarding). Empty means "not reachable yet, ask to open it".
	URL string `json:"url,omitempty"`
	// Forwarding is true when kunai is holding a listener for this port.
	Forwarding bool `json:"forwarding"`
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

	lsof, err := previewScan("lsof", "-iTCP", "-sTCP:LISTEN", "-P", "-n", "-F", "pcn")
	if err != nil && lsof == "" {
		// lsof exits non-zero when it finds nothing, so only an EMPTY result is a
		// real failure. Treating a normal "nothing listening" as an error would put
		// a permanent red state on a machine that is simply idle.
		return nil, errNoLSOF
	}
	ps, err := previewScan("ps", "-Ao", "pid=,ppid=")
	if err != nil && ps == "" {
		return nil, err
	}

	all := preview.ParseLSOF(lsof)
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

// ownPort reports whether a port is one kunai itself serves.
func (s *Server) servesPort(port int) bool {
	if p := portOf(s.cfg.Addr); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n == port {
			return true
		}
	}
	return s.previews != nil && s.previews.holds(port)
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
	out := make([]previewView, 0, len(servers))
	for _, srv := range servers {
		v := previewView{Server: srv}
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

// previewURL is where a forwarded port answers: this machine's own origin with
// the port swapped. Built from -public-url so it carries the scheme and hostname
// a browser can actually use, rather than a bare IP with no certificate.
func (s *Server) previewURL(port int) string {
	origin := normalizeOrigin(s.cfg.PublicURL)
	if origin == "" {
		return "http://localhost:" + strconv.Itoa(port)
	}
	u, err := url.Parse(origin)
	if err != nil {
		return ""
	}
	u.Host = net.JoinHostPort(u.Hostname(), strconv.Itoa(port))
	return u.String()
}

var (
	errNoSession = errors.New("no such session")
	errNoLSOF    = errors.New("cannot list local servers on this machine (lsof is missing)")
)
