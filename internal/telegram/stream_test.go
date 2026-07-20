package telegram

import (
	"context"
	"testing"
	"time"
)

// fakeSender records what a stream would have sent.
type fakeSender struct {
	sends  []string
	edits  []string
	nextID int64
}

func (f *fakeSender) Send(_ context.Context, _ int64, text string, _ *SendOptions) (int64, error) {
	f.sends = append(f.sends, text)
	f.nextID++
	return f.nextID, nil
}

func (f *fakeSender) Edit(_ context.Context, _, _ int64, text string, _ *SendOptions) error {
	f.edits = append(f.edits, text)
	return nil
}

// A reply arrives in fragments but should read as one message, so the first
// fragment posts and the rest edit.
func TestStreamPostsOnceThenEdits(t *testing.T) {
	f := &fakeSender{}
	s := newStream(f, 1)
	now := time.Now()
	s.clock = func() time.Time { return now }

	s.Append(context.Background(), "Fixing ")
	now = now.Add(editEvery + time.Second) // the edit window has passed
	s.Append(context.Background(), "the test.")
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(f.sends) != 1 {
		t.Fatalf("sent %d messages, want 1: %v", len(f.sends), f.sends)
	}
	if got := lastOf(f.edits, f.sends); got != "Fixing the test." {
		t.Errorf("final text = %q", got)
	}
}

// Telegram rate-limits edits, so fragments arriving in a burst must not each
// become a request.
func TestStreamThrottlesWithinTheEditWindow(t *testing.T) {
	f := &fakeSender{}
	s := newStream(f, 1)
	now := time.Now()
	s.clock = func() time.Time { return now }

	for i := 0; i < 20; i++ {
		s.Append(context.Background(), "word ") // clock never advances
	}
	if len(f.edits) != 0 {
		t.Errorf("made %d edits inside one window, want 0", len(f.edits))
	}
	if len(f.sends) != 1 {
		t.Errorf("sent %d messages, want the first one only", len(f.sends))
	}
}

// A turn of pure tool work produces no prose, and an empty message is a hard
// rejection from Telegram.
func TestStreamSendsNothingWhenThereIsNoText(t *testing.T) {
	f := &fakeSender{}
	s := newStream(f, 1)
	s.Append(context.Background(), "")
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.sends)+len(f.edits) != 0 {
		t.Errorf("sent something for an empty reply: %v %v", f.sends, f.edits)
	}
	if s.Active() {
		t.Error("a stream with nothing in it should not count as active")
	}
}

// Telegram rejects an edit that changes nothing, which would otherwise happen
// every time a flush follows a push with no new text.
func TestStreamSkipsUnchangedEdits(t *testing.T) {
	f := &fakeSender{}
	s := newStream(f, 1)
	now := time.Now()
	s.clock = func() time.Time { return now }

	s.Append(context.Background(), "done")
	_ = s.Flush(context.Background())
	_ = s.Flush(context.Background()) // nothing new
	if len(f.edits) != 0 {
		t.Errorf("re-sent unchanged text: %v", f.edits)
	}
}

// Each turn gets its own message, or a long conversation becomes one bubble
// that grows past Telegram's limit.
func TestStreamResetStartsANewMessage(t *testing.T) {
	f := &fakeSender{}
	s := newStream(f, 1)
	s.clock = time.Now

	s.Append(context.Background(), "first turn")
	_ = s.Flush(context.Background())
	s.Reset()
	s.Append(context.Background(), "second turn")
	_ = s.Flush(context.Background())

	if len(f.sends) != 2 {
		t.Fatalf("sent %d messages, want one per turn: %v", len(f.sends), f.sends)
	}
	if f.sends[1] != "second turn" {
		t.Errorf("second message = %q, want just the second turn", f.sends[1])
	}
}

func lastOf(edits, sends []string) string {
	if len(edits) > 0 {
		return edits[len(edits)-1]
	}
	if len(sends) > 0 {
		return sends[len(sends)-1]
	}
	return ""
}
