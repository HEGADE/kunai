package telegram

import (
	"context"
	"strings"
	"sync"
	"time"
)

// One reply being written into one chat, live.
//
// There are two ways to do this and the difference is visible. Telegram's
// streaming method (sendMessageDraft, Bot API 9.3, opened to all bots in 9.5)
// animates text in the way its own assistant does. Failing that, a reply can be
// posted and then rewritten with editMessageText, which works everywhere but
// arrives in jerks because edits are rate-limited hard enough that rewriting
// more than about once a second gets the bot throttled mid-answer.
//
// So: draft when we can, edit when we cannot, and decide which by trying rather
// than by guessing. A draft is a private-chat method and the bot may be in a
// group, so rather than sniff the chat type, the first draft that is refused
// turns drafting off for that chat for good and the reply carries on as edits.
// One wasted call, once, and no capability check to keep in sync with Telegram.
//
// A draft is a preview, not a message: Telegram shows it for about thirty
// seconds and then it is gone. That is why Flush still posts the finished reply
// for real, and it is also the one place drafting reads worse than editing. If a
// turn writes some prose and then spends a minute in tool calls, the preview
// expires and that prose is off screen until the turn ends. The alternative was
// a keep-alive timer per chat, which is a lot of machinery for a gap the tool
// lines and the typing indicator already fill.

// draftEvery and editEvery are how often a growing reply is pushed out. They
// differ by an order of magnitude on purpose: a draft is the endpoint Telegram
// built for exactly this and animates between updates, while an edit is a
// general-purpose write on a rate limiter that a token-by-token rewrite would
// exhaust within a sentence.
const (
	draftEvery = 400 * time.Millisecond
	editEvery  = 1500 * time.Millisecond
)

// sender is the slice of the API a stream needs. An interface so the stream can
// be tested without a network.
type sender interface {
	Send(ctx context.Context, chatID int64, text string, opts *SendOptions) (int64, error)
	Edit(ctx context.Context, chatID, messageID int64, text string, opts *SendOptions) error
	Draft(ctx context.Context, chatID, draftID int64, text string) error
}

// stream is one reply being written into a single chat.
//
// Nothing is sent until there is something to say, so a turn that only calls
// tools never posts an empty bubble.
type stream struct {
	api    sender
	chatID int64
	clock  func() time.Time

	mu       sync.Mutex
	buf      strings.Builder
	drafting bool  // Telegram's streaming endpoint still works for this chat
	draftID  int64 // one per reply; must be non-zero, and equal ids animate

	messageID int64 // the posted message, once there is one
	lastPush  time.Time
	sentText  string
	shown     bool // the user has seen something, as a draft or as a message
}

func newStream(api sender, chatID int64) *stream {
	return &stream{api: api, chatID: chatID, clock: time.Now, drafting: true, draftID: 1}
}

// Append adds text and pushes it out if enough time has passed since the last
// write. Errors are dropped: a failed push is retried by the next append, and
// the final Flush is what actually has to land.
func (s *stream) Append(ctx context.Context, text string) {
	if text == "" {
		return
	}
	s.mu.Lock()
	s.buf.WriteString(text)
	due := s.clock().Sub(s.lastPush) >= s.intervalLocked()
	s.mu.Unlock()
	if due {
		_ = s.push(ctx, false)
	}
}

// Flush lands the reply for good, regardless of the push interval. A draft is
// only a preview and expires on its own, so this is the call that turns what was
// watched being written into a message that is still there tomorrow.
func (s *stream) Flush(ctx context.Context) error { return s.push(ctx, true) }

// Active reports whether this stream has shown anything, which tells the caller
// whether the complete assistant message still needs posting.
func (s *stream) Active() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shown
}

// intervalLocked is the current push cadence. Caller holds the lock.
func (s *stream) intervalLocked() time.Duration {
	if s.drafting {
		return draftEvery
	}
	return editEvery
}

// push writes the buffer out. final says the turn is over, which is what decides
// between animating a draft and posting the message that persists.
func (s *stream) push(ctx context.Context, final bool) error {
	s.mu.Lock()
	text := strings.TrimSpace(s.buf.String())
	drafting, id, draftID := s.drafting, s.messageID, s.draftID
	unchanged := text == s.sentText
	s.mu.Unlock()

	// Telegram rejects an empty message outright, and an edit that changes
	// nothing, so both are skipped rather than sent and apologised for. The
	// unchanged check does not apply to a final push that has only ever been
	// drafted: a draft the user watched is a preview, not a message, and
	// skipping it would leave the turn with nothing in the chat at all.
	if text == "" {
		return nil
	}
	if unchanged && !(final && drafting && id == 0) {
		return nil
	}

	if drafting && !final {
		if err := s.api.Draft(ctx, s.chatID, draftID, text); err != nil {
			// This chat cannot take drafts (a group, or an older Bot API on the
			// far end). Stop trying, and let the next push post a message.
			s.mu.Lock()
			s.drafting = false
			s.mu.Unlock()
			return err
		}
		s.mu.Lock()
		s.sentText, s.lastPush, s.shown = text, s.clock(), true
		s.mu.Unlock()
		return nil
	}

	// Post the first fragment, or the finished reply. Everything shown as a
	// draft was ephemeral, so this is what actually stays in the chat.
	if id == 0 {
		newID, err := s.api.Send(ctx, s.chatID, text, nil)
		if err != nil {
			return err
		}
		s.mu.Lock()
		s.messageID, s.sentText, s.lastPush, s.shown = newID, text, s.clock(), true
		s.mu.Unlock()
		return nil
	}
	if err := s.api.Edit(ctx, s.chatID, id, text, nil); err != nil {
		return err
	}
	s.mu.Lock()
	s.sentText, s.lastPush = text, s.clock()
	s.mu.Unlock()
	return nil
}

// Reset starts a new reply for the next turn, so replies do not accumulate into
// one ever-growing bubble across a conversation. The draft id moves on with it:
// Telegram animates between updates sharing an id, so reusing one would make a
// new answer morph out of the last one.
//
// Whether drafting still works is deliberately NOT reset. That is a fact about
// the chat, not about the turn, and re-learning it every turn would mean a
// failed call every turn for the life of a group chat.
func (s *stream) Reset() {
	s.mu.Lock()
	s.buf.Reset()
	s.messageID, s.sentText, s.shown = 0, "", false
	s.lastPush = time.Time{}
	s.draftID++
	s.mu.Unlock()
}
