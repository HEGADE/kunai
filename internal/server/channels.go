package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/hegade/kunai/internal/telegram"
)

// Channels: the ways to reach kunai other than the app itself.
//
// Telegram is the only one today and Slack is the obvious next, so the wire
// shape is a list of channels rather than a Telegram endpoint. A second channel
// is then a new entry with the same fields, and the client renders it without
// changes.

// ChannelPerson is someone allowed to drive kunai through a channel.
type ChannelPerson struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Username string `json:"username,omitempty"`
}

// ChannelRequest is an unapproved request to use a channel.
type ChannelRequest struct {
	Code     string `json:"code"`
	Name     string `json:"name,omitempty"`
	Username string `json:"username,omitempty"`
	AskedAt  int64  `json:"asked_at"`
}

// ChannelInfo is one channel's state. Mirrors web/src/lib/types.ts.
type ChannelInfo struct {
	ID   string `json:"id"`   // "telegram"
	Name string `json:"name"` // "Telegram"
	// Available is false for a channel that exists in the UI but is not built
	// yet, so the client can show what is coming without pretending it works.
	Available bool `json:"available"`
	// Connected means it has credentials and is running.
	Connected bool `json:"connected"`
	// HasSecret reports that a token is stored, without ever sending it back.
	HasSecret bool             `json:"has_secret"`
	People    []ChannelPerson  `json:"people"`
	Waiting   []ChannelRequest `json:"waiting"`
	// Detail is the opt-out from redaction: tool inputs and outputs leave the
	// machine when it is on.
	Detail bool `json:"detail"`
	// Help is the one thing a person needs to do outside kunai to set it up.
	Help string `json:"help,omitempty"`
}

// handleChannels lists every channel and its state.
func (s *Server) handleChannels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []ChannelInfo{s.telegramInfo(), slackPlaceholder()})
}

func (s *Server) telegramInfo() ChannelInfo {
	info := ChannelInfo{
		ID:        "telegram",
		Name:      "Telegram",
		Available: true,
		People:    []ChannelPerson{},
		Waiting:   []ChannelRequest{},
		Help:      "Create a bot with @BotFather, then paste its token here.",
	}
	if s.telegram == nil {
		return info
	}
	token, people, waiting, detail := s.telegram.Snapshot()
	info.HasSecret = token != ""
	info.Connected = token != ""
	info.Detail = detail
	for _, p := range people {
		info.People = append(info.People, ChannelPerson{
			ID: strconv.FormatInt(p.ID, 10), Name: p.Name, Username: p.Username,
		})
	}
	for _, wq := range waiting {
		info.Waiting = append(info.Waiting, ChannelRequest{
			Code: wq.Code, Name: wq.Name, Username: wq.Username, AskedAt: wq.AskedAt,
		})
	}
	return info
}

// slackPlaceholder is listed but not available, so the shape of the screen is
// honest about what is coming without claiming it works.
func slackPlaceholder() ChannelInfo {
	return ChannelInfo{
		ID: "slack", Name: "Slack",
		People: []ChannelPerson{}, Waiting: []ChannelRequest{},
	}
}

// handleChannelUpdate saves a channel's settings: its secret and its redaction
// choice. An empty token is a disconnect, which is the only way to stop a
// channel from the app.
func (s *Server) handleChannelUpdate(w http.ResponseWriter, r *http.Request) {
	if s.telegram == nil || r.PathValue("id") != "telegram" {
		writeErr(w, http.StatusNotFound, "unknown channel")
		return
	}
	var req struct {
		Token  *string `json:"token"`
		Detail *bool   `json:"detail"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Token != nil {
		s.telegram.SetToken(strings.TrimSpace(*req.Token))
	}
	if req.Detail != nil {
		s.telegram.SetDetail(*req.Detail)
	}
	writeJSON(w, http.StatusOK, s.telegramInfo())
}

// handleChannelApprove turns a pairing request into access, or refuses it. The
// code is what the person read out of their chat.
func (s *Server) handleChannelApprove(w http.ResponseWriter, r *http.Request) {
	if s.telegram == nil || r.PathValue("id") != "telegram" {
		writeErr(w, http.StatusNotFound, "unknown channel")
		return
	}
	code := r.PathValue("code")
	var req struct {
		Approve bool `json:"approve"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if req.Approve {
		if _, ok := s.telegram.Approve(code); !ok {
			writeErr(w, http.StatusNotFound, "that request expired; ask them to message the bot again")
			return
		}
	} else if !s.telegram.Deny(code) {
		writeErr(w, http.StatusNotFound, "no such request")
		return
	}
	writeJSON(w, http.StatusOK, s.telegramInfo())
}

// handleChannelRevoke removes someone's access to a channel.
func (s *Server) handleChannelRevoke(w http.ResponseWriter, r *http.Request) {
	if s.telegram == nil || r.PathValue("id") != "telegram" {
		writeErr(w, http.StatusNotFound, "unknown channel")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("person"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad person id")
		return
	}
	s.telegram.Revoke(id)
	writeJSON(w, http.StatusOK, s.telegramInfo())
}

// startTelegram launches the bot. Unlike most of kunai's background loops it
// starts even with nothing configured: the token arrives from the app, and the
// bot waits for it rather than making someone restart the server.
func (s *Server) startTelegram(ctx context.Context) {
	s.telegram = telegram.LoadStore(s.cfg.DataDir, s.cfg.TelegramToken, s.cfg.TelegramAllowed)
	if s.cfg.TelegramDetail {
		s.telegram.SetDetail(true)
	}
	go telegram.New(s.telegram, channelSessions{srv: s}).Run(ctx)
}
