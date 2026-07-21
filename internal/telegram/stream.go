package telegram

import (
	"context"
	"errors"
	"log"
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
// for real, and it is why Refresh exists. A model can easily spend longer than
// thirty seconds thinking or inside a tool call without emitting a token, and
// without a heartbeat the preview expires and the chat sits blank until the turn
// ends: the reported symptom was a long answer appearing only once it finished.
//
// Degrading is per capability and only ever on a flat refusal from Telegram. A
// timeout or a 429 says nothing about what this chat supports, and treating one
// as a refusal drops the chat to the slowest path permanently, on a hiccup.
// See giveUp.

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
	SendRich(ctx context.Context, chatID int64, markdown string, opts *SendOptions) (int64, error)
	DraftRich(ctx context.Context, chatID, draftID int64, markdown string) error
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
	rich     bool  // rich messages still work here, so the reply keeps its Markdown
	drafting bool  // Telegram's streaming endpoint still works for this chat
	draftID  int64 // one per reply; must be non-zero, and equal ids animate

	messageID int64 // the posted message, once there is one
	lastPush  time.Time
	sentText  string
	shown     bool      // the user has seen something, as a draft or as a message
	coolUntil time.Time // honouring a 429; nothing goes out before this
}

func newStream(api sender, chatID int64) *stream {
	return &stream{api: api, chatID: chatID, clock: time.Now, rich: true, drafting: true, draftID: 1}
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
	due := s.clock().Sub(s.lastPush) >= s.intervalLocked() && !s.coolingLocked()
	s.mu.Unlock()
	if due {
		_ = s.push(ctx, false)
	}
}

// coolingLocked reports whether a 429 is still being served out. Caller holds
// the lock.
func (s *stream) coolingLocked() bool { return s.clock().Before(s.coolUntil) }

// maxFinalWait bounds how long posting the finished reply will sit out a
// throttle. Long enough to clear an ordinary penalty window, short enough that
// nobody is left staring at a chat for a reply that is not coming.
const maxFinalWait = 30 * time.Second

// retryAfter pulls Telegram's requested wait out of an error.
func retryAfter(err error) (time.Duration, bool) {
	var api *APIError
	if !errors.As(err, &api) || api.RetryAfter <= 0 {
		return 0, false
	}
	return time.Duration(api.RetryAfter) * time.Second, true
}

// backOff records the wait Telegram asked for.
//
// retry_after has to be obeyed, not merely noticed. Telegram's edge caches the
// penalty window, so retrying early resets it and the wait gets longer: a
// streaming reply that ignores it turns one throttled push into a throttled
// turn. This was the shape of a real bug in other bots before it was one here.
func (s *stream) backOff(err error) {
	wait, ok := retryAfter(err)
	if !ok {
		return
	}
	s.mu.Lock()
	until := s.clock().Add(wait)
	if until.After(s.coolUntil) {
		s.coolUntil = until
	}
	s.mu.Unlock()
	log.Printf("telegram: rate limited in chat %d, holding %s", s.chatID, wait)
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
		if err := s.draft(ctx, draftID, text); err != nil {
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
		newID, err := s.post(ctx, text)
		if err != nil {
			return err
		}
		s.mu.Lock()
		s.messageID, s.sentText, s.lastPush, s.shown = newID, text, s.clock(), true
		s.mu.Unlock()
		return nil
	}
	// An edit only happens on the fallback path, where the message was posted
	// as plain text in the first place.
	if err := s.api.Edit(ctx, s.chatID, id, text, nil); err != nil {
		return err
	}
	s.mu.Lock()
	s.sentText, s.lastPush = text, s.clock()
	s.mu.Unlock()
	return nil
}

// draft streams one fragment, giving up one capability per failure.
//
// A failed draft is only a lost preview, so it degrades and returns rather than
// retrying: rich first, because a rich draft failing is more likely to be about
// rich than about drafting, then plain drafting, then nothing (the next push
// posts a message instead).
func (s *stream) draft(ctx context.Context, draftID int64, text string) error {
	s.mu.Lock()
	rich := s.rich
	s.mu.Unlock()

	if rich {
		if err := s.api.DraftRich(ctx, s.chatID, draftID, text); err != nil {
			s.backOff(err)
			s.giveUp(&s.rich, "rich messages", err)
			return err
		}
		return nil
	}
	if err := s.api.Draft(ctx, s.chatID, draftID, text); err != nil {
		s.backOff(err)
		s.giveUp(&s.drafting, "drafting", err)
		return err
	}
	return nil
}

// giveUp turns a capability off, but only for a reason that will still be true
// next time. A downgrade lasts for the life of the chat, so a timeout on a bad
// route or a 429 for pushing too fast must not cause one: those would drop every
// chat to the slowest path on the first hiccup and never let it back. It is
// logged, because otherwise the only symptom is a reply that quietly got worse.
func (s *stream) giveUp(flag *bool, what string, err error) {
	if !unsupported(err) {
		return
	}
	s.mu.Lock()
	already := !*flag
	*flag = false
	s.mu.Unlock()
	if !already {
		log.Printf("telegram: %s unavailable in chat %d, falling back: %v", what, s.chatID, err)
	}
}

// post lands the message that stays in the chat.
//
// Unlike a draft this one cannot be allowed to fail quietly: Flush runs once per
// turn, so a rejected rich message would lose the reply outright. It falls back
// to plain text within the same call, which costs a wasted request once and
// never a lost answer.
func (s *stream) post(ctx context.Context, text string) (int64, error) {
	s.mu.Lock()
	rich := s.rich
	s.mu.Unlock()

	if rich {
		id, err := s.api.SendRich(ctx, s.chatID, text, nil)
		if err == nil {
			return id, nil
		}
		s.backOff(err)
		s.giveUp(&s.rich, "rich messages", err)
	}
	id, err := s.api.Send(ctx, s.chatID, text, nil)
	if err == nil {
		return id, nil
	}
	// The reply itself is the one thing that must not be dropped, so a throttle
	// here is waited out rather than surrendered to. Bounded, because a turn
	// that ended twenty minutes ago is not worth posting.
	wait, ok := retryAfter(err)
	if !ok || wait > maxFinalWait {
		return 0, err
	}
	s.backOff(err)
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-time.After(wait):
	}
	return s.api.Send(ctx, s.chatID, text, nil)
}

// Refresh re-asserts the draft so it does not expire mid-turn.
//
// This is the fix for a long answer showing nothing until it lands. A draft
// lives about thirty seconds, and the model can easily spend longer than that
// thinking or in a tool call without emitting a single token, so the preview
// expires and the chat goes blank until the turn ends. A heartbeat keeps it up.
// With nothing written yet it sends an empty draft, which is Telegram's own
// "Thinking..." placeholder, so the wait before the first word is shown as a
// wait rather than as silence.
func (s *stream) Refresh(ctx context.Context) {
	s.mu.Lock()
	drafting, id := s.drafting, s.messageID
	text := strings.TrimSpace(s.buf.String())
	draftID := s.draftID
	s.mu.Unlock()

	// Once the reply is a real message there is no draft left to keep alive.
	// A keep-alive is also the least urgent thing there is, so it never spends
	// a request while Telegram has asked us to wait.
	s.mu.Lock()
	cooling := s.coolingLocked()
	s.mu.Unlock()
	if !drafting || id != 0 || cooling {
		return
	}
	if err := s.draft(ctx, draftID, text); err == nil {
		s.mu.Lock()
		s.lastPush = s.clock()
		if text != "" {
			s.sentText, s.shown = text, true
		}
		s.mu.Unlock()
	}
}

// Reset starts a new reply for the next turn, so replies do not accumulate into
// one ever-growing bubble across a conversation. The draft id moves on with it:
// Telegram animates between updates sharing an id, so reusing one would make a
// new answer morph out of the last one.
//
// Whether drafting and rich messages still work is deliberately NOT reset. Both
// are facts about the chat, not about the turn, and re-learning them every turn
// would mean a failed call every turn for the life of that chat.
func (s *stream) Reset() {
	s.mu.Lock()
	s.buf.Reset()
	s.messageID, s.sentText, s.shown = 0, "", false
	s.lastPush = time.Time{}
	s.draftID++
	s.mu.Unlock()
}
