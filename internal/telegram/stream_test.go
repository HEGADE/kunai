package telegram

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A reply has to arrive several different ways and all of them have to be
// right: as a rich message that keeps the model's Markdown, as an animated
// draft, and as an edited plain message where neither is available. The
// fallbacks are not rare paths, they are every group chat and every older
// Telegram deployment.

// draftCall is one streamed fragment, with the id that decides whether Telegram
// animates it into the previous one, and which endpoint carried it.
type draftCall struct {
	id   int64
	text string
	rich bool
}

// fakeSender records what a stream would have sent.
type fakeSender struct {
	sends     []string // plain sendMessage
	richSends []string // sendRichMessage
	edits     []string
	drafts    []draftCall
	nextID    int64

	draftErr     error // plain sendMessageDraft fails
	sendErrOnce  error // first sendMessage fails with this, then it works
	richDraftErr error // sendRichMessageDraft fails
	richSendErr  error // sendRichMessage fails
}

func (f *fakeSender) Send(_ context.Context, _ int64, text string, _ *SendOptions) (int64, error) {
	if f.sendErrOnce != nil {
		err := f.sendErrOnce
		f.sendErrOnce = nil
		return 0, err
	}
	f.sends = append(f.sends, text)
	f.nextID++
	return f.nextID, nil
}

func (f *fakeSender) SendRich(_ context.Context, _ int64, md string, _ *SendOptions) (int64, error) {
	if f.richSendErr != nil {
		return 0, f.richSendErr
	}
	f.richSends = append(f.richSends, md)
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

func (f *fakeSender) DraftRich(_ context.Context, _, id int64, md string) error {
	if f.richDraftErr != nil {
		return f.richDraftErr
	}
	f.drafts = append(f.drafts, draftCall{id: id, text: md, rich: true})
	return nil
}

// noRich puts a stream on the plain-text path, which is what a chat that cannot
// take rich messages gets.
func noRich(s *stream) *stream { s.rich = false; return s }

// noDrafts puts a stream on the edit path, which is what a group chat gets.
func noDrafts(s *stream) *stream { s.drafting = false; return s }

// content returns the drafts that carried text. Nothing should ever send an
// empty one, so this doubles as a guard: see TestNoEmptyDraftIsEverSent.
func (f *fakeSender) content() []draftCall {
	var out []draftCall
	for _, d := range f.drafts {
		if d.text != "" {
			out = append(out, d)
		}
	}
	return out
}

func (f *fakeSender) draftTexts() []string {
	out := make([]string, 0, len(f.drafts))
	for _, d := range f.content() {
		out = append(out, d.text)
	}
	return out
}

// --- formatting ---

// The reported bug: the model writes Markdown and it arrived as literal
// asterisks and backticks, because the reply was posted as plain text.
func TestReplyIsSentAsRichMarkdown(t *testing.T) {
	const md = "- **Machine:** `linux-1`\n\n```go\nfmt.Println()\n```"
	f := &fakeSender{}
	s := newStream(f, 1)
	s.clock = time.Now

	s.Append(context.Background(), md)
	_ = s.Flush(context.Background())

	if len(f.richSends) != 1 || f.richSends[0] != md {
		t.Fatalf("the reply was not sent as a rich message: rich=%v plain=%v", f.richSends, f.sends)
	}
	if len(f.sends) != 0 {
		t.Errorf("also sent it as plain text: %v", f.sends)
	}
}

// The streamed preview has to be rich too, or the answer reformats itself the
// moment the turn ends.
func TestDraftsAreRichToo(t *testing.T) {
	f := &fakeSender{}
	s := newStream(f, 1)
	now := time.Now()
	s.clock = func() time.Time { return now }

	s.Append(context.Background(), "**bold** ")
	now = now.Add(draftEvery + time.Millisecond)
	s.Append(context.Background(), "and more")
	_ = s.Flush(context.Background())

	if len(f.content()) != 2 {
		t.Fatalf("want 2 drafts, got %v", f.drafts)
	}
	for i, d := range f.content() {
		if !d.rich {
			t.Errorf("draft %d went out as plain text", i)
		}
	}
}

// A chat that will not take rich messages must still get the reply, in the same
// call. Flush runs once per turn, so a refusal that was not retried would lose
// the answer outright.
func TestRichRefusalOnTheFinalSendStillDeliversTheReply(t *testing.T) {
	f := &fakeSender{richSendErr: refusal("rich messages are not available in this chat")}
	s := noDrafts(newStream(f, 1))
	s.clock = time.Now

	s.Append(context.Background(), "the answer")
	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("flush failed instead of falling back: %v", err)
	}

	if len(f.sends) != 1 || f.sends[0] != "the answer" {
		t.Fatalf("the reply was lost: plain=%v rich=%v", f.sends, f.richSends)
	}
	if s.rich {
		t.Error("a refused rich message left rich on, so every turn pays for it again")
	}
}

// A refused rich draft gives up rich, not drafting: the next fragment should
// still stream, just as plain text.
func TestRichDraftRefusalKeepsDrafting(t *testing.T) {
	f := &fakeSender{richDraftErr: refusal("no rich here")}
	s := newStream(f, 1)
	now := time.Now()
	s.clock = func() time.Time { return now }

	s.Append(context.Background(), "first ")
	if s.rich {
		t.Fatal("a refused rich draft must turn rich off")
	}
	if !s.drafting {
		t.Fatal("a refused rich draft must not turn drafting off as well")
	}
	now = now.Add(draftEvery + time.Millisecond)
	s.Append(context.Background(), "second")

	if len(f.content()) != 1 || f.content()[0].rich {
		t.Fatalf("want one plain draft after the refusal, got %v", f.drafts)
	}
}

// Both capabilities are facts about the chat, not the turn. A chat that refuses
// everything should walk down the ladder once and stay at the bottom, rather
// than paying for the same two refusals on every turn it ever has.
func TestCapabilitiesStayOffAcrossTurns(t *testing.T) {
	f := &fakeSender{richDraftErr: refusal("no"), richSendErr: refusal("no"), draftErr: refusal("no")}
	s := newStream(f, 1)
	now := time.Now()
	s.clock = func() time.Time { return now }

	// Each fragment costs one rung: the rich draft, then the plain draft.
	s.Append(context.Background(), "first ")
	now = now.Add(draftEvery + time.Millisecond)
	s.Append(context.Background(), "turn")
	if s.rich || s.drafting {
		t.Fatalf("expected both off after two refusals, rich=%v drafting=%v", s.rich, s.drafting)
	}
	_ = s.Flush(context.Background())
	s.Reset()

	if s.rich {
		t.Error("Reset re-armed rich on a chat that refused it")
	}
	if s.drafting {
		t.Error("Reset re-armed drafting on a chat that refused it")
	}

	// A second turn must not re-try either endpoint.
	before := len(f.drafts)
	s.Append(context.Background(), "second turn")
	_ = s.Flush(context.Background())
	if len(f.drafts) != before {
		t.Errorf("tried drafting again on a chat that cannot: %v", f.drafts)
	}
	if len(f.richSends) != 0 {
		t.Errorf("tried a rich send again: %v", f.richSends)
	}
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
	if len(f.richSends) != 1 || f.richSends[0] != "Fixing the test." {
		t.Fatalf("want the finished reply posted once, got %v", f.richSends)
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

	c := f.content()
	if len(c) != 3 {
		t.Fatalf("want 3 drafts, got %v", f.drafts)
	}
	if c[0].id != c[1].id {
		t.Errorf("one turn used two draft ids (%d, %d), so it will not animate",
			c[0].id, c[1].id)
	}
	if c[2].id == c[0].id {
		t.Errorf("the second turn reused draft id %d, so it grows out of the first",
			c[2].id)
	}
	if c[0].id == 0 {
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
	if len(f.content()) != 1 {
		t.Errorf("made %d drafts inside one window, want the first only", len(f.content()))
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

	if len(f.richSends) != 1 || f.richSends[0] != "done" {
		t.Fatalf("the reply was never posted for real: %v", f.richSends)
	}
	_ = s.Flush(context.Background()) // a second flush has nothing new to say
	if len(f.richSends) != 1 || len(f.sends) != 0 || len(f.edits) != 0 {
		t.Errorf("re-sent an unchanged reply: rich=%v plain=%v edits=%v",
			f.richSends, f.sends, f.edits)
	}
}

// --- falling back to edits ---

// sendMessageDraft is a private-chat method. Rather than sniff the chat type,
// the first refusal turns drafting off and the reply carries on as edits.
func TestStreamFallsBackToEditsWhenDraftsAreRefused(t *testing.T) {
	f := &fakeSender{draftErr: refusal("method is unavailable")}
	s := noRich(newStream(f, 1))
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

// --- the edit path ---

// A reply arrives in fragments but should read as one message, so the first
// fragment posts and the rest edit.
func TestStreamPostsOnceThenEdits(t *testing.T) {
	f := &fakeSender{}
	s := noRich(noDrafts(newStream(f, 1)))
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
	s := noRich(noDrafts(newStream(f, 1)))
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
	if len(f.sends)+len(f.richSends)+len(f.edits)+len(f.drafts) != 0 {
		t.Errorf("sent something for an empty reply")
	}
	if s.Active() {
		t.Error("a stream with nothing in it should not count as active")
	}
}

// Telegram rejects an edit that changes nothing, which would otherwise happen
// every time a flush follows a push with no new text.
func TestStreamSkipsUnchangedEdits(t *testing.T) {
	f := &fakeSender{}
	s := noRich(noDrafts(newStream(f, 1)))
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
	s := noRich(noDrafts(newStream(f, 1)))
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

// refusal is a flat rejection from Telegram: the only thing that may cost a
// chat a capability for good.
func refusal(desc string) error {
	return &APIError{Method: "sendRichMessage", Code: 400, Description: desc}
}

// --- degrading only for the right reasons ---

// The bug behind "streaming got weird and slow": every error downgraded the
// chat, so on a flaky route to Telegram a timeout dropped rich, the next one
// dropped drafting, and the chat was stuck on 1500ms edits for good. A timeout
// says nothing about what the chat supports.
func TestTimeoutDoesNotCostACapability(t *testing.T) {
	f := &fakeSender{richDraftErr: context.DeadlineExceeded}
	s := newStream(f, 1)
	s.clock = time.Now

	s.Append(context.Background(), "hello")

	if !s.rich {
		t.Error("a timeout turned rich off, so one bad moment degrades the chat forever")
	}
	if !s.drafting {
		t.Error("a timeout turned drafting off")
	}
}

// A 429 means "slower", not "never". Telegram sends it the same way it sends a
// refusal, so it has to be told apart explicitly.
func TestRateLimitDoesNotCostACapability(t *testing.T) {
	f := &fakeSender{richDraftErr: &APIError{Code: 429, Description: "Too Many Requests", RetryAfter: 3}}
	s := newStream(f, 1)
	s.clock = time.Now

	s.Append(context.Background(), "hello")

	if !s.rich {
		t.Error("a rate limit turned rich off; pushing too fast is not a capability problem")
	}
}

func TestUnsupportedTellsRefusalsFromHiccups(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"flat refusal", &APIError{Code: 400, Description: "method not found"}, true},
		{"not found", &APIError{Code: 404}, true},
		{"rate limit", &APIError{Code: 429, RetryAfter: 5}, false},
		{"rate limit without a code", &APIError{RetryAfter: 5}, false},
		{"server wobble", &APIError{Code: 500}, false},
		{"timeout", context.DeadlineExceeded, false},
		{"plain transport error", errors.New("connection reset"), false},
	}
	for _, c := range cases {
		if got := unsupported(c.err); got != c.want {
			t.Errorf("%s: unsupported = %v, want %v", c.name, got, c.want)
		}
	}
}

// --- keeping the preview alive ---

// The other half of the report: a long answer showed nothing until it finished.
// A draft expires in about thirty seconds and the model can think for longer
// than that without writing a word, so the preview has to be re-asserted.
func TestRefreshKeepsTheDraftAlive(t *testing.T) {
	f := &fakeSender{}
	s := newStream(f, 1)
	s.clock = time.Now

	s.Append(context.Background(), "half an answer")
	before := len(f.drafts)
	s.Refresh(context.Background())

	if len(f.drafts) != before+1 {
		t.Fatalf("refresh did not re-assert the draft: %v", f.drafts)
	}
	if got := f.drafts[len(f.drafts)-1].text; got != "half an answer" {
		t.Errorf("refreshed with %q, want the text so far", got)
	}
}

// Once the reply is a real message the draft is over, and refreshing would post
// a stray preview after the answer had already landed.
func TestRefreshStopsOnceTheReplyIsPosted(t *testing.T) {
	f := &fakeSender{}
	s := newStream(f, 1)
	s.clock = time.Now

	s.Append(context.Background(), "done")
	_ = s.Flush(context.Background())
	before := len(f.drafts)
	s.Refresh(context.Background())

	if len(f.drafts) != before {
		t.Errorf("refreshed a draft after the message was posted: %v", f.drafts)
	}
}

// --- honouring retry_after ---

// throttle is Telegram asking us to wait, which is not a refusal.
func throttle(seconds int) error {
	return &APIError{Method: "sendRichMessageDraft", Code: 429,
		Description: "Too Many Requests: retry after", RetryAfter: seconds}
}

// The bug other bots filed against themselves: retry_after was parsed and then
// ignored, so a throttled push became a throttled turn. Telegram's edge caches
// the penalty, so retrying early makes the wait longer, not shorter.
func TestRateLimitStopsFurtherPushes(t *testing.T) {
	f := &fakeSender{richDraftErr: throttle(5)}
	s := newStream(f, 1)
	now := time.Now()
	s.clock = func() time.Time { return now }

	s.Append(context.Background(), "one ") // trips the 429
	f.richDraftErr = nil                   // the endpoint would work again now

	// Well past the push interval, but inside the penalty window.
	now = now.Add(2 * time.Second)
	s.Append(context.Background(), "two ")
	if len(f.drafts) != 0 {
		t.Fatalf("pushed during the cool-off: %v", f.drafts)
	}

	// Once it lapses, streaming resumes on its own.
	now = now.Add(4 * time.Second)
	s.Append(context.Background(), "three")
	if len(f.drafts) != 1 {
		t.Fatalf("did not resume after the cool-off: %v", f.drafts)
	}
}

// The keep-alive is the least urgent request there is, so it must not spend one
// while Telegram has asked for quiet.
func TestRefreshRespectsTheCoolOff(t *testing.T) {
	f := &fakeSender{richDraftErr: throttle(5)}
	s := newStream(f, 1)
	now := time.Now()
	s.clock = func() time.Time { return now }

	s.Append(context.Background(), "text")
	f.richDraftErr = nil
	now = now.Add(time.Second)
	s.Refresh(context.Background())

	if len(f.drafts) != 0 {
		t.Errorf("keep-alive ignored the cool-off: %v", f.drafts)
	}
}

// A 429 is not a capability problem, so it must not cost the chat its
// formatting or its streaming on top of the wait.
func TestRateLimitKeepsCapabilities(t *testing.T) {
	f := &fakeSender{richDraftErr: throttle(2)}
	s := newStream(f, 1)
	s.clock = time.Now

	s.Append(context.Background(), "hello")
	if !s.rich || !s.drafting {
		t.Errorf("a 429 cost a capability: rich=%v drafting=%v", s.rich, s.drafting)
	}
}

// The finished reply is the one thing that must not be dropped, so a throttle on
// the final send is waited out rather than surrendered to.
func TestFinalSendWaitsOutAThrottle(t *testing.T) {
	f := &fakeSender{}
	s := noRich(noDrafts(newStream(f, 1)))
	s.clock = time.Now
	f.sendErrOnce = throttle(1)

	// The first push is the one that posts, so the throttle lands there; the
	// timer has to cover it.
	start := time.Now()
	s.Append(context.Background(), "the answer")
	_ = s.Flush(context.Background())

	if time.Since(start) < 900*time.Millisecond {
		t.Error("retried immediately, which resets Telegram's penalty window")
	}
	if len(f.sends) != 1 || f.sends[0] != "the answer" {
		t.Fatalf("the reply was lost: %v", f.sends)
	}
}

// An empty draft is never sent, and this is the test that would have caught the
// worst of it. A rich draft rejects empty text with a 400, which giveUp reads as
// "this chat cannot do rich messages", so a placeholder cost every chat its
// formatting seconds into the first turn.
func TestNoEmptyDraftIsEverSent(t *testing.T) {
	f := &fakeSender{}
	s := newStream(f, 1)
	s.clock = time.Now

	s.Refresh(context.Background()) // before a single token has arrived
	s.Append(context.Background(), "text")
	_ = s.Flush(context.Background())

	for _, d := range f.drafts {
		if d.text == "" {
			t.Fatalf("sent an empty draft: %v", f.drafts)
		}
	}
	if !s.rich {
		t.Error("rich was lost, which is what an empty rich draft causes")
	}
}

// The keep-alive before any text has arrived must do nothing at all, rather than
// reserve space in the chat for a draft with nothing in it.
func TestRefreshDoesNothingBeforeAnyText(t *testing.T) {
	f := &fakeSender{}
	s := newStream(f, 1)
	s.clock = time.Now

	s.Refresh(context.Background())

	if len(f.drafts) != 0 {
		t.Fatalf("planted a draft with no content: %v", f.drafts)
	}
	if s.Active() {
		t.Error("a stream that has shown nothing must not read as active")
	}
}
