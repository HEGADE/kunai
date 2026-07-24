package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
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

func (m *checkpointManager) list(id string) []checkpointEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]checkpointEntry, len(m.byID[id]))
	copy(out, m.byID[id])
	return out
}

func (m *checkpointManager) refForSeq(id string, seq uint64) (checkpoint.Ref, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.byID[id] {
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
	writeJSON(w, http.StatusOK, s.checkpoints.list(r.PathValue("id")))
}

// handleRevert restores the working tree to a turn's pre-turn snapshot (undo the
// turn's file changes) or to a raw ref (used to undo a previous revert). It returns
// the safety ref it captured first, so the revert is itself undoable. It does NOT
// touch the conversation or un-do a commit the agent made -- only the working tree.
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
		ref = checkpoint.Ref(body.Ref)
	case s.checkpoints != nil:
		if got, found := s.checkpoints.refForSeq(id, body.Seq); found {
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
