package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
)

// Thermal-guard settings: an opt-in, per-machine policy persisted to the data dir
// and re-applied on boot, exactly like the keep-awake toggle. The live tripped
// state rides on /api/stats, so the dashboard fan-out already carries it.

func (s *Server) thermalPath() string {
	return filepath.Join(s.cfg.DataDir, "thermal.json")
}

// loadThermal overrides the flag-seeded defaults with a persisted preference at
// startup (best-effort).
func (s *Server) loadThermal() {
	if s.cfg.DataDir == "" {
		return
	}
	b, err := os.ReadFile(s.thermalPath())
	if err != nil {
		return
	}
	var cfg guardConfig
	if json.Unmarshal(b, &cfg) == nil {
		s.guardian.setConfig(cfg)
	}
}

func (s *Server) saveThermal(cfg guardConfig) {
	if s.cfg.DataDir == "" {
		return
	}
	b, _ := json.Marshal(cfg)
	_ = os.WriteFile(s.thermalPath(), b, 0o600)
}

func (s *Server) handleThermal(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, s.guardian.config())
		return
	}
	var cfg guardConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	cfg = clampGuardConfig(cfg)
	s.guardian.setConfig(cfg)
	s.saveThermal(cfg)
	writeJSON(w, http.StatusOK, s.guardian.config())
}

// clampGuardConfig keeps the thresholds sane whatever a client sends: a guard set
// to trip at 5°C would fire the instant it is enabled, and one set to 200°C would
// never fire. The bounds are wide enough for any real CPU and narrow enough that
// a fat-fingered value cannot make the safety net useless.
func clampGuardConfig(cfg guardConfig) guardConfig {
	if cfg.SoftC != 0 {
		if cfg.SoftC < guardMinSoftC {
			cfg.SoftC = guardMinSoftC
		}
		if cfg.SoftC > guardMaxSoftC {
			cfg.SoftC = guardMaxSoftC
		}
	}
	if cfg.MaxHours < 0 {
		cfg.MaxHours = 0
	}
	if cfg.MaxHours > guardMaxHours {
		cfg.MaxHours = guardMaxHours
	}
	return cfg
}

const (
	guardMinSoftC = 50.0  // below a normal idle die temp; anything lower is a mistake
	guardMaxSoftC = 105.0 // most CPUs throttle around 95-100C, so this is the ceiling
	guardMaxHours = 72.0  // a cap measured in days is not a cap
)
