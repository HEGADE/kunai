package server

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hegade/kunai/internal/session"
	"github.com/hegade/kunai/internal/share"
)

// noSessions stands in for the manager. The gate is only allowed to look a
// session up, so this is the entire surface it can reach.
type noSessions struct{}

func (noSessions) Get(string) (*session.Session, bool) { return nil, false }

func testGate(t *testing.T) (*shareGate, *share.Store) {
	t.Helper()
	store := share.NewStore(filepath.Join(t.TempDir(), "shares.json"))
	return newShareGate(store, noSessions{}, testPWA{}), store
}

// testPWA is an empty asset tree. The routing is what these tests are about, so
// nothing needs to be in it: a served file and a missing one both prove the
// route exists, and every path under test is one that must not.
type testPWA struct{}

func (testPWA) Open(string) (fs.File, error) { return nil, fs.ErrNotExist }

// THE test. The whole design rests on one claim: a guest's listener physically
// cannot serve the owner's routes, because it is a different mux, not because
// some check on those routes turns them down.
//
// Funnelling kunai's own port would publish GET /api/browse, which lists any
// directory on the machine with no root restriction at all, and POST
// /api/sessions, which creates a session with any cwd and is a complete escape
// in one request. If somebody ever "simplifies" this by mounting the share
// handlers on the main mux behind a token check, this test is what says no.
func TestShareGateServesNothingButShareRoutes(t *testing.T) {
	g, _ := testGate(t)
	mux := g.mux()

	forbidden := []struct{ method, path string }{
		{"GET", "/api/sessions"},
		{"POST", "/api/sessions"},
		{"GET", "/api/browse?path=/"},
		{"POST", "/api/upload"},
		{"GET", "/api/stats"},
		{"GET", "/api/usage"},
		{"GET", "/api/machines"},
		{"GET", "/api/history"},
		{"GET", "/api/clis"},
		{"GET", "/api/accounts"},
		{"GET", "/api/providers"},
		{"GET", "/api/shares"},
		{"GET", "/api/worktrees"},
		{"GET", "/api/sessions/abc/revert"},
		{"POST", "/api/sessions/abc/revert"},
		{"GET", "/ws/app/abc"},
		{"GET", "/ws/fleet"},
		{"GET", "/"},
		{"GET", "/index.html"},
		{"GET", "/settings"},
	}
	for _, c := range forbidden {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s returned %d from the public gate; it must not be routed at all",
				c.method, c.path, rec.Code)
		}
	}
}

// An unknown token is a 404 that says nothing else, so probing the endpoint
// cannot distinguish a real token from a wrong one.
func TestShareGateHidesWhetherATokenIsReal(t *testing.T) {
	g, store := testGate(t)
	sh, err := store.Create(share.Share{SessionID: "s", Tier: share.TierView, Title: "secret project"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	mux := g.mux()

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/share/not-a-real-token", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown token gave %d, want 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "expired") {
		t.Error("the response distinguishes an expired token from an unknown one")
	}

	// A revoked token answers exactly the same way.
	if err := store.Revoke(sh.Token); err != nil {
		t.Fatal(err)
	}
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, httptest.NewRequest("GET", "/api/share/"+sh.Token, nil))
	if rec2.Code != http.StatusNotFound || rec2.Body.String() != rec.Body.String() {
		t.Errorf("a revoked token answered differently from an unknown one: %d %q vs %d %q",
			rec2.Code, rec2.Body.String(), rec.Code, rec.Body.String())
	}
}

// The hello a guest gets must not carry the machine's business: no cwd, no
// account name, no project list, no fleet.
func TestShareHelloSaysNothingAboutTheMachine(t *testing.T) {
	g, store := testGate(t)
	sh, err := store.Create(share.Share{
		SessionID: "s", Tier: share.TierView, Title: "Fix the parser",
		Roots: []string{"/home/someone/private-repo"},
	}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	g.mux().ServeHTTP(rec, httptest.NewRequest("GET", "/api/share/"+sh.Token, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("hello gave %d", rec.Code)
	}
	body := rec.Body.String()
	for _, leak := range []string{"/home/someone", "private-repo", "roots", "session_id", "token"} {
		if strings.Contains(body, leak) {
			t.Errorf("the guest hello leaks %q: %s", leak, body)
		}
	}
	var hello shareHello
	if json.Unmarshal(rec.Body.Bytes(), &hello) != nil {
		t.Fatal("hello was not decodable")
	}
	if hello.Title != "Fix the parser" || hello.Tier != "view" {
		t.Errorf("hello lost what the guest does need: %+v", hello)
	}
}

// The three-command allowlist. Everything else a websocket client can normally
// send is refused, whatever the tier and whoever is asking.
func TestGuestCommandAllowlistIsPositive(t *testing.T) {
	allowed := []string{session.CmdPrompt, session.CmdInterrupt, session.CmdCancelQueued}
	for _, c := range allowed {
		if !guestCommands[c] {
			t.Errorf("%q should be available to a guest", c)
		}
	}
	// Each of these is refused for its own reason; see guestCommands.
	for _, c := range []string{
		session.CmdPermission, // approving is the authority that voids every guard
		session.CmdSetMode,    // would disarm the permission gate
		session.CmdAddProject, // arbitrary filesystem path: a one-frame escape
		session.CmdSetModel,   // spends the owner's quota differently
		session.CmdStartLoop,  // unattended spend
		session.CmdStopLoop,   // ends the owner's unattended run
	} {
		if guestCommands[c] {
			t.Errorf("%q must never be available to a guest", c)
		}
	}
	// And the list is exactly that long, so a command added to the protocol later
	// is unavailable until somebody opts it in deliberately.
	if len(guestCommands) != len(allowed) {
		t.Errorf("the guest allowlist has %d entries, want %d: a new command was added without a decision",
			len(guestCommands), len(allowed))
	}
}

// The share's floor is enforced against the stored record, never against the
// number the guest sends. The ring only offers since(n), so a guest handed a
// floor could otherwise ask for a lower one and read the whole conversation the
// owner meant to keep back.
func TestFromSeqFloorCannotBeLoweredByTheGuest(t *testing.T) {
	if got := maxSeq(100, 0); got != 100 {
		t.Errorf("a guest asking for since=0 against a floor of 100 got %d", got)
	}
	if got := maxSeq(100, 5); got != 100 {
		t.Errorf("a guest asking for since=5 against a floor of 100 got %d", got)
	}
	// Above the floor, the guest's own resume point is honoured, or reconnecting
	// would replay everything they already have.
	if got := maxSeq(100, 250); got != 250 {
		t.Errorf("a reconnecting guest lost their place: got %d, want 250", got)
	}
}

func TestShareURLUsesTheFunnelPort(t *testing.T) {
	const origin = "https://box.tail1234.ts.net:8443"
	if got := shareURL(origin, 10000, "TOK"); got != "https://box.tail1234.ts.net:10000/s/TOK" {
		t.Errorf("shareURL = %q", got)
	}
	// 443 is implicit in a URL, so it must not be written out.
	if got := shareURL(origin, 443, "TOK"); got != "https://box.tail1234.ts.net/s/TOK" {
		t.Errorf("shareURL on 443 = %q, want no explicit port", got)
	}
}

// Funnel is outward-facing, so the exact command is asserted rather than run.
func TestFunnelCommands(t *testing.T) {
	on := strings.Join(funnelOnArgs(10000, 41234), " ")
	if on != "tailscale funnel --bg --https=10000 http://127.0.0.1:41234" {
		t.Errorf("funnel on = %q", on)
	}
	off := strings.Join(funnelOffArgs(10000), " ")
	if off != "tailscale funnel --https=10000 off" {
		t.Errorf("funnel off = %q", off)
	}
	// Only the three ports Tailscale actually serves.
	for _, p := range []int{443, 8443, 10000} {
		if !allowedFunnelPort(p) {
			t.Errorf("port %d should be allowed", p)
		}
	}
	for _, p := range []int{80, 8080, 0, 44300} {
		if allowedFunnelPort(p) {
			t.Errorf("port %d is not a Funnel port and must be refused", p)
		}
	}
}

// The guest page must not install the owner's service worker. The PWA is scoped
// to "/", precaches the owner's bundle, and is how a long-open window updates
// itself; a visitor holding a temporary link should install none of it, and one
// they did install would outlive the share.
//
// Asserted against the REAL built share.html, not a fixture, because the tag is
// injected by vite-plugin-pwa and the whole point is to survive it changing.
func TestGuestPageDoesNotRegisterTheServiceWorker(t *testing.T) {
	built, err := os.ReadFile(filepath.Join("..", "webui", "dist", "share.html"))
	if err != nil {
		t.Skip("no built share.html to check")
	}
	if !bytes.Contains(built, []byte("registerSW.js")) {
		t.Log("the build no longer injects registerSW.js; the strip is now redundant but harmless")
	}
	served := withoutServiceWorker(built)
	if bytes.Contains(served, []byte("registerSW.js")) {
		t.Fatal("the guest page still registers the owner's service worker")
	}
	// And it is still a working page: the entry script must survive.
	if !bytes.Contains(served, []byte("/assets/share-")) {
		t.Fatalf("the strip removed the guest bundle too: %s", served)
	}
}

// The owner's own page is untouched by any of this; its registration is what
// makes a long-open PWA pick up a deploy.
func TestOwnerPageKeepsItsServiceWorker(t *testing.T) {
	built, err := os.ReadFile(filepath.Join("..", "webui", "dist", "index.html"))
	if err != nil {
		t.Skip("no built index.html to check")
	}
	if !bytes.Contains(built, []byte("registerSW.js")) {
		t.Error("the owner's app stopped registering its service worker, so it will not auto-update")
	}
}

// kunai binds its own port directly rather than through `tailscale serve`, so
// tailscale reports it as free. Offering it would have the owner turn on public
// access by knocking their own app off the air.
func TestFunnelNeverOffersKunaisOwnPort(t *testing.T) {
	prev := execOut
	defer func() { execOut = prev }()
	// An empty serve config: as far as tailscale is concerned every port is free.
	execOut = func(string, ...string) (string, error) { return `{}`, nil }

	s := &Server{cfg: Config{Addr: "100.64.0.1:8443"}}
	st := s.funnelStatus(41234)

	for _, p := range st.Free {
		if p == 8443 {
			t.Fatal("offered 8443, which is the port kunai itself is listening on")
		}
	}
	if st.InUse[8443] == "" {
		t.Error("8443 should be reported as taken, with a reason the owner can read")
	}
	if len(st.Free) == 0 {
		t.Error("no port was offered at all")
	}
}

func TestOwnPortReadsTheBindAddress(t *testing.T) {
	for addr, want := range map[string]int{
		"100.64.0.1:8443": 8443,
		"127.0.0.1:8899":  8899,
		"":                0,
		"nonsense":        0,
	} {
		if got := (&Server{cfg: Config{Addr: addr}}).ownPort(); got != want {
			t.Errorf("ownPort(%q) = %d, want %d", addr, got, want)
		}
	}
}
