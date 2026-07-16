package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
)

// The lid-closed hold keeps the machine working with the lid SHUT, which plain
// idle-sleep inhibition (the awake package) does not cover: a closed lid triggers
// a separate force-sleep both platforms gate behind root. This is the Phase 2,
// privileged, default-off part.
//
// It is kept apart from the awake package on purpose. That package promises
// "nothing global or sticky, a crash can never strand the machine"; a lid hold on
// macOS is exactly a sticky global setting (pmset disablesleep), so it cannot live
// there without breaking that promise. The mitigation for the stranding risk lives
// here instead: the macOS keeper clears the setting at boot, so a crash that left
// it on is undone the next time kunai starts.
type lidKeeper interface {
	Set(on bool) error
	Enabled() bool
	// Supported reports whether the mechanism exists on this host (the client hides
	// the toggle when false). It is NOT a promise the hold will take: both
	// platforms gate the actual hold behind a privilege the installer grants, so a
	// supported host can still refuse Set until that grant is in place. Set returns
	// the refusal rather than pretending, so the toggle can report the truth.
	Supported() bool
}

type lidState struct {
	Enabled bool `json:"enabled"`
}

func (s *Server) lidPath() string {
	return filepath.Join(s.cfg.DataDir, "lid.json")
}

// loadLid re-applies a persisted lid preference at startup. It runs AFTER the
// macOS keeper's boot-time unstick, so a machine only holds the lid if the owner
// actually asked for it, never because a prior crash left the setting on.
func (s *Server) loadLid() {
	if s.cfg.DataDir == "" || s.lid == nil || !s.lid.Supported() {
		return
	}
	b, err := os.ReadFile(s.lidPath())
	if err != nil {
		return
	}
	var st lidState
	if json.Unmarshal(b, &st) == nil && st.Enabled {
		_ = s.lid.Set(true)
	}
}

func (s *Server) saveLid(enabled bool) {
	if s.cfg.DataDir == "" {
		return
	}
	b, _ := json.Marshal(lidState{Enabled: enabled})
	_ = os.WriteFile(s.lidPath(), b, 0o600)
}

func (s *Server) handleLid(w http.ResponseWriter, r *http.Request) {
	var req lidState
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if s.lid != nil && s.lid.Supported() {
		if err := s.lid.Set(req.Enabled); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.saveLid(req.Enabled)
	}
	writeJSON(w, http.StatusOK, map[string]bool{
		"enabled":   s.lid != nil && s.lid.Enabled(),
		"supported": s.lid != nil && s.lid.Supported(),
	})
}
