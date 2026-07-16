package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hegade/kunai/internal/session"
)

// loopTranscript is shaped like the real thing: a loop's iterations reach the CLI
// as ordinary user frames, because that is the only way to send a turn, so that
// is how the CLI writes them down. Captured from an actual two-iteration run.
var loopTranscript = `
{"type":"user","message":{"role":"user","content":"fix the tests"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"on it"}]}}
{"type":"user","message":{"role":"user","content":` + quoted(session.LoopPrompt(session.LoopConfig{Prompt: "Append one line naming a color to /tmp/lt/c.txt", Promise: "DONE", MaxIters: 2}, 1)) + `}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"added blue"}]}}
{"type":"user","message":{"role":"user","content":` + quoted(session.LoopPrompt(session.LoopConfig{Prompt: "Append one line naming a color to /tmp/lt/c.txt", Promise: "DONE", MaxIters: 2}, 2)) + `}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"added red"}]}}
{"type":"user","message":{"role":"user","content":"thanks"}}
`

// Resuming a looped session must not replay every iteration's prompt as a user
// turn. The loop sends the same instructions round after round, so at fifty
// iterations this is fifty walls of identical text burying the actual work, which
// is the compaction summary's bug wearing a different hat.
func TestLoopPromptsAreNotSeededAsUserTurns(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude", "projects", "-tmp-p")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(loopTranscript), 0o644); err != nil {
		t.Fatal(err)
	}

	turns := loadTranscriptTurns("", id)

	var users []string
	for _, tr := range turns {
		if tr.Role != "user" {
			continue
		}
		if strings.Contains(tr.Text, "running in a loop") || strings.Contains(tr.Text, "Loop iteration") {
			t.Errorf("a loop iteration leaked into the log as a user turn (%d chars): %.60q", len(tr.Text), tr.Text)
		}
		users = append(users, tr.Text)
	}

	// What a person actually typed survives; what the loop said on their behalf
	// does not.
	want := []string{"fix the tests", "thanks"}
	if len(users) != len(want) {
		t.Fatalf("user turns = %q, want %q", users, want)
	}
	for i := range want {
		if users[i] != want[i] {
			t.Fatalf("user turns = %q, want %q", users, want)
		}
	}
}

// The loop's own beats are worth keeping, so a resumed session reads the way the
// live one did: seams between iterations rather than nothing at all.
func TestLoopIterationsAreSeededAsSeams(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude", "projects", "-tmp-p")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	id := "bbbbbbbb-cccc-dddd-eeee-ffffffffffff"
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(loopTranscript), 0o644); err != nil {
		t.Fatal(err)
	}

	var iters []int
	for _, tr := range loadTranscriptTurns("", id) {
		if tr.Role == "loop" {
			iters = append(iters, tr.Iteration)
		}
	}
	if len(iters) != 2 || iters[0] != 1 || iters[1] != 2 {
		t.Fatalf("loop seams = %v, want [1 2]", iters)
	}
}

// quoted renders s as a JSON string literal for the fixture above.
func quoted(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}
