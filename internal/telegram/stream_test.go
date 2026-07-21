package telegram

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A reply has to arrive two different ways and both have to be right: as an
// animated draft where Telegram supports it, and as an edited message where it
// does not. The fallback is not a rare path, it is every group chat.

// draftCall is one streamed fragment, with the id that decides whether Telegram
// animates it into the previous one or starts a new bubble.
type draftCall struct {
	id   int64
	text string
}

// fakeSender records what a stream would have sent.
type fakeSender struct {
	sends    []string
	edits    []string
	drafts   []draftCall
	nextID   int64
	draftErr error // what sendMessageDraft returns; nil means it works
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

func (f *fakeSender) Draft(_ context.Context, _, id int64, text string) error {
	if f.draftErr != nil {
		return f.draftErr
	}
	f.drafts = append(f.drafts, draftCall{id: id, text: text})
	return nil
}

// noDrafts puts a stream on the fallback path, which is what a group chat gets.
func noDrafts(s *stream) *stream {
	s.drafting = false
	return s
}

func (f *fakeSender) draftTexts() []string {
	out := make([]string, 0, len(f.drafts))
	for _, d := range f.drafts {
		out = append(out, d.text)
	}
	return out
}

// --- the draft path ---

// A draft is a thirty-second preview, not a message. Streaming into one and
// stopping there would leave the chat empty once it expired, so the finished
// reply still has to be posted for real.
func TestStreamDraftsThenPostsTheRealMessage(t *testing.T) {
	f := &fakeSender{}
	s := newStream(f, 1)
	now := time.Now()
	s.clock = func() time.Time { return now }

	s.Append(context.Background(), "Fixing ")
	now = now.Add(draftEvery + time.Millisecond)
	s.Append(context.Background(), "the test.")
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := f.draftTexts(); len(got) != 2 || got[1] != "Fixing the test." {
		t.Fatalf("drafts = %v, want the reply growing", got)
	}
	if len(f.sends) != 1 || f.sends[0] != "Fixing the test." {
		t.Fatalf("want the finished reply posted once, sends = %v", f.sends)
	}
	if len(f.edits) != 0 {
		t.Errorf("drafting should need no edits, got %v", f.edits)
	}
}

// Telegram animates between updates that share a draft id, so one id per reply,
// and a new one per turn or the next answer morphs out of the last.
func TestStreamUsesOneDraftIDPerTurn(t *testing.T) {
	f := &fakeSender{}
	s := newStream(f, 1)
	now := time.Now()
	s.clock = func() time.Time { return now }

	s.Append(context.Background(), "first ")
	now = now.Add(draftEvery + time.Millisecond)
	s.Append(context.Background(), "turn")
	_ = s.Flush(context.Background())
	s.Reset()
	s.Append(context.Background(), "second turn")
	_ = s.Flush(context.Background())

	if len(f.drafts) != 3 {
		t.Fatalf("want 3 drafts, got %v", f.drafts)
	}
	if f.drafts[0].id != f.drafts[1].id {
		t.Errorf("one turn used two draft ids (%d, %d), so it will not animate",
			f.drafts[0].id, f.drafts[1].id)
	}
	if f.drafts[2].id == f.drafts[0].id {
		t.Errorf("the second turn reused draft id %d, so it grows out of the first",
			f.drafts[2].id)
	}
	if f.drafts[0].id == 0 {
		t.Error("draft id must be non-zero; Telegram rejects 0")
	}
}

// The draft endpoint is built for streaming, but a call per token is still
// pointless traffic.
func TestStreamThrottlesDrafts(t *testing.T) {
	f := &fakeSender{}
	s := newStream(f, 1)
	now := time.Now()
	s.clock = func() time.Time { return now }

	for i := 0; i < 20; i++ {
		s.Append(context.Background(), "word ") // clock never advances
	}
	if len(f.drafts) != 1 {
		t.Errorf("made %d drafts inside one window, want the first only", len(f.drafts))
	}
}

// A reply short enough to finish inside one throttle window has the same text at
// flush time as the draft already showed. Skipping it as "unchanged" would leave
// the turn with a preview that expires and nothing else.
func TestStreamPostsAShortReplyThatOnlyEverDrafted(t *testing.T) {
	f := &fakeSender{}
	s := newStream(f, 1)
	now := time.Now()
	s.clock = func() time.Time { return now }

	s.Append(context.Background(), "done")
	_ = s.Flush(context.Background())

	if len(f.sends) != 1 || f.sends[0] != "done" {
		t.Fatalf("the reply was never posted for real: sends = %v", f.sends)
	}
	_ = s.Flush(context.Background()) // a second flush has nothing new to say
	if len(f.sends) != 1 || len(f.edits) != 0 {
		t.Errorf("re-sent an unchanged reply: sends = %v, edits = %v", f.sends, f.edits)
	}
}

// --- falling back ---

// sendMessageDraft is a private-chat method. Rather than sniff the chat type,
// the first refusal turns drafting off and the reply carries on as edits.
func TestStreamFallsBackToEditsWhenDraftsAreRefused(t *testing.T) {
	f := &fakeSender{draftErr: errors.New("bad request: method unavailable")}
	s := newStream(f, 1)
	now := time.Now()
	s.clock = func() time.Time { return now }

	s.Append(context.Background(), "Fixing ")
	if s.drafting {
		t.Fatal("a refused draft must turn drafting off")
	}
	now = now.Add(editEvery + time.Millisecond)
	s.Append(context.Background(), "the test.")
	_ = s.Flush(context.Background())

	if len(f.sends) != 1 {
		t.Fatalf("want one posted message, got %v", f.sends)
	}
	if got := lastOf(f.edits, f.sends); got != "Fixing the test." {
		t.Errorf("final text = %q", got)
	}
}

// Drafting is a fact about the chat, not the turn. Re-learning it every turn
// would mean a wasted failing call every turn for the life of a group chat.
func TestStreamStaysFallenBackAcrossTurns(t *testing.T) {
	f := &fakeSender{draftErr: errors.New("nope")}
	s := newStream(f, 1)
	s.clock = time.Now

	s.Append(context.Background(), "first turn")
	_ = s.Flush(context.Background())
	s.Reset()
	if s.drafting {
		t.Fatal("Reset re-armed drafting on a chat that cannot take it")
	}
	s.Append(context.Background(), "second turn")
	_ = s.Flush(context.Background())

	if len(f.drafts) != 0 {
		t.Errorf("kept trying drafts: %v", f.drafts)
	}
}

// --- the edit path ---

// A reply arrives in fragments but should read as one message, so the first
// fragment posts and the rest edit.
func TestStreamPostsOnceThenEdits(t *testing.T) {
	f := &fakeSender{}
	s := noDrafts(newStream(f, 1))
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
	s := noDrafts(newStream(f, 1))
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
	if len(f.sends)+len(f.edits)+len(f.drafts) != 0 {
		t.Errorf("sent something for an empty reply: %v %v %v", f.sends, f.edits, f.drafts)
	}
	if s.Active() {
		t.Error("a stream with nothing in it should not count as active")
	}
}

// Telegram rejects an edit that changes nothing, which would otherwise happen
// every time a flush follows a push with no new text.
func TestStreamSkipsUnchangedEdits(t *testing.T) {
	f := &fakeSender{}
	s := noDrafts(newStream(f, 1))
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
	s := noDrafts(newStream(f, 1))
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

// Active is what tells the caller a complete assistant message still needs
// posting, so a stream that only ever drafted must already count as active or
// the reply is sent twice.
func TestStreamCountsADraftAsShown(t *testing.T) {
	f := &fakeSender{}
	s := newStream(f, 1)
	s.clock = time.Now

	s.Append(context.Background(), "partial")
	if !s.Active() {
		t.Fatal("a drafted reply does not read as shown, so it will be posted twice")
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
