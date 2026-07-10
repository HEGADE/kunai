package server

import (
	"context"
	"errors"
	"log"
	"net/http"

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
		err = sess.Prompt(cmd.Text, content)
	case session.CmdPermission:
		err = sess.ResolvePermission(cmd.RequestID, cmd.Behavior, cmd.Always)
	case session.CmdInterrupt:
		err = sess.Interrupt()
	case session.CmdSetModel:
		err = sess.SetModel(cmd.Model)
	default:
		err = errors.New("unknown command: " + cmd.T)
	}
	if err != nil {
		log.Printf("ws dispatch %s: %v", cmd.T, err)
	}
}
