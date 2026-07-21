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
