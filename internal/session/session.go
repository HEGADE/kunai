package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"github.com/hegade/kunai/internal/claude"
	"github.com/hegade/kunai/internal/project"
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
	mode            string            // permission mode
	effort          string            // reasoning effort (spawn-time; changed by restart)
	cliName         string            // which Claude CLI/account this session runs on
	cliBin          string            // the binary that account runs (persisted so a resumed loop stays on it)
	cliEnv          map[string]string // extra env that account needs
	title           string
	claudeSessionID string // CLI-assigned id, for --resume cold-start
	state           string
	contextTokens   int64   // context-window occupancy from the latest result (or seeded on resume)
	overhead        int64   // fixed resident cost (system prompt, tools, memory, skills) a compaction's postTokens omits
	pendingPost     int64   // a just-compacted conversation size, awaiting the next usage to measure overhead
	histBefore      int64   // transcript byte offset older-than-seed history begins before; 0 = none. Reverse-scroll cursor.
	lastCostUSD     float64 // running session total from the CLI, to difference per turn
	buf             *ring
	subs            map[*Subscriber]struct{}
	queue           []*queuedPrompt // prompts waiting for the running turn to end
	projects        []project.Info  // codebases this session has been given context for
	loop            *loopRun        // self-prompting run, if one was ever started
	lastText        string          // the newest assistant text this turn, for the loop's promise
	rateLimited     bool            // the usage window is spent; a loop must not push on

	pending         map[string]AppEvent // unresolved permission asks, keyed by request_id
	suggestionByReq map[string]json.RawMessage
	closed          bool
	done            chan struct{} // closed when the driver has ended
	notify          func(kind, detail string)
	onRateLimit     func(window string, resetsAt int64)
	loopPersist     func(LoopPersist) // save/clear a running loop so it survives a restart
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
	Role        string       // "user" | "assistant" | "tool_result" | "compact" | "loop"
	Text        string       // user text, or tool_result output
	Blocks      []AppBlock   // assistant content blocks
	ToolUseID   string       // tool_result correlation
	IsError     bool         // tool_result
	Attachments []Attachment // user: files sent with the prompt

	// compact: where the context window went when the conversation was summarised.
	Trigger    string
	PreTokens  int64
	PostTokens int64

	// loop: which lap of a self-prompting run this seam marks.
	Iteration int
	MaxIters  int
}

// Seed pre-populates the replay buffer with transcript history so a resumed
// session opens with its past conversation visible. Call before clients attach.
// SeedEvent converts one seed turn into the app event a client renders, the same
// wire shape a live turn takes, so replayed history and paged-in older history
// (the reverse-scroll endpoint) build identical items. overhead is added to a
// compaction's post size for the meter.
func SeedEvent(t SeedTurn, overhead int64) AppEvent {
	switch t.Role {
	case "user":
		return AppEvent{T: EvUser, Text: t.Text, Attachments: t.Attachments}
	case "tool_result":
		return AppEvent{T: EvToolResult, ToolUseID: t.ToolUseID, Content: t.Text, IsError: t.IsError}
	case "compact":
		return AppEvent{T: EvCompact, Trigger: t.Trigger, PreTokens: t.PreTokens, PostTokens: t.PostTokens, ContextTokens: t.PostTokens + overhead}
	case "loop":
		// LoopSeam, not LoopRunning: this lap is history recovered from a
		// transcript. The loop it belonged to died with the process that ran it,
		// and a resumed session must not show a live meter for it.
		return AppEvent{T: EvLoop, Loop: &LoopStatus{State: LoopSeam, Iteration: t.Iteration, MaxIters: t.MaxIters}}
	default:
		return AppEvent{T: EvAssistant, Blocks: t.Blocks}
	}
}

// Overhead exposes the measured context overhead, so the history endpoint can
// convert paged-in compaction turns with the same meter basis a live seed uses.
func (s *Session) SeedOverhead() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.overhead
}

func (s *Session) Seed(turns []SeedTurn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range turns {
		s.emitLocked(s.sequenceLocked(SeedEvent(t, s.overhead)))
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
			s.mu.Lock()
			if ctx > 0 {
				s.contextTokens = ctx
				// First real usage after a compaction reports the true full size,
				// so its excess over the compacted conversation is the resident
				// overhead (plus this turn's new prompt). Keep the smallest such
				// gap as the estimate the next compaction will add back.
				if s.pendingPost > 0 {
					if gap := ctx - s.pendingPost; gap > 0 && (s.overhead == 0 || gap < s.overhead) {
						s.overhead = gap
					}
					s.pendingPost = 0
				}
			}
			// Remember the turn's newest words: a loop's completion promise has to
			// be found in the last thing the model actually said.
			if txt := assistantText(ev.Assistant); txt != "" {
				s.lastText = txt
			}
			s.mu.Unlock()
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

		case claude.EventCompact:
			// Compaction is the one time the context window shrinks, and it reports
			// the new size itself: no assistant message follows it, so without this
			// the meter would sit on the pre-compaction number until the next turn.
			//
			// But post_tokens counts only the compacted conversation, not the fixed
			// overhead that stays resident in the window: the system prompt, tool
			// schemas, memory files, and skills, tens of thousands of tokens. Our
			// meter comes from an assistant usage that included that overhead, so
			// showing the bare post_tokens reads far too low right after a /compact
			// (13k when Claude's own /context shows ~50k). The overhead is not in the
			// frame (pre_tokens is the full pre-compaction context, the same basis as
			// our meter, so pre-post over-subtracts to ~post), so we add the overhead
			// measured from a prior compaction's regrowth: post_tokens+overhead. With
			// no measurement yet the meter falls back to post_tokens and the next
			// assistant usage, which reports the true full size, corrects it.
			c := ev.Compact
			s.mu.Lock()
			newCtx := s.contextTokens
			if c.PostTokens > 0 {
				newCtx = c.PostTokens + s.overhead
				s.contextTokens = newCtx
				s.pendingPost = c.PostTokens
			}
			s.mu.Unlock()
			s.broadcast(AppEvent{
				T:             EvCompact,
				Trigger:       c.Trigger,
				PreTokens:     c.PreTokens,
				PostTokens:    c.PostTokens,
				ContextTokens: newCtx,
			})

		case claude.EventResult:
			s.setState(StateIdle)
			res := s.turnResult(ev.Raw)
			s.broadcast(res)
			// Only tell you the work is done when nothing is queued behind it —
			// otherwise the next prompt starts immediately and it isn't. A running
			// loop is the same kind of "not really done", and it announces its own
			// ending, so it must not buzz you once per iteration all night.
			s.mu.Lock()
			more := len(s.queue) > 0 || (s.loop != nil && s.loop.state == LoopRunning)
			s.mu.Unlock()
			if !more {
				s.notifyAttention("done", "")
			}
			s.drainQueue()
			s.afterTurn(res.IsError)

		case claude.EventRateLimit:
			s.mu.Lock()
			fn := s.onRateLimit
			// Only a hard "rejected" means the window is actually spent.
			// "allowed_warning" is the CLI approaching the limit (e.g. 91%), not a
			// wall: treating it as limited cried "rate-limited" before the quota was
			// gone and would stop a loop early. This is latched for afterTurn to read.
			s.rateLimited = ev.LimitStatus != "" && ev.LimitStatus != "allowed" && ev.LimitStatus != "allowed_warning"
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

// queuedPrompt is a prompt parked until the running turn ends. The queue lives
// here rather than in the client because the client is not required to be
// present: a phone can queue work and drop off, and the session still runs it.
type queuedPrompt struct {
	ID          string
	Text        string
	Attachments []Attachment
	content     any    // built content (attachments) for the CLI; not sent to clients
	label       string // what the queue shows, when the text itself is not for reading
	silent      bool   // context handed to the model; another event already stands for it
}

// Prompt sends a user turn, or queues it if a turn is already running. The
// user's text and any attachments are echoed into the sequenced event stream so
// reconnects and reloads replay the full conversation, not just Claude's side of
// it. content carries the attachments to the CLI; atts is the display copy, so
// the message shows what was sent with it.
func (s *Session) Prompt(text string, content any, atts []Attachment) error {
	return s.prompt(&queuedPrompt{Text: text, Attachments: atts, content: content})
}

// prompt runs a turn, or queues it if one is already going. q carries how it
// should appear: a silent prompt is context for the model that some other event
// already represents, so it never shows as something the user typed.
func (s *Session) prompt(q *queuedPrompt) error {
	s.mu.Lock()
	if s.state == StateRunning || s.state == StateAwaiting {
		q.ID = newQueueID()
		s.queue = append(s.queue, q)
		s.emitLocked(s.sequenceLocked(AppEvent{T: EvQueued, QueueID: q.ID, Text: q.display(), Attachments: q.Attachments}))
		s.mu.Unlock()
		return nil
	}
	// Claim the turn under the same lock that tested for it, so a second prompt
	// arriving now queues instead of racing this one into the CLI mid-turn.
	s.startTurnLocked(q)
	s.mu.Unlock()
	return s.deliver(q.Text, q.content)
}

// display is what a queued prompt shows in the queue.
func (q *queuedPrompt) display() string {
	if q.label != "" {
		return q.label
	}
	return q.Text
}

// startTurnLocked records a prompt as the turn that is now running.
func (s *Session) startTurnLocked(q *queuedPrompt) {
	s.lastText = "" // this turn has not said anything yet
	if !q.silent {
		s.emitLocked(s.sequenceLocked(AppEvent{T: EvUser, Text: q.Text, Attachments: q.Attachments}))
	}
	s.state = StateRunning
	s.emitLocked(s.sequenceLocked(AppEvent{T: EvState, State: StateRunning}))
}

func (s *Session) deliver(text string, content any) error {
	if content != nil {
		return s.drv.SendUser(content)
	}
	return s.drv.SendUserText(text)
}

// AddProject gives the session another codebase to work with. Nothing is read:
// the model is handed a description and the path, and reaches for files itself
// when it needs them. The brief goes in as a silent turn — the project event is
// what the conversation shows — and queues behind any turn already running.
// Adding the same path twice is a no-op.
func (s *Session) AddProject(info project.Info) error {
	s.mu.Lock()
	for _, p := range s.projects {
		if p.Path == info.Path {
			s.mu.Unlock()
			return nil
		}
	}
	s.projects = append(s.projects, info)
	cp := info
	s.emitLocked(s.sequenceLocked(AppEvent{T: EvProject, Project: &cp}))
	s.mu.Unlock()

	return s.prompt(&queuedPrompt{
		Text:   info.Brief(),
		label:  "Add project: " + info.Name,
		silent: true,
	})
}

// Projects lists the codebases this session has context for.
func (s *Session) Projects() []project.Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]project.Info(nil), s.projects...)
}

// drainQueue starts the next queued prompt now that the turn has ended. Called
// after every result, so a queue runs itself down without a client attached.
func (s *Session) drainQueue() {
	s.mu.Lock()
	if s.closed || s.state != StateIdle || len(s.queue) == 0 {
		s.mu.Unlock()
		return
	}
	q := s.queue[0]
	s.queue = s.queue[1:]
	s.emitLocked(s.sequenceLocked(AppEvent{T: EvUnqueued, QueueID: q.ID}))
	s.startTurnLocked(q)
	s.mu.Unlock()

	if err := s.deliver(q.Text, q.content); err != nil {
		s.broadcast(AppEvent{T: EvError, Message: "queued prompt failed: " + err.Error()})
		s.setState(StateIdle)
	}
}

// CancelQueued drops a prompt that has not started yet.
func (s *Session) CancelQueued(id string) {
	s.mu.Lock()
	for i, q := range s.queue {
		if q.ID == id {
			s.queue = append(s.queue[:i:i], s.queue[i+1:]...)
			s.emitLocked(s.sequenceLocked(AppEvent{T: EvUnqueued, QueueID: id}))
			break
		}
	}
	s.mu.Unlock()
}

// dropQueueLocked clears the queue, telling clients each prompt is gone.
func (s *Session) dropQueueLocked() {
	for _, q := range s.queue {
		s.emitLocked(s.sequenceLocked(AppEvent{T: EvUnqueued, QueueID: q.ID}))
	}
	s.queue = nil
}

func newQueueID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
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

// Interrupt aborts the current turn and drops anything queued behind it: Stop
// means stop, not "move on to the next one".
func (s *Session) Interrupt() error {
	return s.interrupt("you stopped it")
}

// StopForThermal aborts the turn and any loop because the host is too hot. It is
// the same stop as the Stop button, with a reason that says who pulled it, so a
// session ended by the guardian reads "the host got too hot" rather than looking
// as if you did it. Called across every session by the guardian.
func (s *Session) StopForThermal() error {
	return s.interrupt("the host got too hot")
}

// interrupt is the shared stop path: drop the queue, settle any loop with the
// given reason, abort the turn, and go idle. Stopping the loop under the lock
// that tests it is what keeps a loop from starting its next iteration a moment
// later and making the stop look broken.
func (s *Session) interrupt(reason string) error {
	s.mu.Lock()
	s.dropQueueLocked()
	s.stopLoopLocked(LoopStopped, reason)
	s.mu.Unlock()
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
	// A mode change does not always come from a click: a loop borrows acceptEdits
	// for its duration and hands it back at the end. Say so, or the composer goes
	// on showing the mode you last picked while the session runs in another one.
	s.emitLocked(s.sequenceLocked(AppEvent{T: EvMode, Mode: mode}))
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
	// The queue rides along on hello (like pending asks) so a client that missed
	// the events — or attached late — still sees what is waiting to run.
	queued := make([]AppEvent, 0, len(s.queue))
	for _, q := range s.queue {
		queued = append(queued, AppEvent{T: EvQueued, QueueID: q.ID, Text: q.Text, Attachments: q.Attachments})
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
		HistBefore:    s.histBefore,
		Loop:          s.loopStatusLocked(),
		Pending:       pending,
		Queued:        queued,
		Projects:      append([]project.Info(nil), s.projects...),
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
	CLI       string    `json:"cli,omitempty"` // the Claude account this session runs on
	Title     string    `json:"title"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
	// Pinned is a user override the server merges in from its session-metadata
	// store; the session itself never sets it (a live session doesn't know it is
	// pinned). Kept here so the live list and the Recent list carry the same flag.
	Pinned bool `json:"pinned,omitempty"`
}

func (s *Session) Meta() Meta {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Meta{ID: s.ID, Cwd: s.Cwd, Model: s.model, Effort: s.effort, CLI: s.cliName, Title: s.title, State: s.state, CreatedAt: s.CreatedAt}
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

// Overhead returns the measured resident context overhead, so a restart can
// carry it into the respawned session and keep the meter right if it compacts
// before the next assistant turn re-measures it.
func (s *Session) Overhead() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.overhead
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

// turnResult decodes a result frame into this turn's numbers. The CLI reports
// total_cost_usd as the session's running total, so every turn was showing the
// whole session's spend as if it were its own; difference it against the last
// total to get what this turn actually cost. (A resumed session starts counting
// from whatever it inherited, so its first turn can only be the total so far.)
func (s *Session) turnResult(raw json.RawMessage) AppEvent {
	ev := parseResult(raw)
	if ev.CostUSD <= 0 {
		return ev
	}
	s.mu.Lock()
	total := ev.CostUSD
	turn := total - s.lastCostUSD
	if turn < 0 {
		turn = total // the total went backwards; trust the frame
	}
	s.lastCostUSD = total
	s.mu.Unlock()
	ev.CostUSD = turn
	return ev
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
		T:            EvResult,
		Message:      r.Subtype,
		IsError:      r.IsError,
		DurationMs:   r.DurationMs,
		Tokens:       tokens,
		NewTokens:    r.Usage.Input + r.Usage.CacheCreate, // read fresh, billed in full
		CachedTokens: r.Usage.CacheRead,                   // context re-read, billed at a fraction
		OutputTokens: r.Usage.Output,
		CostUSD:      r.CostUSD,
	}
}
