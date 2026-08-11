package server

// The guest's websocket: what a shared session sends out, and the very short list
// of what it will take in.
//
// This deliberately does NOT reuse dispatch (ws.go). That switch has nine
// commands, every one of them mutating, and no notion of who is asking. Reusing
// it and subtracting would mean a command added later is available to guests
// until somebody remembers to take it away. Here the list is positive: three
// commands, and anything else is refused whatever it is.

import (
	"context"
	"errors"
	"strings"
	"time"

	"net/http"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/hegade/kunai/internal/session"
	"github.com/hegade/kunai/internal/share"
)

// guestCommands is everything a guest may send. An allowlist, not a denylist.
//
// What is missing matters more than what is here:
//   - "permission" is the authority that makes every other guard moot. A guest who
//     can answer can_use_tool simply approves whatever the path guard denied.
//     There is no tier in which they may send it.
//   - "set_mode" would move the session to acceptEdits and disarm the gate.
//   - "add_project" takes an arbitrary filesystem path off the wire and hands it
//     to the model as a working codebase: a one-frame escape from the roots.
//   - "set_model", "start_loop" and "stop_loop" spend the owner's quota on work
//     the owner did not ask for.
var guestCommands = map[string]bool{
	session.CmdPrompt:       true,
	session.CmdInterrupt:    true,
	session.CmdCancelQueued: true,
}

// shareWSPing keeps a guest's socket alive through a proxy that would otherwise
// idle it out mid-turn, and is how expiry is noticed on a quiet connection.
const shareWSPing = 25 * time.Second

func (g *shareGate) handleWS(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	sh, err := g.shares.Get(token)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	sess, ok := g.sessions.Get(sh.SessionID)
	if !ok {
		http.Error(w, "that session has ended", http.StatusGone)
		return
	}
	device := deviceOfSocket(r)

	// Bounded before the socket is accepted, so a refusal costs a handshake rather
	// than a subscriber on the owner's session.
	if !g.enterGuest() {
		http.Error(w, "too many people are watching this machine's shares right now", http.StatusServiceUnavailable)
		return
	}
	defer g.leaveGuest()

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Unlike /ws/app, this is NOT a wildcard. The tailnet is not the perimeter
		// out here, and the share token travels in the URL where a hostile page
		// could read it from a referrer or a history entry. Only the gate's own
		// page may open this socket.
		OriginPatterns: originsFor(r),
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	// Far smaller than the owner's 16MB: a guest sends prose, never an attachment.
	conn.SetReadLimit(64 << 10)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// The floor is taken from the stored share, never from the query string. The
	// ring only offers since(n), so a guest handed a floor could simply ask for a
	// lower one and read everything the owner meant to keep back.
	hello, backlog, sub := sess.Attach(maxSeq(sh.FromSeq, parseSince(r)))
	defer sess.Detach(sub)

	go g.readGuest(ctx, cancel, conn, sess, token, device)

	if err := writeGuest(ctx, conn, redactHello(hello, sh)); err != nil {
		return
	}
	for _, ev := range backlog {
		out, keep := redactEvent(ev, sh)
		if !keep {
			continue
		}
		if err := writeGuest(ctx, conn, out); err != nil {
			return
		}
	}

	ping := time.NewTicker(shareWSPing)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			// Expiry is enforced per frame, not once at connect: a guest holding an
			// open socket when the clock runs out has to be disconnected, or the
			// expiry only bounds how long they can take to arrive.
			if _, err := g.shares.Get(token); err != nil {
				_ = conn.Close(websocket.StatusNormalClosure, "this link has expired")
				return
			}
			if err := conn.Ping(ctx); err != nil {
				return
			}
		case ev, open := <-sub.Events():
			if !open {
				_ = conn.Close(websocket.StatusGoingAway, "the session ended")
				return
			}
			if _, err := g.shares.Get(token); err != nil {
				_ = conn.Close(websocket.StatusNormalClosure, "this link has been revoked")
				return
			}
			out, keep := redactEvent(ev, sh)
			if !keep {
				continue
			}
			if err := writeGuest(ctx, conn, out); err != nil {
				return
			}
		}
	}
}

// readGuest is the only place a guest's bytes turn into an action.
func (g *shareGate) readGuest(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, sess *session.Session, token, device string) {
	defer cancel()
	for {
		var cmd session.Command
		if err := wsjson.Read(ctx, conn, &cmd); err != nil {
			return
		}
		if err := g.runGuestCommand(sess, token, device, cmd); err != nil {
			// Reported to the guest rather than logged and swallowed: they are not
			// looking at the machine's logs, and a prompt that silently did nothing
			// reads as kunai being broken.
			_ = wsjson.Write(ctx, conn, session.AppEvent{T: session.EvError, Message: err.Error()})
		}
	}
}

// runGuestCommand applies the allowlist and the share's own limits. Every path
// re-reads the share, because a revoke or an expiry between two frames has to
// take effect on the second one.
func (g *shareGate) runGuestCommand(sess *session.Session, token, device string, cmd session.Command) error {
	if !guestCommands[cmd.T] {
		return errors.New("that is not something a shared link can do")
	}
	sh, err := g.shares.Get(token)
	if err != nil {
		return errors.New("this link is no longer live")
	}
	if !sh.MayPrompt(device, time.Now()) {
		if sh.Guest == nil {
			return errors.New("ask the owner to approve you before sending anything")
		}
		if left, capped := sh.TurnsLeft(); capped && left == 0 {
			return errors.New("this link has used all the turns it was given")
		}
		return errors.New("this link cannot send right now")
	}

	switch cmd.T {
	case session.CmdPrompt:
		// Attachments are resolved against what THIS link was issued, never
		// merely validated in shape. The uploads directory holds the owner's
		// files too, and an id is all that names one, so an unchecked id is a way
		// to have somebody else's picture read into the conversation and handed
		// back. See shareupload.go.
		atts, content, err := g.guestAttachments(token, sess.Cwd, cmd.Text, cmd.Attachments)
		if err != nil {
			return err
		}
		// An empty prompt is rejected by the CLI and leaves the turn sitting on
		// "Working..." forever, having also spent one of the guest's turns. The
		// same trap the Telegram adapter had to close.
		if strings.TrimSpace(cmd.Text) == "" && len(cmd.Attachments) == 0 {
			return errors.New("type something to send")
		}
		// The cap is spent under the store's own lock, before the turn is sent, so
		// two prompts arriving together cannot both take the last remaining turn.
		if err := g.shares.SpendTurn(token, device); err != nil {
			return err
		}
		if content != nil {
			return sess.PromptWithFilesFrom(cmd.Text, content, atts, session.FromGuest)
		}
		return sess.PromptFrom(cmd.Text, session.FromGuest)

	case session.CmdInterrupt:
		// Narrowed deliberately. Session.Interrupt drops the whole queue and stops
		// a running loop, so handing a guest the ordinary one would let them end an
		// overnight run and discard prompts the owner queued. A guest may stop what
		// a guest started, and nothing else.
		return sess.InterruptFrom(session.FromGuest)

	case session.CmdCancelQueued:
		// Same shape at a smaller scale: the queue ids are broadcast to everyone
		// attached, so without an origin check a guest could cancel the owner's.
		return sess.CancelQueuedFrom(cmd.QueueID, session.FromGuest)
	}
	return nil
}

// redactHello strips the hello frame down to what a guest is entitled to. The
// full one carries cwd, the account name, the machine's project list and a running
// loop's whole prompt, none of which is theirs to see.
func redactHello(hello session.AppEvent, sh *share.Share) session.AppEvent {
	out := session.AppEvent{
		T:             session.EvHello,
		Epoch:         hello.Epoch,
		Title:         sh.Title,
		State:         hello.State,
		HighSeq:       hello.HighSeq,
		ContextTokens: hello.ContextTokens,
	}
	// Pending permission asks ride along only so the guest can see the session is
	// waiting on its owner; the ask itself is filtered like any other event.
	for _, p := range hello.Pending {
		if ev, keep := redactEvent(p, sh); keep {
			out.Pending = append(out.Pending, ev)
		}
	}
	for _, q := range hello.Queued {
		out.Queued = append(out.Queued, session.AppEvent{T: session.EvQueued, QueueID: q.QueueID, Text: q.Text, From: q.From})
	}
	return out
}

// redactEvent decides what may leave the machine, and is the single place that
// decision is made. The default is the conversation and the shape of the work:
// tool calls by name, never their arguments or their output.
//
// The risk being guarded is not really the source, which the guest can usually
// see anyway. It is the incidental spill: a config file the agent happened to
// read, a token a test echoed, an env dump in a debug command. None of that is
// something anyone would choose to publish, and a link on the internet is more
// exposed than a chat, not less.
func redactEvent(ev session.AppEvent, sh *share.Share) (session.AppEvent, bool) {
	if sh.FromSeq > 0 && ev.Seq > 0 && ev.Seq <= sh.FromSeq {
		return session.AppEvent{}, false
	}

	switch ev.T {
	case session.EvDelta, session.EvThinking, session.EvQueued,
		session.EvUnqueued, session.EvState, session.EvCompact, session.EvLoop,
		session.EvMode, session.EvError, session.EvPermissionResolved:
		return ev, true

	case session.EvUser:
		if sh.Detail.ToolInputs {
			return ev, true
		}
		// A guest keeps its own files. It just sent them, so hiding them means the
		// message it typed comes back without the picture that was the point of it.
		if ev.From == string(session.FromGuest) {
			return ev, true
		}
		// The owner's, no. A strict share withholds what a tool read, so passing
		// the owner's attachments through verbatim was inconsistent: the names
		// alone can say more than the conversation does.
		out := ev
		out.Attachments = nil
		return out, true

	case session.EvAssistant:
		if sh.Detail.ToolInputs {
			return ev, true
		}
		// Keep the text and the fact that a tool ran; drop the arguments, which for
		// an Edit are the contents of the file.
		out := ev
		out.Blocks = make([]session.AppBlock, 0, len(ev.Blocks))
		for _, b := range ev.Blocks {
			if b.Type == "tool_use" {
				b.Input = nil
			}
			out.Blocks = append(out.Blocks, b)
		}
		return out, true

	case session.EvToolResult:
		if !sh.Detail.ToolOutputs {
			// The call still appears, so the conversation reads correctly; only what
			// came back is withheld.
			return session.AppEvent{Seq: ev.Seq, T: ev.T, ToolUseID: ev.ToolUseID, IsError: ev.IsError,
				ParentToolUseID: ev.ParentToolUseID, Truncated: ev.Truncated}, true
		}
		return ev, true

	case session.EvPermission:
		// A guest may know the session is waiting on a decision, because otherwise
		// it looks stalled. They may not see what is being asked unless inputs are
		// shared, and they can never answer it.
		out := session.AppEvent{Seq: ev.Seq, T: ev.T, RequestID: ev.RequestID, ToolName: ev.ToolName,
			ToolUseID: ev.ToolUseID, PermTitle: ev.PermTitle, From: ev.From}
		if sh.Detail.ToolInputs {
			out.Input, out.Description = ev.Input, ev.Description
		}
		return out, true

	case session.EvResult:
		// Timing yes, money no: what the owner's account is spending is not part of
		// the conversation being shared.
		return session.AppEvent{Seq: ev.Seq, T: ev.T, IsError: ev.IsError, DurationMs: ev.DurationMs}, true

	case session.EvRateLimit:
		// The owner's quota is the owner's business.
		return session.AppEvent{}, false
	}
	return session.AppEvent{}, false
}

func writeGuest(ctx context.Context, conn *websocket.Conn, ev session.AppEvent) error {
	wctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return wsjson.Write(wctx, conn, ev)
}

func maxSeq(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

// originsFor limits who may open the guest socket to the origin the page itself
// was served from.
func originsFor(r *http.Request) []string {
	if h := r.Host; h != "" {
		return []string{h}
	}
	return []string{}
}
