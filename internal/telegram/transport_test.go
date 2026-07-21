package telegram

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// These cover the two things that turned a flaky route into a fifteen-minute
// outage, and the credential that outage wrote into the log while it happened.

// --- the family breaker ---

// fakeDial records the network each dial asked for, and can be told to fail.
type fakeDial struct {
	networks []string
	failOn   map[string]bool
}

func (f *fakeDial) dial(_ context.Context, network, _ string) (net.Conn, error) {
	f.networks = append(f.networks, network)
	if f.failOn[network] {
		return nil, fmt.Errorf("dial %s: refused", network)
	}
	c, _ := net.Pipe()
	return c, nil
}

func testDialer(f *fakeDial, now func() time.Time) *familyDialer {
	return &familyDialer{dial: f.dial, now: now, pinFor: familyPin}
}

// Untripped, the dialer must not interfere: both families are raced as Go
// intends, which is right on every network that is not broken.
func TestDialerLeavesFamilyAloneUntilItFails(t *testing.T) {
	f := &fakeDial{}
	d := testDialer(f, time.Now)

	if _, err := d.DialContext(context.Background(), "tcp", "api.telegram.org:443"); err != nil {
		t.Fatal(err)
	}
	if len(f.networks) != 1 || f.networks[0] != "tcp" {
		t.Fatalf("dialed %v, want a plain dual-stack tcp", f.networks)
	}
}

// Once tripped, new connections go over IPv4, which is the half measured to
// work.
func TestDialerPinsToIPv4WhenTripped(t *testing.T) {
	f := &fakeDial{}
	d := testDialer(f, time.Now)
	d.pin()

	if _, err := d.DialContext(context.Background(), "tcp", "api.telegram.org:443"); err != nil {
		t.Fatal(err)
	}
	if len(f.networks) != 1 || f.networks[0] != "tcp4" {
		t.Fatalf("dialed %v, want tcp4 while pinned", f.networks)
	}
}

// The pin is a bad-patch measure, not a permanent policy: IPv6 has to get
// another chance once it lapses.
func TestDialerPinLapses(t *testing.T) {
	f := &fakeDial{}
	now := time.Now()
	d := testDialer(f, func() time.Time { return now })
	d.pin()
	now = now.Add(familyPin + time.Second)

	if _, err := d.DialContext(context.Background(), "tcp", "api.telegram.org:443"); err != nil {
		t.Fatal(err)
	}
	if f.networks[0] != "tcp" {
		t.Fatalf("still pinned after the window: %v", f.networks)
	}
}

// On an IPv6-only network, pinning to IPv4 would mean never connecting again.
// A failed v4 dial has to release the pin and fall back to the full address
// list, in the same attempt.
func TestDialerReleasesPinWhenIPv4IsTheBrokenOne(t *testing.T) {
	f := &fakeDial{failOn: map[string]bool{"tcp4": true}}
	d := testDialer(f, time.Now)
	d.pin()

	if _, err := d.DialContext(context.Background(), "tcp", "api.telegram.org:443"); err != nil {
		t.Fatalf("gave up instead of falling back: %v", err)
	}
	if len(f.networks) != 2 || f.networks[0] != "tcp4" || f.networks[1] != "tcp" {
		t.Fatalf("dialed %v, want tcp4 then a dual-stack retry", f.networks)
	}
	if d.pinned() {
		t.Error("stayed pinned to the family that just failed")
	}
}

// Anything that is not a dual-stack dial has already chosen its family, so the
// breaker must not rewrite it.
func TestDialerDoesNotRewriteAnExplicitFamily(t *testing.T) {
	f := &fakeDial{}
	d := testDialer(f, time.Now)
	d.pin()

	_, _ = d.DialContext(context.Background(), "tcp6", "example:443")
	if f.networks[0] != "tcp6" {
		t.Fatalf("rewrote an explicit tcp6 dial to %q", f.networks[0])
	}
}

// --- tripping it from a real failure ---

// A request that never reached Telegram must drop the pooled connection and pin
// the family. Reusing the connection is what made one bad route permanent.
func TestFailedRequestTripsTheBreaker(t *testing.T) {
	// A listener that accepts and says nothing: the request goes out and no
	// answer comes back, which is the shape of the real failure.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()

	old := apiBase
	apiBase = "http://" + ln.Addr().String()
	defer func() { apiBase = old }()

	c := NewClient("tok")
	c.http.Timeout = 300 * time.Millisecond

	if _, err := c.Send(context.Background(), 1, "hi", nil); err == nil {
		t.Fatal("want an error from a server that never answers")
	}
	if !c.dial.pinned() {
		t.Error("a dead request left the breaker untripped, so the next one takes the same route")
	}
}

// Telegram's own refusals are not route failures. Tripping on them would pin the
// family every time a chat was blocked or a message was malformed.
func TestAPIRefusalDoesNotTripTheBreaker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"error_code":403,"description":"bot was blocked by the user"}`))
	}))
	defer srv.Close()
	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	c := NewClient("tok")
	if _, err := c.Send(context.Background(), 1, "hi", nil); err == nil {
		t.Fatal("want the refusal to surface")
	}
	if c.dial.pinned() {
		t.Error("a refusal from Telegram tripped the route breaker")
	}
}

// --- the token in the log ---

// The token is in the request URL, so an unredacted transport error puts full
// control of the bot into journalctl and into every pasted log.
func TestTransportErrorDoesNotLeakTheToken(t *testing.T) {
	const token = "8717728449:AAFakeTokenValueForTheTest"
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing is listening, so the dial is refused at once

	old := apiBase
	apiBase = "http://" + addr
	defer func() { apiBase = old }()

	c := NewClient(token)
	_, err = c.Send(context.Background(), 1, "hi", nil)
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("the bot token is in the error text: %v", err)
	}
	if !strings.Contains(err.Error(), "<token>") {
		t.Errorf("want the token replaced with a marker, got %v", err)
	}
}

// Redacting must not flatten the error: the poll loop and the tests below it
// still need to see what actually went wrong underneath.
func TestRedactKeepsTheUnderlyingError(t *testing.T) {
	wrapped := fmt.Errorf("post https://api.telegram.org/botSECRET/getUpdates: %w", context.DeadlineExceeded)
	got := redact(wrapped, "SECRET")

	if strings.Contains(got.Error(), "SECRET") {
		t.Fatalf("token survived: %v", got)
	}
	if !errors.Is(got, context.DeadlineExceeded) {
		t.Error("errors.Is no longer sees the deadline underneath")
	}
}

func TestRedactPassesThroughWhenThereIsNothingToHide(t *testing.T) {
	err := errors.New("connection refused")
	if got := redact(err, "SECRET"); got != err {
		t.Errorf("wrapped an error that had no token in it: %v", got)
	}
	if redact(nil, "SECRET") != nil {
		t.Error("redact(nil) must stay nil")
	}
}
