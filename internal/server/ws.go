package server

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/hegade/kunai/internal/session"
)

// handleWS bridges a phone connection to a live session. The client passes
// ?since=<seq>; we reply with a hello frame, replay any events after that seq
// from the ring buffer, then stream live events. Client→server frames are
// session.Command messages (prompt / permission / interrupt / set_model).
//
// The claude process is untouched by this connection's lifecycle: when the phone
// backgrounds and the socket dies, the session keeps running and the next
// connection resumes from its last-seen seq.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.mgr.Get(r.PathValue("id"))
	if !ok {
		http.Error(w, "no such session", http.StatusNotFound)
		return
	}

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"}, // same-origin PWA; tailnet is the perimeter
	})
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(16 << 20) // attachments/tool inputs can be large

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	hello, backlog, sub := sess.Attach(parseSince(r))
	defer sess.Detach(sub)

	// Reader goroutine: client commands → session.
	go func() {
		defer cancel()
		for {
			var cmd session.Command
			if err := wsjson.Read(ctx, c, &cmd); err != nil {
				return
			}
			s.dispatch(sess, cmd)
		}
	}()

	// Writer (this goroutine): hello, backlog, then live events. coder/websocket
	// requires a single writer, so all writes happen here.
	if err := wsjson.Write(ctx, c, hello); err != nil {
		return
	}
	for _, ev := range backlog {
		if err := wsjson.Write(ctx, c, ev); err != nil {
			return
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case ev, open := <-sub.Events():
			if !open {
				// Session ended or this subscriber was dropped for lag.
				c.Close(websocket.StatusGoingAway, "session closed")
				return
			}
			if err := wsjson.Write(ctx, c, ev); err != nil {
				return
			}
		}
	}
}

func (s *Server) dispatch(sess *session.Session, cmd session.Command) {
	var err error
	switch cmd.T {
	case session.CmdPrompt:
		var content any
		if len(cmd.Attachments) > 0 {
			content = s.buildContent(sess.Cwd, cmd.Text, cmd.Attachments)
		}
		err = sess.Prompt(cmd.Text, content, cmd.Attachments)
	case session.CmdPermission:
		err = sess.ResolvePermission(cmd.RequestID, cmd.Behavior, cmd.Always, cmd.Answers)
	case session.CmdInterrupt:
		err = sess.Interrupt()
	case session.CmdSetModel:
		err = sess.SetModel(cmd.Model)
	case session.CmdSetMode:
		err = s.setMode(sess, cmd.Mode)
	case session.CmdCancelQueued:
		sess.CancelQueued(cmd.QueueID)
	case session.CmdAddProject:
		err = s.addProject(sess, cmd.Path)
	case session.CmdStartLoop:
		if cmd.Loop == nil {
			err = errors.New("start_loop: no loop given")
			break
		}
		err = sess.StartLoop(*cmd.Loop)
	case session.CmdStopLoop:
		sess.StopLoop("you stopped it")
	default:
		err = errors.New("unknown command: " + cmd.T)
	}
	if err != nil {
		log.Printf("ws dispatch %s: %v", cmd.T, err)
		// And say so on screen, not only in the journal. A refused command that
		// looks identical to a delivered one is how a rule becomes a bug report:
		// the composer keeps showing the old mode and nothing explains why.
		sess.ReportError(err.Error())
	}
}

// setMode changes a session's permission mode, by the only route that works for
// the mode being asked for.
//
// Every mode except one is a live control request the running CLI accepts.
// bypassPermissions is not: the CLI refuses it on a running process ("Cannot set
// permission mode to bypassPermissions because the session was not launched with
// --dangerously-skip-permissions"), so it has to be a spawn flag, so entering it
// replaces the process. The conversation survives on --resume, exactly as it does
// for an effort change or a share's tool restrictions.
//
// Leaving it stays the live path, verified against the same CLI: a running
// bypassed session accepts set_permission_mode auto and answers success. That
// asymmetry is worth keeping rather than respawning both ways, because getting
// OUT of Yolo should be instant and should never depend on a respawn working.
func (s *Server) setMode(sess *session.Session, mode string) error {
	if mode != session.BypassPermissionMode {
		return sess.SetPermissionMode(mode)
	}
	if sess.Mode() == session.BypassPermissionMode {
		return nil // already there; a respawn would cost the turn for nothing
	}
	// Checked here rather than after, because the respawn produces a Session with
	// no guard on it and the check would then always pass. Session.SetPermissionMode
	// makes the same refusal on the live path, for the orders that go that way.
	if sess.Guarded() {
		return errors.New("this session is shared, so it cannot run without permission prompts: stop sharing it first")
	}
	ctx, cancel := context.WithTimeout(s.baseCtx, 45*time.Second)
	defer cancel()
	next, err := s.mgr.RestartWithMode(ctx, sess.ID, mode, loadTranscriptTurns)
	if err != nil {
		return err
	}
	s.armSession(next)
	return nil
}
