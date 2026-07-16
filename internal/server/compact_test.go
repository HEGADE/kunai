package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Records shaped like the ones a real transcript holds around a /compact. Note
// the file spells the metadata camelCase, unlike the wire (see
// claude.CompactBoundary) — that difference is exactly what this pins.
const compactTranscript = `
{"type":"user","message":{"role":"user","content":"hello there"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}
{"type":"system","subtype":"compact_boundary","content":"Conversation compacted","compactMetadata":{"trigger":"manual","preTokens":836411,"postTokens":12537,"durationMs":132497}}
{"type":"user","isVisibleInTranscriptOnly":true,"isCompactSummary":true,"message":{"role":"user","content":"This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion of the conversation."}}
{"type":"user","message":{"role":"user","content":"<local-command-stdout>Compacted </local-command-stdout>"}}
{"type":"user","message":{"role":"user","content":"carry on"}}
`

// Resuming a compacted session used to replay the summary as a user turn: it is
// a user record only because that is how the CLI feeds it back to the model, and
// at tens of thousands of characters it buried the conversation under a wall of
// text. It must be dropped, and the boundary shown in its place.
func TestCompactSummaryIsNotSeededAsAUserTurn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude", "projects", "-tmp-p")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	id := "11111111-2222-3333-4444-555555555555"
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(compactTranscript), 0o644); err != nil {
		t.Fatal(err)
	}

	turns := loadTranscriptTurns("", id)

	var users []string
	var compacts int
	for _, tr := range turns {
		switch tr.Role {
		case "user":
			if strings.Contains(tr.Text, "This session is being continued") {
				t.Errorf("the compaction summary leaked into the log as a user turn (%d chars)", len(tr.Text))
			}
			users = append(users, tr.Text)
		case "compact":
			compacts++
			if tr.PostTokens != 12537 || tr.PreTokens != 836411 || tr.Trigger != "manual" {
				t.Errorf("boundary = %+v, want manual 836411 -> 12537", tr)
			}
		}
	}
	if compacts != 1 {
		t.Errorf("want exactly 1 compact boundary seeded, got %d", compacts)
	}
	// The real turns survive; the harness wrapper does not.
	want := []string{"hello there", "carry on"}
	if len(users) != len(want) {
		t.Fatalf("user turns = %q, want %q", users, want)
	}
	for i := range want {
		if users[i] != want[i] {
			t.Fatalf("user turns = %q, want %q", users, want)
		}
	}
}
