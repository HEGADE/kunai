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

// The wire shape of sendMessageDraft, asserted rather than assumed: the method
// name and the three fields Telegram requires, with draft_id non-zero.
func TestDraftWireShape(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
	}))
	defer srv.Close()
	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	if err := NewClient("tok").Draft(context.Background(), 42, 7, "half a sentence"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(gotPath, "/sendMessageDraft") {
		t.Fatalf("called %q", gotPath)
	}
	for k, want := range map[string]any{"chat_id": float64(42), "draft_id": float64(7), "text": "half a sentence"} {
		if gotBody[k] != want {
			t.Errorf("%s = %v, want %v", k, gotBody[k], want)
		}
	}
}

// Telegram reports refusals inside a 200 body, and a refusal is what triggers
// the fallback, so it must surface as an error rather than a silent success.
func TestDraftRefusalIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: chat is not a private chat"}`)
	}))
	defer srv.Close()
	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	err := NewClient("tok").Draft(context.Background(), 42, 7, "hi")
	if err == nil {
		t.Fatal("a refused draft must error, or the stream never falls back")
	}
	if !strings.Contains(err.Error(), "private chat") {
		t.Errorf("error lost its reason: %v", err)
	}
}

// The wire shape of the rich-message methods. Exactly one of html or markdown
// may be set, and we always send markdown, because that is what the model wrote.
func TestRichWireShape(t *testing.T) {
	calls := map[string]map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var got map[string]any
		_ = json.Unmarshal(b, &got)
		parts := strings.Split(r.URL.Path, "/")
		calls[parts[len(parts)-1]] = got
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":11,"chat":{"id":1}}}`)
	}))
	defer srv.Close()
	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	c := NewClient("tok")
	const md = "- **Machine:** `linux-1`"
	if _, err := c.SendRich(context.Background(), 42, md, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.DraftRich(context.Background(), 42, 7, md); err != nil {
		t.Fatal(err)
	}

	for _, method := range []string{"sendRichMessage", "sendRichMessageDraft"} {
		body, ok := calls[method]
		if !ok {
			t.Fatalf("%s was never called", method)
		}
		rich, ok := body["rich_message"].(map[string]any)
		if !ok {
			t.Fatalf("%s sent no rich_message object: %v", method, body)
		}
		if rich["markdown"] != md {
			t.Errorf("%s markdown = %v, want the model's text verbatim", method, rich["markdown"])
		}
		if _, has := rich["html"]; has {
			t.Errorf("%s set html as well as markdown; exactly one is allowed", method)
		}
	}
	if calls["sendRichMessageDraft"]["draft_id"] != float64(7) {
		t.Errorf("draft_id = %v, want 7", calls["sendRichMessageDraft"]["draft_id"])
	}
}

// A rich message allows far more text than a plain one, so a long answer that
// used to be truncated should now arrive whole.
func TestRichClampUsesTheLargerLimit(t *testing.T) {
	long := strings.Repeat("x", maxMessageRunes+500)
	if got := clampRich(long); len([]rune(got)) != len([]rune(long)) {
		t.Errorf("clamped a message that fits the rich limit: %d -> %d",
			len([]rune(long)), len([]rune(got)))
	}
	huge := strings.Repeat("y", maxRichRunes+10)
	if got := clampRich(huge); len([]rune(got)) > maxRichRunes {
		t.Errorf("rich clamp let %d runes through, over the %d limit",
			len([]rune(got)), maxRichRunes)
	}
}
