package server

import "testing"

func TestNativeGrokRouting(t *testing.T) {
	grok := Provider{Name: "Grok", Models: map[string]string{"opus": "grok-4.5"}}
	codex := Provider{Name: "Codex", Models: map[string]string{"opus": "gpt-5.5"}}

	if !isGrokModel("grok-4.5") || isGrokModel("gpt-5.5") {
		t.Fatal("isGrokModel wrong")
	}

	// native grok on: a Grok provider is native, and an all-Grok setup needs no sidecar.
	on := &Server{nativeGrok: newNativeGrokManager()}
	on.providers = &providerStore{list: []Provider{grok}}
	if !on.providerUsesNativeGrok("Grok") {
		t.Error("Grok should route to native grok")
	}
	if on.anyProviderNeedsSidecar() {
		t.Error("all-Grok native setup should not need the sidecar")
	}
	// A Codex provider still needs the sidecar when only native grok is on.
	on.providers = &providerStore{list: []Provider{grok, codex}}
	if !on.anyProviderNeedsSidecar() {
		t.Error("Codex needs the sidecar when only native grok is enabled")
	}
	// native grok off: nothing routes native.
	off := &Server{}
	off.providers = &providerStore{list: []Provider{grok}}
	if off.providerUsesNativeGrok("Grok") {
		t.Error("native off: Grok must not route native")
	}
}
