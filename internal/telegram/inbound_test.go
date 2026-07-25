package telegram

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A photo arrives in several renditions, ascending by size. The largest is the one
// worth showing a model, and a photo has no Text at all -- what the sender typed is
// the Caption, which is why reading only Text made a screenshot vanish silently.
func TestInboundFiles_PhotoPicksLargest(t *testing.T) {
	m := &Message{
		Caption: "what is wrong here?",
		Photo: []PhotoSize{
			{FileID: "small", Width: 90, Height: 60},
			{FileID: "medium", Width: 320, Height: 213},
			{FileID: "large", Width: 1280, Height: 853},
		},
	}
	got := inboundFiles(m)
	if len(got) != 1 {
		t.Fatalf("want one file, got %d", len(got))
	}
	if got[0].id != "large" {
		t.Errorf("picked %q, want the largest rendition", got[0].id)
	}
	if got[0].mimeType != "image/jpeg" {
		t.Errorf("mime = %q, want image/jpeg", got[0].mimeType)
	}
}

// A document is taken as sent, keeping its name and type. An image sent "as a file"
// arrives this way and Telegram does not recompress it, so it holds detail a photo
// would have lost.
func TestInboundFiles_Document(t *testing.T) {
	got := inboundFiles(&Message{Document: &Document{FileID: "d1", FileName: "trace.log", MimeType: "text/plain"}})
	if len(got) != 1 || got[0].id != "d1" || got[0].name != "trace.log" || got[0].mimeType != "text/plain" {
		t.Fatalf("document not carried through: %+v", got)
	}
}

// A document with no filename still needs a name, since the name is what the agent
// sees when the file is copied into the project.
func TestInboundFiles_DocumentWithoutName(t *testing.T) {
	got := inboundFiles(&Message{Document: &Document{FileID: "d2"}})
	if len(got) != 1 || got[0].name == "" {
		t.Fatalf("want a fallback name, got %+v", got)
	}
}

// An ordinary text message carries no files, so it must keep going down the command
// path rather than being mistaken for an upload.
func TestInboundFiles_TextOnlyHasNone(t *testing.T) {
	if got := inboundFiles(&Message{Text: "/status"}); len(got) != 0 {
		t.Fatalf("text message should carry no files, got %+v", got)
	}
}

// Both at once (Telegram allows a photo and a document in separate messages, but a
// forwarded album can produce both fields) yields both, in a stable order.
func TestInboundFiles_PhotoAndDocument(t *testing.T) {
	got := inboundFiles(&Message{
		Photo:    []PhotoSize{{FileID: "p"}},
		Document: &Document{FileID: "d", FileName: "a.txt"},
	})
	if len(got) != 2 || got[0].id != "p" || got[1].id != "d" {
		t.Fatalf("want photo then document, got %+v", got)
	}
}

// The download half, against a stand-in Telegram: getFile resolves a path, then the
// bytes come from the /file/bot<token>/ route. Two steps because Telegram makes the
// path short-lived, so it cannot be cached.
func TestGetFileAndDownload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getFile"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"ok":true,"result":{"file_id":"f1","file_path":"photos/shot.jpg"}}`)
		case strings.Contains(r.URL.Path, "/file/bot"):
			if !strings.HasSuffix(r.URL.Path, "photos/shot.jpg") {
				t.Errorf("unexpected download path %q", r.URL.Path)
			}
			_, _ = w.Write([]byte("PNGDATA"))
		default:
			t.Errorf("unexpected call %q", r.URL.Path)
		}
	}))
	old := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = old; srv.Close() })

	c := NewClient("tok")
	f, err := c.GetFile(context.Background(), "f1")
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if f.FilePath != "photos/shot.jpg" {
		t.Fatalf("path = %q", f.FilePath)
	}
	data, err := c.DownloadFile(context.Background(), f.FilePath)
	if err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	if string(data) != "PNGDATA" {
		t.Errorf("data = %q, want PNGDATA", data)
	}
}

// getFile answering without a path is a failure, not an empty download: attempting
// the fetch anyway would hit /file/bot<token>/ and read the error page as image
// bytes.
func TestGetFileWithoutPathIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true,"result":{"file_id":"f1"}}`)
	}))
	old := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = old; srv.Close() })

	if _, err := NewClient("tok").GetFile(context.Background(), "f1"); err == nil {
		t.Fatal("expected an error when getFile returns no path")
	}
}

// A file past the cap is refused rather than silently truncated, which would hand
// the model a corrupt image.
func TestDownloadRefusesOversizeFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, maxInboundFile+1024))
	}))
	old := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = old; srv.Close() })

	_, err := NewClient("tok").DownloadFile(context.Background(), "big.bin")
	if err == nil {
		t.Fatal("an oversize file must be refused, not truncated")
	}
	if !strings.Contains(err.Error(), "larger than") {
		t.Errorf("error should say what happened, got %v", err)
	}
}
