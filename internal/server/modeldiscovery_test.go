package server

import (
	"os"
	"path/filepath"
	"testing"
)

// A minor version is 1-2 digits (opus-4-8 -> 4.8); a longer second segment is a
// DATE (opus-4-20250514), not a minor, and must not beat a real newer version.
// This is the bug the real binary exposed: opus-4-<date> read as 4.20250514.
func TestScanClaudeModelVersions(t *testing.T) {
	// A stand-in "binary": a blob of the ids the real one bakes in, dates and all.
	blob := []byte("junk\x00claude-opus-4-20250514\x00claude-opus-4-8 " +
		"claude-opus-5 claude-fable-5 claude-fable-5-mythos-5 " +
		"claude-haiku-4-5-20251001-v1 claude-sonnet-4-6-20251114 claude-sonnet-5\x00more")
	dir := t.TempDir()
	path := filepath.Join(dir, "fakeclaude")
	if err := os.WriteFile(path, blob, 0o755); err != nil {
		t.Fatal(err)
	}
	got := scanClaudeModelVersions(path)
	want := map[string]string{"opus": "5", "fable": "5", "haiku": "4.5", "sonnet": "5"}
	for fam, v := range want {
		if got[fam] != v {
			t.Errorf("%s = %q, want %q (full: %v)", fam, got[fam], v, got)
		}
	}
}

// A model id split across a read boundary must still be found (the streaming
// overlap): put one right at a 4MB+ chunk edge.
func TestScanClaudeModelVersions_AcrossChunkBoundary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big")
	f, _ := os.Create(path)
	pad := make([]byte, (4<<20)-8) // leave "claude-o" before the boundary
	f.Write(pad)
	f.WriteString("claude-opus-5 padding") // straddles the 4MB read edge
	f.Close()
	if got := scanClaudeModelVersions(path); got["opus"] != "5" {
		t.Fatalf("opus across boundary = %q, want 5", got["opus"])
	}
}
