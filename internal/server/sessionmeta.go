package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/hegade/kunai/internal/session"
)

// sessionMetaStore persists per-session user overrides — a custom name (rename)
// and a pin — keyed by the session id. Identity is the id, which is stable across
// the live -> resumed transition (the same id doubles as the CLI --session-id),
// so a pin or rename set while a session runs still applies once it becomes a
// resumable transcript, and vice versa. One JSON file in the data dir; the store
// mirrors machineStore, with the atomic-write idiom looppersist.go uses.
type sessionMeta struct {
	Name   string `json:"name,omitempty"`   // rename; overrides the derived title
	Pinned bool   `json:"pinned,omitempty"` // sticks to the top of the sidebar
	// Workspace is what the sidebar groups this session under, replacing the
	// directory it was started in. It lives here rather than on the session
	// because the grouping has to outlive the process: a session named into a
	// workspace while running must still be in that workspace tomorrow, when it
	// is a transcript in Recent and its project list is no longer in memory.
	Workspace string `json:"workspace,omitempty"`
}

type sessionMetaStore struct {
	mu   sync.Mutex
	path string
	data map[string]sessionMeta
}

func newSessionMetaStore(path string) *sessionMetaStore {
	s := &sessionMetaStore{path: path, data: map[string]sessionMeta{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &s.data)
		if s.data == nil {
			s.data = map[string]sessionMeta{}
		}
	}
	return s
}

// all returns a copy of the overlay, for merging into the live and Recent lists.
func (s *sessionMetaStore) all() map[string]sessionMeta {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]sessionMeta, len(s.data))
	for k, v := range s.data {
		out[k] = v
	}
	return out
}

func (s *sessionMetaStore) get(id string) sessionMeta {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[id]
}

// pinnedIDs is the set of pinned session ids, used so the Recent scan keeps a
// pinned session even when it falls outside the newest-N window.
func (s *sessionMetaStore) pinnedIDs() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]bool{}
	for id, m := range s.data {
		if m.Pinned {
			out[id] = true
		}
	}
	return out
}

// update applies a partial change: a nil field is left as-is. Once a session has
// neither a name nor a pin its entry is dropped, so the file only ever holds
// sessions the user actually customized.
func (s *sessionMetaStore) update(id string, name *string, pinned *bool, workspace *string) sessionMeta {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.data[id]
	if name != nil {
		m.Name = strings.TrimSpace(*name)
	}
	if pinned != nil {
		m.Pinned = *pinned
	}
	if workspace != nil {
		m.Workspace = strings.TrimSpace(*workspace)
	}
	if m.Name == "" && !m.Pinned && m.Workspace == "" {
		delete(s.data, id)
	} else {
		s.data[id] = m
	}
	s.saveLocked()
	return m
}

func (s *sessionMetaStore) delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[id]; ok {
		delete(s.data, id)
		s.saveLocked()
	}
}

func (s *sessionMetaStore) saveLocked() {
	if s.path == "" {
		return
	}
	b, err := json.Marshal(s.data)
	if err != nil {
		return
	}
	// Atomic write: a crash mid-save never truncates the overlay.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, s.path)
}

// mergeMeta overlays custom names and pins onto the live session list. A custom
// name replaces the derived title; the pin rides alongside.
func mergeMeta(metas []session.Meta, over map[string]sessionMeta) {
	for i := range metas {
		if o, ok := over[metas[i].ID]; ok {
			if o.Name != "" {
				metas[i].Title = o.Name
			}
			metas[i].Pinned = o.Pinned
			metas[i].Workspace = o.Workspace
		}
	}
}

// --- HTTP ---

// handleUpdateSessionMeta renames and/or pins a session by id. Because the id is
// shared by a live session and its resumable transcript, this works whether the
// session is running or sitting in Recent. Body: {"name": "...", "pinned": true};
// both fields are optional, and omitting one leaves it unchanged.
func (s *Server) handleUpdateSessionMeta(w http.ResponseWriter, r *http.Request) {
	if s.sessionMeta == nil {
		writeErr(w, http.StatusServiceUnavailable, "no data dir configured")
		return
	}
	var req struct {
		Name      *string `json:"name"`
		Pinned    *bool   `json:"pinned"`
		Workspace *string `json:"workspace"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	m := s.sessionMeta.update(r.PathValue("id"), req.Name, req.Pinned, req.Workspace)
	writeJSON(w, http.StatusOK, m)
}

// handleDeleteHistory permanently removes a past session: its transcript file on
// disk and any pin/rename override. It refuses a session that is currently live
// (close it first) so a running CLI never loses the file out from under it.
func (s *Server) handleDeleteHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.mgr.Get(id); ok {
		writeErr(w, http.StatusConflict, "session is running; close it first")
		return
	}
	s.deleteTranscript(id)
	if s.sessionMeta != nil {
		s.sessionMeta.delete(id)
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteTranscript removes a session's transcript from every account's projects
// folder. A session id is globally unique, so at most one file matches; scanning
// all roots covers whichever account owned it. The id is guarded so it can never
// escape the projects folder.
func (s *Server) deleteTranscript(id string) {
	if id == "" || strings.ContainsAny(id, `/\.`) {
		return
	}
	for _, ar := range s.accountRoots() {
		dirs, err := os.ReadDir(ar.root)
		if err != nil {
			continue
		}
		for _, d := range dirs {
			if !d.IsDir() {
				continue
			}
			p := filepath.Join(ar.root, d.Name(), id+".jsonl")
			if _, err := os.Stat(p); err == nil {
				_ = os.Remove(p)
			}
		}
	}
}
