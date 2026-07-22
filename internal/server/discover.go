package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// Peer discovery: the hub shells `tailscale status --json`, then probes each
// online peer on the Kunai port to see which ones actually run Kunai. Results
// feed GET /api/machines so peers "appear on their own". Everything here is
// server-to-server over the tailnet (no browser/CORS), so peers' valid Tailscale
// certs verify normally.

const (
	// How long a scan result stays fresh before a background refresh is kicked.
	discoverTTL = 45 * time.Second
	// How long a discovered peer is kept after it was last seen answering. A
	// single blipped scan or probe (tailscale slow, a peer's /api/stats timing
	// out for one round) must NOT drop a live machine from the fleet, so the peer
	// stays listed until it has been unreachable for this whole window. This is
	// the fix for machines flickering out of the sidebar until a hard refresh:
	// discovery is now sticky, not "gone the instant one scan comes back empty".
	peerTTL = 4 * time.Minute
)

type discoveryCache struct {
	mu       sync.Mutex
	peers    map[string]seenPeer // by id; sticky across a blipped scan
	at       time.Time           // last scan where tailscale itself answered
	inflight bool
}

// seenPeer is a discovered machine plus when it last answered as Kunai.
type seenPeer struct {
	info MachineInfo
	seen time.Time
}

// currentLocked returns the peers still within the last-seen window, sorted by
// id so the list order is stable across calls (a map would shuffle it).
func (d *discoveryCache) currentLocked() []MachineInfo {
	out := make([]MachineInfo, 0, len(d.peers))
	for _, sp := range d.peers {
		out = append(out, sp.info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// merge folds one scan into the sticky cache. A scan where tailscale itself
// could not be reached (ok=false) leaves the known peers untouched and does not
// advance `at`, so we retry soon rather than dropping the whole fleet over a
// transient CLI failure. A successful scan refreshes the last-seen of every peer
// it found and drops only peers unseen for the whole grace window, so a peer
// that blipped for one round keeps its recent last-seen and survives.
func (d *discoveryCache) merge(found []MachineInfo, ok bool, now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.inflight = false
	if !ok {
		return
	}
	if d.peers == nil {
		d.peers = map[string]seenPeer{}
	}
	for _, m := range found {
		d.peers[m.ID] = seenPeer{info: m, seen: now}
	}
	for id, sp := range d.peers {
		if now.Sub(sp.seen) > peerTTL {
			delete(d.peers, id)
		}
	}
	d.at = now
}

// handleDiscover forces a fresh scan and returns Kunai-running peers.
func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.discover(true))
}

// discoverCached returns the last scan, refreshing in the background when stale
// so it never adds latency to GET /api/machines.
func (s *Server) discoverCached() []MachineInfo {
	return s.discover(false)
}

func (s *Server) discover(force bool) []MachineInfo {
	if force {
		s.runScan()
		s.disco.mu.Lock()
		defer s.disco.mu.Unlock()
		return s.disco.currentLocked()
	}
	s.disco.mu.Lock()
	fresh := !s.disco.at.IsZero() && time.Since(s.disco.at) < discoverTTL
	if !fresh && !s.disco.inflight {
		// Stale (or never scanned): kick one background refresh, return what we
		// have. A refresh that finds nothing new leaves recent peers in place.
		s.disco.inflight = true
		go s.runScan()
	}
	out := s.disco.currentLocked()
	s.disco.mu.Unlock()
	return out
}

// runScan scans once and folds the result into the sticky cache.
func (s *Server) runScan() {
	found, ok := s.scanPeers()
	s.disco.merge(found, ok, time.Now())
}

// scanPeers lists online tailnet peers and keeps those that answer as Kunai. The
// bool is false only when tailscale itself could not be queried (missing CLI,
// timeout, parse error): the caller must treat that as "unknown", not "empty",
// so a transient failure never prunes a live peer. A true with no peers is a
// real answer (nothing on the tailnet runs Kunai).
func (s *Server) scanPeers() ([]MachineInfo, bool) {
	peers, ok := tailscalePeers()
	if !ok {
		return nil, false
	}
	port := s.probePort()
	selfSlug := slugFromURL(s.cfg.PublicURL)

	var wg sync.WaitGroup
	var mu sync.Mutex
	out := make([]MachineInfo, 0, len(peers))
	for _, p := range peers {
		if !p.Online || p.DNSName == "" {
			continue
		}
		host := strings.TrimSuffix(p.DNSName, ".")
		slug := strings.SplitN(host, ".", 2)[0]
		if slug == selfSlug {
			continue // don't rediscover ourselves
		}
		origin := "https://" + net.JoinHostPort(host, port)
		label := p.HostName
		if label == "" {
			label = slug
		}
		wg.Add(1)
		go func(origin, slug, label string) {
			defer wg.Done()
			if probeKunai(origin) {
				mu.Lock()
				out = append(out, MachineInfo{ID: slug, Label: label, URL: origin})
				mu.Unlock()
			}
		}(origin, slug, label)
	}
	wg.Wait()
	return out, true
}

// --- tailscale CLI -----------------------------------------------------------

type tsStatus struct {
	Peer map[string]tsPeer `json:"Peer"`
}

type tsPeer struct {
	DNSName  string `json:"DNSName"`
	HostName string `json:"HostName"`
	Online   bool   `json:"Online"`
}

// tailscalePeers returns the tailnet peers. The bool is false when tailscale
// could not be queried at all (no CLI, timeout, unparseable output), which the
// caller must treat as "unknown" rather than "no peers".
func tailscalePeers() ([]tsPeer, bool) {
	bin := tailscaleBin()
	if bin == "" {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "status", "--json").Output()
	if err != nil {
		return nil, false
	}
	var st tsStatus
	if json.Unmarshal(out, &st) != nil {
		return nil, false
	}
	peers := make([]tsPeer, 0, len(st.Peer))
	for _, p := range st.Peer {
		peers = append(peers, p)
	}
	return peers, true
}

// tailscaleBin finds the tailscale CLI on PATH (Linux, Homebrew) or the macOS
// app bundle path used by the GUI client.
func tailscaleBin() string {
	if p, err := exec.LookPath("tailscale"); err == nil {
		return p
	}
	const macApp = "/Applications/Tailscale.app/Contents/MacOS/Tailscale"
	if fileExists(macApp) {
		return macApp
	}
	return ""
}

// probeKunai returns true if origin serves Kunai's stats endpoint.
func probeKunai(origin string) bool {
	client := &http.Client{
		Timeout: 1500 * time.Millisecond,
		Transport: &http.Transport{
			TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
			DisableKeepAlives: true,
		},
	}
	resp, err := client.Get(origin + "/api/stats")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var probe struct {
		Hostname string `json:"hostname"`
		OS       string `json:"os"`
	}
	if json.NewDecoder(resp.Body).Decode(&probe) != nil {
		return false
	}
	return probe.OS != "" // a real Kunai stats payload always sets os
}

func (s *Server) probePort() string {
	if u, err := url.Parse(s.cfg.PublicURL); err == nil && u.Port() != "" {
		return u.Port()
	}
	if _, p, err := net.SplitHostPort(s.cfg.Addr); err == nil && p != "" {
		return p
	}
	return "8443"
}
