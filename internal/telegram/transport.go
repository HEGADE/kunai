package telegram

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Surviving a broken route to Telegram.
//
// Measured on a real connection that failed for a quarter of an hour: IPv6 to
// api.telegram.org completed 3 TCP handshakes out of 10, while IPv4 to the same
// host and IPv6 to other hosts were both 10 for 10. The v6 route left the
// country and came back at 270ms; the v4 route stayed regional at 9ms. ICMP
// crossed the bad path happily, so a plain ping said everything was fine.
//
// What turned an intermittent fault into a permanent one is connection reuse.
// Go races the two families, keeps whichever answers first, and then pins every
// later request to that connection. Win the race on v6 once and every poll after
// it rides the bad path, burning the full client timeout each time, until
// something finally tears the connection down. That is the "worked once, then
// dead for fifteen minutes" shape exactly.
//
// So the fix is not at the dial. Dialing was never what broke: it is that a
// connection, once chosen, is kept. On a transport failure this drops the pooled
// connection so the next attempt has to race again, and pins that race to IPv4
// for a while, on the evidence that v4 is the working half.

// familyPin is how long a transport failure keeps new connections on IPv4. Long
// enough to ride out a bad patch on the v6 route, short enough that a machine
// which is genuinely IPv6-only is not held off it for the rest of the day.
const familyPin = 5 * time.Minute

// dialTimeout bounds one connection attempt. The default would let a black-holed
// SYN sit for far longer than the whole request is worth.
const dialTimeout = 10 * time.Second

// fallbackDelay is how long a v6 attempt gets before v4 is raced alongside it.
// Go's default is 300ms, which is under one round trip on a path like the one
// above, so v6 could still "win" a race it was going to lose.
const fallbackDelay = 150 * time.Millisecond

// familyDialer dials with a circuit breaker on address family.
type familyDialer struct {
	// dial is the real dial, injectable so the breaker's logic can be tested
	// without a network. Same reason guardian.go keeps execRun in a var.
	dial   func(ctx context.Context, network, addr string) (net.Conn, error)
	now    func() time.Time
	pinFor time.Duration

	mu          sync.Mutex
	pinnedUntil time.Time
}

func newFamilyDialer() *familyDialer {
	return &familyDialer{
		dial:   (&net.Dialer{Timeout: dialTimeout, FallbackDelay: fallbackDelay}).DialContext,
		now:    time.Now,
		pinFor: familyPin,
	}
}

// DialContext dials addr, forcing IPv4 while the breaker is tripped.
func (d *familyDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if !d.pinned() || network != "tcp" {
		return d.dial(ctx, network, addr)
	}
	conn, err := d.dial(ctx, "tcp4", addr)
	if err != nil {
		// IPv4 is not the answer here after all: this may be an IPv6-only
		// network, where staying pinned would mean never connecting again.
		// Let go of the pin and take the address list as it comes.
		d.unpin()
		return d.dial(ctx, network, addr)
	}
	return conn, nil
}

// pin trips the breaker: new connections go over IPv4 until it lapses.
func (d *familyDialer) pin() {
	d.mu.Lock()
	d.pinnedUntil = d.now().Add(d.pinFor)
	d.mu.Unlock()
}

func (d *familyDialer) unpin() {
	d.mu.Lock()
	d.pinnedUntil = time.Time{}
	d.mu.Unlock()
}

func (d *familyDialer) pinned() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.now().Before(d.pinnedUntil)
}

// newTransport builds the bot's own HTTP transport. Its own, rather than the
// shared default, because dropping pooled connections is a blunt instrument and
// it must not reach the rest of kunai's HTTP.
func newTransport() (*http.Transport, *familyDialer) {
	d := newFamilyDialer()
	return &http.Transport{
		DialContext:           d.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		MaxIdleConns:          4,
		IdleConnTimeout:       90 * time.Second,
		ForceAttemptHTTP2:     true,
	}, d
}

// redactedError is a transport failure with the bot token taken out of it.
//
// The token is in the request URL, so an unredacted error puts full control of
// the bot into the log, into journalctl, and into any bug report that quotes it.
// The wrapped error is kept, so errors.Is still sees a deadline or a
// cancellation underneath.
type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

// redact removes the token from an error's message. It returns nil for nil, so
// callers can wrap unconditionally.
func redact(err error, token string) error {
	if err == nil {
		return nil
	}
	if token == "" {
		return err
	}
	msg := strings.ReplaceAll(err.Error(), token, "<token>")
	if msg == err.Error() {
		return err
	}
	return &redactedError{msg: msg, err: err}
}
