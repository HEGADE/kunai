package codex

// Ported verbatim from CLIProxyAPI internal/util (claude_attribution.go,
// claude_tool_id.go). Pure stdlib. See EMBED note in doc.go.

import (
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
)

const claudeCodeAttributionSystemPrefix = "x-anthropic-billing-header:"

// IsClaudeCodeAttributionSystemText reports whether text is the Claude Code
// attribution block that carries per-request billing and prompt fingerprint data.
func IsClaudeCodeAttributionSystemText(text string) bool {
	text = strings.TrimLeftFunc(text, unicode.IsSpace)
	return strings.HasPrefix(text, claudeCodeAttributionSystemPrefix)
}

var (
	claudeToolUseIDSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_-]`)
	claudeToolUseIDCounter   uint64
)

// SanitizeClaudeToolID ensures the given id conforms to Claude's tool_use.id regex
// ^[a-zA-Z0-9_-]+$. Non-conforming characters are replaced with '_'; an empty
// result gets a generated fallback.
func SanitizeClaudeToolID(id string) string {
	s := claudeToolUseIDSanitizer.ReplaceAllString(id, "_")
	if s == "" {
		s = fmt.Sprintf("toolu_%d_%d", time.Now().UnixNano(), atomic.AddUint64(&claudeToolUseIDCounter, 1))
	}
	return s
}
