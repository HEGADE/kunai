package server

import (
	"context"
	"log"

	"github.com/hegade/kunai/internal/telegram"
)

// Wiring for the optional Telegram bot.
//
// It is off unless a token is configured, and it refuses everyone unless a user
// id is allowed, because a chat with this bot is equivalent to a shell on this
// machine. The bot lives in its own package and reaches the session manager
// through a narrow interface; this file is the only place the two meet.

// startTelegram launches the bot when one is configured. A missing token is the
// normal case and says nothing; a token with no allow list is a misconfiguration
// worth complaining about, since the bot would refuse every message.
func (s *Server) startTelegram(ctx context.Context) {
	cfg := telegram.Config{
		Token:   s.cfg.TelegramToken,
		Allowed: s.cfg.TelegramAllowed,
		DataDir: s.cfg.DataDir,
		Policy:  telegram.StrictPolicy(),
	}
	if s.cfg.TelegramDetail {
		// Opt-in: send what tools were given and what they returned. That is
		// file contents and command output going to a third party, so it is
		// never the default.
		cfg.Policy = telegram.Policy{ToolInputs: true, ToolOutputs: true}
	}
	if !cfg.Enabled() {
		return
	}
	if len(cfg.Allowed) == 0 {
		log.Print("telegram: token set but no allowed user ids; the bot would refuse every message, not starting")
		return
	}
	go telegram.New(cfg, s.mgr).Run(ctx)
}
