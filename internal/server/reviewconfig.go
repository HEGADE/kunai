package server

// Which account and model a review runs on.
//
// Its own setting because a review is not like the work you do in a session. It
// is chunky, it is unattended, and it happens on somebody else's schedule (a
// colleague opened a pull request), so spending the same window you are using to
// work is the wrong default the moment you review more than occasionally. Pointed
// at a second account or a provider, a review can never wall the session you are
// sitting in.
//
// Per machine, like every other credential and preference here, and stored beside
// them. An empty choice means "the machine's default", so a machine that never
// sets one behaves exactly as it did before this existed.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type reviewConfig struct {
	// CLI is the account or provider name a review session runs on. Empty is the
	// machine's default account.
	CLI string `json:"cli,omitempty"`
	// Model is the Claude tier (or a provider's model) reviews use. Empty is the
	// machine's default.
	Model string `json:"model,omitempty"`
}

type reviewConfigStore struct {
	mu   sync.Mutex
	path string
	cfg  reviewConfig
}

func newReviewConfigStore(dataDir string) *reviewConfigStore {
	s := &reviewConfigStore{}
	if dataDir == "" {
		return s
	}
	s.path = filepath.Join(dataDir, "reviewconfig.json")
	if b, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(b, &s.cfg)
	}
	return s
}

func (s *reviewConfigStore) get() reviewConfig {
	if s == nil {
		return reviewConfig{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

func (s *reviewConfigStore) set(cfg reviewConfig) reviewConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = reviewConfig{CLI: strings.TrimSpace(cfg.CLI), Model: strings.TrimSpace(cfg.Model)}
	if s.path != "" {
		if b, err := json.Marshal(s.cfg); err == nil {
			tmp := s.path + ".tmp"
			if os.WriteFile(tmp, b, 0o600) == nil {
				_ = os.Rename(tmp, s.path)
			}
		}
	}
	return s.cfg
}

// handleReviewConfig reads (GET) or sets (POST) which account and model reviews
// run on.
func (s *Server) handleReviewConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, s.reviewCfg.get())
		return
	}
	var req reviewConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	writeJSON(w, http.StatusOK, s.reviewCfg.set(req))
}
