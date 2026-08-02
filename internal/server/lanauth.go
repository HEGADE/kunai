package server

// The HTTP half of the network lock. The rules live in internal/lanauth; this
// file only carries them over the wire.
//
// Where the lock applies is as much of the design as how it works:
//
//   - The NETWORK listener is locked. That is the one that can be reached by
//     something you do not control.
//   - The LOOPBACK listener never is, and that is deliberate rather than an
//     omission. Anything that can reach loopback is already running as you on
//     this machine and could run `claude` without us. Locking it would buy
//     nothing and would make a forgotten PIN unrecoverable; instead the machine
//     itself is always the way back in.
//   - The TAILNET listener is unchanged, because the tailnet is already an auth
//     perimeter with its own identity and ACLs.

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/hegade/kunai/internal/lanauth"
)

// lanCookie is the session cookie's name.
const lanCookie = "kunai_lan"

// lanAuthGate requires a signed-in session for anything that does something.
//
// The allowlist is the shell, not the app: the static files have to load so the
// PIN screen can be shown at all, and everything under /api/ or /ws/ is refused
// without a session. Framed that way round on purpose -- a new endpoint is
// private by default, and only static assets are open, which is the direction
// that fails safe when somebody adds a route and forgets this file exists.
func (s *Server) lanAuthGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if lanPublicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		c, err := r.Cookie(lanCookie)
		if err != nil || !s.lanAuth.Valid(c.Value) {
			writeErr(w, http.StatusUnauthorized, "sign in with the PIN")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// lanPublicPath reports whether a path may be served without signing in: the app
// shell, and the endpoints used to sign in.
func lanPublicPath(p string) bool {
	switch p {
	case "/api/lan/status", "/api/lan/login":
		return true
	}
	return !strings.HasPrefix(p, "/api/") && !strings.HasPrefix(p, "/ws/")
}

// lanAuthRoutes registers the sign-in endpoints. They live on the same mux as
// everything else; the gate above is what makes them reachable unauthenticated.
func (s *Server) lanAuthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/lan/status", s.handleLANStatus)
	mux.HandleFunc("POST /api/lan/login", s.handleLANLogin)
	mux.HandleFunc("POST /api/lan/logout", s.handleLANLogout)
}

// handleLANStatus tells a client whether it is signed in and, if it is locked
// out, how long it has to wait.
//
// It reports the wait deliberately. Hiding it would not slow an attacker down --
// they can see they are being refused -- and without it the owner cannot tell a
// lockout from a broken server.
func (s *Server) handleLANStatus(w http.ResponseWriter, r *http.Request) {
	signedIn := false
	if c, err := r.Cookie(lanCookie); err == nil {
		signedIn = s.lanAuth.Valid(c.Value)
	}
	wait := time.Duration(0)
	if !signedIn {
		wait = s.lanAuth.LockedFor(sourceOf(r))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"signed_in":      signedIn,
		"retry_after_ms": wait.Milliseconds(),
	})
}

func (s *Server) handleLANLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PIN   string `json:"pin"`
		Label string `json:"label"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	token, err := s.lanAuth.Login(body.PIN, sourceOf(r), body.Label)
	if err != nil {
		var locked *lanauth.ErrLocked
		if errors.As(err, &locked) {
			w.Header().Set("Retry-After", itoaSeconds(locked.RetryAfter))
			writeJSON(w, http.StatusTooManyRequests, map[string]any{
				"error":          "too many attempts",
				"retry_after_ms": locked.RetryAfter.Milliseconds(),
			})
			return
		}
		// One message for every failure. "No PIN is set" and "wrong PIN" are
		// different to us and must look identical from outside, or an attacker
		// learns the state of the lock for free.
		writeErr(w, http.StatusUnauthorized, "wrong PIN")
		return
	}
	http.SetCookie(w, s.lanSessionCookie(r, token, int(lanauth.SessionTTL/time.Second)))
	writeJSON(w, http.StatusOK, map[string]any{"signed_in": true})
}

func (s *Server) handleLANLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(lanCookie); err == nil {
		s.lanAuth.Forget(c.Value)
	}
	http.SetCookie(w, s.lanSessionCookie(r, "", -1))
	writeJSON(w, http.StatusOK, map[string]any{"signed_in": false})
}

// lanSessionCookie builds the session cookie.
//
// HttpOnly so a scripting bug cannot read the token. SameSite=Strict so no other
// site can cause the browser to send it -- which, with a cookie, is the whole
// CSRF question, and is why a cookie is safe here specifically: this listener
// serves no wildcard CORS header and refuses a cross-site Origin outright.
//
// A cookie rather than a header because the WEBSOCKET needs it. A browser cannot
// set headers on a websocket handshake, so a bearer token would have to travel in
// the query string, which is the one place credentials reliably end up in logs.
func (s *Server) lanSessionCookie(r *http.Request, token string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     lanCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil,
		MaxAge:   maxAge,
	}
}

// sourceOf is the throttle key: the peer's address without its port, so a single
// attacker cannot get a fresh budget per connection.
//
// Taken from the connection itself and never from a header. X-Forwarded-For and
// friends are chosen by the caller, so keying a rate limit on one would let an
// attacker mint a new identity per guess and defeat the whole thing.
func sourceOf(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func itoaSeconds(d time.Duration) string {
	secs := int(d.Seconds())
	if secs < 1 {
		secs = 1
	}
	return itoa(secs)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
