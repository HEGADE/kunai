package claude

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// EventKind classifies a decoded event emitted by a Session.
type EventKind string

const (
	EventInit       EventKind = "init"        // session/init: SessionID, Model, Cwd populated
	EventTextDelta  EventKind = "text_delta"  // Text populated (streaming token)
	EventThinking   EventKind = "thinking"    // Text populated (streaming thinking token)
	EventAssistant  EventKind = "assistant"   // full assistant turn; Assistant populated
	EventPermission EventKind = "permission"  // can_use_tool ask; Permission populated
	EventResult     EventKind = "result"      // turn complete; Raw is the result frame
	EventToolResult EventKind = "tool_result" // tool output; ToolResult populated
	EventCompact    EventKind = "compact"     // conversation compacted; Compact populated
	EventSystem     EventKind = "system"      // other system frames; Raw populated
	EventRateLimit  EventKind = "rate_limit"  // usage-window status; ResetsAt/Window set
	EventError      EventKind = "error"       // driver/transport error; Err populated
)

// Event is a decoded message from the CLI, surfaced to the session layer.
type Event struct {
	Kind EventKind

	// EventInit
	SessionID string
	Model     string
	Cwd       string

	// EventTextDelta / EventThinking
	Text string

	// EventAssistant
	Assistant *AssistantMessage

	// EventPermission
	Permission *PermissionAsk

	// EventToolResult
	ToolResult *ToolResult

	// EventCompact
	Compact *Compact

	// EventError
	Err error

	// EventRateLimit: when the current usage window resets and which window.
	ResetsAt    int64
	Window      string
	LimitStatus string

	// ParentToolUseID is non-empty when this event came from INSIDE a subagent: it
	// is the id of the Agent tool call that spawned it. Set on the assistant, text/
	// thinking-delta, and tool_result events a subagent produces, so the client can
	// nest that work under its Agent card instead of showing it as the main agent's.
	ParentToolUseID string

	// Raw is the original frame (always set for result/system; useful for debugging).
	Raw json.RawMessage
}

// Compact reports a compaction boundary: the conversation was summarised and the
// context window dropped from PreTokens to PostTokens.
type Compact struct {
	Trigger    string // "manual" (/compact) | "auto" (near the context limit)
	PreTokens  int64
	PostTokens int64
}

// PermissionAsk is a decoded can_use_tool request awaiting a verdict.
type PermissionAsk struct {
	RequestID   string          // control_request envelope request_id (echo in the response)
	ToolName    string          //
	ToolUseID   string          //
	Input       json.RawMessage //
	Title       string          //
	DisplayName string          //
	Description string          //
	Suggestions json.RawMessage // permission_suggestions (raw PermissionUpdate[])
}

// providerModelArg guards the --model value for a provider session. A session that
// ran on Claude has its model resolved to a full id (e.g. claude-opus-4-8) by the
// CLI's init event; when it is then switched to a provider, spawning --model
// claude-opus-4-8 sends that id straight through to the proxy/sidecar, which 404s
// with "unknown provider for model claude-opus-4-8" and the turn fails. A provider
// session is detected by ANTHROPIC_BASE_URL in its env; for one, a claude-* model is
// swapped for the opus slot, which the provider's ANTHROPIC_DEFAULT_OPUS_MODEL env
// remaps to the real upstream model. A non-Claude model (already a provider model, or
// a Claude session with no base URL) passes through untouched.
func providerModelArg(model string, env []string) string {
	if !strings.HasPrefix(strings.ToLower(model), "claude-") {
		return model
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "ANTHROPIC_BASE_URL=") && len(kv) > len("ANTHROPIC_BASE_URL=") {
			return "opus"
		}
	}
	return model
}

// Options configure a Session.
type Options struct {
	Cwd            string
	Model          string // optional model alias/name
	Effort         string // optional reasoning effort: low|medium|high|xhigh|max (spawn-time only)
	PermissionMode string // default "default"
	Resume         string // session id to resume (loads prior transcript), optional
	SessionID      string // explicit session id (must be a UUID), optional
	// Bin is the CLI binary to run; empty means "claude" (the default account).
	// A differently named or wrapped binary lets one machine drive more than one
	// Claude account. Env is extra environment (KEY=VALUE) appended to the process,
	// e.g. a CLAUDE_CONFIG_DIR that points at another account's auth.
	Bin string
	Env []string
	// AppendSystemPrompt is extra text appended to the CLI's own system prompt,
	// used to state facts the model must not lose (which git worktree it is in).
	// It is deliberately not sent as a first turn: a turn can be compacted away,
	// and would be, in exactly the long unattended run where forgetting matters
	// most. A system prompt stays resident for the life of the process.
	AppendSystemPrompt string
	// DisallowedTools names tools the CLI must not offer the model at all, passed
	// as --disallowedTools. Spawn-time, like everything else here, so changing it
	// means respawning the process.
	DisallowedTools []string
	// ReadyTimeout bounds how long Start waits for the init handshake.
	ReadyTimeout time.Duration
}

// Session drives one long-lived `claude` process over stdio. It stays open for
// multiple turns: send a user message, consume events until a result, repeat.
type Session struct {
	opts Options

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	out    chan any   // frames to write to the CLI's stdin
	events chan Event // decoded events to the caller

	procCtx    context.Context
	procCancel context.CancelFunc

	initID    string
	readyOnce sync.Once
	ready     chan struct{}

	mu        sync.Mutex
	sessionID string
	// procMu guards cmd and procCancel, which Start writes while Close may
	// already be reading them. Close races Start whenever a session is ended
	// while it is still booting, which is not exotic: the thermal guard stops
	// every session including a starting one, a restart closes and re-creates,
	// and a tab closed straight after opening does it by hand.
	procMu sync.Mutex

	// errTail keeps the CLI's last words to stderr, so the reason it exited
	// survives the exit; see exitReason.
	errTail stderrTail

	closeOnce sync.Once
	closed    chan struct{}
}

// NewSession creates an unstarted Session.
func NewSession(opts Options) *Session {
	if opts.PermissionMode == "" {
		opts.PermissionMode = "default"
	}
	if opts.ReadyTimeout == 0 {
		opts.ReadyTimeout = 30 * time.Second
	}
	return &Session{
		opts:   opts,
		out:    make(chan any, 64),
		events: make(chan Event, 256),
		ready:  make(chan struct{}),
		closed: make(chan struct{}),
	}
}

// Events returns the decoded event stream. The channel is closed when the
// session ends.
func (s *Session) Events() <-chan Event { return s.events }

// SessionID returns the CLI-assigned session id (available after init).
func (s *Session) SessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

func (s *Session) args() []string {
	a := []string{
		"-p",
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--include-partial-messages",
		"--verbose",
		"--permission-prompt-tool", "stdio",
		"--permission-mode", s.opts.PermissionMode,
	}
	if s.opts.Model != "" {
		a = append(a, "--model", providerModelArg(s.opts.Model, s.opts.Env))
	}
	if s.opts.Effort != "" {
		a = append(a, "--effort", s.opts.Effort)
	}
	if s.opts.AppendSystemPrompt != "" {
		a = append(a, "--append-system-prompt", s.opts.AppendSystemPrompt)
	}
	// Tools the session may not use at all, for a session shared with someone who
	// is not its owner. This removes them from the model's toolset rather than
	// gating them behind a prompt, which is the distinction the whole share design
	// rests on: a tool that is merely gated is one careless approval away from
	// running. Verified against claude 2.1.220 in exactly this mode.
	if len(s.opts.DisallowedTools) > 0 {
		a = append(a, "--disallowedTools", strings.Join(s.opts.DisallowedTools, ","))
	}
	if s.opts.SessionID != "" {
		a = append(a, "--session-id", s.opts.SessionID)
	}
	if s.opts.Resume != "" {
		a = append(a, "--resume", s.opts.Resume)
	}
	return a
}

// Start spawns the CLI, performs the initialize handshake, and blocks until the
// session is ready (or the ready timeout elapses / ctx is cancelled). Events
// begin flowing on Events() immediately.
func (s *Session) Start(ctx context.Context) error {
	// The process lifetime is owned by the session, NOT the caller's ctx: the
	// caller's ctx only bounds the readiness wait below. Binding the process to a
	// request context would kill claude the moment the HTTP handler returns.
	procCtx, procCancel := context.WithCancel(context.Background())
	s.procMu.Lock()
	s.procCtx, s.procCancel = procCtx, procCancel
	s.procMu.Unlock()
	bin := s.opts.Bin
	if bin == "" {
		bin = "claude"
	}
	cmd := exec.CommandContext(s.procCtx, bin, s.args()...)
	cmd.Dir = s.opts.Cwd
	cmd.Env = append(os.Environ(), s.opts.Env...)
	// Still to the service log, but kept as well: the CLI's parting words are the
	// only account of why it left, and sending them somewhere kunai cannot read
	// meant a session could die with the reason sitting in a journal nobody had
	// correlated to it.
	cmd.Stderr = io.MultiWriter(os.Stderr, &s.errTail)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn claude: %w", err)
	}
	s.procMu.Lock()
	s.cmd = cmd
	s.procMu.Unlock()
	s.stdin = stdin

	// Send initialize directly, before the write loop starts draining queued
	// frames — callers may enqueue user turns while the process is still
	// booting, and initialize must reach the CLI first.
	s.initID = randHex(8)
	if err := json.NewEncoder(stdin).Encode(ControlRequest{Type: TypeControlRequest, RequestID: s.initID, Request: InitializeRequest{Subtype: SubInitialize}}); err != nil {
		s.Close()
		return fmt.Errorf("write initialize: %w", err)
	}

	go s.writeLoop(stdin)
	go s.readLoop(stdout)
	go func() {
		// When the process exits, say WHY before closing down.
		//
		// This was `_ = cmd.Wait()`, throwing away the only signal available, with
		// stderr going straight to the service log where nothing could reason about
		// it. So a CLI that quit mid-conversation -- which is what hitting the usage
		// wall looks like -- ended the session in silence: no reason for the person
		// watching, and nothing for auto-failover to act on. Every later prompt then
		// failed with "session closed" and no explanation of what had closed it.
		werr := cmd.Wait()
		if reason := exitReason(werr, s.errTail.text()); reason != "" {
			s.emit(Event{Kind: EventError, Err: errors.New(reason)})
		}
		s.shutdown()
	}()

	select {
	case <-s.ready:
		return nil
	case <-time.After(s.opts.ReadyTimeout):
		s.Close()
		return errors.New("claude: init handshake timed out")
	case <-ctx.Done():
		s.Close()
		return ctx.Err()
	case <-s.closed:
		return errors.New("claude: process exited before ready")
	}
}

// SendUserText sends a plain-text user turn.
func (s *Session) SendUserText(text string) error {
	return s.SendUser(text)
}

// SendUser sends a user turn. content may be a string or a []ContentBlock.
func (s *Session) SendUser(content any) error {
	return s.send(UserEnvelope{
		Type:      TypeUser,
		Message:   UserMessage{Role: "user", Content: content},
		SessionID: s.SessionID(),
	})
}

// Resolve answers a pending can_use_tool request.
func (s *Session) Resolve(requestID string, result PermissionResult) error {
	return s.send(ControlResponse{
		Type: TypeControlResponse,
		Response: ControlResponseBody{
			Subtype:   "success",
			RequestID: requestID,
			Response:  result,
		},
	})
}

// PID is the CLI process's id, or 0 before it has started (or after it exits).
//
// Exposed for one reason: a server the agent starts -- a dev server, a test
// runner -- is a DESCENDANT of this process, and that parentage is the only
// honest way to say which session a listening port belongs to. See
// internal/preview.
func (s *Session) PID() int {
	s.procMu.Lock()
	defer s.procMu.Unlock()
	if s.cmd == nil || s.cmd.Process == nil {
		return 0
	}
	return s.cmd.Process.Pid
}

// Interrupt aborts the current turn.
func (s *Session) Interrupt() error {
	return s.send(ControlRequest{Type: TypeControlRequest, RequestID: randHex(8), Request: InterruptRequest{Subtype: SubInterrupt}})
}

// SetModel switches the model for subsequent turns.
func (s *Session) SetModel(model string) error {
	return s.send(ControlRequest{Type: TypeControlRequest, RequestID: randHex(8), Request: SetModelRequest{Subtype: SubSetModel, Model: model}})
}

// SetPermissionMode switches the permission mode for the session.
func (s *Session) SetPermissionMode(mode string) error {
	return s.send(ControlRequest{Type: TypeControlRequest, RequestID: randHex(8), Request: SetPermissionModeRequest{Subtype: SubSetPermMode, Mode: mode}})
}

// Close terminates the session and its process.
func (s *Session) Close() error {
	s.procMu.Lock()
	cancel, cmd := s.procCancel, s.cmd
	s.procMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	s.shutdown()
	return nil
}

func (s *Session) shutdown() {
	s.closeOnce.Do(func() {
		close(s.closed)
		close(s.events)
	})
}

func (s *Session) send(frame any) error {
	select {
	case s.out <- frame:
		return nil
	case <-s.closed:
		return errors.New("claude: session closed")
	}
}

func (s *Session) emit(ev Event) {
	// shutdown closes s.events, and a select can still pick the send case on a
	// closed channel (a closed-send is "ready" and panics) even when <-s.closed
	// is also ready. That race fires when a respawn (effort/account/model change)
	// closes the session while readLoop is still draining stdout. Recover turns a
	// late emit into a dropped event -- harmless, the session is tearing down --
	// instead of an unrecovered goroutine panic that would crash the whole server.
	defer func() { _ = recover() }()
	select {
	case s.events <- ev:
	case <-s.closed:
	}
}

func (s *Session) writeLoop(w io.WriteCloser) {
	enc := json.NewEncoder(w) // Encode writes value + '\n' — NDJSON
	for {
		select {
		case <-s.closed:
			return
		case frame := <-s.out:
			if err := enc.Encode(frame); err != nil {
				return
			}
		}
	}
}

func (s *Session) readLoop(r io.Reader) {
	dec := json.NewDecoder(r)
	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			if err != io.EOF {
				s.emit(Event{Kind: EventError, Err: err})
			}
			s.shutdown()
			return
		}
		var env Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			continue
		}
		s.route(env, raw)
	}
}

func (s *Session) route(env Envelope, raw json.RawMessage) {
	switch env.Type {
	case TypeControlRequest:
		var body ControlRequestBody
		_ = json.Unmarshal(env.Request, &body)
		if body.Subtype == SubCanUseTool {
			s.emit(Event{Kind: EventPermission, Raw: raw, Permission: &PermissionAsk{
				RequestID:   env.RequestID,
				ToolName:    body.ToolName,
				ToolUseID:   body.ToolUseID,
				Input:       body.Input,
				Title:       body.Title,
				DisplayName: body.DisplayName,
				Description: body.Description,
				Suggestions: body.PermissionSuggestions,
			}})
		}
		// Other CLI-originated control requests (rewind, mcp, dialogs) are not
		// handled yet; the CLI tolerates no response for these in our flows.

	case TypeControlResponse:
		if respRequestID(env.Response) == s.initID {
			s.markReady()
		}

	case TypeSystem:
		switch env.Subtype {
		case SubInit:
			// Also re-emitted mid-session after a compaction; harmless, the ids match.
			s.mu.Lock()
			s.sessionID = env.SessionID
			s.mu.Unlock()
			s.emit(Event{Kind: EventInit, SessionID: env.SessionID, Model: env.Model, Cwd: env.Cwd, Raw: raw})
			s.markReady()
		case SubCompactBoundary:
			var cb CompactBoundary
			if json.Unmarshal(raw, &cb) == nil {
				s.emit(Event{Kind: EventCompact, Raw: raw, Compact: &Compact{
					Trigger:    cb.Metadata.Trigger,
					PreTokens:  cb.Metadata.PreTokens,
					PostTokens: cb.Metadata.PostTokens,
				}})
			}
		default:
			s.emit(Event{Kind: EventSystem, Raw: raw})
		}

	case TypeStreamEvent:
		var ev StreamEvent
		if err := json.Unmarshal(env.Event, &ev); err == nil && ev.Type == "content_block_delta" {
			switch ev.Delta.Type {
			case "text_delta":
				if ev.Delta.Text != "" {
					s.emit(Event{Kind: EventTextDelta, Text: ev.Delta.Text, ParentToolUseID: env.ParentToolUseID})
				}
			case "thinking_delta":
				if ev.Delta.Text != "" {
					s.emit(Event{Kind: EventThinking, Text: ev.Delta.Text, ParentToolUseID: env.ParentToolUseID})
				}
			}
		}

	case TypeAssistant:
		var msg AssistantMessage
		if err := json.Unmarshal(env.Message, &msg); err == nil {
			s.emit(Event{Kind: EventAssistant, Assistant: &msg, Raw: raw, ParentToolUseID: env.ParentToolUseID})
		}

	case TypeResult:
		s.emit(Event{Kind: EventResult, Raw: raw})

	case TypeUser:
		// The CLI feeds tool outputs back to the model as user frames; surface them.
		// A frame from inside a subagent carries the spawning Agent call's id.
		s.emitToolResults(env.Message, env.ParentToolUseID)

	case TypeRateLimit:
		var rl struct {
			Info struct {
				Status   string `json:"status"`
				ResetsAt int64  `json:"resetsAt"`
				Type     string `json:"rateLimitType"`
			} `json:"rate_limit_info"`
		}
		if json.Unmarshal(raw, &rl) == nil && rl.Info.ResetsAt > 0 {
			s.emit(Event{Kind: EventRateLimit, ResetsAt: rl.Info.ResetsAt, Window: rl.Info.Type, LimitStatus: rl.Info.Status, Raw: raw})
		}

	case TypeKeepAlive:
		// ignore
	}
}

func (s *Session) markReady() {
	s.readyOnce.Do(func() { close(s.ready) })
}

// respRequestID digs request_id out of a control_response body.
func respRequestID(resp json.RawMessage) string {
	var v struct {
		RequestID string `json:"request_id"`
	}
	_ = json.Unmarshal(resp, &v)
	return v.RequestID
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// stderrTail keeps the last few KB the CLI wrote to stderr, so the reason it
// exited survives the exit. Bounded, because a chatty process should not be able
// to grow this without limit, and only the end matters: the complaint that comes
// immediately before quitting.
type stderrTail struct {
	mu  sync.Mutex
	buf []byte
}

const stderrTailMax = 4096

func (t *stderrTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > stderrTailMax {
		t.buf = t.buf[len(t.buf)-stderrTailMax:]
	}
	t.mu.Unlock()
	return len(p), nil
}

func (t *stderrTail) text() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}

// exitReason turns a process exit into a sentence worth showing, or "" when the
// exit needs no explanation.
//
// A clean exit after Close is the ordinary case and says nothing. Anything else
// ended the conversation without being asked to, and the person watching is
// owed the reason: the CLI's own last words when it left any, and its exit
// status when it left none, which is still better than a session that simply
// stops.
func exitReason(waitErr error, tail string) string {
	if waitErr == nil && tail == "" {
		return ""
	}
	// One line, so it fits where an error is shown. The CLI's complaint is
	// usually the last thing it printed.
	last := ""
	for _, ln := range strings.Split(tail, "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			last = ln
		}
	}
	if len(last) > 300 {
		last = last[:300] + "…"
	}
	switch {
	case last != "" && waitErr != nil:
		return "claude exited (" + waitErr.Error() + "): " + last
	case last != "":
		return "claude exited: " + last
	default:
		return "claude exited unexpectedly (" + waitErr.Error() + ")"
	}
}
