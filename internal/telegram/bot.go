package telegram

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/hegade/kunai/internal/session"
)

// Sessions is the bot's view of kunai's session manager: the few things driving
// a conversation from a chat actually needs. An interface rather than the
// concrete *session.Manager so the bot's logic can be tested without spawning a
// real claude process.
type Sessions interface {
	Create(ctx context.Context, opts session.CreateOptions) (*session.Session, error)
	Get(id string) (*session.Session, bool)
	List() []session.Meta
	Close(id string)
}

// pollBackoff is how long to wait after a failed poll. Telegram being briefly
// unreachable is ordinary (a laptop changing networks), so it retries quietly
// rather than giving up on the bot for the life of the process.
const pollBackoff = 5 * time.Second

// Bot connects a Telegram chat to kunai's sessions.
type Bot struct {
	cfg Config
	api *Client
	mgr Sessions
	st  *state

	mu       sync.Mutex
	watchers map[int64]context.CancelFunc // chat id -> stop its event pump
}

// New builds a bot. Call Run to start it.
func New(cfg Config, mgr Sessions) *Bot {
	return &Bot{
		cfg:      cfg,
		api:      NewClient(cfg.Token),
		mgr:      mgr,
		st:       loadState(cfg.DataDir),
		watchers: map[int64]context.CancelFunc{},
	}
}

// Run polls for updates until ctx is cancelled. It is the only long-lived
// goroutine the bot owns; everything else hangs off a chat.
func (b *Bot) Run(ctx context.Context) {
	log.Printf("telegram: bot started (%d allowed user(s))", len(b.cfg.Allowed))
	// Re-attach chats that were driving a session before the restart, so a
	// reboot does not silently strand a conversation.
	b.resumeWatchers(ctx)

	for {
		if ctx.Err() != nil {
			return
		}
		updates, err := b.api.GetUpdates(ctx, b.st.offset())
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("telegram: poll failed: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(pollBackoff):
			}
			continue
		}
		for _, u := range updates {
			// Advance past this update before handling it. A handler that panics
			// or a restart mid-handle must not replay the same command forever.
			b.st.setOffset(u.UpdateID + 1)
			b.handle(ctx, u)
		}
	}
}

// handle dispatches one update. Everything is gated on the allow list first:
// talking to this bot is equivalent to a shell on the machine.
func (b *Bot) handle(ctx context.Context, u Update) {
	switch {
	case u.Message != nil:
		if !b.authorized(ctx, u.Message.Chat.ID, u.Message.From) {
			return
		}
		b.handleMessage(ctx, u.Message)
	case u.Callback != nil:
		b.handleCallback(ctx, u.Callback)
	}
}

// authorized reports whether the sender may drive kunai, and tells them when
// they may not. Silence would look like a broken bot; the refusal names no
// detail about the machine.
func (b *Bot) authorized(ctx context.Context, chatID int64, from *User) bool {
	if from != nil && b.cfg.permits(from.ID) {
		return true
	}
	id := int64(0)
	if from != nil {
		id = from.ID
	}
	log.Printf("telegram: refused user %d in chat %d", id, chatID)
	b.say(ctx, chatID, "Not authorised. Add your Telegram user id to kunai's allow list.")
	return false
}

func (b *Bot) handleMessage(ctx context.Context, m *Message) {
	cmd := ParseCommand(m.Text)
	if cmd.IsPrompt() {
		b.prompt(ctx, m.Chat.ID, cmd.Arg)
		return
	}
	switch cmd.Name {
	case CmdStart, CmdHelp:
		b.say(ctx, m.Chat.ID, HelpText)
	case CmdNew:
		b.newSession(ctx, m.Chat.ID, cmd.Arg)
	case CmdSessions:
		b.listSessions(ctx, m.Chat.ID)
	case CmdUse:
		b.useSession(ctx, m.Chat.ID, cmd.Arg)
	case CmdStatus:
		b.status(ctx, m.Chat.ID)
	case CmdStop:
		b.withSession(ctx, m.Chat.ID, func(s *session.Session) {
			if err := s.Interrupt(); err != nil {
				b.say(ctx, m.Chat.ID, "Could not stop it: "+err.Error())
				return
			}
			b.say(ctx, m.Chat.ID, "Stopped.")
		})
	case CmdEnd:
		b.endSession(ctx, m.Chat.ID)
	default:
		b.say(ctx, m.Chat.ID, "Unknown command. Send /help.")
	}
}

// handleCallback answers a permission prompt from an inline button.
func (b *Bot) handleCallback(ctx context.Context, q *CallbackQuery) {
	var chatID int64
	if q.Message != nil {
		chatID = q.Message.Chat.ID
	}
	if !b.authorized(ctx, chatID, q.From) {
		_ = b.api.AnswerCallback(ctx, q.ID, "Not authorised")
		return
	}
	action, requestID, ok := ParseCallback(q.Data)
	if !ok {
		_ = b.api.AnswerCallback(ctx, q.ID, "")
		return
	}
	sess, found := b.current(chatID)
	if !found {
		_ = b.api.AnswerCallback(ctx, q.ID, "That session is gone")
		return
	}
	behavior := "allow"
	answer := "Approved"
	if action == CallbackDeny {
		behavior, answer = "deny", "Denied"
	}
	if err := sess.ResolvePermission(requestID, behavior, false, nil); err != nil {
		_ = b.api.AnswerCallback(ctx, q.ID, "Could not answer")
		return
	}
	_ = b.api.AnswerCallback(ctx, q.ID, answer)
	// Replace the buttons with the outcome, so the message reads as settled
	// rather than still waiting.
	if q.Message != nil {
		_ = b.api.Edit(ctx, chatID, q.Message.MessageID, answer+".", nil)
	}
}

// --- commands ---

func (b *Bot) newSession(ctx context.Context, chatID int64, dir string) {
	if dir == "" {
		b.say(ctx, chatID, "Give it a directory: /new /path/to/project")
		return
	}
	// Session start blocks on the CLI handshake, so it gets its own budget
	// rather than inheriting the poll loop's.
	startCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
	defer cancel()

	sess, err := b.mgr.Create(startCtx, session.CreateOptions{Cwd: dir})
	if err != nil {
		b.say(ctx, chatID, "Could not start it: "+err.Error())
		return
	}
	b.bind(ctx, chatID, sess)
	b.say(ctx, chatID, "Started in "+dir+". Send a message to prompt it.")
}

func (b *Bot) listSessions(ctx context.Context, chatID int64) {
	metas := b.mgr.List()
	if len(metas) == 0 {
		b.say(ctx, chatID, "Nothing running. Start one with /new <path>.")
		return
	}
	bound := b.st.boundTo(chatID)
	var sb strings.Builder
	sb.WriteString("Running sessions:\n")
	for _, m := range metas {
		mark := " "
		if m.ID == bound {
			mark = "*"
		}
		fmt.Fprintf(&sb, "\n%s %s\n  %s (%s)", mark, m.ID, m.Cwd, m.State)
	}
	sb.WriteString("\n\nSwitch with /use <id>.")
	b.say(ctx, chatID, sb.String())
}

func (b *Bot) useSession(ctx context.Context, chatID int64, id string) {
	if id == "" {
		b.say(ctx, chatID, "Which one? /use <id>, or /sessions to list them.")
		return
	}
	sess, ok := b.mgr.Get(id)
	if !ok {
		b.say(ctx, chatID, "No session with that id.")
		return
	}
	b.bind(ctx, chatID, sess)
	b.say(ctx, chatID, "Now driving "+id+".")
}

func (b *Bot) status(ctx context.Context, chatID int64) {
	b.withSession(ctx, chatID, func(s *session.Session) {
		m := s.Meta()
		b.say(ctx, chatID, fmt.Sprintf("%s\n%s\n%s, on %s", m.ID, m.Cwd, m.State, m.CLI))
	})
}

func (b *Bot) endSession(ctx context.Context, chatID int64) {
	id := b.st.boundTo(chatID)
	if id == "" {
		b.say(ctx, chatID, "This chat is not driving a session.")
		return
	}
	b.stopWatching(chatID)
	b.mgr.Close(id)
	b.st.unbind(chatID)
	b.say(ctx, chatID, "Closed.")
}

func (b *Bot) prompt(ctx context.Context, chatID int64, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	b.withSession(ctx, chatID, func(s *session.Session) {
		if err := s.Prompt(text, nil, nil); err != nil {
			b.say(ctx, chatID, "Could not send it: "+err.Error())
			return
		}
		_ = b.api.SendChatAction(ctx, chatID, "typing")
	})
}

// withSession runs fn against the chat's session, or explains why it cannot.
func (b *Bot) withSession(ctx context.Context, chatID int64, fn func(*session.Session)) {
	sess, ok := b.current(chatID)
	if !ok {
		b.say(ctx, chatID, "No session yet. Start one with /new <path>.")
		return
	}
	fn(sess)
}

// current resolves the chat's bound session, clearing the binding when the
// session is gone so the chat is not stuck pointing at a corpse.
func (b *Bot) current(chatID int64) (*session.Session, bool) {
	id := b.st.boundTo(chatID)
	if id == "" {
		return nil, false
	}
	sess, ok := b.mgr.Get(id)
	if !ok {
		b.st.unbind(chatID)
		return nil, false
	}
	return sess, true
}

// --- event pump ---

// bind points a chat at a session and starts streaming that session's events
// into the chat.
func (b *Bot) bind(ctx context.Context, chatID int64, sess *session.Session) {
	b.st.bind(chatID, sess.Meta().ID)
	b.watch(ctx, chatID, sess)
}

// resumeWatchers re-attaches every chat that was driving a session before the
// process restarted.
func (b *Bot) resumeWatchers(ctx context.Context) {
	b.st.mu.Lock()
	bound := make(map[string]string, len(b.st.Bound))
	for k, v := range b.st.Bound {
		bound[k] = v
	}
	b.st.mu.Unlock()

	for chatKey, sessionID := range bound {
		sess, ok := b.mgr.Get(sessionID)
		if !ok {
			continue
		}
		var chatID int64
		if _, err := fmt.Sscanf(chatKey, "%d", &chatID); err != nil {
			continue
		}
		b.watch(ctx, chatID, sess)
	}
}

// watch pumps one session's events into one chat until the chat rebinds, the
// session ends, or the process stops.
func (b *Bot) watch(parent context.Context, chatID int64, sess *session.Session) {
	b.stopWatching(chatID)

	ctx, cancel := context.WithCancel(parent)
	b.mu.Lock()
	b.watchers[chatID] = cancel
	b.mu.Unlock()

	// Attach from the live edge: a chat joining a session mid-flight wants what
	// happens next, not a replay of everything it missed.
	hello, _, sub := sess.Attach(^uint64(0))
	_ = hello

	go func() {
		defer sess.Detach(sub)
		reply := newStream(b.api, chatID)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, open := <-sub.Events():
				if !open {
					b.say(ctx, chatID, "That session ended.")
					return
				}
				b.dispatchEvent(ctx, chatID, reply, ev)
			}
		}
	}()
}

// dispatchEvent turns one session event into chat output. Assistant prose grows
// the current reply message; everything else is its own message.
func (b *Bot) dispatchEvent(ctx context.Context, chatID int64, reply *stream, ev session.AppEvent) {
	switch ev.T {
	case session.EvDelta:
		reply.Append(ctx, ev.Text)
		return
	case session.EvResult:
		// The turn is over: land the reply before reporting how it went.
		_ = reply.Flush(ctx)
		reply.Reset()
	}

	out, ok := RenderEvent(ev, b.cfg.Policy)
	if !ok {
		return
	}
	if out.Stream {
		// The complete assistant message. Deltas may already have painted it,
		// so this only matters when they did not arrive.
		if !reply.Active() {
			reply.Append(ctx, out.Text)
			_ = reply.Flush(ctx)
			reply.Reset()
		}
		return
	}
	b.sayWith(ctx, chatID, out.Text, out.Keyboard)
}

func (b *Bot) stopWatching(chatID int64) {
	b.mu.Lock()
	if cancel, ok := b.watchers[chatID]; ok {
		cancel()
		delete(b.watchers, chatID)
	}
	b.mu.Unlock()
}

// --- helpers ---

func (b *Bot) say(ctx context.Context, chatID int64, text string) {
	b.sayWith(ctx, chatID, text, nil)
}

func (b *Bot) sayWith(ctx context.Context, chatID int64, text string, kb *InlineKeyboard) {
	if text == "" {
		return
	}
	var opts *SendOptions
	if kb != nil {
		opts = &SendOptions{Keyboard: kb}
	}
	// Send on a context that survives the caller: a message explaining that
	// something failed must not be cancelled by the same failure.
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	if _, err := b.api.Send(sendCtx, chatID, text, opts); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("telegram: send to %d failed: %v", chatID, err)
	}
}
