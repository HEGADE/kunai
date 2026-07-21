package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hegade/kunai/internal/session"
)

// The scenario these are about: you chat in Telegram, then close the session in
// the kunai app. The conversation is still on disk. Before, the next message got
// "No session yet" and the only way back was to start a fresh one, throwing the
// conversation away. Every path out of a session now has to hand back the way in.

// --- fakes ---

// sentMsg is one outgoing Bot API call, as the test wants to read it.
type sentMsg struct {
	Method   string
	Text     string
	Keyboard *InlineKeyboard
}

// telegramStub stands in for the Bot API, recording everything sent.
type telegramStub struct {
	mu   sync.Mutex
	sent []sentMsg
}

func newTelegramStub(t *testing.T) *telegramStub {
	t.Helper()
	stub := &telegramStub{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var got struct {
			Text     string          `json:"text"`
			Keyboard *InlineKeyboard `json:"reply_markup"`
		}
		_ = json.Unmarshal(body, &got)
		parts := strings.Split(r.URL.Path, "/")
		stub.mu.Lock()
		stub.sent = append(stub.sent, sentMsg{
			Method: parts[len(parts)-1], Text: got.Text, Keyboard: got.Keyboard,
		})
		stub.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":1,"chat":{"id":1}}}`)
	}))
	old := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = old; srv.Close() })
	return stub
}

// messages returns just the chat messages, which is what the human sees.
func (s *telegramStub) messages() []sentMsg {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []sentMsg
	for _, m := range s.sent {
		if m.Method == "sendMessage" {
			out = append(out, m)
		}
	}
	return out
}

func (s *telegramStub) last(t *testing.T) sentMsg {
	t.Helper()
	msgs := s.messages()
	if len(msgs) == 0 {
		t.Fatal("nothing was sent")
	}
	return msgs[len(msgs)-1]
}

// fakeSessions records what the bot asked of the session layer. It never returns
// a live session: these tests are about what the bot does when there is not one,
// which is exactly the case that was broken.
type fakeSessions struct {
	mu      sync.Mutex
	started []string
	resumed []string
	closed  []string
	past    []Past
}

func (f *fakeSessions) Start(_ context.Context, cwd string) (*session.Session, error) {
	f.mu.Lock()
	f.started = append(f.started, cwd)
	f.mu.Unlock()
	return nil, errNotInTest
}

func (f *fakeSessions) Resume(_ context.Context, id string) (*session.Session, error) {
	f.mu.Lock()
	f.resumed = append(f.resumed, id)
	f.mu.Unlock()
	return nil, errNotInTest
}

func (f *fakeSessions) Recent(int) []Past                   { return f.past }
func (f *fakeSessions) Get(string) (*session.Session, bool) { return nil, false }
func (f *fakeSessions) List() []session.Meta                { return nil }
func (f *fakeSessions) Close(id string)                     { f.mu.Lock(); f.closed = append(f.closed, id); f.mu.Unlock() }

var errNotInTest = errorString("spawning a real claude is out of scope here")

type errorString string

func (e errorString) Error() string { return string(e) }

const testUser = int64(7)
const testChat = int64(99)

// newTestBot wires a bot over a stub API, with one approved person.
func newTestBot(t *testing.T, mgr Sessions) (*Bot, *telegramStub) {
	t.Helper()
	stub := newTelegramStub(t)
	st := LoadStore("", "tok", []int64{testUser})
	return New(st, mgr), stub
}

func msg(text string) Update {
	return Update{Message: &Message{
		MessageID: 1,
		Chat:      Chat{ID: testChat},
		From:      &User{ID: testUser, FirstName: "Ninja"},
		Text:      text,
	}}
}

// --- the reported bug ---

// Closing the session in the kunai app used to strand the chat: the next message
// said "No session yet", with no mention of the conversation still on disk.
func TestPromptAfterSessionClosedOffersItBack(t *testing.T) {
	const id = "0f3c9a4e-1b2d-4c8f-9a7e-5d6b8c1f2a30"
	mgr := &fakeSessions{}
	b, stub := newTestBot(t, mgr)
	b.st.bind(testChat, id) // the chat was driving it; the session is now gone

	b.handle(context.Background(), msg("carry on then"))

	got := stub.last(t)
	if !strings.Contains(got.Text, "/resume "+id) {
		t.Fatalf("no way back was offered: %q", got.Text)
	}
	if got.Keyboard == nil {
		t.Error("want a one-tap resume button alongside the command")
	}
	if strings.Contains(got.Text, "No session yet") {
		t.Error("told to start a fresh session, which would abandon the conversation")
	}
}

// The binding is the only record of which conversation this chat was having, so
// a dead session must not clear it. Ask twice, get offered it twice.
func TestClosedSessionStaysOfferableAcrossMessages(t *testing.T) {
	const id = "abc-123"
	b, stub := newTestBot(t, &fakeSessions{})
	b.st.bind(testChat, id)

	b.handle(context.Background(), msg("hello"))
	b.handle(context.Background(), msg("still there?"))

	msgs := stub.messages()
	if len(msgs) != 2 {
		t.Fatalf("want 2 replies, got %d", len(msgs))
	}
	for i, m := range msgs {
		if !strings.Contains(m.Text, "/resume "+id) {
			t.Errorf("reply %d forgot the session: %q", i, m.Text)
		}
	}
}

// A chat that never had a session is a different situation and gets a different
// answer: there is nothing to bring back.
func TestPromptWithNoSessionEverPointsAtNew(t *testing.T) {
	b, stub := newTestBot(t, &fakeSessions{})

	b.handle(context.Background(), msg("hello"))

	got := stub.last(t)
	if !strings.Contains(got.Text, "/new") {
		t.Fatalf("want a pointer to /new, got %q", got.Text)
	}
	if strings.Contains(got.Text, "/resume "+" ") {
		t.Errorf("offered to resume nothing: %q", got.Text)
	}
}

// --- /end ---

func TestEndClosesButOffersTheSessionBack(t *testing.T) {
	const id = "sess-1"
	mgr := &fakeSessions{}
	b, stub := newTestBot(t, mgr)
	b.st.bind(testChat, id)

	b.handle(context.Background(), msg("/end"))

	if len(mgr.closed) != 1 || mgr.closed[0] != id {
		t.Fatalf("want the session closed, closed = %v", mgr.closed)
	}
	got := stub.last(t)
	if !strings.Contains(got.Text, "/resume "+id) {
		t.Errorf("closing hid the way back: %q", got.Text)
	}
	if b.st.boundTo(testChat) != id {
		t.Error("closing dropped the binding, so the next message cannot offer it back")
	}
}

// --- /new ---

// /new goes through the server's adapter now, not the raw session manager, which
// is what gets a chat-born session its notifications and its account.
func TestNewStartsInTheGivenDirectory(t *testing.T) {
	mgr := &fakeSessions{}
	b, _ := newTestBot(t, mgr)

	b.handle(context.Background(), msg("/new /home/ninja/coding/kunai"))

	if len(mgr.started) != 1 || mgr.started[0] != "/home/ninja/coding/kunai" {
		t.Fatalf("want a start in the given directory, got %v", mgr.started)
	}
}

func TestNewWithoutADirectoryAsksForOne(t *testing.T) {
	mgr := &fakeSessions{}
	b, stub := newTestBot(t, mgr)

	b.handle(context.Background(), msg("/new"))

	if len(mgr.started) != 0 {
		t.Fatalf("started a session with no directory: %v", mgr.started)
	}
	if got := stub.last(t); !strings.Contains(got.Text, "/new /path") {
		t.Errorf("want an example, got %q", got.Text)
	}
}

// --- /resume ---

func TestResumeCommandResumesThatSession(t *testing.T) {
	mgr := &fakeSessions{}
	b, _ := newTestBot(t, mgr)

	b.handle(context.Background(), msg("/resume sess-42"))

	if len(mgr.resumed) != 1 || mgr.resumed[0] != "sess-42" {
		t.Fatalf("want a resume of sess-42, got %v", mgr.resumed)
	}
}

// A bare /resume must not be an error: nobody remembers a uuid on a phone.
func TestBareResumeListsWhatThereIs(t *testing.T) {
	mgr := &fakeSessions{past: []Past{{ID: "a", Cwd: "/srv/app", Title: "Nightly"}}}
	b, stub := newTestBot(t, mgr)

	b.handle(context.Background(), msg("/resume"))

	got := stub.last(t)
	if !strings.Contains(got.Text, "Nightly") || !strings.Contains(got.Text, "/resume a") {
		t.Fatalf("list did not offer the session: %q", got.Text)
	}
	if got.Keyboard == nil {
		t.Error("want a button per listed session")
	}
	if len(mgr.resumed) != 0 {
		t.Errorf("a bare /resume must not resume anything, resumed = %v", mgr.resumed)
	}
}

func TestResumeFailureIsReportedNotSwallowed(t *testing.T) {
	b, stub := newTestBot(t, &fakeSessions{})

	b.handle(context.Background(), msg("/resume gone"))

	if got := stub.last(t); !strings.Contains(got.Text, "Could not resume") {
		t.Fatalf("a failed resume said nothing useful: %q", got.Text)
	}
}

// --- buttons ---

// The resume button has to keep working on a message from days ago, which is the
// whole reason it carries the id rather than relying on the chat's binding.
func TestResumeButtonResumesTheSessionItCarries(t *testing.T) {
	mgr := &fakeSessions{}
	b, _ := newTestBot(t, mgr)
	b.st.bind(testChat, "some-newer-session")

	b.handle(context.Background(), Update{Callback: &CallbackQuery{
		ID:      "cb1",
		From:    &User{ID: testUser},
		Message: &Message{MessageID: 5, Chat: Chat{ID: testChat}},
		Data:    callbackData(CallbackResume, "old-session"),
	}})

	if len(mgr.resumed) != 1 || mgr.resumed[0] != "old-session" {
		t.Fatalf("button resumed the wrong thing: %v", mgr.resumed)
	}
}

// A button from an older build must be answered, or Telegram spins it forever,
// and must not be mistaken for an action we do understand.
func TestUnknownButtonIsAnsweredAndIgnored(t *testing.T) {
	mgr := &fakeSessions{}
	b, stub := newTestBot(t, mgr)

	b.handle(context.Background(), Update{Callback: &CallbackQuery{
		ID:      "cb1",
		From:    &User{ID: testUser},
		Message: &Message{MessageID: 5, Chat: Chat{ID: testChat}},
		Data:    "zz:whatever",
	}})

	if len(mgr.resumed) != 0 {
		t.Errorf("unknown button triggered a resume: %v", mgr.resumed)
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	var answered bool
	for _, m := range stub.sent {
		if m.Method == "answerCallbackQuery" {
			answered = true
		}
	}
	if !answered {
		t.Error("the button was never acknowledged, so it spins on the phone")
	}
}

// A stranger tapping a resume button must not get a session, and must not be
// able to enumerate ids by trying.
func TestButtonFromAStrangerIsRefused(t *testing.T) {
	mgr := &fakeSessions{}
	b, _ := newTestBot(t, mgr)

	b.handle(context.Background(), Update{Callback: &CallbackQuery{
		ID:      "cb1",
		From:    &User{ID: 12345},
		Message: &Message{MessageID: 5, Chat: Chat{ID: testChat}},
		Data:    callbackData(CallbackResume, "sess-1"),
	}})

	if len(mgr.resumed) != 0 {
		t.Fatalf("a stranger resumed a session: %v", mgr.resumed)
	}
}
