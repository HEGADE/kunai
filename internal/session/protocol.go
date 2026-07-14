// Package session turns raw claude.Session event streams into the app-facing
// protocol the PWA consumes: monotonically-sequenced events with a per-session
// replay buffer, permission tracking, and command handling. One claude process
// per Session; many phone connections may attach/detach without disturbing it.
package session

import "encoding/json"

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

	// "delta" / "thinking"
	Text string `json:"text,omitempty"`

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

	// "result"
	IsError    bool    `json:"is_error,omitempty"`
	DurationMs int64   `json:"duration_ms,omitempty"`
	Tokens     int64   `json:"tokens,omitempty"`   // total tokens (input+output+cache) for the turn
	CostUSD    float64 `json:"cost_usd,omitempty"` // total_cost_usd for the turn

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
	EvDelta              = "delta"
	EvThinking           = "thinking"
	EvAssistant          = "assistant"
	EvPermission         = "permission"
	EvPermissionResolved = "permission_resolved"
	EvToolResult         = "tool_result"
	EvResult             = "result"
	EvState              = "state"
	EvError              = "error"
	EvRateLimit          = "rate_limit"
)

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
}

// Command type tags.
const (
	CmdPrompt     = "prompt"
	CmdPermission = "permission"
	CmdInterrupt  = "interrupt"
	CmdSetModel   = "set_model"
	CmdSetMode    = "set_mode"
)

// Attachment is an uploaded file/image referenced by a prompt (Phase 3). The
// server resolves ID to bytes it staged during upload.
type Attachment struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	MediaType string `json:"media_type,omitempty"`
}
