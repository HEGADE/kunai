package codex

import (
	"reflect"
	"testing"
)

// parseCodexModels must pull the selectable slugs from the real codex backend
// shape (models[].slug) and drop the internal auto-review model, so the picker
// offers gpt-5.5/gpt-5.4/gpt-5.4-mini rather than the empty stub that made a
// Codex session show only its one saved model.
func TestParseCodexModels_RealShape(t *testing.T) {
	body := []byte(`{"models":[
		{"slug":"gpt-5.5","prefer_websockets":true},
		{"slug":"gpt-5.4"},
		{"slug":"gpt-5.4-mini"},
		{"slug":"codex-auto-review"}
	]}`)
	got, err := parseCodexModels(body)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"gpt-5.5", "gpt-5.4", "gpt-5.4-mini"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v (auto-review excluded)", got, want)
	}
}

func TestParseCodexModels_Empty(t *testing.T) {
	got, err := parseCodexModels([]byte(`{"models":[]}`))
	if err != nil || len(got) != 0 {
		t.Errorf("empty models: got %v err %v", got, err)
	}
}
