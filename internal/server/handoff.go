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
	"net/url"
	"os"
	"strings"
)

// handoffReply is the link, plus enough about the session for the command to
// print something recognisable before it exits.
type handoffReply struct {
	URL   string `json:"url"`
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	Cwd   string `json:"cwd,omitempty"`
	// New marks a conversation kunai has no transcript for yet, which on this
	// machine means young rather than missing: the CLI writes as it goes and the
	// resume only needs the file when the link is opened.
	New bool `json:"new,omitempty"`
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
	cwd := strings.TrimSpace(body.Cwd)
	if id == "" {
		writeErr(w, http.StatusBadRequest,
			"no session id. Run this from inside a Claude Code session, where CLAUDE_CODE_SESSION_ID is set.")
		return
	}
	// The one check that is about safety rather than helpfulness: this value comes
	// from a shell and is used to build a filename.
	if strings.ContainsAny(id, `/\.`) || len(id) > 64 {
		writeErr(w, http.StatusBadRequest, "that is not a session id")
		return
	}

	out := handoffReply{ID: id, Cwd: cwd, URL: s.resumeURL(id, cwd)}

	// Already open here: hand back its link rather than telling somebody their own
	// live session cannot be found.
	if sess, live := s.mgr.Get(id); live {
		meta := sess.Meta()
		out.Title, out.Cwd = meta.Title, meta.Cwd
		out.URL = s.resumeURL(id, out.Cwd)
		writeJSON(w, http.StatusOK, out)
		return
	}

	// A transcript is NOT required to mint the link, and requiring it was a real
	// bug: the CLI has not necessarily written one by the time a slash command
	// runs inside the very first turn, so handing off early failed with "no
	// conversation on this machine" for a conversation that was plainly on the
	// screen. Nothing here depends on the file yet either -- the resume happens
	// when the link is OPENED, by which point the terminal has exited and the CLI
	// has flushed.
	//
	// So the transcript is used for what it is good for (a title) and its absence
	// is only fatal in the one case it genuinely means something: kunai cannot see
	// this folder at all, which is what a terminal on a DIFFERENT machine looks
	// like. That is the real limitation, and it is worth a precise error.
	if s.transcriptForID(id) != "" {
		for _, h := range s.pastSessions(historyLimit) {
			if h.ID == id {
				out.Title = h.Title
				if out.Cwd == "" {
					out.Cwd = h.Cwd
				}
				break
			}
		}
		out.URL = s.resumeURL(id, out.Cwd)
		writeJSON(w, http.StatusOK, out)
		return
	}
	if cwd != "" && !isDir(cwd) {
		writeErr(w, http.StatusNotFound,
			"kunai cannot see "+cwd+" on this machine, so it cannot continue that conversation. "+
				"A handoff only works when the terminal and kunai are on the same machine.")
		return
	}
	// Same machine, no transcript yet: the conversation is young, not missing.
	out.New = true
	writeJSON(w, http.StatusOK, out)
}

// isDir reports whether path is a directory kunai can see, which is how a
// terminal on this machine is told apart from one somewhere else.
func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// resumeURL is the link that continues a session here. It points at the app,
// not the API: opening it is what performs the resume, which is what keeps the
// terminal's process and kunai's from overlapping.
//
// The folder rides along because the client needs it to resume, and the Recent
// list is not a reliable place to look it up: a conversation minutes old may not
// have been scanned yet, and one handed off during its very first turn has
// nothing on disk at all when the link is minted. Carrying it makes the link
// self-sufficient.
func (s *Server) resumeURL(id, cwd string) string {
	base := strings.TrimSuffix(s.cfg.PublicURL, "/")
	u := base + "/resume/" + url.PathEscape(id)
	if cwd != "" {
		u += "?cwd=" + url.QueryEscape(cwd)
	}
	return u
}
