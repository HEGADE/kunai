// Package claude implements the driver that speaks Claude Code's stream-json
// control protocol over the spawned `claude` CLI's stdin/stdout (newline-delimited
// JSON). This is the same protocol the official @anthropic-ai/claude-agent-sdk
// speaks; the hidden `--sdk-url` websocket transport is locked to Anthropic's own
// backend on current CLI versions and is not usable for a self-hosted server.
//
// The protocol is undocumented (the SDK's sdk.d.ts is the closest reference).
// These types are deliberately tolerant — known fields are typed, everything else
// is preserved as raw JSON — so the driver can observe the real message shapes
// without dropping data as the CLI evolves.
package claude

import "encoding/json"

// Envelope is the top-level message exchanged with the CLI. Every frame is a
// single JSON object with a "type" discriminator.
type Envelope struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype,omitempty"`
	RequestID string          `json:"request_id,omitempty"`
	Request   json.RawMessage `json:"request,omitempty"`
	Response  json.RawMessage `json:"response,omitempty"`
	Event     json.RawMessage `json:"event,omitempty"`
	Message   json.RawMessage `json:"message,omitempty"`
	SessionID string          `json:"session_id,omitempty"`
	Model     string          `json:"model,omitempty"`
	Cwd       string          `json:"cwd,omitempty"`
	UUID      string          `json:"uuid,omitempty"`
}

// Message type discriminators (the "type" field).
const (
	TypeControlRequest  = "control_request"
	TypeControlResponse = "control_response"
	TypeControlCancel   = "control_cancel_request"
	TypeSystem          = "system"
	TypeUser            = "user"
	TypeAssistant       = "assistant"
	TypeStreamEvent     = "stream_event"
	TypeResult          = "result"
	TypeKeepAlive       = "keep_alive"
	TypeRateLimit       = "rate_limit_event"
)

// Control request subtypes.
const (
	SubInitialize      = "initialize"
	SubCanUseTool      = "can_use_tool"
	SubInterrupt       = "interrupt"
	SubSetModel        = "set_model"
	SubSetPermMode     = "set_permission_mode"
	SubInit            = "init"             // system/init
	SubCompactBoundary = "compact_boundary" // system/compact_boundary
)

// CompactBoundary is the system/compact_boundary frame: the CLI has replaced the
// conversation with a summary, either from /compact or on its own near the
// context limit. PostTokens is the only report of the new context size, because
// a compaction emits no assistant message — miss this frame and the meter keeps
// showing the pre-compaction number until the next turn happens to correct it.
//
// The wire spells the metadata snake_case. The transcript file on disk carries
// the same data camelCase (compactMetadata/postTokens), so the transcript reader
// in the server decodes its own copy rather than reusing this type.
type CompactBoundary struct {
	Metadata struct {
		Trigger    string `json:"trigger"` // "manual" (/compact) | "auto"
		PreTokens  int64  `json:"pre_tokens"`
		PostTokens int64  `json:"post_tokens"`
	} `json:"compact_metadata"`
}

// ControlRequestBody is the inner "request" of a control_request. Only the
// discriminating subtype and the fields we act on are typed here.
type ControlRequestBody struct {
	Subtype string `json:"subtype"`

	// can_use_tool fields
	ToolName              string          `json:"tool_name,omitempty"`
	Input                 json.RawMessage `json:"input,omitempty"`
	ToolUseID             string          `json:"tool_use_id,omitempty"`
	PermissionSuggestions json.RawMessage `json:"permission_suggestions,omitempty"`
	Title                 string          `json:"title,omitempty"`
	DisplayName           string          `json:"display_name,omitempty"`
	Description           string          `json:"description,omitempty"`
}

// InitializeRequest is the control_request we send to configure the session.
// All fields are optional; we send a minimal one.
type InitializeRequest struct {
	Subtype string `json:"subtype"` // "initialize"
}

// InterruptRequest aborts the current turn.
type InterruptRequest struct {
	Subtype string `json:"subtype"` // "interrupt"
}

// SetModelRequest changes the model for subsequent turns.
type SetModelRequest struct {
	Subtype string `json:"subtype"` // "set_model"
	Model   string `json:"model,omitempty"`
}

// SetPermissionModeRequest switches the session's permission mode
// ("default", "acceptEdits", "auto", "plan", …).
type SetPermissionModeRequest struct {
	Subtype string `json:"subtype"` // "set_permission_mode"
	Mode    string `json:"mode"`
}

// ControlRequest is a control_request we originate (initialize, interrupt, …).
type ControlRequest struct {
	Type      string      `json:"type"` // "control_request"
	RequestID string      `json:"request_id"`
	Request   interface{} `json:"request"`
}

// ControlResponse is what we send back to a CLI-originated control_request
// (e.g. our verdict for can_use_tool).
type ControlResponse struct {
	Type     string              `json:"type"` // "control_response"
	Response ControlResponseBody `json:"response"`
}

type ControlResponseBody struct {
	Subtype   string      `json:"subtype"` // "success" | "error"
	RequestID string      `json:"request_id"`
	Response  interface{} `json:"response,omitempty"`
	Error     string      `json:"error,omitempty"`
}

// PermissionResult is the body of a can_use_tool response (behavior "allow" or
// "deny"). Mirrors the SDK's PermissionResult.
type PermissionResult struct {
	Behavior           string             `json:"behavior"` // "allow" | "deny"
	UpdatedInput       json.RawMessage    `json:"updatedInput,omitempty"`
	UpdatedPermissions []PermissionUpdate `json:"updatedPermissions,omitempty"`
	Message            string             `json:"message,omitempty"` // deny reason
	Interrupt          bool               `json:"interrupt,omitempty"`
	ToolUseID          string             `json:"toolUseID,omitempty"`
}

// PermissionUpdate expresses a persisted permission change, e.g. "always allow
// Bash(git *) for this session".
type PermissionUpdate struct {
	Type        string           `json:"type"` // "addRules", "setMode", …
	Rules       []PermissionRule `json:"rules,omitempty"`
	Behavior    string           `json:"behavior,omitempty"` // "allow" | "deny" | "ask"
	Destination string           `json:"destination,omitempty"`
	Mode        string           `json:"mode,omitempty"`
}

type PermissionRule struct {
	ToolName    string `json:"toolName"`
	RuleContent string `json:"ruleContent,omitempty"`
}

// UserEnvelope is a user turn we send into the CLI.
type UserEnvelope struct {
	Type            string      `json:"type"` // "user"
	Message         UserMessage `json:"message"`
	ParentToolUseID *string     `json:"parent_tool_use_id"`
	SessionID       string      `json:"session_id"`
}

type UserMessage struct {
	Role    string      `json:"role"`    // "user"
	Content interface{} `json:"content"` // string, or []ContentBlock for attachments
}

// ContentBlock is an API-style content block used for attachments (text/image).
type ContentBlock struct {
	Type   string       `json:"type"` // "text" | "image"
	Text   string       `json:"text,omitempty"`
	Source *ImageSource `json:"source,omitempty"`
}

type ImageSource struct {
	Type      string `json:"type"`       // "base64"
	MediaType string `json:"media_type"` // "image/png", …
	Data      string `json:"data"`       // base64
}

// StreamEvent is the Anthropic streaming event carried in a stream_event frame
// (only emitted with --verbose --include-partial-messages).
type StreamEvent struct {
	Type  string `json:"type"` // e.g. "content_block_delta"
	Delta struct {
		Type string `json:"type"` // "text_delta" | "thinking_delta" | …
		Text string `json:"text"`
	} `json:"delta"`
}

// MessageUsage is one model call's token accounting. Unlike the result frame's
// usage (which is cumulative over every call in a turn), this is per-call, so
// its input side is the context actually sent to the model for that call.
type MessageUsage struct {
	Input       int64 `json:"input_tokens"`
	Output      int64 `json:"output_tokens"`
	CacheCreate int64 `json:"cache_creation_input_tokens"`
	CacheRead   int64 `json:"cache_read_input_tokens"`
}

// ContextTokens is the context-window occupancy for this call: everything sent
// as prompt (fresh input plus cache-created and cache-read tokens).
func (u *MessageUsage) ContextTokens() int64 {
	if u == nil {
		return 0
	}
	return u.Input + u.CacheCreate + u.CacheRead
}

// AssistantMessage is the full assistant turn (content blocks, incl. tool_use).
type AssistantMessage struct {
	ID         string                  `json:"id"`
	Model      string                  `json:"model"`
	Role       string                  `json:"role"`
	Content    []AssistantContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason"`
	Usage      *MessageUsage           `json:"usage,omitempty"`
}

type AssistantContentBlock struct {
	Type  string          `json:"type"` // "text" | "tool_use" | "thinking"
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`    // tool_use id
	Name  string          `json:"name,omitempty"`  // tool name
	Input json.RawMessage `json:"input,omitempty"` // tool input
}
