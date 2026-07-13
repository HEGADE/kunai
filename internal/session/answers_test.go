package session

import (
	"encoding/json"
	"testing"
)

func TestMergeAnswers(t *testing.T) {
	in := json.RawMessage(`{"questions":[{"question":"Q?","header":"h"}]}`)
	out := mergeAnswers(in, map[string]string{"Q?": "Option A"})

	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("result not JSON: %v (%s)", err, out)
	}
	if _, ok := m["questions"]; !ok {
		t.Fatalf("original input dropped: %s", out)
	}
	ans, ok := m["answers"].(map[string]any)
	if !ok || ans["Q?"] != "Option A" {
		t.Fatalf("answers not merged: %s", out)
	}

	// Non-object input is passed through unchanged (never corrupt the payload).
	bad := json.RawMessage(`"not-an-object"`)
	if string(mergeAnswers(bad, map[string]string{"x": "y"})) != `"not-an-object"` {
		t.Fatal("non-object input should be returned unchanged")
	}
}
