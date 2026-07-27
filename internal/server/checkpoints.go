package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/hegade/kunai/internal/checkpoint"
)

// checkpointManager captures a git snapshot of the working tree at the start of
// every turn (keyed by the turn's user-message Seq) and restores one on request, so
// a user can undo an agent turn's file changes. Snapshots live on git shadow refs;
// this only tracks which ref belongs to which turn, per live session.
type checkpointManager struct {
	mu   sync.Mutex
	byID map[string][]checkpointEntry
}

// checkpointEntry maps a turn (its user-message Seq) to the pre-turn snapshot ref.
type checkpointEntry struct {
	Seq        uint64 `json:"seq"`
	Ref        string `json:"ref"`
	CapturedAt int64  `json:"captured_at"`
}

func newCheckpointManager() *checkpointManager {
	return &checkpointManager{byID: map[string][]checkpointEntry{}}
}

// capture is the session's pre-turn hook: snapshot the working tree BEFORE the CLI
// gets the prompt, so the checkpoint is the true pre-turn state. It runs on the
// turn-start path, so a git failure or a slow repo must never block the turn: the
// snapshot happens in a goroutine and the turn proceeds after a bounded wait (in the
// normal case the snapshot is done in tens of milliseconds). Only for git repos.
func (m *checkpointManager) capture(id, cwd string, seq uint64) {
	if cwd == "" || !checkpoint.IsRepo(cwd) {
		return
	}
	ref := checkpoint.RefFor(id, seq)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := checkpoint.Capture(cwd, ref, fmt.Sprintf("kunai turn %d", seq)); err != nil {
			log.Printf("checkpoint: capture %s turn %d: %v", id, seq, err)
			return
		}
		m.record(id, seq, ref)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		// Pathological (a huge or hung repo). Let the turn go; the snapshot, if it
		// ever finishes, records itself. Better a turn with no checkpoint than a hang.
		log.Printf("checkpoint: capture slow for %s turn %d; proceeding without waiting", id, seq)
	}
}

func (m *checkpointManager) record(id string, seq uint64, ref checkpoint.Ref) {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.byID[id]
	for i, e := range list {
		if e.Seq == seq { // a re-prompt of the same turn replaces its checkpoint
			list[i].Ref = string(ref)
			list[i].CapturedAt = time.Now().Unix()
			return
		}
	}
	m.byID[id] = append(list, checkpointEntry{Seq: seq, Ref: string(ref), CapturedAt: time.Now().Unix()})
}

// list returns a session's turn checkpoints. cwd lets it fall back to the git
// shadow refs when the in-memory map is empty -- which is exactly the case after a
// restart, since the refs persist but the map does not. Git is the source of truth;
// the map is a warm cache for the live process.
func (m *checkpointManager) list(id, cwd string) []checkpointEntry {
	m.mu.Lock()
	cached := m.byID[id]
	if len(cached) > 0 {
		out := make([]checkpointEntry, len(cached))
		copy(out, cached)
		m.mu.Unlock()
		return out
	}
	m.mu.Unlock()

	if cwd == "" {
		return []checkpointEntry{}
	}
	snaps := checkpoint.List(cwd, id)
	out := make([]checkpointEntry, 0, len(snaps))
	for _, s := range snaps {
		out = append(out, checkpointEntry{Seq: s.Seq, Ref: string(s.Ref), CapturedAt: s.CapturedAt})
	}
	// Warm the cache so the client's follow-up calls (and a revert) hit memory.
	if len(out) > 0 {
		m.mu.Lock()
		if len(m.byID[id]) == 0 {
			m.byID[id] = append([]checkpointEntry(nil), out...)
		}
		m.mu.Unlock()
	}
	return out
}

func (m *checkpointManager) refForSeq(id, cwd string, seq uint64) (checkpoint.Ref, bool) {
	m.mu.Lock()
	for _, e := range m.byID[id] {
		if e.Seq == seq {
			m.mu.Unlock()
			return checkpoint.Ref(e.Ref), true
		}
	}
	m.mu.Unlock()
	// Not in the live cache -- reconstruct from git (post-restart) using cwd.
	for _, e := range m.list(id, cwd) {
		if e.Seq == seq {
			return checkpoint.Ref(e.Ref), true
		}
	}
	return "", false
}

// forget drops a session's checkpoint records (the shadow refs are left for git GC).
func (m *checkpointManager) forget(id string) {
	m.mu.Lock()
	delete(m.byID, id)
	m.mu.Unlock()
}

// --- handlers ----------------------------------------------------------------

// handleListCheckpoints returns the turns that have a restorable pre-turn snapshot,
// so the client can show a revert affordance on those turns.
func (s *Server) handleListCheckpoints(w http.ResponseWriter, r *http.Request) {
	if s.checkpoints == nil {
		writeJSON(w, http.StatusOK, []checkpointEntry{})
		return
	}
	id := r.PathValue("id")
	// cwd, if the session is live, lets list() rebuild from git after a restart.
	var cwd string
	if sess, ok := s.mgr.Get(id); ok {
		cwd = sess.Cwd
	}
	writeJSON(w, http.StatusOK, s.checkpoints.list(id, cwd))
}

// handleRevert restores the working tree to a turn's pre-turn snapshot (undo the
// turn's file changes) or to a raw ref (used to undo a previous revert). It returns
// the safety ref it captured first, so the revert is itself undoable. It does NOT
// touch the conversation or un-do a commit the agent made -- only the working tree.
// handleRevertPreview reports exactly what a revert of this turn would change,
// so the client can ask the question with the answer in hand.
//
// It exists because a revert is a whole-repository operation, not a per-turn one:
// it also discards every later turn's edits, anything changed in an editor since,
// and any untracked file in the repo. The client could list the files the turn's
// own tool calls touched, and that list would be reassuringly short and wrong.
// Only git knows.
func (s *Server) handleRevertPreview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, ok := s.mgr.Get(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	if s.checkpoints == nil {
		writeErr(w, http.StatusBadRequest, "checkpoints are not available")
		return
	}
	seq, _ := strconv.ParseUint(r.URL.Query().Get("seq"), 10, 64)
	ref, found := s.checkpoints.refForSeq(id, sess.Cwd, seq)
	if !found {
		writeErr(w, http.StatusBadRequest, "no checkpoint for that turn")
		return
	}
	changed, removed, err := checkpoint.Preview(sess.Cwd, ref)
	if err != nil {
		if err == checkpoint.ErrNoRef {
			writeErr(w, http.StatusGone, "that checkpoint no longer exists")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"changed": changed,
		"removed": removed,
	})
}

func (s *Server) handleRevert(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, ok := s.mgr.Get(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	var body struct {
		Seq uint64 `json:"seq"`
		Ref string `json:"ref"` // undo-a-revert: restore directly to a safety ref
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	var ref checkpoint.Ref
	switch {
	case body.Ref != "":
		// Only a ref kunai minted for THIS session. Restore is a hard reset of the
		// whole repository, and this field exists solely so an undo can name the
		// safety ref the previous revert handed back; it is not a way to ask for an
		// arbitrary commit-ish. Anything git would resolve used to be accepted here,
		// including another session's checkpoint, a branch or "HEAD~50".
		ref = checkpoint.Ref(body.Ref)
		if !ref.OwnedBy(id) {
			writeErr(w, http.StatusBadRequest, "that is not a checkpoint of this session")
			return
		}
	case s.checkpoints != nil:
		if got, found := s.checkpoints.refForSeq(id, sess.Cwd, body.Seq); found {
			ref = got
		}
	}
	if ref == "" {
		writeErr(w, http.StatusBadRequest, "no checkpoint for that turn")
		return
	}

	cwd := sess.Cwd
	// A nanosecond-tagged safety ref so concurrent reverts never collide.
	safety, err := checkpoint.Restore(cwd, ref, checkpoint.SafetyRefFor(id, uint64(time.Now().UnixNano())))
	if err != nil {
		if err == checkpoint.ErrNoRef {
			writeErr(w, http.StatusGone, "that checkpoint no longer exists")
			return
		}
		writeErr(w, http.StatusBadRequest, "revert failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"reverted_to": string(ref),
		"safety_ref":  string(safety), // POST this back as {"ref": ...} to undo the revert
	})
}
