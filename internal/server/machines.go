package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
)

// MachineInfo is one entry in the registry the hub serves at GET /api/machines.
// The client uses URL as the origin to reach that machine directly (REST + WS)
// over the tailnet — the hub only supplies the list, never proxies traffic.
type MachineInfo struct {
	ID    string `json:"id"`    // short stable slug (first FQDN label)
	Label string `json:"label"` // human label
	URL   string `json:"url"`   // tailnet origin, no trailing slash
	Self  bool   `json:"self"`  // the machine serving this response
}

// machineStore persists manually-added peer machines plus an ignore set for
// discovered peers the user removed. One JSON file in the data dir; the pattern
// mirrors internal/push's subscription store.
type machineStore struct {
	mu   sync.Mutex
	path string
	data machineData
}

type machineData struct {
	Peers   []MachineInfo `json:"peers"`
	Ignored []string      `json:"ignored"`
}

func newMachineStore(path string) *machineStore {
	s := &machineStore{path: path}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &s.data)
	}
	return s
}

func (s *machineStore) list() []MachineInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]MachineInfo, len(s.data.Peers))
	copy(out, s.data.Peers)
	return out
}

func (s *machineStore) ignored() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := make(map[string]bool, len(s.data.Ignored))
	for _, id := range s.data.Ignored {
		m[id] = true
	}
	return m
}

func (s *machineStore) add(m MachineInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, p := range s.data.Peers {
		if p.ID == m.ID {
			s.data.Peers[i] = m // replace, keep list stable
			s.saveLocked()
			return
		}
	}
	// A re-added peer is no longer ignored.
	s.data.Ignored = removeString(s.data.Ignored, m.ID)
	s.data.Peers = append(s.data.Peers, m)
	s.saveLocked()
}

func (s *machineStore) remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Peers = filterPeers(s.data.Peers, id)
	if !containsString(s.data.Ignored, id) {
		s.data.Ignored = append(s.data.Ignored, id)
	}
	s.saveLocked()
}

func (s *machineStore) saveLocked() {
	if b, err := json.Marshal(s.data); err == nil {
		_ = os.WriteFile(s.path, b, 0o600)
	}
}

// --- self identity -----------------------------------------------------------

// selfMachine derives this machine's registry entry from the configured public
// URL. When PublicURL is empty (e.g. local dev) it returns ok=false and the
// client seeds "self" from window.location instead.
func selfMachine(publicURL string) (MachineInfo, bool) {
	origin := normalizeOrigin(publicURL)
	if origin == "" {
		return MachineInfo{}, false
	}
	slug := slugFromURL(origin)
	label := slug
	if h, err := os.Hostname(); err == nil && h != "" {
		label = h
	}
	return MachineInfo{ID: slug, Label: label, URL: origin, Self: true}, true
}

// --- handlers ----------------------------------------------------------------

// handleMachines returns self (if known) plus manually-added peers and, later,
// discovered peers — deduped by id, excluding the ignore set.
func (s *Server) handleMachines(w http.ResponseWriter, r *http.Request) {
	out := make([]MachineInfo, 0, 4)
	seen := map[string]bool{}
	if self, ok := selfMachine(s.cfg.PublicURL); ok {
		out = append(out, self)
		seen[self.ID] = true
	}
	ignored := s.machines.ignored()
	for _, m := range s.machines.list() {
		if seen[m.ID] || ignored[m.ID] {
			continue
		}
		seen[m.ID] = true
		out = append(out, m)
	}
	for _, m := range s.discoverCached() { // empty until discovery lands
		if seen[m.ID] || ignored[m.ID] {
			continue
		}
		seen[m.ID] = true
		out = append(out, m)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAddMachine(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Label string `json:"label"`
		URL   string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	origin := normalizeOrigin(body.URL)
	if origin == "" {
		writeErr(w, http.StatusBadRequest, "url must be an absolute http(s) origin")
		return
	}
	slug := slugFromURL(origin)
	label := strings.TrimSpace(body.Label)
	if label == "" {
		label = slug
	}
	m := MachineInfo{ID: slug, Label: label, URL: origin}
	s.machines.add(m)
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleDeleteMachine(w http.ResponseWriter, r *http.Request) {
	s.machines.remove(r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}

// --- small helpers -----------------------------------------------------------

// normalizeOrigin returns scheme://host[:port] with no trailing slash, or "" if
// the input is not an absolute http(s) URL with a host.
func normalizeOrigin(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// slugFromURL is the first DNS label of the host, a stable per-tailnet id.
func slugFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	return strings.SplitN(u.Hostname(), ".", 2)[0]
}

func containsString(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func removeString(xs []string, v string) []string {
	out := xs[:0]
	for _, x := range xs {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func filterPeers(xs []MachineInfo, id string) []MachineInfo {
	out := make([]MachineInfo, 0, len(xs))
	for _, x := range xs {
		if x.ID != id {
			out = append(out, x)
		}
	}
	return out
}
