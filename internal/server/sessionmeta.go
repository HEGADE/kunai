package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
	// SnoozedUntil parks the session on the sidebar's snoozed shelf until this
	// time (unix ms). It is a display promise, not a control: the session keeps
	// running, and the client wakes the row early the moment it has something
	// for you (a permission ask, a finished turn). Server-side so a session
	// snoozed on the phone stays snoozed on the laptop.
	SnoozedUntil int64 `json:"snoozed_until,omitempty"`
	// SnoozedAt is when the snooze was set, the reference point for "did the
	// agent do anything since I parked this": a turn that ended after it is what
	// wakes the row early.
	SnoozedAt int64 `json:"snoozed_at,omitempty"`
	// HiddenPreviews are ports this session has been told not to show on the
	// preview card. Attribution is a fact about processes, not about what anybody
	// wants to look at, so a correctly-found server can still be noise: the
	// editor's own language server, a database, a dev server somebody already
	// knows the address of. Server-side rather than per-device because the row is
	// noise on the phone for the same reason it is noise on the laptop.
	HiddenPreviews []int `json:"hidden_previews,omitempty"`
}

// hidesPreview reports whether a port has been dismissed for this session.
func (m sessionMeta) hidesPreview(port int) bool {
	for _, p := range m.HiddenPreviews {
		if p == port {
			return true
		}
	}
	return false
}

// metaPatch is a partial update: a nil field is left as-is. It exists so adding
// a field means one struct member, not another positional pointer argument at
// every call site.
type metaPatch struct {
	Name      *string
	Pinned    *bool
	Workspace *string
	// SnoozedUntil > 0 sets the snooze (the store stamps SnoozedAt itself, so
	// there is one clock); 0 clears both fields.
	SnoozedUntil *int64
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

// keepIDs is the set of session ids the Recent scan must keep even when they
// fall outside the newest-N window: pinned ones (they sit above the list) and
// snoozed ones (they sit on the snoozed shelf, and a shelf whose rows silently
// age out of the scan would lose sessions it promised to bring back).
func (s *sessionMetaStore) keepIDs() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]bool{}
	for id, m := range s.data {
		if m.Pinned || m.SnoozedUntil > 0 {
			out[id] = true
		}
	}
	return out
}

// update applies a metaPatch. Once a session has no override left its entry is
// dropped, so the file only ever holds sessions the user actually customized.
func (s *sessionMetaStore) update(id string, p metaPatch) sessionMeta {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.data[id]
	if p.Name != nil {
		m.Name = strings.TrimSpace(*p.Name)
	}
	if p.Pinned != nil {
		m.Pinned = *p.Pinned
	}
	if p.Workspace != nil {
		m.Workspace = strings.TrimSpace(*p.Workspace)
	}
	if p.SnoozedUntil != nil {
		if *p.SnoozedUntil > 0 {
			m.SnoozedUntil = *p.SnoozedUntil
			m.SnoozedAt = time.Now().UnixMilli()
		} else {
			m.SnoozedUntil, m.SnoozedAt = 0, 0
		}
	}
	s.putLocked(id, m)
	return m
}

// setPreviewHidden dismisses a discovered server, or brings it back.
//
// Its own method rather than a metaPatch field, because a patch carrying the
// whole set would make two devices dismissing two different rows a
// last-write-wins race that silently un-hides one of them. This is
// read-modify-write on one port under the store's lock, which is what the
// operation actually is.
func (s *sessionMetaStore) setPreviewHidden(id string, port int, hidden bool) sessionMeta {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.data[id]
	out := m.HiddenPreviews[:0:0]
	for _, p := range m.HiddenPreviews {
		if p != port {
			out = append(out, p)
		}
	}
	if hidden {
		out = append(out, port)
	}
	m.HiddenPreviews = out
	s.putLocked(id, m)
	return m
}

// putLocked stores an entry, or drops it once it holds no override at all, so
// the file only ever names sessions the user actually customized.
func (s *sessionMetaStore) putLocked(id string, m sessionMeta) {
	if m.Name == "" && !m.Pinned && m.Workspace == "" && m.SnoozedUntil == 0 && len(m.HiddenPreviews) == 0 {
		delete(s.data, id)
	} else {
		s.data[id] = m
	}
	s.saveLocked()
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
// name replaces the derived title; the pin, workspace and snooze ride alongside.
func mergeMeta(metas []session.Meta, over map[string]sessionMeta) {
	for i := range metas {
		if o, ok := over[metas[i].ID]; ok {
			if o.Name != "" {
				metas[i].Title = o.Name
			}
			metas[i].Pinned = o.Pinned
			metas[i].Workspace = o.Workspace
			metas[i].SnoozedUntil = o.SnoozedUntil
			metas[i].SnoozedAt = o.SnoozedAt
		}
	}
}

// --- HTTP ---

// handleUpdateSessionMeta renames, pins and/or snoozes a session by id. Because
// the id is shared by a live session and its resumable transcript, this works
// whether the session is running or sitting in Recent. Body:
// {"name": "...", "pinned": true, "workspace": "...", "snoozed_until": 0};
// every field is optional, and omitting one leaves it unchanged.
func (s *Server) handleUpdateSessionMeta(w http.ResponseWriter, r *http.Request) {
	if s.sessionMeta == nil {
		writeErr(w, http.StatusServiceUnavailable, "no data dir configured")
		return
	}
	var req struct {
		Name         *string `json:"name"`
		Pinned       *bool   `json:"pinned"`
		Workspace    *string `json:"workspace"`
		SnoozedUntil *int64  `json:"snoozed_until"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	m := s.sessionMeta.update(r.PathValue("id"), metaPatch{
		Name: req.Name, Pinned: req.Pinned, Workspace: req.Workspace, SnoozedUntil: req.SnoozedUntil,
	})
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
