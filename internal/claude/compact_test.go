package claude

import (
	"encoding/json"
	"testing"
)

// The frame below is captured verbatim from a real `claude` CLI run (/compact on
// a live stdio session), so this pins the wire spelling. It matters because the
// transcript file on disk writes the same data camelCase (compactMetadata /
// preTokens / postTokens) while the wire uses snake_case — decoding one with the
// other's tags silently yields zeros rather than an error.
const realCompactBoundaryFrame = `{"type":"system","subtype":"compact_boundary","session_id":"ad3b4cf0-3d53-49e7-abfb-64ff66ee868c","uuid":"da4a5958-a3c6-40ca-9386-fac83ab14198","compact_metadata":{"trigger":"manual","pre_tokens":26184,"post_tokens":1103,"cumulative_dropped_tokens":25081,"duration_ms":7730,"preserved_segment":{"head_uuid":"e85ca74c-bf54-44ad-af07-836ddf07024e","anchor_uuid":"749741e8-aa86-415e-adad-c18d072c97a8","tail_uuid":"736b096d-3411-4b9d-bc88-a19d62b157c1"},"preserved_messages":{"anchor_uuid":"749741e8-aa86-415e-adad-c18d072c97a8","uuids":["e85ca74c-bf54-44ad-af07-836ddf07024e"],"all_uuids":["e85ca74c-bf54-44ad-af07-836ddf07024e"]}}}`

func TestCompactBoundaryDecodesRealWireFrame(t *testing.T) {
	var env Envelope
	if err := json.Unmarshal([]byte(realCompactBoundaryFrame), &env); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if env.Type != TypeSystem || env.Subtype != SubCompactBoundary {
		t.Fatalf("got %s/%s, want system/compact_boundary", env.Type, env.Subtype)
	}

	var cb CompactBoundary
	if err := json.Unmarshal([]byte(realCompactBoundaryFrame), &cb); err != nil {
		t.Fatalf("boundary: %v", err)
	}
	if cb.Metadata.Trigger != "manual" {
		t.Errorf("trigger = %q, want manual", cb.Metadata.Trigger)
	}
	if cb.Metadata.PreTokens != 26184 {
		t.Errorf("pre = %d, want 26184", cb.Metadata.PreTokens)
	}
	// The whole point: this is the only report of the post-compaction context size.
	if cb.Metadata.PostTokens != 1103 {
		t.Errorf("post = %d, want 1103", cb.Metadata.PostTokens)
	}
}

// A compaction feeds its summary back as a plain-string user frame. That is the
// model's new context, not a tool result, so it must decode to nothing rather
// than leak into the chat log.
func TestCompactSummaryUserFrameYieldsNoToolResults(t *testing.T) {
	content := json.RawMessage(`"This session is being continued from a previous conversation that ran out of context."`)
	if got := ParseToolResultBlocks(content); len(got) != 0 {
		t.Fatalf("want no tool results from a summary frame, got %d", len(got))
	}
}
