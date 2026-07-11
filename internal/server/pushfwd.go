package server

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// Push forwarding: a peer machine (started with -hub-url) cannot reach the
// phone's push subscription — that lives on the hub. So instead of sending a
// Web Push itself, the peer POSTs a generic wake-up to the hub over the tailnet,
// and the hub fans it out to its subscribers. The payload is still generic
// (title/body only), preserving the "no session content leaves the tailnet" rule
// — and push already leaves the tailnet via APNs/FCM from the hub either way.

// forwardWake sends a generic wake-up to the hub, fire-and-forget.
func (s *Server) forwardWake(title, body string) {
	origin := normalizeOrigin(s.cfg.HubURL)
	if origin == "" {
		return
	}
	payload, _ := json.Marshal(map[string]string{"title": title, "body": body})
	go func() {
		client := &http.Client{Timeout: 4 * time.Second}
		req, err := http.NewRequest(http.MethodPost, origin+"/api/push/relay", bytes.NewReader(payload))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("push forward to hub: %v", err)
			return
		}
		_ = resp.Body.Close()
	}()
}

// handlePushRelay (hub side) turns a forwarded wake-up into a real push.
func (s *Server) handlePushRelay(w http.ResponseWriter, r *http.Request) {
	if s.push == nil {
		writeErr(w, http.StatusNotFound, "push disabled")
		return
	}
	var body struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.Title == "" {
		body.Title = "Kunai"
	}
	if body.Body == "" {
		body.Body = "A session needs your attention"
	}
	s.push.Notify(body.Title, body.Body)
	w.WriteHeader(http.StatusNoContent)
}
