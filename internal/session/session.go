package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
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
	// PID is the CLI process, or 0 before it starts. Needed because a server the
	// agent runs is a descendant of it, which is how a listening port is
	// attributed to a session. See internal/preview.
	PID() int
	Close() error
}

// Session is one app-facing conversation: a single long-lived claude process
// plus the sequencing, buffering, and fan-out that let many phone connections
// attach and detach without disturbing it.
type Session struct {
	ID        string    // our stable handle (used in URLs)
	Cwd       string    //
	CreatedAt time.Time //

	// epoch identifies THIS process behind the stable ID, and changes whenever the
	// session is respawned (an effort change, an account switch, auto-failover).
	//
	// It has to exist because a respawn builds a whole new Session: seq restarts at
	// 1 and the ring is empty, while every attached client is still holding the old
	// high-water mark. They reconnect with ?since=<old seq> and then drop every
	// event at or below it, so they go silently deaf to the new process, for good,
	// in a long session. The SPA's own tab escaped only because it throws its
	// connection away by hand; a second phone, a chat, or a shared link never knew
	// to. Carried on hello so any client can notice the process changed under it
	// and start over. Immutable for the life of the Session, so it needs no lock.
	epoch string

	drv driver

	mu      sync.Mutex
	seq     uint64
	model   string
	mode    string            // permission mode
	effort  string            // reasoning effort (spawn-time; changed by restart)
	cliName string            // which Claude CLI/account this session runs on
	cliBin  string            // the binary that account runs (persisted so a resumed loop stays on it)
	cliEnv  map[string]string // extra env that account needs
	// appendPrompt is extra system prompt baked in at spawn (a worktree brief).
	// It is spawn-time only, so like effort it has to be carried across a restart
	// or an effort/account change would silently drop it.
	appendPrompt string
	// disallowedTools is the toolset withheld from this session. Spawn-time, like
	// appendPrompt, and carried by spawnSpec for the same reason with a much
	// sharper edge: losing it hands a guest the tools the share exists to withhold.
	disallowedTools []string
	// toolsOwner names the feature that withheld them, and exists because more
	// than one now does. A share reconciles restrictions against its own store and
	// gives back the toolset of any session whose share has ended; with no owner to
	// check it read "has withheld tools" as "was shared", so it respawned a pull
	// request review a minute after it started and killed the running turn. Whoever
	// imposed a restriction is the only one who may lift it.
	toolsOwner      string
	title           string
	claudeSessionID string // CLI-assigned id, for --resume cold-start
	state           string
	contextTokens   int64   // context-window occupancy from the latest result (or seeded on resume)
	overhead        int64   // fixed resident cost (system prompt, tools, memory, skills) a compaction's postTokens omits
	pendingPost     int64   // a just-compacted conversation size, awaiting the next usage to measure overhead
	histBefore      int64   // transcript byte offset older-than-seed history begins before; 0 = none. Reverse-scroll cursor.
	lastCostUSD     float64 // running session total from the CLI, to difference per turn
	turnFrom        Origin  // who started the turn now running; see origin.go
	turnStartedAt   int64   // unix ms the running turn began; 0 when nothing runs
	turnEndedAt     int64   // unix ms the last turn finished; 0 until one has
	buf             *ring
	subs            map[*Subscriber]struct{}
	queue           []*queuedPrompt // prompts waiting for the running turn to end
	projects        []project.Info  // codebases this session has been given context for
	loop            *loopRun        // self-prompting run, if one was ever started
	lastText        string          // the newest assistant text this turn, for the loop's promise
	lastPromptText  string          // the newest real user prompt, so failover can resend it
	failingOver     bool            // auto-failover is looking for an account with headroom
	rateLimited     bool            // the usage window is spent; a loop must not push on
	wallFromText    bool            // ...and we learned it from a turn's error text (see wall.go)
	limitWindow     string          // the window that was reported spent ("five_hour"/"seven_day")
	limitResetsAt   int64           // when that window resets (unix secs), for failover to report

	pending         map[string]AppEvent // unresolved permission asks, keyed by request_id
	suggestionByReq map[string]json.RawMessage
	closed          bool
	done            chan struct{} // closed when the driver has ended
	notify          func(kind, detail string)
	// onListChange fires when this session's row in a listing would look
	// different, so the server can push rather than have clients poll.
	onListChange func()
	onRateLimit  func(window string, resetsAt int64)
	// onTurnEnd fires after every turn. rateLimited says the subscription window
	// is spent (failover reacts to that); inLoop says a self-prompting run was
	// still going when the turn began, so a handler that must not act mid-run can
	// tell even though the loop may have stopped by the time it is called.
	onTurnEnd func(rateLimited, inLoop bool)
	// onAnswer is given the model's spoken text at the end of every turn.
	// Separate from onTurnEnd because a session has one of those and failover
	// owns it, and because this one must not be missable: see answer.go.
	onAnswer    func(text string)
	loopPersist func(LoopPersist) // save/clear a running loop so it survives a restart
	// checkpointHook, if set, snapshots the working tree at the start of a turn --
	// synchronously, BEFORE the prompt reaches the CLI, so the checkpoint is the true
	// pre-turn state (a later capture would race the agent's first edit). seq is the
	// turn's user-message Seq, so the client can map the checkpoint to a turn.
	checkpointHook func(seq uint64)
	// guard confines what a guest's prompt can reach while this session is
	// shared. Nil for an ordinary session. See guard.go.
	guard ToolGuard
}

// SetCheckpointHook registers the pre-turn working-tree snapshot callback. The hook
// runs off the session lock but on the turn-start path, so it must be fast and never
// block the turn (the server gives it a timeout and swallows failures).
func (s *Session) SetCheckpointHook(fn func(seq uint64)) {
	s.mu.Lock()
	s.checkpointHook = fn
	s.mu.Unlock()
}

// runCheckpointHook invokes the pre-turn snapshot for a real (non-silent) turn.
func (s *Session) runCheckpointHook(seq uint64) {
	s.mu.Lock()
	fn := s.checkpointHook
	s.mu.Unlock()
	if seq != 0 && fn != nil {
		fn(seq)
	}
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

// SetTurnEndHook registers a callback fired once each time a turn ends, told
// whether the turn ended against the usage wall. Account auto-failover uses it to
// roll a rate-limited session onto another account. It runs after the turn is
// fully settled, so the handler may safely respawn the session.
func (s *Session) SetTurnEndHook(fn func(rateLimited, inLoop bool)) {
	s.mu.Lock()
	s.onTurnEnd = fn
	s.mu.Unlock()
}

// LastPromptText is the most recent real (non-silent) user prompt, so failover can
// resend it on the account it switches to.
func (s *Session) LastPromptText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastPromptText
}

// LastLimit reports the window last seen spent and when it resets (0,"" if none).
func (s *Session) LastLimit() (window string, resetsAt int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.limitWindow, s.limitResetsAt
}

// Mode is the session's current permission mode. Meta does not carry it (the app
// learns it from the hello frame), but a channel showing a mode picker needs to say
// which one you are on.
func (s *Session) Mode() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

// InLoop reports whether a self-prompting loop is currently running, so failover
// can leave loops to their own limit handling (loop failover is not yet wired).
func (s *Session) InLoop() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loop != nil && s.loop.state == LoopRunning
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
		epoch:           newQueueID(), // any fresh random string; see the field
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
			s.broadcast(AppEvent{T: EvDelta, Text: ev.Text, ParentToolUseID: ev.ParentToolUseID})

		case claude.EventThinking:
			s.broadcast(AppEvent{T: EvThinking, Text: ev.Text, ParentToolUseID: ev.ParentToolUseID})

		case claude.EventAssistant:
			// A frame from inside a subagent is that agent's own model call, with its
			// OWN context window and its own words. Neither belongs to this session:
			// adopting its usage made the context meter lurch down mid-turn, and
			// adopting its text let a subagent's "done" satisfy a loop's completion
			// promise. So a nested frame is broadcast for display only.
			nested := ev.ParentToolUseID != ""
			// Each assistant message is one model call, so its usage reports the
			// context actually sent for that call. Track the newest as the current
			// context-window occupancy (the result frame's usage is cumulative over
			// the whole turn and would overcount a long tool loop).
			ctx := ev.Assistant.Usage.ContextTokens()
			if nested {
				s.broadcast(AppEvent{T: EvAssistant, Blocks: toAppBlocks(ev.Assistant), ParentToolUseID: ev.ParentToolUseID})
				continue
			}
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
				T:               EvToolResult,
				ToolUseID:       tr.ToolUseID,
				Content:         tr.Content,
				IsError:         tr.IsError,
				Truncated:       tr.Truncated,
				ParentToolUseID: ev.ParentToolUseID,
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
			// A turn the subscription window rejected does not always arrive as a
			// rate_limit_event; more often the CLI just ends the turn with an error
			// whose text says the limit was reached. Read that too, before
			// afterTurn, which is what asks whether the window is spent. Without
			// this the wall was visible only in the chat (the client parses the same
			// text) while everything that acts on one — auto-failover, a loop's stop
			// condition, a pinned reset job — never learned of it. See wall.go.
			if res.IsError {
				if reset, isWall := parseWall(resultText(ev.Raw)); isWall {
					s.recordWall(reset)
				}
			} else {
				s.clearWall() // a turn that ran is proof the window is not spent
			}
			// Only tell you the work is done when nothing is queued behind it —
			// otherwise the next prompt starts immediately and it isn't. A running
			// loop is the same kind of "not really done", and it announces its own
			// ending, so it must not buzz you once per iteration all night.
			s.mu.Lock()
			more := len(s.queue) > 0 || (s.loop != nil && s.loop.state == LoopRunning)
			s.mu.Unlock()
			if !more {
				// A turn that ended in an error used to report as finished, which
				// is the one thing you would want distinguished at a glance.
				kind := NotifyDone
				if res.IsError {
					kind = NotifyFailed
				}
				s.notifyAttention(kind, turnSummary(res))
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
			// A control frame is the CLI's own answer, so it supersedes anything
			// inferred from a turn's error text and takes the latch back off the
			// text path (see wall.go).
			s.wallFromText = false
			if s.rateLimited {
				s.limitWindow, s.limitResetsAt = ev.Window, ev.ResetsAt
			}
			s.mu.Unlock()
			if fn != nil && ev.ResetsAt > 0 {
				go fn(ev.Window, ev.ResetsAt)
			}
			// Surface to the chat so it can offer "schedule after reset".
			s.broadcast(AppEvent{T: EvRateLimit, Window: ev.Window, ResetsAt: ev.ResetsAt, LimitStatus: ev.LimitStatus})

		case claude.EventError:
			// A driver error can itself be the wall: the CLI reports a spent window
			// on its way out as well as inside a turn, and read only in the turn it
			// was invisible whenever the process left rather than replied.
			if reset, isWall := parseWall(ev.Err.Error()); isWall {
				s.recordWall(reset)
			}
			s.broadcast(AppEvent{T: EvError, Message: ev.Err.Error()})
		}
	}
	// Driver ended.
	s.mu.Lock()
	s.closed = true
	subs := s.subs
	s.subs = make(map[*Subscriber]struct{})
	// Was a turn still in flight when the process went? That is a turn that ended,
	// however badly, and the turn-end hook is what auto-failover listens to.
	diedMidTurn := s.state == StateRunning || s.state == StateAwaiting
	hook, rl := s.onTurnEnd, s.rateLimited
	s.mu.Unlock()
	for sub := range subs {
		close(sub.ch)
	}
	close(s.done)

	// Fired after done is closed, and that ordering is required: failover
	// responds by respawning the session, and the respawn waits on Done().
	//
	// Until now the hook only ran on a result frame, so a CLI that EXITED mid-turn
	// -- which is what hitting the usage wall looks like -- ended the session
	// without failover ever being asked. It reported nothing because it was never
	// called, which is indistinguishable from working correctly and finding
	// nothing to do. rl carries what is actually known: a wall seen in a control
	// frame or in the CLI's parting words means failover may act, and a process
	// that died for some other reason only clears the chain, rather than rolling a
	// healthy session onto another account on the strength of a crash.
	if diedMidTurn && hook != nil {
		go hook(rl, false)
	}
}

func (s *Session) onPermission(ask *claude.PermissionAsk) {
	// A shared session's guard runs first, before this ask is recorded or sent to
	// anybody. A call outside the share's folders is answered here and dies
	// without a human ever seeing it, which is the point: the owner should not be
	// asked to adjudicate something the boundary already settles, and every ask
	// they are shown is one they might approve by reflex.
	if v := s.judge(ask); v.denied {
		_ = s.drv.Resolve(ask.RequestID, claude.PermissionResult{
			Behavior:  "deny",
			Message:   v.reason,
			ToolUseID: ask.ToolUseID,
		})
		return
	} else if v.autoYes {
		// Cleared by the guard, and the share granted a standing yes for exactly
		// this case, so the guest keeps working instead of waiting for someone who
		// is asleep. Anything the guard could NOT clear has already been denied
		// above, so this never waves through something outside the boundary.
		_ = s.drv.Resolve(ask.RequestID, claude.PermissionResult{
			Behavior:     "allow",
			UpdatedInput: ask.Input, // an allow without it runs the tool with empty input
			ToolUseID:    ask.ToolUseID,
		})
		return
	}
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
	// Carry who caused the turn this ask belongs to. The owner is the only one who
	// can answer it, and they need to know whether they are approving their own
	// work or a visitor's.
	ev.From = string(s.turnFrom)
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

	s.notifyAttention(NotifyPermission, ask.ToolName)
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
	from        Origin // who sent it; empty is the owner. See origin.go.
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
		s.emitLocked(s.sequenceLocked(AppEvent{T: EvQueued, QueueID: q.ID, Text: q.display(), Attachments: q.Attachments, From: string(q.from)}))
		s.mu.Unlock()
		return nil
	}
	// Claim the turn under the same lock that tested for it, so a second prompt
	// arriving now queues instead of racing this one into the CLI mid-turn.
	userSeq := s.startTurnLocked(q)
	s.mu.Unlock()
	s.runCheckpointHook(userSeq) // snapshot the pre-turn tree before the CLI can edit it
	if err := s.deliver(q.Text, q.content); err != nil {
		s.abandonTurn("that message could not be sent: " + err.Error())
		return err
	}
	return nil
}

// abandonTurn undoes a turn that was claimed but never reached the CLI.
//
// The turn is marked running and broadcast BEFORE the CLI is asked to run it,
// which is right: the claim is what stops a second prompt racing into the same
// process. But when the ask itself fails there is no turn, and leaving the
// session marked running strands every attached client on "Working" for a
// prompt that never left the machine. That is what a dead CLI looked like: the
// driver refused with "claude: session closed", the error went to the log, and
// the app sat spinning on a request that had not been made and never would be,
// with nothing spent and nothing to show.
//
// drainQueue already did this for a queued prompt; the direct path did not, so
// the same failure behaved differently depending on whether a turn happened to
// be running when you pressed send.
func (s *Session) abandonTurn(why string) {
	s.mu.Lock()
	s.turnStartedAt = 0
	s.mu.Unlock()
	s.broadcast(AppEvent{T: EvError, Message: why})
	s.setState(StateIdle)
}

// display is what a queued prompt shows in the queue.
func (q *queuedPrompt) display() string {
	if q.label != "" {
		return q.label
	}
	return q.Text
}

// startTurnLocked records a prompt as the turn that is now running. It returns the
// Seq of the turn's user message (0 for a silent turn), which keys the pre-turn
// checkpoint to this turn.
func (s *Session) startTurnLocked(q *queuedPrompt) uint64 {
	s.lastText = "" // this turn has not said anything yet
	// Whose turn this is, so a guest can stop their own work and only their own,
	// and so a permission ask raised by it can say who asked for it.
	s.turnFrom = q.from
	// And when, so the list can say how long it has been at it. Stamped here
	// rather than at the driver, because this is the single place every turn
	// starts (a prompt, a drained queue entry, a loop iteration) and a second
	// site would eventually be the one nobody updated.
	s.turnStartedAt = time.Now().UnixMilli()
	var userSeq uint64
	if !q.silent {
		s.lastPromptText = q.Text // remember it so failover can resend on another account
		ev := s.sequenceLocked(AppEvent{T: EvUser, Text: q.Text, Attachments: q.Attachments, From: string(q.from)})
		userSeq = ev.Seq
		s.emitLocked(ev)
	}
	s.state = StateRunning
	s.emitLocked(s.sequenceLocked(AppEvent{T: EvState, State: StateRunning}))
	return userSeq
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

// PromptBrief sends the model a body of context it must read but nobody wants to
// read back: a pull request's diff and the instructions for reviewing it.
//
// Silent, because a prompt is shown as something you said and this is not that.
// Sent as an ordinary user turn it printed the entire brief into the chat, a
// wall of schema and rules above the work, and the actual conversation started
// several screens down. The label is what the queue shows if the turn has to
// wait, and the card the caller renders is the conversation's record of it.
func (s *Session) PromptBrief(text, label string) error {
	return s.prompt(&queuedPrompt{Text: text, label: label, silent: true})
}

// Projects lists the codebases this session has context for.
// DisallowedTools is the toolset currently withheld from this session, so a
// caller can tell whether a respawn would actually change anything.
func (s *Session) DisallowedTools() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.disallowedTools...)
}

// ToolsOwner names whatever withheld those tools, so a feature that reconciles
// its own restrictions can tell them apart from somebody else's.
func (s *Session) ToolsOwner() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.toolsOwner
}

// HighSeq is the newest event number this session has emitted. A share made
// "from now on" freezes this as its floor, so the guest never sees the
// conversation that happened before the link existed.
func (s *Session) HighSeq() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seq
}

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
	userSeq := s.startTurnLocked(q)
	s.mu.Unlock()
	s.runCheckpointHook(userSeq) // snapshot the pre-turn tree before the CLI can edit it

	if err := s.deliver(q.Text, q.content); err != nil {
		s.abandonTurn("that queued message could not be sent: " + err.Error())
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
		Epoch:         s.epoch,
		Cwd:           s.Cwd,
		Model:         s.model,
		Title:         s.title,
		State:         s.state,
		Mode:          s.mode,
		Effort:        s.effort,
		CLI:           s.cliName,
		HighSeq:       s.seq,
		FailoverState: failoverStateOf(s.failingOver),
		ContextTokens: s.contextTokens,
		HistBefore:    s.histBefore,
		Loop:          s.loopStatusLocked(),
		Pending:       pending,
		Queued:        queued,
		Projects:      append([]project.Info(nil), s.projects...),
	}
	// A client asking for events after a seq this session never reached is holding
	// a number from a previous incarnation of it: the respawn built a new Session
	// whose numbering restarts at 1, so "everything after 500" matches nothing and
	// the client gets an empty backlog forever. Treat it as a cold attach and send
	// the lot. The alternative, trusting the number, is silence that looks exactly
	// like an idle session.
	if afterSeq > s.seq {
		afterSeq = 0
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
	// The turn clock stops when the session goes idle, and only then. Awaiting a
	// permission answer is the SAME turn paused, so clearing it there would
	// restart the count from zero on approval and make a long turn look young.
	if state == StateIdle {
		// The end is stamped only when a turn was actually running: abandonTurn
		// zeroes the clock before it sets idle precisely so a prompt the CLI
		// refused never reads as finished work. This timestamp is what lets a
		// client say "the agent finished while you were away" (unread Done),
		// so a false stamp would light rows up for work that never happened.
		if s.turnStartedAt != 0 {
			s.turnEndedAt = time.Now().UnixMilli()
		}
		s.turnStartedAt = 0
	}
	sequenced := s.sequenceLocked(AppEvent{T: EvState, State: state})
	s.emitLocked(sequenced)
	changed := s.onListChange
	s.mu.Unlock()
	// Outside the lock: a listener fans out to every connected client, and doing
	// that while holding this session's mutex would let a slow socket stall the
	// turn that caused the change.
	if changed != nil {
		changed()
	}
}

// SetOnListChange registers a callback fired whenever something a session
// LISTING would show has changed: today that is the state. It exists so the
// server can push the session list to clients instead of having them poll for
// it, which is the difference between a status board that is current and one
// that is up to eight seconds behind.
func (s *Session) SetOnListChange(fn func()) {
	s.mu.Lock()
	s.onListChange = fn
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
	// TurnStartedAt is when the turn now running began, in unix milliseconds, and
	// 0 whenever nothing is running. It exists so the sidebar can say how long a
	// session has been working rather than only that it is, which is the
	// difference between "busy" and "stuck": twenty seconds is a session thinking
	// and twenty minutes is one you want to look at.
	//
	// Zeroed the moment a turn ends, so it can never outlive the work it measures.
	// A resumed session has no running turn and therefore no duration, which is
	// the honest answer rather than a clock started at the reopen.
	TurnStartedAt int64 `json:"turn_started_at,omitempty"`
	// TurnEndedAt is when the last turn finished, unix milliseconds, 0 until one
	// has. It is the other half of the attention story: TurnStartedAt says how
	// long a session has been working, this says when it stopped, which is what a
	// client compares against "when did I last look" to mark a session Done and
	// unread. Only a turn that actually ran stamps it (see setState), so a
	// refused prompt or a resumed session never claims finished work.
	TurnEndedAt int64 `json:"turn_ended_at,omitempty"`
	// Pinned is a user override the server merges in from its session-metadata
	// store; the session itself never sets it (a live session doesn't know it is
	// pinned). Kept here so the live list and the Recent list carry the same flag.
	Pinned bool `json:"pinned,omitempty"`
	// Workspace is what the sidebar groups this session under instead of its
	// directory. Like Pinned it is merged in by the server from the override
	// store, not set by the session.
	Workspace string `json:"workspace,omitempty"`
	// SnoozedUntil and SnoozedAt park this session on the sidebar's snoozed
	// shelf, unix ms. Merged in by the server like Pinned; the session itself
	// never sets them. The client wakes the row early when the session needs its
	// person, so these are a display promise rather than anything the session
	// acts on.
	SnoozedUntil int64 `json:"snoozed_until,omitempty"`
	SnoozedAt    int64 `json:"snoozed_at,omitempty"`
	// Projects counts the codebases this session has context for. One is an
	// ordinary session; more than one is what makes it a workspace, and is the
	// signal the client uses to offer naming it. The session does know this, so
	// unlike Pinned and Workspace it is filled in here.
	Projects int `json:"projects,omitempty"`
	// Repo is the main checkout when Cwd is a git worktree of it. Like Pinned and
	// Workspace it is merged in by the server, because only the server knows which
	// worktrees it made. It exists so the sidebar groups a worktree session under
	// the codebase it belongs to: grouping by its own directory would give every
	// worktree a heading of its own and scatter one repository across the list.
	Repo string `json:"repo,omitempty"`
	// Branch is the worktree's branch, when Cwd is one. Merged in by the server
	// like Repo. It is shown rather than the directory name because a worktree
	// that names itself from its first prompt renames the branch and leaves the
	// directory alone (a live process is running in it), so the directory goes
	// stale and the branch is the identity.
	Branch string `json:"branch,omitempty"`
}

func (s *Session) Meta() Meta {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Meta{
		ID: s.ID, Cwd: s.Cwd, Model: s.model, Effort: s.effort, CLI: s.cliName,
		Title: s.title, State: s.state, CreatedAt: s.CreatedAt,
		Projects: len(s.projects), TurnStartedAt: s.turnStartedAt, TurnEndedAt: s.turnEndedAt,
	}
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

// turnSummary describes a finished turn the way kunai measured it: how long it
// ran and what it cost. Both are numbers kunai produced about its own work, not
// anything that was asked or answered, so they can ride the notification channel
// without carrying session content. Returns "" when the frame reported neither,
// which leaves the notification at its plain wording.
func turnSummary(ev AppEvent) string {
	parts := make([]string, 0, 2)
	if d := shortDuration(ev.DurationMs); d != "" {
		parts = append(parts, d)
	}
	// Below a cent the rounded figure would read "$0.00", which says less than
	// saying nothing.
	if ev.CostUSD >= 0.01 {
		parts = append(parts, fmt.Sprintf("$%.2f", ev.CostUSD))
	}
	return strings.Join(parts, " · ")
}

// shortDuration renders a turn length for a notification: "38s", "4m 12s",
// "1h 4m". Compact because it sits on a lock screen next to other words.
func shortDuration(ms int64) string {
	if ms <= 0 {
		return ""
	}
	sec := ms / 1000
	switch {
	case sec < 60:
		return fmt.Sprintf("%ds", sec)
	case sec < 3600:
		return fmt.Sprintf("%dm %ds", sec/60, sec%60)
	default:
		return fmt.Sprintf("%dh %dm", sec/3600, (sec%3600)/60)
	}
}

// PID is the CLI process behind this session, or 0 when it is not running.
//
// Exposed so a listening port can be attributed to the session responsible for
// it: whatever the agent started is a descendant of this process.
func (s *Session) PID() int { return s.drv.PID() }
