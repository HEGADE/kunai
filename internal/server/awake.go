package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
)

// Keep-awake: an opt-in, per-machine toggle that prevents idle system sleep so a
// locked or idle machine stays reachable. The choice is persisted to the data
// dir and re-applied on boot; the live state also rides on /api/stats so the
// dashboard fan-out already carries it.

type awakeState struct {
	Enabled bool `json:"enabled"`
}

func (s *Server) awakePath() string {
	return filepath.Join(s.cfg.DataDir, "awake.json")
}

// loadAwake re-applies a persisted preference at startup (best-effort).
func (s *Server) loadAwake() {
	if s.cfg.DataDir == "" || !s.awake.Supported() {
		return
	}
	b, err := os.ReadFile(s.awakePath())
	if err != nil {
		return
	}
	var st awakeState
	if json.Unmarshal(b, &st) == nil && st.Enabled {
		_ = s.awake.Set(true)
	}
}

func (s *Server) saveAwake(enabled bool) {
	if s.cfg.DataDir == "" {
		return
	}
	b, _ := json.Marshal(awakeState{Enabled: enabled})
	_ = os.WriteFile(s.awakePath(), b, 0o600)
}

func (s *Server) handleAwake(w http.ResponseWriter, r *http.Request) {
	var req awakeState
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if s.awake.Supported() {
		if err := s.awake.Set(req.Enabled); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.saveAwake(req.Enabled)
	}
	writeJSON(w, http.StatusOK, map[string]bool{
		"enabled":   s.awake.Enabled(),
		"supported": s.awake.Supported(),
	})
}
