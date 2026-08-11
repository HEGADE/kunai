package claude

import (
	"encoding/json"
	"strings"
	"testing"
)

// An image in a tool's output leaves as a marker, not as bytes.
//
// This is a wire contract, not a cosmetic choice: the client pairs the marker
// with the path the tool was given and fetches the picture from the file route
// itself (imageResultPath in web/src/lib/toolMeta.ts). Sending the base64 would
// put a megabyte on the socket, in the replay ring and on every reconnect, to
// show a file the machine already has.
func TestAnImageInAToolResultBecomesTheMarker(t *testing.T) {
	raw := json.RawMessage(`[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aVZCT1J3MEtHZ28="}}]`)
	text, truncated := toolResultText(raw)
	if truncated {
		t.Error("a marker cannot be truncated")
	}
	if text != ImageResultMarker {
		t.Errorf("got %q, want %q", text, ImageResultMarker)
	}
	// The bytes must not survive in any form; that is the whole point.
	if strings.Contains(text, "aVZCT1J") {
		t.Errorf("base64 leaked into the result: %q", text)
	}
}

func TestTextAndImageBothSurviveInOrder(t *testing.T) {
	// A mixed result keeps its prose: the marker stands in for the picture only.
	raw := json.RawMessage(`[{"type":"text","text":"here it is: "},{"type":"image","source":{"type":"base64","data":"eA=="}}]`)
	text, _ := toolResultText(raw)
	if want := "here it is: " + ImageResultMarker; text != want {
		t.Errorf("got %q, want %q", text, want)
	}
}
