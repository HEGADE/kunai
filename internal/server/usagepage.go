package server

// What all of this cost.
//
// The dashboard already shows how full the subscription windows are, which
// answers "can I keep going". It has never answered "what have I been spending
// it on" -- which model ate the month, whether the Codex provider is pulling its
// weight, what a heavy day actually looks like. That answer was always on disk
// (every assistant message in every transcript carries its own usage block), just
// never read.
//
// This is the seam between that data and the page. internal/usagestats owns the
// scanning and the pricing; this file owns the lifecycle, which is really one
// decision: the first scan of a real corpus takes seconds, so it must never
// happen inside a request. It runs in the background, the endpoint answers
// immediately with whatever the last scan produced, and the client polls while
// `scanning` is true. A page that hangs for three seconds on open would be worse
// than no page.

import (
	"net/http"
	"time"

	"github.com/hegade/kunai/internal/usagestats"
)

// usageMaxAge is how stale a report may be before a read triggers a rebuild.
// A rebuild is milliseconds once the index is warm, so this is about not
// hammering the disk on every poll rather than about the scan being expensive.
const usageMaxAge = 60 * time.Second

// usageRoots adapts the account list this server already keeps into the form the
// collector wants. It is passed as a function, not a slice, so an account added
// from the app appears in the next scan rather than requiring a restart.
func (s *Server) usageRoots() []usagestats.Root {
	rs := s.accountRoots()
	out := make([]usagestats.Root, 0, len(rs))
	for _, r := range rs {
		out = append(out, usagestats.Root{Name: r.name, Dir: r.root})
	}
	return out
}

func (s *Server) usageRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/usage/stats", s.handleUsageStats)
}

// usageStatsResponse is the report plus enough state for the client to know
// whether to poll again.
type usageStatsResponse struct {
	*usagestats.Report
	Scanning  bool  `json:"scanning"`
	ScannedAt int64 `json:"scanned_at,omitempty"`
}

func (s *Server) handleUsageStats(w http.ResponseWriter, r *http.Request) {
	if s.usageStats == nil {
		writeErr(w, http.StatusServiceUnavailable, "usage stats need a data dir")
		return
	}
	// Kick a rebuild if the report is old, but never wait for it: an answer that
	// is a minute stale beats a request that blocks on a disk walk.
	if s.usageStats.Stale(usageMaxAge) {
		go s.usageStats.Refresh()
	}
	rep, st := s.usageStats.Report()
	writeJSON(w, http.StatusOK, usageStatsResponse{
		Report:    rep,
		Scanning:  st.Scanning,
		ScannedAt: st.ScannedAt,
	})
}
