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

const discoverTTL = 45 * time.Second

type discoveryCache struct {
	mu       sync.Mutex
	results  []MachineInfo
	at       time.Time
	inflight bool
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
	s.disco.mu.Lock()
	fresh := time.Since(s.disco.at) < discoverTTL && !s.disco.at.IsZero()
	if !force && fresh {
		out := s.disco.results
		s.disco.mu.Unlock()
		return out
	}
	if !force {
		// Stale: kick a single background refresh, return what we have.
		if !s.disco.inflight {
			s.disco.inflight = true
			go func() {
				res := s.scanPeers()
				s.disco.mu.Lock()
				s.disco.results, s.disco.at, s.disco.inflight = res, time.Now(), false
				s.disco.mu.Unlock()
			}()
		}
		out := s.disco.results
		s.disco.mu.Unlock()
		return out
	}
	s.disco.mu.Unlock()

	res := s.scanPeers()
	s.disco.mu.Lock()
	s.disco.results, s.disco.at = res, time.Now()
	s.disco.mu.Unlock()
	return res
}

// scanPeers lists online tailnet peers and keeps those that answer as Kunai.
func (s *Server) scanPeers() []MachineInfo {
	peers := tailscalePeers()
	if len(peers) == 0 {
		return nil
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
	return out
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

func tailscalePeers() []tsPeer {
	bin := tailscaleBin()
	if bin == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "status", "--json").Output()
	if err != nil {
		return nil
	}
	var st tsStatus
	if json.Unmarshal(out, &st) != nil {
		return nil
	}
	peers := make([]tsPeer, 0, len(st.Peer))
	for _, p := range st.Peer {
		peers = append(peers, p)
	}
	return peers
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
