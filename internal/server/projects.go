package server

import (
	"errors"

	"github.com/hegade/kunai/internal/project"
	"github.com/hegade/kunai/internal/session"
)

// addProject scans a directory and hands it to a session as context. The scan is
// the only slow part and it is bounded, so this stays on the socket's goroutine
// rather than growing a job for it.
func (s *Server) addProject(sess *session.Session, path string) error {
	if path == "" {
		return errors.New("path required")
	}
	info, err := project.Scan(path)
	if err != nil {
		return err
	}
	return sess.AddProject(info)
}
