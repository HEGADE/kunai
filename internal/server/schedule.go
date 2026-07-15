package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/hegade/kunai/internal/schedule"
	"github.com/hegade/kunai/internal/session"
)

// fireJob is the scheduler's run callback: start (or resume) a session, put it
// in an autonomous permission mode, and send the prompt. It returns promptly —
// the session runs asynchronously, unbound to any request.
func (s *Server) fireJob(j schedule.Job) error {
	opts := session.CreateOptions{
		Cwd:    j.Target.Cwd,
		Title:  j.Name,
		Model:  j.Target.Model,
		Effort: j.Target.Effort,
	}
	if opts.Model == "" {
		opts.Model = defaultModel
	}
	if opts.Effort == "" {
		opts.Effort = defaultEffort
	}
	if j.Target.Kind == "resume" && j.Target.SessionID != "" {
		opts.Resume = j.Target.SessionID
		opts.Seed = loadTranscriptTurns(j.Target.SessionID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	sess, err := s.mgr.Create(ctx, opts)
	if err != nil {
		return err
	}
	s.armSession(sess)

	// Unattended runs must not stall on an approval prompt.
	mode := j.Target.Mode
	if mode == "" {
		mode = "acceptEdits"
	}
	if mode != "default" {
		_ = sess.SetPermissionMode(mode)
	}
	return sess.Prompt(j.Prompt, nil, nil)
}

func (s *Server) handleScheduleList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.sched.List())
}

func (s *Server) handleScheduleCreate(w http.ResponseWriter, r *http.Request) {
	var j schedule.Job
	if err := json.NewDecoder(r.Body).Decode(&j); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if j.Target.Cwd == "" || j.Prompt == "" {
		writeErr(w, http.StatusBadRequest, "cwd and prompt are required")
		return
	}
	writeJSON(w, http.StatusCreated, s.sched.Create(j))
}

func (s *Server) handleScheduleReplace(w http.ResponseWriter, r *http.Request) {
	var j schedule.Job
	if err := json.NewDecoder(r.Body).Decode(&j); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	j.ID = r.PathValue("id")
	if !s.sched.Replace(j) {
		writeErr(w, http.StatusNotFound, "no such job")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleScheduleDelete(w http.ResponseWriter, r *http.Request) {
	s.sched.Delete(r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}
