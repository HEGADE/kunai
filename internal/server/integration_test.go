package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/hegade/kunai/internal/server"
	"github.com/hegade/kunai/internal/session"
)

// TestEndToEnd drives the full REST + WS stack against a real `claude` process.
// It is opt-in (needs the CLI + network) — run with:
//
//	KUNAI_E2E=1 go test ./internal/server/ -run TestEndToEnd -v
func TestEndToEnd(t *testing.T) {
	if os.Getenv("KUNAI_E2E") != "1" {
		t.Skip("set KUNAI_E2E=1 to run the live-claude end-to-end test")
	}
	cwd := t.TempDir()
	_ = os.WriteFile(cwd+"/README.md", []byte("# scratch\n"), 0o644)

	mgr := session.NewManager()
	defer mgr.CloseAll()
	ts := httptest.NewServer(server.New(server.Config{}, mgr).Handler())
	defer ts.Close()

	// Create a session over REST.
	id := createSession(t, ts.URL, cwd)

	// Attach the WS and drive one tool-using turn.
	wsURL := strings.Replace(ts.URL, "http", "ws", 1) + "/ws/app/" + id + "?since=0"
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	defer c.CloseNow()
	c.SetReadLimit(16 << 20)

	// First frame is hello.
	hello := readEvent(t, ctx, c)
	if hello.T != session.EvHello || hello.ID != id {
		t.Fatalf("expected hello for %s, got %+v", id, hello)
	}

	// Send a prompt that requires the Write tool (a real permission gate).
	send(t, ctx, c, session.Command{T: session.CmdPrompt,
		Text: "Create a file notes.txt containing the word PING using the Write tool, then say done."})

	var gotDelta, gotPermission, gotResult bool
	deadline := time.After(80 * time.Second)
	for !gotResult {
		select {
		case <-deadline:
			t.Fatalf("timeout; delta=%v permission=%v result=%v", gotDelta, gotPermission, gotResult)
		default:
		}
		ev := readEvent(t, ctx, c)
		switch ev.T {
		case session.EvDelta:
			gotDelta = true
		case session.EvPermission:
			gotPermission = true
			send(t, ctx, c, session.Command{T: session.CmdPermission, RequestID: ev.RequestID, Behavior: "allow"})
		case session.EvResult:
			gotResult = true
		case session.EvError:
			t.Fatalf("error event: %s", ev.Message)
		}
	}
	if !gotPermission {
		t.Fatalf("expected a Write permission request")
	}
	if _, err := os.Stat(cwd + "/notes.txt"); err != nil {
		t.Fatalf("approved Write did not create the file: %v", err)
	}
}

func createSession(t *testing.T, base, cwd string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"cwd": cwd})
	resp, err := http.Post(base+"/api/sessions", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create session status %d", resp.StatusCode)
	}
	var meta session.Meta
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		t.Fatalf("decode meta: %v", err)
	}
	return meta.ID
}

func readEvent(t *testing.T, ctx context.Context, c *websocket.Conn) session.AppEvent {
	t.Helper()
	var ev session.AppEvent
	if err := wsjson.Read(ctx, c, &ev); err != nil {
		t.Fatalf("read event: %v", err)
	}
	return ev
}

func send(t *testing.T, ctx context.Context, c *websocket.Conn, cmd session.Command) {
	t.Helper()
	if err := wsjson.Write(ctx, c, cmd); err != nil {
		t.Fatalf("write cmd: %v", err)
	}
}
