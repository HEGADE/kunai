package telegram

import (
	"context"
	"sync"
	"time"
)

// The "typing" bubble, kept alive for as long as a turn runs.
//
// Telegram's chat action is a one-shot that expires after five seconds, and it
// is cleared early the moment the bot sends anything. A turn here routinely
// takes minutes and posts tool lines while it works, so a single call at prompt
// time is invisible almost immediately: the phone shows the message you sent and
// then nothing, which reads as a bot that died. The fix is a heartbeat.

// typingEvery is how often the status is re-asserted. Under the five-second
// expiry with room for a slow round trip, and cheap enough that a long turn's
// heartbeat is a rounding error next to the model calls it is waiting on.
const typingEvery = 4 * time.Second

// actor is the slice of the API the indicator needs, so it can be tested
// without a network.
type actor interface {
	SendChatAction(ctx context.Context, chatID int64, action string) error
}

// typist keeps one chat's typing status up until the turn ends.
type typist struct {
	api    actor
	chatID int64
	every  time.Duration

	mu     sync.Mutex
	cancel context.CancelFunc
}

func newTypist(api actor, chatID int64) *typist {
	return &typist{api: api, chatID: chatID, every: typingEvery}
}

// Start shows the indicator and keeps showing it until Stop. Starting an
// already-running typist is a no-op, so a burst of state events cannot leave two
// heartbeats racing each other.
func (t *typist) Start(parent context.Context) {
	t.mu.Lock()
	if t.cancel != nil {
		t.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	t.cancel = cancel
	every := t.every
	t.mu.Unlock()

	go func() {
		tick := time.NewTicker(every)
		defer tick.Stop()
		for {
			// A dropped beat is not worth reporting: the indicator is a
			// courtesy, and the next tick retries it anyway.
			_ = t.api.SendChatAction(ctx, t.chatID, "typing")
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
}

// Stop ends the heartbeat. Nothing has to be un-sent: Telegram drops the status
// on its own within a few seconds, and sooner than that once the reply lands.
func (t *typist) Stop() {
	t.mu.Lock()
	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}
	t.mu.Unlock()
}
