package telegram

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hegade/kunai/internal/session"
)

// Turning session events into chat messages, and the one place that decides what
// is allowed to leave the machine.
//
// Telegram is a third party. Everything sent through it lands in a log nobody
// here controls, so this file draws the line: Telegram carries the conversation
// and the controls, never the contents of your files or the output of your
// commands. A tool call is announced by name and shape ("Edit internal/x.go"),
// which is enough to follow along and approve, while the bytes stay on the
// machine where the PWA can show them in full.
//
// The risk this guards against is not really your source. It is the incidental
// spill: a config file the agent reads, a token a test echoes, an env dump in a
// debug command. None of that is anything you would choose to post.

// Policy decides how much of a tool call's detail may be sent.
type Policy struct {
	// ToolInputs sends the arguments a tool was called with (the command line,
	// the file path and the strings being swapped). Off by default: an Edit's
	// arguments are your file's contents.
	ToolInputs bool
	// ToolOutputs sends what a tool returned (file contents, stdout). Off by
	// default, and the most expensive one to turn on, because Read and Bash are
	// where whole files and env dumps come back.
	ToolOutputs bool
}

// StrictPolicy is the default: names and shapes only, no contents either way.
func StrictPolicy() Policy { return Policy{} }

// Rendered is one outgoing message, plus whatever buttons belong on it.
type Rendered struct {
	Text     string
	Keyboard *InlineKeyboard
	// Stream marks a reply that arrives in pieces, so the sender edits one
	// message instead of posting a new one per fragment.
	Stream bool
}

// RenderEvent turns a session event into a message, or reports false when the
// event is not worth a notification. Most events are not: deltas are handled by
// the streamer, and state changes are noise in a chat.
func RenderEvent(ev session.AppEvent, p Policy) (Rendered, bool) {
	switch ev.T {
	case session.EvAssistant:
		if text := assistantText(ev); text != "" {
			return Rendered{Text: text, Stream: true}, true
		}
		// An assistant turn that was only tool calls says nothing on its own.
		return Rendered{}, false

	case session.EvPermission:
		return renderPermission(ev, p), true

	case session.EvToolResult:
		return renderToolResult(ev, p)

	case session.EvResult:
		return Rendered{Text: renderResult(ev)}, true

	case session.EvError:
		return Rendered{Text: "Error: " + oneLine(ev.Text)}, true

	case session.EvCompact:
		return Rendered{Text: fmt.Sprintf("Compacted the conversation (now about %s).",
			tokens(ev.ContextTokens))}, true

	case session.EvRateLimit:
		if ev.LimitStatus == "" || ev.LimitStatus == "allowed" || ev.LimitStatus == "allowed_warning" {
			return Rendered{}, false
		}
		return Rendered{Text: "This account's usage window is spent."}, true
	}
	return Rendered{}, false
}

// assistantText pulls the prose out of an assistant message. Thinking blocks are
// dropped, matching what the web client replays, and tool_use blocks are handled
// as their own events.
func assistantText(ev session.AppEvent) string {
	var b strings.Builder
	for _, blk := range ev.Blocks {
		if blk.Type != "text" || strings.TrimSpace(blk.Text) == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(strings.TrimSpace(blk.Text))
	}
	return b.String()
}

// renderPermission is the approve or deny prompt. It always names the tool, and
// under a strict policy describes the call rather than quoting it: enough to
// judge "this wants to run a command" without posting the command.
func renderPermission(ev session.AppEvent, p Policy) Rendered {
	name := ev.ToolName
	if name == "" {
		name = "A tool"
	}
	head := name + " wants to run."
	if ev.PermTitle != "" {
		head = oneLine(ev.PermTitle)
	}
	body := head
	if detail := toolDetail(ev.ToolName, ev.Input, p); detail != "" {
		body += "\n" + detail
	}
	if !p.ToolInputs {
		body += "\n\nOpen kunai to see the full request."
	}
	return Rendered{
		Text: body,
		Keyboard: &InlineKeyboard{Rows: [][]InlineButton{{
			{Text: "Approve", Data: CallbackApprove + ":" + ev.RequestID},
			{Text: "Deny", Data: CallbackDeny + ":" + ev.RequestID},
		}}},
	}
}

// renderToolResult reports that a tool finished. Under a strict policy it sends
// no output at all, only whether it failed, since this is the event that would
// otherwise carry a file the agent just read.
func renderToolResult(ev session.AppEvent, p Policy) (Rendered, bool) {
	if !p.ToolOutputs {
		if ev.IsError {
			return Rendered{Text: "A tool call failed. Open kunai for the output."}, true
		}
		// A successful tool call is not news without its output.
		return Rendered{}, false
	}
	out := strings.TrimSpace(ev.Content)
	if out == "" {
		return Rendered{}, false
	}
	label := "Output"
	if ev.IsError {
		label = "Failed"
	}
	return Rendered{Text: label + ":\n" + out}, true
}

// toolDetail describes a call. With inputs allowed it quotes the arguments; with
// the strict default it says only what kind of thing is being asked for, which
// is the difference between "Bash wants to run" and posting your command.
func toolDetail(tool string, input json.RawMessage, p Policy) string {
	if len(input) == 0 {
		return ""
	}
	var args map[string]any
	if json.Unmarshal(input, &args) != nil {
		return ""
	}
	if p.ToolInputs {
		return oneLine(compactArgs(args))
	}
	// Paths are structure, not contents: knowing which file is being edited is
	// what makes an approval a decision rather than a coin flip.
	if path := firstString(args, "file_path", "path", "notebook_path"); path != "" {
		return path
	}
	switch tool {
	case "Bash":
		return "A shell command (hidden)"
	case "Read", "Grep", "Glob":
		return "Reading from the project"
	}
	return ""
}

// renderResult is the end of a turn: how long it took and what it cost, the same
// facts the push notification carries, and never anything that was said.
func renderResult(ev session.AppEvent) string {
	if ev.IsError {
		return "Turn failed." + durationSuffix(ev)
	}
	parts := []string{"Done"}
	if d := duration(ev.DurationMs); d != "" {
		parts = append(parts, d)
	}
	if ev.CostUSD >= 0.01 {
		parts = append(parts, fmt.Sprintf("$%.2f", ev.CostUSD))
	}
	return strings.Join(parts, " · ")
}

func durationSuffix(ev session.AppEvent) string {
	if d := duration(ev.DurationMs); d != "" {
		return " " + d
	}
	return ""
}

// duration renders a turn length compactly for a chat line.
func duration(ms int64) string {
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

// tokens renders a context size the way the app does.
func tokens(n int64) string {
	switch {
	case n <= 0:
		return "0"
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 1_000_000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
}

// compactArgs renders tool arguments on one line, for the permissive policy.
func compactArgs(args map[string]any) string {
	if cmd, ok := args["command"].(string); ok {
		return cmd
	}
	b, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	return string(b)
}

func firstString(args map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := args[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// oneLine flattens text to a single line so a message cannot be exploded by a
// model that decided to answer in a table.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
