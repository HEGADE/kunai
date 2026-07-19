// Package session turns raw claude.Session event streams into the app-facing
// protocol the PWA consumes: monotonically-sequenced events with a per-session
// replay buffer, permission tracking, and command handling. One claude process
// per Session; many phone connections may attach/detach without disturbing it.
package session

import (
	"encoding/json"

	"github.com/hegade/kunai/internal/project"
)

// AppEvent is a server→client frame. Every event carries a monotonic Seq within
// its session so a reconnecting client can request "everything after Seq N".
// Fields are shared across event types and omitted when empty; the client
// dispatches on T.
type AppEvent struct {
	Seq uint64 `json:"seq"`
	T   string `json:"t"`

	// "hello" (sent once on attach)
	ID      string     `json:"id,omitempty"`
	Cwd     string     `json:"cwd,omitempty"`
	Model   string     `json:"model,omitempty"`
	Title   string     `json:"title,omitempty"`
	State   string     `json:"state,omitempty"`
	Mode    string     `json:"mode,omitempty"`   // permission mode
	Effort  string     `json:"effort,omitempty"` // reasoning effort (hello)
	HighSeq uint64     `json:"high_seq,omitempty"`
	Pending []AppEvent `json:"pending,omitempty"` // unresolved permission asks
	Queued  []AppEvent `json:"queued,omitempty"`  // prompts waiting for the current turn

	// "hello": every codebase this session has been given context for.
	Projects []project.Info `json:"projects,omitempty"`

	// "project": a codebase just added to the session. Metadata only — nothing in
	// it has been read; the model reaches the files by path when it needs them.
	Project *project.Info `json:"project,omitempty"`

	// "hello" / "loop": the session's self-prompting run, if it has one. Sent on
	// every change (start, each iteration, and the ending) so a client that was
	// away can render the whole thing without keeping its own tally.
	Loop *LoopStatus `json:"loop,omitempty"`

	// "queued" / "unqueued": a prompt parked until the current turn ends. It is
	// unqueued when it starts running (a "user" event follows) or is cancelled.
	QueueID string `json:"queue_id,omitempty"`

	// "delta" / "thinking" / "user"
	Text string `json:"text,omitempty"`

	// "user": what was attached to the prompt. Metadata only (name + type) — the
	// bytes already went to Claude and are never served back; this just lets the
	// message show what rode along with it.
	Attachments []Attachment `json:"attachments,omitempty"`

	// "assistant"
	Blocks []AppBlock `json:"blocks,omitempty"`

	// "permission" / "permission_resolved"
	RequestID   string          `json:"request_id,omitempty"`
	ToolName    string          `json:"tool_name,omitempty"`
	ToolUseID   string          `json:"tool_use_id,omitempty"`
	Input       json.RawMessage `json:"input,omitempty"`
	PermTitle   string          `json:"perm_title,omitempty"`
	Description string          `json:"description,omitempty"`
	Suggestions json.RawMessage `json:"suggestions,omitempty"`
	Behavior    string          `json:"behavior,omitempty"`

	// "hello" / "assistant" / "compact": tokens occupying the context window. On
	// hello/assistant it is the newest model call's per-call usage; on compact it
	// is the window after the summary replaced the conversation. Drives the
	// context meter. (Not on "result" — that frame's usage is cumulative over the
	// turn, not the context size.)
	ContextTokens int64 `json:"context_tokens,omitempty"`

	// "compact": the conversation was replaced by a summary. ContextTokens above
	// carries the full window afterwards (conversation plus the resident overhead
	// that never leaves), which drives the meter; PostTokens is the raw
	// conversation-only size the CLI reported, which the compaction divider shows
	// as the "after" number so it matches Claude's own /compact banner. PreTokens
	// is where it came from. The summary text itself is deliberately never sent: it
	// is the model's context, not a message anyone wrote, and dumping it in the log
	// buries the conversation.
	PreTokens  int64  `json:"pre_tokens,omitempty"`
	PostTokens int64  `json:"post_tokens,omitempty"`
	Trigger    string `json:"trigger,omitempty"` // "manual" (/compact) | "auto"

	// "result"
	IsError    bool    `json:"is_error,omitempty"`
	DurationMs int64   `json:"duration_ms,omitempty"`
	Tokens     int64   `json:"tokens,omitempty"`   // total tokens (input+output+cache) for the turn
	CostUSD    float64 `json:"cost_usd,omitempty"` // this turn's cost (see turnResult)

	// "result": how the turn's tokens break down, summed over its model calls.
	// New is what the model read fresh and cached is conversation it re-read on
	// each tool call, which is why a long turn's total runs to millions while
	// costing little — cached reads bill at a fraction of new input.
	NewTokens    int64 `json:"new_tokens,omitempty"`
	CachedTokens int64 `json:"cached_tokens,omitempty"`
	OutputTokens int64 `json:"output_tokens,omitempty"`

	// "tool_result" (ToolUseID + IsError reused from above)
	Content   string `json:"content,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`

	// "error" / generic message
	Message string `json:"message,omitempty"`

	// "rate_limit": the current usage window, when it resets, and whether the
	// last turn was allowed/limited — drives the in-chat "schedule after reset".
	Window      string `json:"window,omitempty"`
	ResetsAt    int64  `json:"resets_at,omitempty"`
	LimitStatus string `json:"limit_status,omitempty"`
}

// App event type tags (the T field).
const (
	EvHello              = "hello"
	EvUser               = "user"
	EvQueued             = "queued"
	EvProject            = "project"
	EvUnqueued           = "unqueued"
	EvDelta              = "delta"
	EvThinking           = "thinking"
	EvAssistant          = "assistant"
	EvPermission         = "permission"
	EvPermissionResolved = "permission_resolved"
	EvToolResult         = "tool_result"
	EvCompact            = "compact"
	EvLoop               = "loop"
	EvMode               = "mode"
	EvResult             = "result"
	EvState              = "state"
	EvError              = "error"
	EvRateLimit          = "rate_limit"
)

// DefaultPermissionMode is the mode every session starts in — new, resumed, or
// respawned. Auto lets Claude get on with safe work and stop only for what
// genuinely needs a decision: these sessions are usually driven from a phone,
// where a prompt per tool call is the difference between useful and unusable.
// It is applied when the process is spawned, so it holds from the first tool
// call; the composer can still switch any session to another mode.
const DefaultPermissionMode = "auto"

// Turn/session states.
const (
	StateStarting = "starting" // claude process is booting
	StateIdle     = "idle"
	StateRunning  = "running"
	StateAwaiting = "awaiting_permission"
)

// AppBlock is one content block of a full assistant message.
type AppBlock struct {
	Type  string          `json:"type"` // "text" | "tool_use" | "thinking"
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

// Command is a client→server frame.
type Command struct {
	T string `json:"t"`

	// "prompt"
	Text        string       `json:"text,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`

	// "permission"
	RequestID string `json:"request_id,omitempty"`
	Behavior  string `json:"behavior,omitempty"` // "allow" | "deny"
	Always    bool   `json:"always,omitempty"`   // persist as a session rule
	// Answers is set only for the AskUserQuestion tool: question text -> chosen
	// answer (multi-select comma-joined). Merged into updatedInput on allow.
	Answers map[string]string `json:"answers,omitempty"`

	// "set_model"
	Model string `json:"model,omitempty"`

	// "set_mode"
	Mode string `json:"mode,omitempty"`

	// "cancel_queued"
	QueueID string `json:"queue_id,omitempty"`

	// "add_project"
	Path string `json:"path,omitempty"`

	// "start_loop"
	Loop *LoopConfig `json:"loop,omitempty"`
}

// Command type tags.
const (
	CmdPrompt       = "prompt"
	CmdPermission   = "permission"
	CmdInterrupt    = "interrupt"
	CmdSetModel     = "set_model"
	CmdSetMode      = "set_mode"
	CmdCancelQueued = "cancel_queued"
	CmdAddProject   = "add_project"
	CmdStartLoop    = "start_loop"
	CmdStopLoop     = "stop_loop"
)

// Attachment is an uploaded file/image referenced by a prompt (Phase 3). The
// server resolves ID to bytes it staged during upload.
type Attachment struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	MediaType string `json:"media_type,omitempty"`
}
