package session

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/hegade/kunai/internal/claude"
)

// ringCapacity bounds per-session replay history in durable events (streaming
// deltas are never buffered, so this counts real turns: user/assistant/
// tool_result/state/result). Generous so any within-server-lifetime reconnect
// replays the whole conversation; a session that somehow exceeds it loses only
// its oldest events (a fresh --resume cold-start recovers from the transcript).
const ringCapacity = 8000

// subChanBuf is the per-Subscriber outbound buffer. A phone that can't keep up
// past this is dropped and must reconnect (which replays the gap).
const subChanBuf = 256

// driver is the subset of claude.Session the app layer uses. Extracting it as an
// interface keeps Session unit-testable with a fake in place of a real process.
type driver interface {
	Events() <-chan claude.Event
	SendUser(content any) error
	SendUserText(text string) error
	Resolve(requestID string, r claude.PermissionResult) error
	Interrupt() error
	SetModel(model string) error
	SetPermissionMode(mode string) error
	Close() error
}

// Session is one app-facing conversation: a single long-lived claude process
// plus the sequencing, buffering, and fan-out that let many phone connections
// attach and detach without disturbing it.
type Session struct {
	ID        string    // our stable handle (used in URLs)
	Cwd       string    //
	CreatedAt time.Time //

	drv driver

	mu              sync.Mutex
	seq             uint64
	model           string
	mode            string // permission mode
	effort          string // reasoning effort (spawn-time; changed by restart)
	title           string
	claudeSessionID string // CLI-assigned id, for --resume cold-start
	state           string
	contextTokens   int64 // context-window occupancy from the latest result (or seeded on resume)
	buf             *ring
	subs            map[*Subscriber]struct{}
	pending         map[string]AppEvent // unresolved permission asks, keyed by request_id
	suggestionByReq map[string]json.RawMessage
	closed          bool
	done            chan struct{} // closed when the driver has ended
	notify          func(kind, detail string)
	onRateLimit     func(window string, resetsAt int64)
}

// SetNotifier registers a callback invoked when the session needs attention
// (a permission ask, or a turn finishing). The client's service worker decides
// whether to actually surface it (suppressed while a Kunai window is focused).
func (s *Session) SetNotifier(fn func(kind, detail string)) {
	s.mu.Lock()
	s.notify = fn
	s.mu.Unlock()
}

// SetRateLimitHandler registers a callback fired when the CLI reports a usage
// window's reset time (once per turn). Used by the scheduler to fire jobs
// relative to the quota reset.
func (s *Session) SetRateLimitHandler(fn func(window string, resetsAt int64)) {
	s.mu.Lock()
	s.onRateLimit = fn
	s.mu.Unlock()
}

// notifyAttention fires the notifier whenever the session needs attention (a
// permission ask or a finished turn). It always fires: whether a notification is
// actually shown is decided client-side by the service worker, which suppresses
// it when a Kunai window is focused (so you are not pinged for what you are
// already watching). The server can only see WebSocket attachment, not tab
// focus — and on desktop the socket stays open across tab switches — so gating
// here wrongly swallowed the wake-up whenever you switched tabs.
func (s *Session) notifyAttention(kind, detail string) {
	s.mu.Lock()
	fn := s.notify
	s.mu.Unlock()
	if fn != nil {
		go fn(kind, detail)
	}
}

// Subscriber is one attached phone connection's live event feed.
type Subscriber struct {
	ch chan AppEvent
}

// Events is the live feed; it is closed when the subscriber is detached, dropped
// for lag, or the session ends.
func (sub *Subscriber) Events() <-chan AppEvent { return sub.ch }

// newSession wraps a started driver and begins pumping its events.
func newSession(id, cwd, title string, drv driver) *Session {
	s := &Session{
		ID:              id,
		Cwd:             cwd,
		CreatedAt:       time.Now(),
		drv:             drv,
		title:           title,
		state:           StateStarting,
		mode:            DefaultPermissionMode, // Create overrides when asked for another
		buf:             newRing(ringCapacity),
		subs:            make(map[*Subscriber]struct{}),
		pending:         make(map[string]AppEvent),
		suggestionByReq: make(map[string]json.RawMessage),
		done:            make(chan struct{}),
	}
	go s.pump()
	return s
}

// Done is closed when the underlying claude process has ended and the session is
// no longer usable.
func (s *Session) Done() <-chan struct{} { return s.done }

// SeedTurn is a prior conversation turn loaded from a transcript when resuming.
type SeedTurn struct {
	Role        string       // "user" | "assistant" | "tool_result"
	Text        string       // user text, or tool_result output
	Blocks      []AppBlock   // assistant content blocks
	ToolUseID   string       // tool_result correlation
	IsError     bool         // tool_result
	Attachments []Attachment // user: files sent with the prompt
}

// Seed pre-populates the replay buffer with transcript history so a resumed
// session opens with its past conversation visible. Call before clients attach.
func (s *Session) Seed(turns []SeedTurn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range turns {
		var ev AppEvent
		switch t.Role {
		case "user":
			ev = AppEvent{T: EvUser, Text: t.Text, Attachments: t.Attachments}
		case "tool_result":
			ev = AppEvent{T: EvToolResult, ToolUseID: t.ToolUseID, Content: t.Text, IsError: t.IsError}
		default:
			ev = AppEvent{T: EvAssistant, Blocks: t.Blocks}
		}
		s.emitLocked(s.sequenceLocked(ev))
	}
}

// pump translates driver events into app events until the driver closes.
func (s *Session) pump() {
	for ev := range s.drv.Events() {
		switch ev.Kind {
		case claude.EventInit:
			s.mu.Lock()
			s.claudeSessionID = ev.SessionID
			if ev.Model != "" {
				s.model = ev.Model
			}
			booting := s.state == StateStarting
			s.mu.Unlock()
			if booting {
				s.setState(StateIdle)
			}

		case claude.EventTextDelta:
			s.broadcast(AppEvent{T: EvDelta, Text: ev.Text})

		case claude.EventThinking:
			s.broadcast(AppEvent{T: EvThinking, Text: ev.Text})

		case claude.EventAssistant:
			// Each assistant message is one model call, so its usage reports the
			// context actually sent for that call. Track the newest as the current
			// context-window occupancy (the result frame's usage is cumulative over
			// the whole turn and would overcount a long tool loop).
			ctx := ev.Assistant.Usage.ContextTokens()
			if ctx > 0 {
				s.mu.Lock()
				s.contextTokens = ctx
				s.mu.Unlock()
			}
			s.broadcast(AppEvent{T: EvAssistant, Blocks: toAppBlocks(ev.Assistant), ContextTokens: ctx})

		case claude.EventPermission:
			s.onPermission(ev.Permission)

		case claude.EventToolResult:
			tr := ev.ToolResult
			s.broadcast(AppEvent{
				T:         EvToolResult,
				ToolUseID: tr.ToolUseID,
				Content:   tr.Content,
				IsError:   tr.IsError,
				Truncated: tr.Truncated,
			})

		case claude.EventResult:
			s.setState(StateIdle)
			s.broadcast(parseResult(ev.Raw))
			s.notifyAttention("done", "")

		case claude.EventRateLimit:
			s.mu.Lock()
			fn := s.onRateLimit
			s.mu.Unlock()
			if fn != nil && ev.ResetsAt > 0 {
				go fn(ev.Window, ev.ResetsAt)
			}
			// Surface to the chat so it can offer "schedule after reset".
			s.broadcast(AppEvent{T: EvRateLimit, Window: ev.Window, ResetsAt: ev.ResetsAt, LimitStatus: ev.LimitStatus})

		case claude.EventError:
			s.broadcast(AppEvent{T: EvError, Message: ev.Err.Error()})
		}
	}
	// Driver ended.
	s.mu.Lock()
	s.closed = true
	subs := s.subs
	s.subs = make(map[*Subscriber]struct{})
	s.mu.Unlock()
	for sub := range subs {
		close(sub.ch)
	}
	close(s.done)
}

func (s *Session) onPermission(ask *claude.PermissionAsk) {
	ev := AppEvent{
		T:           EvPermission,
		RequestID:   ask.RequestID,
		ToolName:    ask.ToolName,
		ToolUseID:   ask.ToolUseID,
		Input:       ask.Input,
		PermTitle:   ask.Title,
		Description: ask.Description,
		Suggestions: ask.Suggestions,
	}
	s.mu.Lock()
	if s.state != StateAwaiting {
		s.state = StateAwaiting
		stateEv := s.sequenceLocked(AppEvent{T: EvState, State: StateAwaiting})
		s.emitLocked(stateEv)
	}
	s.suggestionByReq[ask.RequestID] = ask.Suggestions
	// Record the sequenced copy in pending so reconnecting clients can re-arm it.
	sequenced := s.sequenceLocked(ev)
	s.pending[ask.RequestID] = sequenced
	s.emitLocked(sequenced)
	s.mu.Unlock()

	s.notifyAttention("permission", ask.ToolName)
}

// --- commands (client → session) ---

// Prompt sends a user turn. The user's text and any attachments are echoed into
// the sequenced event stream so reconnects and reloads replay the full
// conversation, not just Claude's side of it. content carries the attachments to
// the CLI; atts is the display copy, so the message shows what was sent with it.
func (s *Session) Prompt(text string, content any, atts []Attachment) error {
	s.broadcast(AppEvent{T: EvUser, Text: text, Attachments: atts})
	s.setState(StateRunning)
	if content != nil {
		return s.drv.SendUser(content)
	}
	return s.drv.SendUserText(text)
}

// ResolvePermission answers a pending permission ask. When always is true and
// the verdict is allow, the ask's own suggestions are persisted as session rules
// so the same tool won't prompt again.
func (s *Session) ResolvePermission(requestID, behavior string, always bool, answers map[string]string) error {
	s.mu.Lock()
	ask := s.pending[requestID]
	suggestions := s.suggestionByReq[requestID]
	delete(s.pending, requestID)
	delete(s.suggestionByReq, requestID)
	morePending := len(s.pending) > 0
	s.mu.Unlock()

	result := claude.PermissionResult{Behavior: behavior, ToolUseID: ask.ToolUseID}
	if behavior == "allow" {
		// Echo the original tool input back — the CLI executes with updatedInput,
		// so an allow that omits it runs the tool with nothing. For AskUserQuestion
		// the user's selections ride along as an `answers` field the tool reads.
		result.UpdatedInput = ask.Input
		if len(answers) > 0 {
			result.UpdatedInput = mergeAnswers(ask.Input, answers)
		}
		if always && len(suggestions) > 0 {
			var ups []claude.PermissionUpdate
			if err := json.Unmarshal(suggestions, &ups); err == nil {
				result.UpdatedPermissions = ups
			}
		}
	} else {
		result.Message = "denied by user"
	}
	if err := s.drv.Resolve(requestID, result); err != nil {
		return err
	}
	s.broadcast(AppEvent{T: EvPermissionResolved, RequestID: requestID, Behavior: behavior})
	if !morePending {
		s.setState(StateRunning)
	}
	return nil
}

// mergeAnswers returns the tool input with an `answers` field added (the
// AskUserQuestion contract: question text -> chosen answer). If the input isn't
// a JSON object it's returned unchanged.
func mergeAnswers(input json.RawMessage, answers map[string]string) json.RawMessage {
	var m map[string]any
	if len(input) == 0 || json.Unmarshal(input, &m) != nil {
		return input
	}
	m["answers"] = answers
	if b, err := json.Marshal(m); err == nil {
		return b
	}
	return input
}

// Interrupt aborts the current turn.
func (s *Session) Interrupt() error {
	err := s.drv.Interrupt()
	s.setState(StateIdle)
	return err
}

// SetModel switches the model for subsequent turns.
func (s *Session) SetModel(model string) error {
	s.mu.Lock()
	s.model = model
	s.mu.Unlock()
	return s.drv.SetModel(model)
}

// SetPermissionMode switches the permission mode ("default", "acceptEdits",
// "auto", "plan", …).
func (s *Session) SetPermissionMode(mode string) error {
	s.mu.Lock()
	s.mode = mode
	s.mu.Unlock()
	return s.drv.SetPermissionMode(mode)
}

// Close terminates the session.
func (s *Session) Close() {
	s.drv.Close()
}

// FailStart surfaces an async boot failure to attached clients, then ends the
// session.
func (s *Session) FailStart(msg string) {
	s.broadcast(AppEvent{T: EvError, Message: "claude failed to start: " + msg})
	s.Close()
}

// --- attach / fan-out ---

// attach registers a Subscriber and returns a hello frame plus the backlog of
// events after afterSeq, then live events on ch. detach removes the Subscriber.
func (s *Session) Attach(afterSeq uint64) (hello AppEvent, backlog []AppEvent, sub *Subscriber) {
	s.mu.Lock()
	defer s.mu.Unlock()

	pending := make([]AppEvent, 0, len(s.pending))
	for _, p := range s.pending {
		pending = append(pending, p)
	}
	hello = AppEvent{
		T:             EvHello,
		ID:            s.ID,
		Cwd:           s.Cwd,
		Model:         s.model,
		Title:         s.title,
		State:         s.state,
		Mode:          s.mode,
		Effort:        s.effort,
		HighSeq:       s.seq,
		ContextTokens: s.contextTokens,
		Pending:       pending,
	}
	backlog = s.buf.since(afterSeq)

	sub = &Subscriber{ch: make(chan AppEvent, subChanBuf)}
	if !s.closed {
		s.subs[sub] = struct{}{}
	} else {
		close(sub.ch)
	}
	return hello, backlog, sub
}

func (s *Session) Detach(sub *Subscriber) {
	s.mu.Lock()
	if _, ok := s.subs[sub]; ok {
		delete(s.subs, sub)
		close(sub.ch)
	}
	s.mu.Unlock()
}

// --- internals ---

func (s *Session) broadcast(ev AppEvent) {
	s.mu.Lock()
	// Streaming tokens (delta/thinking) are transient: a single turn emits
	// hundreds of them, and they are fully superseded by the committed assistant
	// event. Fan them out live for the active client, but never put them in the
	// replay ring — buffering them would evict real conversation history within a
	// few turns (the bug where reopening a session showed only recent messages).
	if ev.T == EvDelta || ev.T == EvThinking {
		s.emitLocked(ev)
		s.mu.Unlock()
		return
	}
	sequenced := s.sequenceLocked(ev)
	s.emitLocked(sequenced)
	s.mu.Unlock()
}

// sequenceLocked stamps a Seq and records the event in the replay buffer.
func (s *Session) sequenceLocked(ev AppEvent) AppEvent {
	s.seq++
	ev.Seq = s.seq
	s.buf.add(ev)
	return ev
}

// emitLocked fans a sequenced event out to subscribers, dropping any that can't
// keep up (they reconnect and replay the gap).
func (s *Session) emitLocked(ev AppEvent) {
	for sub := range s.subs {
		select {
		case sub.ch <- ev:
		default:
			delete(s.subs, sub)
			close(sub.ch)
		}
	}
}

func (s *Session) setState(state string) {
	s.mu.Lock()
	if s.state == state {
		s.mu.Unlock()
		return
	}
	s.state = state
	sequenced := s.sequenceLocked(AppEvent{T: EvState, State: state})
	s.emitLocked(sequenced)
	s.mu.Unlock()
}

// Meta is a snapshot for session listings.
type Meta struct {
	ID        string    `json:"id"`
	Cwd       string    `json:"cwd"`
	Model     string    `json:"model"`
	Effort    string    `json:"effort"`
	Title     string    `json:"title"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Session) Meta() Meta {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Meta{ID: s.ID, Cwd: s.Cwd, Model: s.model, Effort: s.effort, Title: s.title, State: s.state, CreatedAt: s.CreatedAt}
}

// ClaudeSessionID returns the CLI-assigned session id (available after init),
// used to --resume the same conversation when restarting.
func (s *Session) ClaudeSessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.claudeSessionID
}

// ContextTokens returns the latest known context-window occupancy, so a restart
// (e.g. an effort change) can carry it into the respawned session.
func (s *Session) ContextTokens() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.contextTokens
}

func toAppBlocks(msg *claude.AssistantMessage) []AppBlock {
	if msg == nil {
		return nil
	}
	out := make([]AppBlock, 0, len(msg.Content))
	for _, b := range msg.Content {
		out = append(out, AppBlock{Type: b.Type, Text: b.Text, ID: b.ID, Name: b.Name, Input: b.Input})
	}
	return out
}

func parseResult(raw json.RawMessage) AppEvent {
	var r struct {
		Subtype    string  `json:"subtype"`
		IsError    bool    `json:"is_error"`
		DurationMs int64   `json:"duration_ms"`
		CostUSD    float64 `json:"total_cost_usd"`
		Usage      struct {
			Input       int64 `json:"input_tokens"`
			Output      int64 `json:"output_tokens"`
			CacheCreate int64 `json:"cache_creation_input_tokens"`
			CacheRead   int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	_ = json.Unmarshal(raw, &r)
	// This usage is cumulative over every model call in the turn, so it is the
	// turn's total cost — not the context size (context comes from the per-call
	// assistant usage instead; see pump).
	tokens := r.Usage.Input + r.Usage.Output + r.Usage.CacheCreate + r.Usage.CacheRead
	return AppEvent{
		T:          EvResult,
		Message:    r.Subtype,
		IsError:    r.IsError,
		DurationMs: r.DurationMs,
		Tokens:     tokens,
		CostUSD:    r.CostUSD,
	}
}
