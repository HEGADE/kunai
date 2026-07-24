package grok

import (
	"reflect"
	"testing"
)

// parseGrokModels reads xAI's OpenAI-shape /v1/models. The list is honestly small
// (a free account sees only grok-4.5), which is the point: reflect the account,
// not a hardcoded guess.
func TestParseGrokModels_RealShape(t *testing.T) {
	body := []byte(`{"object":"list","data":[
		{"id":"grok-4.5","object":"model","name":"Grok 4.5"},
		{"id":"grok-4-fast","object":"model"}
	]}`)
	got, err := parseGrokModels(body)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"grok-4.5", "grok-4-fast"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
