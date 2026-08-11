package claude

import (
	"encoding/json"
	"strings"
)

// Tool results arrive from the CLI as user-role frames whose content is one or
// more tool_result blocks — the output of a tool the CLI just ran. We decode
// them here and surface each as an EventToolResult, correlated to its tool_use
// by ToolUseID. Kept in its own file so the driver's route() stays a thin switch.

// maxToolResultBytes caps a single tool output. Outputs can be enormous (a Read
// of a large file, a chatty command), and every event lives in the per-session
// ring buffer and is replayed to clients — so we truncate defensively.
const maxToolResultBytes = 24 * 1024

// ImageResultMarker stands in for an image block in a tool's output.
//
// It is a wire contract with the client, not a cosmetic placeholder. The bytes
// are dropped on purpose -- a Read of a screenshot returns base64 that would
// cross the socket, sit in the replay ring and be resent on every reconnect, all
// to show a file the machine already has and can serve on request -- so this is
// what says "there was a picture here". The client pairs it with the path the
// tool was given and fetches the image itself; see ResultView.svelte. Mirrored
// as IMAGE_RESULT_MARKER in web/src/lib/toolMeta.ts.
const ImageResultMarker = "[image]"

// userMessage is an inbound user-role message (env.Message on a "user" frame).
type userMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // []toolResultBlock (or a plain string)
}

// toolResultBlock is a tool_result content block inside a user message.
type toolResultBlock struct {
	Type      string          `json:"type"`        // "tool_result"
	ToolUseID string          `json:"tool_use_id"` // correlates to a tool_use id
	Content   json.RawMessage `json:"content"`     // string, or []ContentBlock
	IsError   bool            `json:"is_error,omitempty"`
}

// ToolResult is a decoded tool output surfaced to the session layer.
type ToolResult struct {
	ToolUseID string
	Content   string
	IsError   bool
	Truncated bool
}

// emitToolResults decodes tool_result blocks from a live user frame and emits
// one EventToolResult per block. Non-tool_result content (e.g. an echoed user
// text message) yields nothing.
// emitToolResults surfaces the tool outputs the CLI feeds back as user frames.
// parentToolUseID is non-empty when the frame came from inside a subagent (the id
// of the Agent call that spawned it), so the result nests under that agent.
func (s *Session) emitToolResults(msg json.RawMessage, parentToolUseID string) {
	var um userMessage
	if json.Unmarshal(msg, &um) != nil {
		return
	}
	for _, r := range ParseToolResultBlocks(um.Content) {
		r := r
		s.emit(Event{Kind: EventToolResult, ToolResult: &r, ParentToolUseID: parentToolUseID})
	}
}

// ParseToolResultBlocks decodes tool_result blocks from a user message's content
// array. Shared by the live driver and transcript seeding (history.go) so both
// normalize output identically.
func ParseToolResultBlocks(content json.RawMessage) []ToolResult {
	var blocks []toolResultBlock
	if json.Unmarshal(content, &blocks) != nil {
		return nil // content was a plain string / not a block array
	}
	out := make([]ToolResult, 0, len(blocks))
	for _, b := range blocks {
		if b.Type != "tool_result" || b.ToolUseID == "" {
			continue
		}
		text, truncated := toolResultText(b.Content)
		out = append(out, ToolResult{
			ToolUseID: b.ToolUseID,
			Content:   text,
			IsError:   b.IsError,
			Truncated: truncated,
		})
	}
	return out
}

// toolResultText normalizes a tool_result's content (a string or an array of
// content blocks) into capped plain text.
func toolResultText(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return capText(str)
	}
	var blocks []ContentBlock
	if json.Unmarshal(raw, &blocks) == nil {
		var sb strings.Builder
		for _, b := range blocks {
			switch b.Type {
			case "text":
				sb.WriteString(b.Text)
			case "image":
				// The bytes are deliberately dropped: a Read of a screenshot comes
				// back as base64 that would go over the socket, into the replay ring
				// and into every reconnect, to show a picture the machine already has
				// on disk and can serve on request. What is left is a MARKER, and the
				// client treats it as one: seeing it beside a tool whose input named
				// an image file is what turns the result into the picture (see
				// ResultView.svelte). Changing this string means changing that.
				sb.WriteString(ImageResultMarker)
			}
		}
		return capText(sb.String())
	}
	return "", false
}

func capText(s string) (string, bool) {
	if len(s) <= maxToolResultBytes {
		return s, false
	}
	// Trim to a rune boundary so we never split a UTF-8 sequence.
	cut := maxToolResultBytes
	for cut > 0 && !utf8RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}

func utf8RuneStart(b byte) bool {
	return b&0xC0 != 0x80
}
