package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hegade/kunai/internal/session"
)

func handoffPost(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleHandoff(rec, httptest.NewRequest("POST", "/api/handoff", strings.NewReader(body)))
	return rec
}

// The reported failure: /kunai inside a session's FIRST turn said "no
// conversation on this machine with that id" for a conversation plainly on the
// screen. The CLI had not written the transcript yet, and the endpoint demanded
// it at mint time -- although nothing needs the file until the link is OPENED,
// which is after the terminal exits.
func TestHandoffWorksBeforeTheTranscriptExists(t *testing.T) {
	dir := t.TempDir() // a real folder, so this reads as "same machine"
	s := &Server{cfg: Config{PublicURL: "https://box.ts.net:8443", DataDir: t.TempDir()}, mgr: session.NewManager()}

	rec := handoffPost(t, s, `{"session_id":"7c9e1a52-0000-4000-8000-abcdefabcdef","cwd":"`+dir+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("handoff refused a young session: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "/resume/7c9e1a52-0000-4000-8000-abcdefabcdef") {
		t.Errorf("no resume link: %s", body)
	}
	// The folder rides along, or the client cannot resume something Recent has
	// never seen.
	if !strings.Contains(body, "cwd=") {
		t.Errorf("the link does not carry the folder, so a young session cannot resume: %s", body)
	}
	if !strings.Contains(body, `"new":true`) {
		t.Errorf("a session with no transcript yet should be marked new: %s", body)
	}
}

// The one case where a missing transcript genuinely means something: the folder
// does not exist here, so the terminal is on another machine.
func TestHandoffFromAnotherMachineSaysSo(t *testing.T) {
	s := &Server{cfg: Config{PublicURL: "https://box.ts.net:8443", DataDir: t.TempDir()}, mgr: session.NewManager()}
	rec := handoffPost(t, s, `{"session_id":"7c9e1a52-0000-4000-8000-abcdefabcdef","cwd":"/not/here/at/all"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404 for a folder this machine cannot see", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "same machine") {
		t.Errorf("the error does not explain the actual limitation: %s", rec.Body.String())
	}
}

// A path-shaped id is refused: it arrives from a shell and is used to build a
// filename.
func TestHandoffRefusesAPathShapedID(t *testing.T) {
	s := &Server{cfg: Config{PublicURL: "https://box.ts.net:8443", DataDir: t.TempDir()}, mgr: session.NewManager()}
	// Written as JSON so a backslash survives the encoding: "a\\b" on the wire is
	// the two-character id a\b, which an earlier version of this test smuggled
	// past as a JSON backspace escape and wrongly declared accepted.
	for _, id := range []string{"../../etc/passwd", "a/b", `a\\b`, "x.jsonl", ""} {
		rec := handoffPost(t, s, `{"session_id":"`+id+`"}`)
		if rec.Code == http.StatusOK {
			t.Errorf("accepted %q as a session id", id)
		}
	}
}

// A transcript that IS there supplies the title, so the command names what it is
// handing over rather than printing a bare uuid.
func TestHandoffNamesAKnownConversation(t *testing.T) {
	cfg := t.TempDir()
	proj := filepath.Join(cfg, "projects", "-tmp-x")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "7c9e1a52-0000-4000-8000-abcdefabcdef"
	line := `{"type":"user","cwd":"/tmp/x","sessionId":"` + id + `","message":{"role":"user","content":"refactor the parser"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: Config{PublicURL: "https://box.ts.net:8443", DataDir: t.TempDir()}, mgr: session.NewManager()}
	s.setCLIs([]CLIProfile{{Name: "Claude", Bin: "claude", Dir: cfg}})

	rec := handoffPost(t, s, `{"session_id":"`+id+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "refactor the parser") {
		t.Errorf("the reply does not name the conversation: %s", rec.Body.String())
	}
}
