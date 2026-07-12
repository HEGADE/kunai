package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

type fakeKeeper struct {
	on, supported bool
}

func (f *fakeKeeper) Set(on bool) error { f.on = on; return nil }
func (f *fakeKeeper) Enabled() bool     { return f.on }
func (f *fakeKeeper) Supported() bool   { return f.supported }

func TestAwakeToggleAndPersist(t *testing.T) {
	dir := t.TempDir()
	fk := &fakeKeeper{supported: true}
	s := &Server{cfg: Config{DataDir: dir}, awake: fk}

	// Enable via the handler.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/awake", strings.NewReader(`{"enabled":true}`))
	s.handleAwake(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if !fk.on {
		t.Fatal("keeper not enabled")
	}
	if _, err := os.Stat(s.awakePath()); err != nil {
		t.Fatalf("awake.json not written: %v", err)
	}

	// A fresh server over the same data dir re-applies the persisted preference.
	fk2 := &fakeKeeper{supported: true}
	s2 := &Server{cfg: Config{DataDir: dir}, awake: fk2}
	s2.loadAwake()
	if !fk2.on {
		t.Fatal("persisted preference not re-applied on boot")
	}

	// Disable persists too.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/awake", strings.NewReader(`{"enabled":false}`))
	s.handleAwake(rec, req)
	if fk.on {
		t.Fatal("keeper still enabled after disable")
	}
}

// On an unsupported host the toggle is a no-op that reports supported:false and
// never persists an enable.
func TestAwakeUnsupported(t *testing.T) {
	dir := t.TempDir()
	fk := &fakeKeeper{supported: false}
	s := &Server{cfg: Config{DataDir: dir}, awake: fk}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/awake", strings.NewReader(`{"enabled":true}`))
	s.handleAwake(rec, req)
	if rec.Code != http.StatusOK || fk.on {
		t.Fatalf("unsupported host should not enable (code=%d on=%v)", rec.Code, fk.on)
	}
	if !strings.Contains(rec.Body.String(), `"supported":false`) {
		t.Fatalf("expected supported:false, got %s", rec.Body.String())
	}
}
