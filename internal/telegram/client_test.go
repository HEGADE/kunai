package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubAPI stands in for Telegram. It records what the client sent and replies
// with whatever the test wants, so none of this touches the network.
func stubAPI(t *testing.T, reply string, seen *map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			body, _ := io.ReadAll(r.Body)
			var got map[string]any
			_ = json.Unmarshal(body, &got)
			*seen = got
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, reply)
	}))
	old := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = old; srv.Close() })
	return srv
}

// Telegram reports its own refusals inside a 200 body, so a client that only
// checked the status code would read "chat not found" as success.
func TestClientTreatsOkFalseAsAnError(t *testing.T) {
	stubAPI(t, `{"ok":false,"error_code":403,"description":"bot was blocked by the user"}`, nil)

	_, err := NewClient("tok").Send(context.Background(), 1, "hi", nil)
	if err == nil {
		t.Fatal("want an error for ok:false")
	}
	var apiErr *APIError
	if !errorAs(err, &apiErr) {
		t.Fatalf("want an APIError, got %T: %v", err, err)
	}
	if apiErr.Code != 403 || !strings.Contains(apiErr.Description, "blocked") {
		t.Errorf("error lost its detail: %v", apiErr)
	}
}

func TestClientSendReturnsTheMessageID(t *testing.T) {
	var sent map[string]any
	stubAPI(t, `{"ok":true,"result":{"message_id":4242,"chat":{"id":7}}}`, &sent)

	id, err := NewClient("tok").Send(context.Background(), 7, "hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	if id != 4242 {
		t.Errorf("message id = %d, want 4242", id)
	}
	if sent["text"] != "hello" {
		t.Errorf("sent %v", sent)
	}
}

// The reply id is what a streaming answer edits, and buttons are what answer a
// permission ask, so both have to survive the round trip.
func TestClientSendsKeyboard(t *testing.T) {
	var sent map[string]any
	stubAPI(t, `{"ok":true,"result":{"message_id":1,"chat":{"id":1}}}`, &sent)

	kb := &InlineKeyboard{Rows: [][]InlineButton{{{Text: "Approve", Data: "ok:r1"}}}}
	if _, err := NewClient("tok").Send(context.Background(), 1, "ask", &SendOptions{Keyboard: kb}); err != nil {
		t.Fatal(err)
	}
	markup, ok := sent["reply_markup"].(map[string]any)
	if !ok {
		t.Fatalf("no reply_markup in %v", sent)
	}
	if _, ok := markup["inline_keyboard"]; !ok {
		t.Errorf("keyboard not sent: %v", markup)
	}
}

// getUpdates has to pass the offset, or a restart replays every old message and
// the bot re-runs commands you already ran.
func TestGetUpdatesSendsTheOffset(t *testing.T) {
	var sent map[string]any
	stubAPI(t, `{"ok":true,"result":[{"update_id":11,"message":{"message_id":1,"chat":{"id":5},"text":"hi"}}]}`, &sent)

	ups, err := NewClient("tok").GetUpdates(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ups) != 1 || ups[0].UpdateID != 11 || ups[0].Message.Text != "hi" {
		t.Fatalf("got %+v", ups)
	}
	if sent["offset"] != float64(10) {
		t.Errorf("offset = %v, want 10", sent["offset"])
	}
}

// Telegram rejects an over-long message outright, so a long reply has to be cut
// here or it is lost entirely.
func TestClampTextCutsOnARuneBoundary(t *testing.T) {
	long := strings.Repeat("é", maxMessageRunes*2)
	got := clampText(long)
	if r := []rune(got); len(r) > maxMessageRunes {
		t.Fatalf("got %d runes, want at most %d", len(r), maxMessageRunes)
	}
	if strings.ContainsRune(got, '�') {
		t.Error("cut mid-character")
	}
	if !strings.Contains(got, "truncated") {
		t.Error("a cut message should say it was cut")
	}
}

func TestClampTextLeavesShortTextAlone(t *testing.T) {
	if got := clampText("short"); got != "short" {
		t.Errorf("got %q", got)
	}
}

// errorAs is errors.As without importing errors into every test.
func errorAs(err error, target **APIError) bool {
	for err != nil {
		if e, ok := err.(*APIError); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
