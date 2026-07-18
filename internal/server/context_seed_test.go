package server

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTranscriptLines(t *testing.T, configDir, id string, lines ...string) {
	t.Helper()
	dir := filepath.Join(configDir, "projects", "-proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The reported bug: a session that grew huge, compacted, and was then resumed
// seeded its context meter from the last big assistant usage instead of the
// compaction's post size, so the meter read ~720k when the real context was ~31k.
func TestLoadContextTokensHonorsTrailingCompaction(t *testing.T) {
	cfg := t.TempDir()
	writeTranscriptLines(t, cfg, "sess",
		`{"type":"assistant","message":{"usage":{"input_tokens":2,"cache_creation_input_tokens":2166,"cache_read_input_tokens":717528}}}`,
		`{"type":"system","subtype":"compact_boundary","compactMetadata":{"trigger":"manual","preTokens":836411,"postTokens":31000}}`,
	)
	if got := loadTranscriptContextTokens(cfg, "sess"); got != 31000 {
		t.Fatalf("seed = %d, want 31000 (the post-compaction size, not the 719k assistant usage)", got)
	}
}

// A turn that regrew the window after a compaction still wins: whichever context
// event is last in the transcript is the current size.
func TestLoadContextTokensUsesAssistantAfterCompaction(t *testing.T) {
	cfg := t.TempDir()
	writeTranscriptLines(t, cfg, "sess",
		`{"type":"system","subtype":"compact_boundary","compactMetadata":{"postTokens":31000}}`,
		`{"type":"assistant","message":{"usage":{"input_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":45000}}}`,
	)
	if got := loadTranscriptContextTokens(cfg, "sess"); got != 45010 {
		t.Fatalf("seed = %d, want 45010 (the assistant usage after the compaction)", got)
	}
}
