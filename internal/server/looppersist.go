package server

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/hegade/kunai/internal/session"
)

// Loop durability: a running loop is written to loops/<sessionId>.json so it
// survives the process dying (an auto-update swap-and-restart, a crash, an OOM,
// the service manager bouncing us). On boot we recreate the session with --resume
// and pick the loop up where it left off.
//
// The safety rests on one rule: a file exists ONLY while the loop is running. Any
// ending deletes it, so the only loops we ever resume are the ones that were still
// going when the process died unexpectedly. A loop the thermal guard stopped, or
// that finished, or that the user stopped, has no file and is never restarted.
//
// maxLoopResumes bounds a crash loop: a loop that keeps dying without ever ending
// cleanly is given up on rather than restarted forever.
const maxLoopResumes = 5

func (s *Server) loopDir() string {
	return filepath.Join(s.cfg.DataDir, "loops")
}

func (s *Server) loopFile(sessionID string) string {
	return filepath.Join(s.loopDir(), sessionID+".json")
}

// loopPersister returns the callback a session calls whenever its loop changes.
// A running record is written; any other state deletes the file. Synchronous on
// purpose: the delete on a thermal stop must land before the guardian's poweroff.
func (s *Server) loopPersister() func(session.LoopPersist) {
	return func(rec session.LoopPersist) {
		if s.cfg.DataDir == "" || rec.SessionID == "" {
			return
		}
		path := s.loopFile(rec.SessionID)
		if rec.State != session.LoopRunning {
			_ = os.Remove(path)
			return
		}
		if err := os.MkdirAll(s.loopDir(), 0o700); err != nil {
			return
		}
		b, err := json.Marshal(rec)
		if err != nil {
			return
		}
		// Write via a temp file + rename so a crash mid-write never leaves a
		// half-written record that fails to parse on the next boot.
		tmp := path + ".tmp"
		if os.WriteFile(tmp, b, 0o600) == nil {
			_ = os.Rename(tmp, path)
		}
	}
}

// resumeLoops recreates and restarts every loop that was running when a prior
// process died. Called once at boot, in the background, since each session start
// blocks on the CLI init handshake.
func (s *Server) resumeLoops(ctx context.Context) {
	entries, err := os.ReadDir(s.loopDir())
	if err != nil {
		return // no loops dir: nothing was ever running
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(s.loopDir(), e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var rec session.LoopPersist
		if json.Unmarshal(b, &rec) != nil || rec.SessionID == "" {
			_ = os.Remove(path) // unreadable record helps nobody
			continue
		}
		if rec.Resumes >= maxLoopResumes {
			log.Printf("loop resume: giving up on %s after %d resumes (crash loop?)", rec.SessionID, rec.Resumes)
			_ = os.Remove(path)
			continue
		}
		s.resumeOneLoop(ctx, rec)
	}
}

func (s *Server) resumeOneLoop(ctx context.Context, rec session.LoopPersist) {
	// A session start blocks on the init handshake; bound it like the create path.
	cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	sess, err := s.mgr.Create(cctx, session.CreateOptions{
		Cwd:           rec.Cwd,
		Model:         rec.Model,
		Effort:        rec.Effort,
		Resume:        rec.SessionID,
		Seed:          loadTranscriptTurns(rec.SessionID),
		ContextTokens: loadTranscriptContextTokens(rec.SessionID),
	})
	if err != nil {
		log.Printf("loop resume: could not recreate session %s: %v", rec.SessionID, err)
		return
	}
	s.armSession(sess)
	if err := sess.ResumeLoop(rec); err != nil {
		log.Printf("loop resume: could not restart loop on %s: %v", rec.SessionID, err)
		return
	}
	log.Printf("loop resume: restarted %s at iteration %d (resume %d/%d)",
		rec.SessionID, rec.Iteration, rec.Resumes+1, maxLoopResumes)
}
