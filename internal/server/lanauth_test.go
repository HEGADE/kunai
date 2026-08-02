package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hegade/kunai/internal/lanauth"
)

// The gate's allowlist is written as "everything under /api/ and /ws/ is private
// unless named", so a route added later is closed by default. This pins that
// direction, because getting it the other way round would be silent: a new
// endpoint would simply be public and nothing would fail.
func TestOnlyTheShellAndSignInAreReachableWithoutAPIN(t *testing.T) {
	private := []string{
		"/api/sessions", "/api/stats", "/api/browse", "/api/machines",
		"/api/lan/pin", "/api/lan/devices", "/api/sessions/abc/file",
		"/ws/app/abc", "/ws/fleet",
		// The shape that matters most: something nobody has written yet.
		"/api/some/endpoint/added/next/year",
	}
	for _, p := range private {
		if lanPublicPath(p) {
			t.Errorf("%s is reachable without signing in", p)
		}
	}
	public := []string{
		"/", "/index.html", "/assets/index-abc123.js", "/manifest.webmanifest",
		"/api/lan/status", "/api/lan/login",
	}
	for _, p := range public {
		if !lanPublicPath(p) {
			t.Errorf("%s is blocked, so the sign-in screen cannot load", p)
		}
	}
}

// A stranger on the network must get nowhere without the PIN, and the failure has
// to be a refusal rather than a partial answer.
func TestGateRefusesWithoutASession(t *testing.T) {
	s := newTestServerWithPIN(t, "481920")
	reached := false
	gated := s.lanAuthGate(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))

	r := httptest.NewRequest(http.MethodPost, "/api/sessions", nil)
	r.Host = "192.168.0.6:8443"
	w := httptest.NewRecorder()
	gated.ServeHTTP(w, r)

	if reached {
		t.Fatal("an unauthenticated request reached the application")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", w.Code)
	}

	// A made-up cookie is no better than none.
	r2 := httptest.NewRequest(http.MethodPost, "/api/sessions", nil)
	r2.AddCookie(&http.Cookie{Name: lanCookie, Value: "not-a-real-token"})
	w2 := httptest.NewRecorder()
	gated.ServeHTTP(w2, r2)
	if reached || w2.Code != http.StatusUnauthorized {
		t.Error("a forged session cookie was accepted")
	}
}

// The round trip: sign in, get a cookie, and have it open the door.
func TestSigningInWithTheRightPINOpensTheGate(t *testing.T) {
	s := newTestServerWithPIN(t, "481920")

	w := httptest.NewRecorder()
	s.handleLANLogin(w, loginReq(`{"pin":"481920","label":"tablet"}`, "192.168.0.20:5000"))
	if w.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", w.Code, w.Body.String())
	}
	cookie := sessionCookie(t, w.Result().Cookies())

	// The cookie a browser will actually store must be locked down.
	if !cookie.HttpOnly {
		t.Error("the session cookie is readable by scripts")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Error("the session cookie is not SameSite=Strict, so another site can cause it to be sent")
	}

	reached := false
	gated := s.lanAuthGate(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	r := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	r.AddCookie(cookie)
	gated.ServeHTTP(httptest.NewRecorder(), r)
	if !reached {
		t.Error("a signed-in request was still refused")
	}
}

// Guessing has to stop, and it has to stop even when the guesser changes address
// between attempts, which on a local network costs them nothing.
func TestGuessingOverHTTPIsStoppedEvenFromManyAddresses(t *testing.T) {
	s := newTestServerWithPIN(t, "481920")

	allowed := 0
	for i := 0; i < 200; i++ {
		w := httptest.NewRecorder()
		// A different source every single time.
		s.handleLANLogin(w, loginReq(`{"pin":"000001"}`, "192.168.0."+itoa(i%250)+":"+itoa(1024+i)))
		if w.Code != http.StatusTooManyRequests {
			allowed++
		}
	}
	if allowed > 20 {
		t.Fatalf("%d guesses got through from rotating addresses; the global throttle is not holding", allowed)
	}
}

// A wrong PIN and an unset PIN must be indistinguishable from outside, or the
// reply becomes a free oracle about the state of the lock.
func TestFailuresAllLookTheSame(t *testing.T) {
	locked := newTestServerWithPIN(t, "481920")
	open := newTestServer(t)

	a := httptest.NewRecorder()
	locked.handleLANLogin(a, loginReq(`{"pin":"000001"}`, "192.168.0.30:5000"))
	b := httptest.NewRecorder()
	open.handleLANLogin(b, loginReq(`{"pin":"000001"}`, "192.168.0.30:5000"))

	if a.Code != b.Code {
		t.Errorf("status differs: %d with a PIN set, %d without", a.Code, b.Code)
	}
	if a.Body.String() != b.Body.String() {
		t.Errorf("body differs:\n  with PIN: %s\n  without:  %s", a.Body.String(), b.Body.String())
	}
	if strings.Contains(strings.ToLower(a.Body.String()), "no pin") {
		t.Error("the reply tells a stranger whether a PIN is even set")
	}
}

// The throttle key must come from the connection, never from a header the caller
// chooses, or an attacker mints a fresh budget per guess.
func TestThrottleKeyIgnoresClientSuppliedHeaders(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/lan/login", nil)
	r.RemoteAddr = "192.168.0.44:5000"
	r.Header.Set("X-Forwarded-For", "10.9.9.9")
	r.Header.Set("X-Real-IP", "10.9.9.9")
	if got := sourceOf(r); got != "192.168.0.44" {
		t.Errorf("sourceOf = %q, want the peer address; a spoofable header is being trusted", got)
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	s := &Server{}
	s.lanAuth = openTestStore(t)
	return s
}

func newTestServerWithPIN(t *testing.T, pin string) *Server {
	t.Helper()
	s := newTestServer(t)
	if err := s.lanAuth.SetPIN(pin); err != nil {
		t.Fatal(err)
	}
	return s
}

func loginReq(body, remote string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/lan/login", strings.NewReader(body))
	r.RemoteAddr = remote
	return r
}

func sessionCookie(t *testing.T, cookies []*http.Cookie) *http.Cookie {
	t.Helper()
	for _, c := range cookies {
		if c.Name == lanCookie {
			return c
		}
	}
	t.Fatal("login set no session cookie")
	return nil
}

func openTestStore(t *testing.T) *lanauth.Store {
	t.Helper()
	return lanauth.Open(filepath.Join(t.TempDir(), "lanauth.json"))
}
