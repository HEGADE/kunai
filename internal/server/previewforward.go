package server

// Making a loopback dev server reachable from your phone.
//
// The design decision here is the whole feature, so it is worth stating against
// the obvious alternative. The obvious thing is to proxy the dev server under a
// path on kunai's own port -- /preview/<session>/3000/ -- and it does not work.
// A dev server emits absolute URLs (/src/main.js, /@vite/client, /_next/...),
// its websocket for hot reload is opened against a path it chose, and its
// redirects are absolute. Serving it under a prefix means rewriting HTML, CSS,
// JavaScript and Location headers on the way past, which is a rewriting engine
// that is wrong for some framework every month.
//
// So kunai forwards the PORT instead: a listener on this machine's network
// address at the SAME port number, handing bytes to 127.0.0.1:<port>. The app
// then sees exactly the origin it expects, one segment shorter --
// https://host.tailnet.ts.net:3000 instead of http://localhost:3000 -- and
// absolute paths, websockets and redirects need no help at all.
//
// It is a TCP splice rather than an HTTP proxy for the same reason: whatever the
// dev server speaks, including protocol upgrades and streaming, passes through
// untouched.
//
// Where it forwards is deliberately narrow. Only the tailnet address, never the
// LAN one, because the tailnet is an auth perimeter and the LAN is not: kunai's
// own LAN listener is behind a PIN, and a raw port forward cannot ask for one.
// Exposing an unauthenticated dev server to a cafe network is not something to
// offer as a convenience.

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"time"
)

// previewDialTimeout bounds connecting to the local server. It is on this
// machine, so slow means gone.
const previewDialTimeout = 3 * time.Second

// previewForwarder holds the listeners kunai has opened on request.
type previewForwarder struct {
	// host is the address to expose on: this machine's tailnet address. Empty
	// disables forwarding entirely, which is the correct behaviour on a
	// loopback-only install (there, localhost already works).
	host string
	ctx  context.Context

	mu     sync.Mutex
	active map[int]*forward // by port
}

type forward struct {
	sessionID string
	ln        net.Listener
}

var errPortBusy = errors.New("something is already using that port on this machine's network address")

func newPreviewForwarder(ctx context.Context, host string) *previewForwarder {
	return &previewForwarder{host: host, ctx: ctx, active: map[int]*forward{}}
}

// forwarding reports whether this session's port is currently exposed.
func (p *previewForwarder) forwarding(sessionID string, port int) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	f := p.active[port]
	return f != nil && f.sessionID == sessionID
}

// holds reports whether kunai has a forwarder on a port, so a scan does not
// offer its own listener back as a discovery.
func (p *previewForwarder) holds(port int) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.active[port] != nil
}

// open starts forwarding host:port to 127.0.0.1:port.
func (p *previewForwarder) open(sessionID string, port int) error {
	if p == nil || p.host == "" {
		return errors.New("this machine has no network address to expose a preview on")
	}
	p.mu.Lock()
	if f := p.active[port]; f != nil {
		p.mu.Unlock()
		if f.sessionID == sessionID {
			return nil // already open for this session: asking twice is not an error
		}
		return errPortBusy
	}
	p.mu.Unlock()

	ln, err := net.Listen("tcp", net.JoinHostPort(p.host, strconv.Itoa(port)))
	if err != nil {
		return errPortBusy
	}

	p.mu.Lock()
	p.active[port] = &forward{sessionID: sessionID, ln: ln}
	p.mu.Unlock()

	go p.accept(ln, port)
	log.Printf("preview: %s:%d is now serving this machine's 127.0.0.1:%d", p.host, port, port)
	return nil
}

// close stops forwarding. Idempotent, and only the owning session may do it.
func (p *previewForwarder) close(sessionID string, port int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	f := p.active[port]
	if f == nil || (sessionID != "" && f.sessionID != sessionID) {
		p.mu.Unlock()
		return
	}
	delete(p.active, port)
	p.mu.Unlock()
	_ = f.ln.Close()
	log.Printf("preview: stopped serving port %d", port)
}

// closeSession drops every forward a session holds, so an ended session cannot
// leave a port exposed after the thing behind it is gone.
func (p *previewForwarder) closeSession(sessionID string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	var ports []int
	for port, f := range p.active {
		if f.sessionID == sessionID {
			ports = append(ports, port)
		}
	}
	p.mu.Unlock()
	for _, port := range ports {
		p.close(sessionID, port)
	}
}

func (p *previewForwarder) accept(ln net.Listener, port int) {
	go func() {
		<-p.ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return // closed, deliberately or with the server
		}
		go p.splice(conn, port)
	}
}

// splice joins an inbound connection to the local server, both ways, and closes
// both when either end finishes.
func (p *previewForwarder) splice(in net.Conn, port int) {
	defer in.Close()
	out, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), previewDialTimeout)
	if err != nil {
		// The dev server has gone away. Nothing to say to the browser at this
		// layer; dropping the connection produces the same "cannot connect" it
		// would get talking to the port directly, which is the honest answer.
		return
	}
	defer out.Close()

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(out, in); done <- struct{}{} }()
	go func() { _, _ = io.Copy(in, out); done <- struct{}{} }()
	<-done
}
