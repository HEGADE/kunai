package codex

import (
	"context"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

// Codex's terminal response.completed can carry an EMPTY output array, with the
// real content only in the streamed output_item.done events. The non-streaming
// path must backfill from those items, or the reply has no content -- which is
// exactly what broke Claude Code's auto-mode Bash classifier (its verdict text
// was dropped and the CLI denied the command as "could not evaluate").
func TestCompletedEventForNonStream_BackfillsEmptyOutput(t *testing.T) {
	raw := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"r1","model":"gpt-5.5"}}`,
		`data: {"type":"response.output_item.done","item":{"id":"rs1","type":"reasoning","content":[],"encrypted_content":"enc"}}`,
		`data: {"type":"response.output_item.done","item":{"id":"m1","type":"message","status":"completed","content":[{"type":"output_text","text":"<block>no</block>"}]}}`,
		`data: {"type":"response.completed","response":{"status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":5}}}`,
	}, "\n")
	completed := CompletedEventForNonStream([]byte(raw))
	if completed == nil {
		t.Fatal("no terminal event found")
	}
	out := gjson.GetBytes(completed, "response.output")
	if len(out.Array()) != 2 {
		t.Fatalf("output not backfilled: %s", out.Raw)
	}
	// And the full non-stream translation now carries the verdict text.
	var param any
	msg := ConvertCodexResponseToClaudeNonStream(context.Background(), "gpt-5.5", []byte(`{"model":"x","messages":[]}`), nil, completed, &param)
	if !strings.Contains(string(msg), "<block>no</block>") {
		t.Errorf("translated non-stream reply lost the verdict text:\n%s", msg)
	}
}

// A terminal event that already has output (Grok's shape) must pass through
// untouched.
func TestCompletedEventForNonStream_KeepsPopulatedOutput(t *testing.T) {
	raw := strings.Join([]string{
		`data: {"type":"response.output_item.done","item":{"id":"other","type":"message","content":[{"type":"output_text","text":"IGNORED"}]}}`,
		`data: {"type":"response.completed","response":{"status":"completed","output":[{"id":"m1","type":"message","status":"completed","content":[{"type":"output_text","text":"real"}]}]}}`,
	}, "\n")
	completed := CompletedEventForNonStream([]byte(raw))
	out := gjson.GetBytes(completed, "response.output")
	if len(out.Array()) != 1 || !strings.Contains(out.Raw, "real") || strings.Contains(out.Raw, "IGNORED") {
		t.Errorf("populated output should be untouched: %s", out.Raw)
	}
}

func TestCompletedEventForNonStream_NoTerminal(t *testing.T) {
	if got := CompletedEventForNonStream([]byte(`data: {"type":"response.created"}`)); got != nil {
		t.Errorf("no terminal should return nil, got %s", got)
	}
}
