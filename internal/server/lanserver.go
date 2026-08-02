package server

// Starting and stopping the network listeners, with the PIN as the switch.
//
// This exists because of a real failure, and the failure is worth stating: the
// owner set a PIN in Settings, which is a panel titled "Network access" that
// explains it lets another device on the wifi reach this machine, and then
// nothing happened. Serving the network also needed a -lan flag, which lives in
// a service file that Settings cannot edit and that a self-update never
// rewrites. So the app said the machine was locked and ready while nothing was
// listening, and the only symptom on the other device was a connection timing
// out -- with nothing anywhere to suggest why.
//
// One control, and it is the one that was already load-bearing. A PIN can only
// exist because somebody deliberately set it, on that panel, having read what it
// is for; and the listener has always refused to run without one. So "a PIN is
// set" and "serve the network" were already the same question asked twice, and
// the flag was only a way for the two answers to disagree.
//
// It also takes effect at once. Needing a restart to apply a setting made in the
// app is its own small lie, and the whole point here is that setting the PIN is
// the moment the owner decides.

import (
	"context"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

// lanServer owns the listeners on this machine's network addresses.
type lanServer struct {
	srv  *Server
	port string

	mu       sync.Mutex
	running  bool
	cancel   context.CancelFunc
	addrs    []string
	firewall *firewallAdvice
}

func newLANServer(s *Server, port string) *lanServer {
	return &lanServer{srv: s, port: port}
}

// Running reports whether the network is being served, for the settings panel.
func (l *lanServer) Running() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.running
}

// Addrs is where the network can reach us, for the settings panel to print.
func (l *lanServer) Addrs() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.addrs...)
}

// Sync brings the listeners into line with whether a PIN exists.
//
// Called at boot and again whenever the PIN changes, so it must be safe to call
// when nothing needs to happen. Idempotent in both directions.
func (l *lanServer) Sync(ctx context.Context) {
	want := l.srv.lanAuth != nil && l.srv.lanAuth.HasPIN()
	switch {
	case want && !l.Running():
		l.start(ctx)
	case !want && l.Running():
		l.stop()
	}
}

func (l *lanServer) start(ctx context.Context) {
	addrs := lanAddrs(l.port)
	if len(addrs) == 0 {
		log.Printf("lan: no private network address on this machine; nothing to serve")
		return
	}
	// Encryption is not optional, whatever the browser says about the certificate.
	// Without it the PIN and every request after it cross the network in clear
	// text, which on a shared wifi hands both to anyone listening.
	hosts := make([]string, 0, len(addrs))
	for _, a := range addrs {
		hosts = append(hosts, hostOf(a))
	}
	tlsCfg, err := lanTLS(l.srv.cfg.DataDir, hosts)
	if err != nil {
		log.Printf("lan: could not prepare a certificate (%v); not serving the network unencrypted", err)
		return
	}

	runCtx, cancel := context.WithCancel(ctx)
	handler := lanGuard(l.srv.lanAuthGate(cors(logRequests(l.srv.routes()), false)))

	var live []string
	for _, addr := range addrs {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			log.Printf("lan: cannot serve %s: %v", addr, err)
			continue
		}
		srv := &http.Server{Handler: handler, TLSConfig: tlsCfg, ReadHeaderTimeout: 10 * time.Second}
		go func() {
			<-runCtx.Done()
			shutCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			_ = srv.Shutdown(shutCtx)
		}()
		go func(l net.Listener) {
			// Certificates come from TLSConfig, so the file arguments are empty.
			if err := srv.ServeTLS(l, "", ""); err != nil && err != http.ErrServerClosed {
				log.Printf("lan listener: %v", err)
			}
		}(ln)
		live = append(live, "https://"+addr)
		log.Printf("kunai also on https://%s (this network, PIN required; the certificate is self-signed, so accept it once per device)", addr)
	}
	if len(live) == 0 {
		cancel()
		return
	}

	// Bound is not the same as reachable, and the difference is invisible from
	// here: a request from this machine to its own address never crosses the
	// network, so everything looks perfect while the host firewall drops every
	// packet that actually arrives. Say so at the moment the address is printed,
	// where somebody about to type it into another device will read it.
	fw := detectFirewall(l.port)
	l.mu.Lock()
	l.running, l.cancel, l.addrs, l.firewall = true, cancel, live, fw
	l.mu.Unlock()
	if fw != nil {
		log.Printf("lan: %s is enabled on this machine and drops incoming connections by default.", fw.Tool)
		log.Printf("lan: if another device cannot reach the address above, allow the port:  %s", fw.Command)
	}
}

// Firewall is the caution to show alongside the address, or nil.
func (l *lanServer) Firewall() *firewallAdvice {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.firewall
}

func (l *lanServer) stop() {
	l.mu.Lock()
	cancel := l.cancel
	l.running, l.cancel, l.addrs, l.firewall = false, nil, nil, nil
	l.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	log.Printf("lan: no PIN, so the network is no longer served")
}
