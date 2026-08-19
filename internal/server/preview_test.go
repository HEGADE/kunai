package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/hegade/kunai/internal/preview"
)

// Dismissing a row must not be able to strand a forward.
//
// The row is the only thing carrying Stop, so hiding a shared port while kunai
// still held the listener would leave a dev server published on the tailnet with
// nothing on screen that could take it back. That is the same failure servesPort
// records from the other direction, arrived at through a different door.
func TestHidingASharedPreviewStopsSharingIt(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := &Server{
		sessionMeta: newSessionMetaStore(filepath.Join(dir, "sessions.json")),
		previews:    newPreviewForwarder(ctx, "127.0.0.1"),
	}

	// A real forward, so "stopped" means the listener is gone rather than a flag
	// having been flipped.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, p, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(p)
	_ = ln.Close()
	if err := s.previews.open("sess-a", port); err != nil {
		t.Fatalf("open: %v", err)
	}

	r := httptest.NewRequest("PATCH", "/api/sessions/sess-a/previews/"+p, strings.NewReader(`{"hidden":true}`))
	r.SetPathValue("id", "sess-a")
	r.SetPathValue("port", p)
	w := httptest.NewRecorder()
	s.handleHidePreview(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if s.previews.forwarding("sess-a", port) {
		t.Error("the port is still forwarded with no row left to stop it")
	}
	if !s.sessionMeta.get("sess-a").hidesPreview(port) {
		t.Error("the dismissal was not recorded")
	}

	// And it is reported as hidden rather than dropped, because a dismissal has
	// to be reversible: filtering it away server-side would leave the client
	// unable to say what it is holding back.
	views := s.decorate("sess-a", []preview.Server{{Port: port, Command: "node"}})
	if len(views) != 1 || !views[0].Hidden {
		t.Errorf("decorate = %+v, want one row marked hidden", views)
	}

	// Bringing it back clears the record, and the store drops an entry that holds
	// nothing rather than keeping an empty one for every session ever dismissed in.
	r2 := httptest.NewRequest("PATCH", "/x", strings.NewReader(`{"hidden":false}`))
	r2.SetPathValue("id", "sess-a")
	r2.SetPathValue("port", p)
	s.handleHidePreview(httptest.NewRecorder(), r2)
	if s.sessionMeta.get("sess-a").hidesPreview(port) {
		t.Error("Show did not bring the row back")
	}
	if len(s.sessionMeta.all()) != 0 {
		t.Error("an entry with no override left was kept")
	}
}

// The forward is a plain TCP splice, so whatever the dev server speaks passes
// through untouched: absolute paths, redirects, streaming, websocket upgrades.
// This proves the byte path end to end against a real server.
//
// The forward binds the SAME port on another address, which is the whole design
// (no path prefix, so nothing has to be rewritten). Testing it therefore needs a
// second local address: 127.0.0.2 is in loopback's own /8 and is not the
// dev server's 127.0.0.1, so the two coexist on one port number.
func TestForwardingCarriesBytesUntouched(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.2:0")
	if err != nil {
		t.Skip("this host has no second loopback address (127.0.0.2); the splice is unexercised here")
	}
	_ = probe.Close()

	// Stand in for `npm run dev`, bound to loopback only.
	dev := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo the path and Host, so a rewriting bug shows up as a wrong answer
		// rather than merely as a failure to connect.
		_, _ = w.Write([]byte("path=" + r.URL.Path + " host=" + r.Host))
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()
	go func() { _ = dev.Serve(ln) }()
	_, devPort, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(devPort)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fwd := newPreviewForwarder(ctx, "127.0.0.2")
	if err := fwd.open("sess-a", port); err != nil {
		t.Fatalf("open: %v", err)
	}

	// Ask the forwarded address for a deep, absolute path.
	res, err := http.Get("http://127.0.0.2:" + devPort + "/assets/app.js?v=1")
	if err != nil {
		t.Fatalf("the forwarded address did not answer: %v", err)
	}
	defer res.Body.Close()
	body := make([]byte, 128)
	n, _ := res.Body.Read(body)
	got := string(body[:n])

	if want := "path=/assets/app.js"; !strings.Contains(got, want) {
		t.Errorf("got %q, want the path delivered unchanged (%q)", got, want)
	}
	// The Host arrives as the client sent it. That is what lets a dev server's
	// own absolute redirects come back pointing at the forwarded address.
	if want := "host=127.0.0.2:" + devPort; !strings.Contains(got, want) {
		t.Errorf("got %q, want the original Host preserved (%q)", got, want)
	}
}

func TestForwarderLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fwd := newPreviewForwarder(ctx, "127.0.0.1")

	// A free port to forward (nothing behind it; open only binds the listener).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, p, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(p)
	_ = ln.Close()

	if err := fwd.open("sess-a", port); err != nil {
		t.Fatalf("open: %v", err)
	}
	if !fwd.forwarding("sess-a", port) {
		t.Error("the port is not reported as forwarding")
	}
	// kunai must not offer its own forward back as a discovered server.
	if !fwd.holds(port) {
		t.Error("holds() does not report a port kunai is listening on")
	}
	// Another session cannot take a port that is already forwarded.
	if err := fwd.open("sess-b", port); err == nil {
		t.Error("a second session was allowed to take a port already forwarded")
	}
	// Asking twice for one you already hold is not an error.
	if err := fwd.open("sess-a", port); err != nil {
		t.Errorf("re-opening an owned forward failed: %v", err)
	}
	// Another session cannot close it either.
	fwd.close("sess-b", port)
	if !fwd.forwarding("sess-a", port) {
		t.Error("a different session was able to close this session's forward")
	}
	// Ending the session drops everything it held, so a dead agent leaves no port
	// answering on the tailnet.
	fwd.closeSession("sess-a")
	if fwd.forwarding("sess-a", port) || fwd.holds(port) {
		t.Error("a forward outlived its session")
	}
}

// With no network address there is nothing to forward onto, and saying so beats
// binding loopback and handing back a URL that only works on the machine you are
// not using.
func TestNoNetworkAddressMeansNoForwarding(t *testing.T) {
	fwd := newPreviewForwarder(context.Background(), "")
	if err := fwd.open("sess-a", 3000); err == nil {
		t.Error("forwarding was allowed with no address to forward onto")
	}
}

// The link must be http even though kunai's own origin is https.
//
// This was a real broken link, not a nicety: -public-url is https because kunai
// terminates TLS on ITS port with a tailscale cert, and one port over there is a
// plain dev server. Inheriting the scheme produced https://host:3000, and the
// browser met a plain HTTP server behind a TLS handshake -- ERR_SSL_PROTOCOL_ERROR
// on the phone, "wrong version number" from OpenSSL. The forwarder cannot save it
// either: it is a raw TCP splice and adds no TLS of its own.
func TestPreviewURLIsAlwaysPlainHTTP(t *testing.T) {
	s := &Server{cfg: Config{PublicURL: "https://linux-1.tail75ba2a.ts.net:8443"}}
	got := s.previewURL(3000)
	want := "http://linux-1.tail75ba2a.ts.net:3000"
	if got != want {
		t.Errorf("previewURL = %q, want %q", got, want)
	}
}

// The hostname still comes from the origin, because it is the one name the other
// device can resolve. Only the scheme and port change.
func TestPreviewURLKeepsTheResolvableHostname(t *testing.T) {
	for _, tc := range []struct{ origin, want string }{
		{"https://box.tail1234.ts.net:8443", "http://box.tail1234.ts.net:5173"},
		{"http://box.tail1234.ts.net:8443", "http://box.tail1234.ts.net:5173"},
		{"", "http://localhost:5173"},
	} {
		s := &Server{cfg: Config{PublicURL: tc.origin}}
		if got := s.previewURL(5173); got != tc.want {
			t.Errorf("origin %q -> %q, want %q", tc.origin, got, tc.want)
		}
	}
}
