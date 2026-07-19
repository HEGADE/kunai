package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A transcript past seedTailBytes is seeded from its tail only: resume time must
// stay constant as a session's transcript grows (a 69MB transcript made resume
// take ~1.8s of synchronous parsing). The tail read aligns to a line start, so a
// cut line never yields a corrupt turn, the newest turns are always present, and
// the oldest are the ones dropped.
func TestSeedReadsOnlyTheTail(t *testing.T) {
	cfg := t.TempDir()
	pad := strings.Repeat("x", 64*1024)
	var lines []string
	n := (seedTailBytes / (64 * 1024)) + 40 // comfortably past the cap
	for i := 0; i < n; i++ {
		lines = append(lines, fmt.Sprintf(
			`{"type":"user","message":{"role":"user","content":"msg-%d %s"}}`, i, pad))
	}
	writeTranscriptLines(t, cfg, "big", lines...)

	turns := loadTranscriptTurns(cfg, "big")
	if len(turns) == 0 || len(turns) >= n {
		t.Fatalf("seeded %d of %d turns, want a proper tail subset", len(turns), n)
	}
	// The newest line survives; the oldest is dropped; every turn is intact.
	if want := fmt.Sprintf("msg-%d", n-1); !strings.HasPrefix(turns[len(turns)-1].Text, want) {
		t.Errorf("last turn = %.20q, want prefix %q", turns[len(turns)-1].Text, want)
	}
	for _, tr := range turns {
		if !strings.HasPrefix(tr.Text, "msg-") {
			t.Fatalf("corrupt turn from a cut line: %.40q", tr.Text)
		}
	}
	first, _ := msgIndex(turns[0].Text)
	if first == 0 {
		t.Errorf("oldest line survived a tail read (turn 0 = %.20q)", turns[0].Text)
	}
}

// msgIndex pulls the index out of a "msg-<i> ..." seed line.
func msgIndex(s string) (int, error) {
	var i int
	_, err := fmt.Sscanf(s, "msg-%d", &i)
	return i, err
}

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
// With no measured overhead yet, the seed is the bare post size.
func TestLoadContextTokensHonorsTrailingCompaction(t *testing.T) {
	cfg := t.TempDir()
	writeTranscriptLines(t, cfg, "sess",
		`{"type":"assistant","message":{"usage":{"input_tokens":2,"cache_creation_input_tokens":2166,"cache_read_input_tokens":717528}}}`,
		`{"type":"system","subtype":"compact_boundary","compactMetadata":{"trigger":"manual","preTokens":836411,"postTokens":31000}}`,
	)
	got, _ := loadTranscriptContextTokens(cfg, "sess")
	if got != 31000 {
		t.Fatalf("seed = %d, want 31000 (the post-compaction size, not the 719k assistant usage)", got)
	}
}

// post_tokens is conversation-only and omits the fixed overhead (system prompt,
// tools, memory, skills). The overhead is NOT in the compaction frame (preTokens
// is the full pre-compaction context, the same basis as the assistant usage), so
// it can only be measured: the gap between a compaction's postTokens and the
// first assistant usage after it. A later trailing compaction then seeds
// postTokens+overhead instead of the bare post size (which read far too LOW,
// 13k when Claude's own /context showed ~50k).
func TestLoadContextTokensKeepsOverheadAcrossCompaction(t *testing.T) {
	cfg := t.TempDir()
	writeTranscriptLines(t, cfg, "sess",
		// A first compaction to a 12k conversation, then the turn that regrew it
		// reports 48k full: the 36k gap is the resident overhead.
		`{"type":"system","subtype":"compact_boundary","compactMetadata":{"trigger":"manual","preTokens":800000,"postTokens":12000}}`,
		`{"type":"assistant","message":{"usage":{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":48000}}}`,
		`{"type":"assistant","message":{"usage":{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":655000}}}`,
		// A second compaction, ending the transcript: seed must keep the overhead.
		`{"type":"system","subtype":"compact_boundary","compactMetadata":{"trigger":"manual","preTokens":655263,"postTokens":13000}}`,
	)
	got, overhead := loadTranscriptContextTokens(cfg, "sess")
	if overhead != 36000 {
		t.Fatalf("overhead = %d, want 36000 (measured from the 48k regrowth over the 12k post)", overhead)
	}
	if got != 49000 {
		t.Fatalf("seed = %d, want 49000 (13k post plus 36k measured overhead, not the bare 13k)", got)
	}
}

// A turn that regrew the window after a compaction still wins: whichever context
// event is last in the transcript is the current size, and its excess over the
// compacted conversation is returned as the measured overhead.
func TestLoadContextTokensUsesAssistantAfterCompaction(t *testing.T) {
	cfg := t.TempDir()
	writeTranscriptLines(t, cfg, "sess",
		`{"type":"system","subtype":"compact_boundary","compactMetadata":{"postTokens":31000}}`,
		`{"type":"assistant","message":{"usage":{"input_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":45000}}}`,
	)
	got, overhead := loadTranscriptContextTokens(cfg, "sess")
	if got != 45010 {
		t.Fatalf("seed = %d, want 45010 (the assistant usage after the compaction)", got)
	}
	if overhead != 14010 {
		t.Fatalf("overhead = %d, want 14010 (45010 regrowth over the 31000 post)", overhead)
	}
}
