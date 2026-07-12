package awake

import "testing"

func TestNewStartsDisabled(t *testing.T) {
	k := New()
	if k == nil {
		t.Fatal("New returned nil")
	}
	if k.Enabled() {
		t.Fatal("a fresh keeper must start disabled")
	}
	_ = k.Supported() // must not panic on any platform
}
