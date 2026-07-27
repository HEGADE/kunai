package server

// The owner's side of sharing: create a link, watch who is asking for it, approve
// or refuse them, and revoke.
//
// These live on the MAIN mux, behind the tailnet, and are never registered on the
// gate. The split is the whole security model: the guest's listener cannot reach
// these handlers because it is a different mux on a different port, not because
// of a check any of them performs.

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"time"

	"github.com/hegade/kunai/internal/session"
	"github.com/hegade/kunai/internal/share"
)

// shareView is a share as the owner sees it, which is the whole record plus the
// link and whatever is currently happening with it.
type shareView struct {
	share.Share
	URL string `json:"url"`
	// Reachable reports whether Funnel is actually serving the gate. A link nobody
	// outside can open should say so rather than look fine and fail for the guest.
	Reachable bool `json:"reachable"`
}

func (s *Server) shareView(sh *share.Share) shareView {
	port, on := s.funnelPortFor(s.gate.Port())
	return shareView{Share: *sh, URL: shareURL(s.cfg.PublicURL, port, sh.Token), Reachable: on}
}

// handleListShares returns every live share on this machine.
func (s *Server) handleListShares(w http.ResponseWriter, r *http.Request) {
	if s.shares == nil {
		writeErr(w, http.StatusServiceUnavailable, "no data dir configured")
		return
	}
	all := s.shares.All()
	out := make([]shareView, 0, len(all))
	for i := range all {
		out = append(out, s.shareView(&all[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCreateShare mints a link for one session.
func (s *Server) handleCreateShare(w http.ResponseWriter, r *http.Request) {
	if s.shares == nil {
		writeErr(w, http.StatusServiceUnavailable, "no data dir configured")
		return
	}
	var body struct {
		SessionID string `json:"session_id"`
		Tier      string `json:"tier"`
		TTLSecs   int64  `json:"ttl_secs"`
		Detail    bool   `json:"detail"`    // send tool inputs and outputs too
		FromNow   bool   `json:"from_now"`  // share only what happens from here
		Mode      string `json:"mode"`      // standing mode for guard-cleared calls
		MaxTurns  int    `json:"max_turns"` // 0 = uncapped
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body) != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	sess, ok := s.mgr.Get(body.SessionID)
	if !ok {
		writeErr(w, http.StatusNotFound, "no such session")
		return
	}
	tier := share.Tier(body.Tier)
	if !tier.Valid() {
		writeErr(w, http.StatusBadRequest, "unknown tier")
		return
	}

	meta := sess.Meta()
	sh := share.Share{
		SessionID: sess.ID,
		Title:     meta.Title,
		Tier:      tier,
		Mode:      session.ValidPermissionMode(body.Mode),
		Detail:    share.StrictPolicy(),
		Roots:     s.shareRoots(sess),
		MaxTurns:  body.MaxTurns,
	}
	if body.Detail {
		sh.Detail = share.FullPolicy()
	}
	if body.FromNow {
		sh.FromSeq = sess.HighSeq()
	}

	created, err := s.shares.Create(sh, time.Duration(body.TTLSecs)*time.Second)
	if err != nil {
		if errors.Is(err, share.ErrNoRoom) {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// The gate only runs while something is shared, so this is where it starts.
	if err := s.gate.start(s.baseCtx); err != nil {
		_ = s.shares.Revoke(created.Token)
		writeErr(w, http.StatusInternalServerError, "could not open the public listener: "+err.Error())
		return
	}
	// A tier above view restricts the agent's tools, and those are spawn-time
	// flags, so the session has to be respawned to take them.
	if tier.CanPrompt() {
		if err := s.applyShareTier(r.Context(), created); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not restrict the session: "+err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, s.shareView(created))
}

// shareRoots is the boundary a guest-prompted tool call may touch: the session's
// own folder plus every codebase it has been given. Read once, here, and frozen
// onto the share, so adding a project later cannot widen a live link.
func (s *Server) shareRoots(sess *session.Session) []string {
	roots := []string{sess.Cwd}
	for _, p := range sess.Projects() {
		if p.Path != "" && p.Path != sess.Cwd {
			roots = append(roots, p.Path)
		}
	}
	return roots
}

// handleGetShare returns the share for one session, if it has one.
func (s *Server) handleGetShare(w http.ResponseWriter, r *http.Request) {
	if s.shares == nil {
		writeErr(w, http.StatusServiceUnavailable, "no data dir configured")
		return
	}
	sh, ok := s.shares.BySession(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "not shared")
		return
	}
	writeJSON(w, http.StatusOK, s.shareView(sh))
}

// handleApproveShare lets exactly one guest through, by the code they are looking
// at. Approving by code rather than by device is deliberate: the owner is
// approving what they can see on the other person's screen.
func (s *Server) handleApproveShare(w http.ResponseWriter, r *http.Request) {
	if s.shares == nil {
		writeErr(w, http.StatusServiceUnavailable, "no data dir configured")
		return
	}
	sh, err := s.shares.Approve(r.PathValue("token"), r.PathValue("code"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.shareView(sh))
}

// handleDenyShare refuses the outstanding request, or removes the guest already
// paired, depending on which is there. Both are "no" and it should take one
// action to say it.
func (s *Server) handleDenyShare(w http.ResponseWriter, r *http.Request) {
	if s.shares == nil {
		writeErr(w, http.StatusServiceUnavailable, "no data dir configured")
		return
	}
	token := r.PathValue("token")
	if r.URL.Query().Get("unpair") == "1" {
		if err := s.shares.Unpair(token); err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
	} else if err := s.shares.Deny(token); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	sh, err := s.shares.Get(token)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.shareView(sh))
}

// handleRevokeShare ends a share now. The link stops resolving on the next
// request and any open guest socket notices within a ping.
func (s *Server) handleRevokeShare(w http.ResponseWriter, r *http.Request) {
	if s.shares == nil {
		writeErr(w, http.StatusServiceUnavailable, "no data dir configured")
		return
	}
	token := r.PathValue("token")
	sh, err := s.shares.Get(token)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	if err := s.shares.Revoke(token); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	// Hand the session its full toolset back. Best-effort and deliberately not
	// fatal: the share is already gone, which is what the owner asked for, and a
	// session left more restricted than it needs to be is a nuisance rather than a
	// risk.
	if sh.Tier.CanPrompt() {
		if err := s.clearShareTier(r.Context(), sh.SessionID); err != nil {
			logShare("session %s kept its restricted toolset after the share ended: %v", sh.SessionID, err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// shareStorePath is where links are persisted. Empty data dir means shares are
// in-memory only, which is the right behaviour for a dev run: the links work for
// the life of the process and leave nothing behind.
func shareStorePath(dataDir string) string {
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, "shares.json")
}
