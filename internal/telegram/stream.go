package telegram

import (
	"context"
	"strings"
	"sync"
	"time"
)

// editEvery bounds how often a growing reply is rewritten. Telegram rate-limits
// edits, and a token-by-token rewrite would burn that budget within a sentence
// and get the bot throttled mid-answer. Slow enough to be safe, quick enough
// that the reply visibly moves.
const editEvery = 1500 * time.Millisecond

// sender is the slice of the API a stream needs. An interface so the stream can
// be tested without a network.
type sender interface {
	Send(ctx context.Context, chatID int64, text string, opts *SendOptions) (int64, error)
	Edit(ctx context.Context, chatID, messageID int64, text string, opts *SendOptions) error
}

// stream is one reply being written into a single chat message.
//
// The first fragment posts a message and the rest edit it, so a long answer
// arrives as one growing message rather than a wall of fragments. Nothing is
// sent until there is something to say, so a turn that only calls tools never
// posts an empty bubble.
type stream struct {
	api    sender
	chatID int64
	clock  func() time.Time

	mu        sync.Mutex
	buf       strings.Builder
	messageID int64
	lastEdit  time.Time
	sentText  string
}

func newStream(api sender, chatID int64) *stream {
	return &stream{api: api, chatID: chatID, clock: time.Now}
}

// Append adds text and pushes it out if enough time has passed since the last
// write. Errors are dropped: a failed edit is retried by the next append, and
// the final Flush is what actually has to land.
func (s *stream) Append(ctx context.Context, text string) {
	if text == "" {
		return
	}
	s.mu.Lock()
	s.buf.WriteString(text)
	due := s.clock().Sub(s.lastEdit) >= editEvery
	s.mu.Unlock()
	if due {
		_ = s.push(ctx)
	}
}

// Flush writes whatever is buffered, regardless of the edit interval. Called
// when a turn ends, so the last words are never left unsent.
func (s *stream) Flush(ctx context.Context) error { return s.push(ctx) }

// Active reports whether this stream has posted anything, which tells the caller
// whether a turn produced a reply at all.
func (s *stream) Active() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.messageID != 0
}

func (s *stream) push(ctx context.Context) error {
	s.mu.Lock()
	text := strings.TrimSpace(s.buf.String())
	id := s.messageID
	unchanged := text == s.sentText
	s.mu.Unlock()

	// Telegram rejects an edit that changes nothing, and an empty message
	// outright, so both are skipped rather than sent and apologised for.
	if text == "" || unchanged {
		return nil
	}

	if id == 0 {
		newID, err := s.api.Send(ctx, s.chatID, text, nil)
		if err != nil {
			return err
		}
		s.mu.Lock()
		s.messageID, s.sentText, s.lastEdit = newID, text, s.clock()
		s.mu.Unlock()
		return nil
	}
	if err := s.api.Edit(ctx, s.chatID, id, text, nil); err != nil {
		return err
	}
	s.mu.Lock()
	s.sentText, s.lastEdit = text, s.clock()
	s.mu.Unlock()
	return nil
}

// Reset starts a new message for the next turn, so replies do not accumulate
// into one ever-growing bubble across a conversation.
func (s *stream) Reset() {
	s.mu.Lock()
	s.buf.Reset()
	s.messageID, s.sentText = 0, ""
	s.lastEdit = time.Time{}
	s.mu.Unlock()
}
