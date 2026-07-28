package server

// Handing a terminal Claude Code session over to kunai, in one command.
//
// You are working in a terminal, you want to keep going from your phone, and the
// conversation is already on disk: the CLI writes every turn to
// <configDir>/projects/<slug>/<sessionId>.jsonl, which is the same file kunai's
// Recent list reopens from. So there is nothing to transfer. All that is missing
// is a way to say "that one" and get a link.
//
// A running session knows its own id: the CLI exports CLAUDE_CODE_SESSION_ID,
// verified to equal the transcript's filename. So the slash command reads it,
// posts it here, and gets back a URL.
//
// This deliberately does NOT start the session. The terminal's `claude` is still
// running when the command fires, and two processes appending to one transcript
// is how a conversation gets corrupted. The link resumes it when OPENED, by which
// time the terminal has exited. Nothing is spawned if the link is never clicked.
//
// The other design considered was --fork-session, which resumes under a new id so
// both can run. It was rejected as the default: a fork diverges silently from the
// moment it is made, and the thing being asked for is to CONTINUE, not to branch.

import (
	"encoding/json"
	"net/http"
	"strings"
)

// handoffReply is the link, plus enough about the session for the command to
// print something recognisable before it exits.
type handoffReply struct {
	URL   string `json:"url"`
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	Cwd   string `json:"cwd,omitempty"`
}

// handleHandoff turns a terminal session id into the URL that continues it here.
func (s *Server) handleHandoff(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string `json:"session_id"`
		Cwd       string `json:"cwd"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body) != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	id := strings.TrimSpace(body.SessionID)
	if id == "" {
		writeErr(w, http.StatusBadRequest,
			"no session id. Run this from inside a Claude Code session, where CLAUDE_CODE_SESSION_ID is set.")
		return
	}
	// transcriptForID refuses a path-shaped id, which is the guard that matters:
	// this value arrives from a shell and is used to build a filename.
	path := s.transcriptForID(id)
	if path == "" {
		writeErr(w, http.StatusNotFound,
			"no conversation on this machine with that id. kunai hands over sessions it can see on disk, "+
				"so the terminal has to be running on the same machine as kunai.")
		return
	}

	out := handoffReply{ID: id, Cwd: strings.TrimSpace(body.Cwd), URL: s.resumeURL(id)}
	// The title the Recent list would show, so the command can name what it is
	// handing over rather than printing a bare uuid.
	for _, h := range s.pastSessions(historyLimit) {
		if h.ID == id {
			out.Title = h.Title
			if out.Cwd == "" {
				out.Cwd = h.Cwd
			}
			break
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// resumeURL is the link that continues a session here. It points at the app,
// not the API: opening it is what performs the resume, which is what keeps the
// terminal's process and kunai's from overlapping.
func (s *Server) resumeURL(id string) string {
	base := strings.TrimSuffix(s.cfg.PublicURL, "/")
	return base + "/resume/" + id
}
