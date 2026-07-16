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
		a = append(a, "--model", s.opts.Model)
	}
	if s.opts.Effort != "" {
		a = append(a, "--effort", s.opts.Effort)
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
	s.procCtx, s.procCancel = context.WithCancel(context.Background())
	bin := s.opts.Bin
	if bin == "" {
		bin = "claude"
	}
	cmd := exec.CommandContext(s.procCtx, bin, s.args()...)
	cmd.Dir = s.opts.Cwd
	cmd.Env = append(os.Environ(), s.opts.Env...)
	cmd.Stderr = os.Stderr

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
	s.cmd = cmd
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
		// When the process exits, close down the session.
		_ = cmd.Wait()
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
	if s.procCancel != nil {
		s.procCancel()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
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
					s.emit(Event{Kind: EventTextDelta, Text: ev.Delta.Text})
				}
			case "thinking_delta":
				if ev.Delta.Text != "" {
					s.emit(Event{Kind: EventThinking, Text: ev.Delta.Text})
				}
			}
		}

	case TypeAssistant:
		var msg AssistantMessage
		if err := json.Unmarshal(env.Message, &msg); err == nil {
			s.emit(Event{Kind: EventAssistant, Assistant: &msg, Raw: raw})
		}

	case TypeResult:
		s.emit(Event{Kind: EventResult, Raw: raw})

	case TypeUser:
		// The CLI feeds tool outputs back to the model as user frames; surface them.
		s.emitToolResults(env.Message)

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
