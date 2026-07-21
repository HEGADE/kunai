package telegram

import (
	"context"
	"time"

	"github.com/hegade/kunai/internal/session"
)

// What a chat channel needs from kunai's sessions, and nothing more.
//
// This is deliberately narrower than the session manager. A chat does not
// choose a model, a reasoning effort or a Claude account: those come from the
// server's configuration, exactly as they do for a session started in the app.
// Keeping that decision on the server side is what makes a session born in
// Telegram indistinguishable from one born in the PWA, push notifications and
// all, rather than a second half-wired code path that drifts.
//
// It is an interface so the bot's logic can be tested without spawning a real
// claude, and so the next channel (Slack) implements one thing rather than
// rediscovering how a session is made.

// Sessions is the channel-facing view of kunai's sessions.
type Sessions interface {
	// Start opens a new session in a directory.
	Start(ctx context.Context, cwd string) (*session.Session, error)
	// Resume brings a past session back with its conversation intact. A session
	// that is still live is returned as-is rather than started twice.
	Resume(ctx context.Context, id string) (*session.Session, error)
	// Recent lists past sessions that can be resumed, newest first.
	Recent(limit int) []Past
	// Get looks up a live session.
	Get(id string) (*session.Session, bool)
	// List reports the live sessions.
	List() []session.Meta
	// Close ends a live session. Its transcript survives, so Resume still works.
	Close(id string)
}

// Past is a session that is no longer running but can be brought back. It is
// the transcript's view of a session, which is why it carries no state.
type Past struct {
	ID    string
	Cwd   string
	Title string
	When  time.Time
}
