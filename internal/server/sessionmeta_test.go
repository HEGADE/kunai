package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hegade/kunai/internal/session"
)

func TestSessionMetaStoreUpdateAndClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessionmeta.json")
	st := newSessionMetaStore(path)

	name := "My session"
	pin := true
	st.update("abc", metaPatch{Name: &name, Pinned: &pin})
	if got := st.get("abc"); got.Name != "My session" || !got.Pinned {
		t.Fatalf("after update: got %+v", got)
	}

	// Reload from disk: the override persisted.
	if reloaded := newSessionMetaStore(path).get("abc"); reloaded.Name != "My session" || !reloaded.Pinned {
		t.Fatalf("after reload: got %+v", reloaded)
	}

	// A partial update leaves the untouched field alone.
	unpin := false
	st.update("abc", metaPatch{Pinned: &unpin})
	if got := st.get("abc"); got.Name != "My session" || got.Pinned {
		t.Fatalf("after unpin: got %+v", got)
	}

	// Clearing the name with no pin left drops the entry entirely, so the file
	// only holds customized sessions.
	empty := ""
	st.update("abc", metaPatch{Name: &empty})
	if got := st.get("abc"); got.Name != "" || got.Pinned {
		t.Fatalf("after clear: expected empty, got %+v", got)
	}
	if _, ok := st.data["abc"]; ok {
		t.Error("cleared entry should be removed from the store")
	}
}

func TestMergeMetaOverlaysNameAndPin(t *testing.T) {
	metas := []session.Meta{
		{ID: "a", Title: "derived-a"},
		{ID: "b", Title: "derived-b"},
	}
	mergeMeta(metas, map[string]sessionMeta{
		"a": {Name: "renamed", Pinned: true},
		"b": {Pinned: true}, // pin only, keep the derived title
	})
	if metas[0].Title != "renamed" || !metas[0].Pinned {
		t.Errorf("a: got %+v", metas[0])
	}
	if metas[1].Title != "derived-b" || !metas[1].Pinned {
		t.Errorf("b: got %+v", metas[1])
	}
}

// A pinned session older than the newest-N window still appears, so a pin is
// never hidden by the limit.
func TestScanHistoryKeepsPinnedBeyondLimit(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	// Three sessions; make "old" the oldest so a limit of 2 would drop it.
	writeTranscript(t, root, "-p", "new", "/p")
	writeTranscript(t, root, "-p", "mid", "/p")
	writeTranscript(t, root, "-p", "old", "/p")
	bumpMtime(t, filepath.Join(root, "-p", "new.jsonl"), 3)
	bumpMtime(t, filepath.Join(root, "-p", "mid.jsonl"), 2)
	bumpMtime(t, filepath.Join(root, "-p", "old.jsonl"), 1)

	roots := []accountRoot{{name: "", root: root}}

	// Without a pin, a limit of 2 drops the oldest.
	got := scanHistory(map[string]bool{}, 2, roots, nil)
	if ids := idset(got); ids["old"] {
		t.Error("old session should have been clamped out")
	}

	// Pin the oldest and it survives the same limit.
	got = scanHistory(map[string]bool{}, 2, roots, map[string]bool{"old": true})
	if ids := idset(got); !ids["old"] || !ids["new"] || !ids["mid"] {
		t.Errorf("pinned old session should survive the limit; got %v", ids)
	}
}

// PATCH /api/sessions/{id} renames+pins, and DELETE /api/history/{id} removes the
// transcript and the override, end to end through the router.
func TestSessionMetaHTTPRoundTrip(t *testing.T) {
	// Isolate HOME so the default ~/.claude root contributes no real transcripts.
	t.Setenv("HOME", t.TempDir())
	data := t.TempDir()
	cfgDir := t.TempDir()
	projects := filepath.Join(cfgDir, "projects")
	writeTranscript(t, projects, "-x", "sess-1", "/x")

	s := New(Config{DataDir: data}, session.NewManager())
	s.setCLIs([]CLIProfile{{Name: "T", Bin: "claude", Dir: cfgDir}})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	// Rename + pin.
	patch(t, srv.URL+"/api/sessions/sess-1", `{"name":"Renamed","pinned":true}`)
	if got := s.sessionMeta.get("sess-1"); got.Name != "Renamed" || !got.Pinned {
		t.Fatalf("store after PATCH: %+v", got)
	}

	// History reflects the override.
	var hist []HistoryEntry
	getJSON(t, srv.URL+"/api/history", &hist)
	if len(hist) != 1 || hist[0].Title != "Renamed" || !hist[0].Pinned {
		t.Fatalf("history after PATCH: %+v", hist)
	}

	// Delete removes the transcript and the override.
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/history/sess-1", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: err=%v status=%v", err, res.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(projects, "-x", "sess-1.jsonl")); !os.IsNotExist(err) {
		t.Error("transcript should be gone after delete")
	}
	if _, ok := s.sessionMeta.data["sess-1"]; ok {
		t.Error("override should be gone after delete")
	}
}

// --- helpers ---

func idset(entries []HistoryEntry) map[string]bool {
	m := map[string]bool{}
	for _, e := range entries {
		m[e.ID] = true
	}
	return m
}

func bumpMtime(t *testing.T, path string, rank int64) {
	t.Helper()
	// A fixed base plus a per-rank offset gives a deterministic mtime order
	// without depending on the wall clock.
	when := time.Unix(1_700_000_000+rank*60, 0)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func patch(t *testing.T, url, body string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPatch, url, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("patch %s: err=%v status=%v", url, err, statusOf(res))
	}
	res.Body.Close()
}

func getJSON(t *testing.T, url string, v any) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

func statusOf(res *http.Response) int {
	if res == nil {
		return 0
	}
	return res.StatusCode
}

// A workspace name is what the sidebar groups a session under, so it has to
// outlive the process: named while running, still grouped once it is a
// transcript in Recent.
func TestWorkspaceSurvivesReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessionmeta.json")

	ws := "Kunai + docs"
	newSessionMetaStore(path).update("abc", metaPatch{Workspace: &ws})

	if got := newSessionMetaStore(path).get("abc").Workspace; got != ws {
		t.Fatalf("workspace = %q after reload, want %q", got, ws)
	}
}

// The three overrides are independent: naming a workspace must not disturb a
// rename or a pin the session already had.
func TestWorkspaceIsIndependentOfNameAndPin(t *testing.T) {
	st := newSessionMetaStore(filepath.Join(t.TempDir(), "m.json"))
	name, pin, ws := "My session", true, "Platform"

	st.update("abc", metaPatch{Name: &name, Pinned: &pin})
	st.update("abc", metaPatch{Workspace: &ws})

	got := st.get("abc")
	if got.Name != name || !got.Pinned || got.Workspace != ws {
		t.Fatalf("got %+v, want all three kept", got)
	}
}

// Clearing the workspace drops the entry only when nothing else is set, so the
// file keeps only sessions the user actually customized.
func TestClearingWorkspaceDropsAnOtherwiseEmptyEntry(t *testing.T) {
	st := newSessionMetaStore(filepath.Join(t.TempDir(), "m.json"))
	ws, blank := "Platform", ""

	st.update("abc", metaPatch{Workspace: &ws})
	st.update("abc", metaPatch{Workspace: &blank})

	if got := st.get("abc"); got != (sessionMeta{}) {
		t.Fatalf("entry survived as %+v, want it dropped", got)
	}
}

// A snooze is a promise to bring the session back, so it must survive a restart
// and carry its reference point (SnoozedAt, stamped by the store so there is one
// clock) for the client's "did the agent do anything since" wake rule.
func TestSnoozePersistsAndClears(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.json")
	st := newSessionMetaStore(path)

	until := time.Now().Add(2 * time.Hour).UnixMilli()
	st.update("abc", metaPatch{SnoozedUntil: &until})
	got := st.get("abc")
	if got.SnoozedUntil != until || got.SnoozedAt == 0 {
		t.Fatalf("after snooze: %+v, want until kept and SnoozedAt stamped", got)
	}
	if reloaded := newSessionMetaStore(path).get("abc"); reloaded.SnoozedUntil != until {
		t.Fatalf("snooze lost on reload: %+v", reloaded)
	}

	// Clearing (0) drops both fields, and an entry with nothing else set.
	zero := int64(0)
	st.update("abc", metaPatch{SnoozedUntil: &zero})
	if got := st.get("abc"); got != (sessionMeta{}) {
		t.Fatalf("after clear: %+v, want the entry dropped", got)
	}
}

// A snoozed session outside the newest-N history window must survive the clamp
// like a pinned one: a shelf that promised to bring a session back cannot have
// the scan quietly age it out.
func TestKeepIDsCoversSnoozedAndPinned(t *testing.T) {
	st := newSessionMetaStore(filepath.Join(t.TempDir(), "m.json"))
	pin := true
	until := time.Now().Add(time.Hour).UnixMilli()
	st.update("pinned", metaPatch{Pinned: &pin})
	st.update("snoozed", metaPatch{SnoozedUntil: &until})

	keep := st.keepIDs()
	if !keep["pinned"] || !keep["snoozed"] {
		t.Fatalf("keepIDs = %v, want both the pin and the snooze", keep)
	}
}

// The live list carries the workspace so the sidebar can group without asking
// each session separately.
func TestMergeMetaCarriesWorkspace(t *testing.T) {
	metas := []session.Meta{{ID: "abc", Title: "derived"}, {ID: "other", Title: "untouched"}}
	mergeMeta(metas, map[string]sessionMeta{"abc": {Workspace: "Platform", Pinned: true}})

	if metas[0].Workspace != "Platform" {
		t.Errorf("workspace = %q, want Platform", metas[0].Workspace)
	}
	if metas[0].Title != "derived" {
		t.Errorf("naming a workspace changed the title to %q", metas[0].Title)
	}
	if metas[1].Workspace != "" {
		t.Errorf("a session with no override got workspace %q", metas[1].Workspace)
	}
}
