package server

// Managing the lock: setting the PIN, reviewing which devices are signed in, and
// signing them out.
//
// Separate from lanauth.go because the audience is different. Those routes face
// whoever is trying to get IN and must assume the worst; these face the owner and
// are only reachable from a listener that already trusts the caller. Keeping them
// apart means the gate's allowlist stays short enough to check by eye.
//
// These are registered on the ordinary mux, so on the network listener they sit
// BEHIND the gate like everything else: a signed-in device can change the PIN,
// which is deliberate (you may be holding the only device to hand), and an
// unauthenticated one cannot reach them at all.

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/hegade/kunai/internal/lanauth"
)

func (s *Server) lanAdminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/lan/pin", s.handleLANPINState)
	mux.HandleFunc("POST /api/lan/pin", s.handleSetLANPIN)
	mux.HandleFunc("DELETE /api/lan/pin", s.handleClearLANPIN)
	mux.HandleFunc("GET /api/lan/devices", s.handleLANDevices)
	mux.HandleFunc("DELETE /api/lan/devices", s.handleForgetLANDevices)
}

// handleLANPINState reports whether a PIN is set, so Settings can show the right
// thing without ever reading the PIN back (there is nothing to read: only a hash
// is stored).
func (s *Server) handleLANPINState(w http.ResponseWriter, r *http.Request) {
	// The addresses are included so the settings panel can show where to point the
	// other device. Knowing a PIN is set is useless without knowing the link, and
	// making somebody find their own LAN address is the step where they give up.
	writeJSON(w, http.StatusOK, map[string]any{
		"set": s.lanAuth != nil && s.lanAuth.HasPIN(),
		// Reported from the listeners themselves rather than from config, so the
		// panel can never claim the network is served when nothing is bound.
		"enabled": s.lanNet != nil && s.lanNet.Running(),
		"urls":    s.lanURLs(),
		"min_len": lanauth.MinPINLength,
		"max_len": lanauth.MaxPINLength,
	})
}

// syncLANListeners brings the listeners into line with the PIN. Safe when there
// is nothing to do, and a no-op before Run has built them.
func (s *Server) syncLANListeners() {
	if s.lanNet == nil {
		return
	}
	ctx := s.baseCtx
	if ctx == nil {
		ctx = context.Background()
	}
	s.lanNet.Sync(ctx)
}

func (s *Server) lanURLs() []string {
	if s.lanNet == nil {
		return []string{}
	}
	if u := s.lanNet.Addrs(); len(u) > 0 {
		return u
	}
	return []string{}
}

func (s *Server) handleSetLANPIN(w http.ResponseWriter, r *http.Request) {
	if s.lanAuth == nil {
		writeErr(w, http.StatusServiceUnavailable, "no data dir, so a PIN cannot be stored")
		return
	}
	var body struct {
		PIN string `json:"pin"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if err := s.lanAuth.SetPIN(body.PIN); err != nil {
		// The validation messages are for the OWNER choosing a PIN, so they say
		// exactly what is wrong. That is the opposite of the login path, which
		// tells a stranger nothing.
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// The PIN IS the switch, so this is the moment the network starts being
	// served. Waiting for a restart is what made the first version look broken.
	s.syncLANListeners()
	writeJSON(w, http.StatusOK, map[string]any{
		"set":  true,
		"urls": s.lanURLs(),
	})
}

func (s *Server) handleClearLANPIN(w http.ResponseWriter, r *http.Request) {
	if s.lanAuth == nil {
		writeErr(w, http.StatusServiceUnavailable, "no data dir")
		return
	}
	if err := s.lanAuth.ClearPIN(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Removing the PIN takes the machine off the network at once, rather than at
	// the next restart. Anything else would leave it served and unlocked in the
	// meantime, which is the one state this must never be in.
	s.syncLANListeners()
	writeJSON(w, http.StatusOK, map[string]any{"set": false})
}

// deviceView is what the owner sees about a signed-in device. It carries no token
// and no hash: knowing which devices exist should never be a way to become one.
type deviceView struct {
	Label   string `json:"label,omitempty"`
	Created int64  `json:"created"`
	Seen    int64  `json:"seen"`
}

func (s *Server) handleLANDevices(w http.ResponseWriter, r *http.Request) {
	out := []deviceView{}
	if s.lanAuth != nil {
		for _, d := range s.lanAuth.Devices() {
			out = append(out, deviceView{Label: d.Label, Created: d.Created.Unix(), Seen: d.Seen.Unix()})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleForgetLANDevices signs every device out while keeping the PIN, for when
// you have handed a tablet on and would rather not change the PIN everywhere.
func (s *Server) handleForgetLANDevices(w http.ResponseWriter, r *http.Request) {
	if s.lanAuth == nil {
		writeErr(w, http.StatusServiceUnavailable, "no data dir")
		return
	}
	if err := s.lanAuth.ForgetAll(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"signed_out_at": time.Now().Unix()})
}
