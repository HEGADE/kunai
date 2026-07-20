package telegram

import "strings"

// Callback data prefixes for inline buttons. Kept short because Telegram caps
// callback data at 64 bytes and a request id already eats most of that.
const (
	CallbackApprove = "ok"
	CallbackDeny    = "no"
)

// Command names the bot understands.
const (
	CmdStart    = "start"
	CmdHelp     = "help"
	CmdNew      = "new"
	CmdSessions = "sessions"
	CmdUse      = "use"
	CmdStatus   = "status"
	CmdStop     = "stop"
	CmdEnd      = "end"
)

// Command is a parsed line from a chat. Name is empty for ordinary text, which
// is the common case: anything that is not a command is a prompt.
type Command struct {
	Name string
	Arg  string
}

// IsPrompt reports whether this line should go to the model rather than to the
// bot itself.
func (c Command) IsPrompt() bool { return c.Name == "" }

// ParseCommand splits a chat line into a command and its argument.
//
// Telegram appends the bot's username to commands sent in a group
// ("/new@kunai_bot /srv/app"), so that suffix is stripped: the same message
// means the same thing in a group and in a private chat.
func ParseCommand(text string) Command {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/") {
		return Command{Arg: trimmed}
	}
	head, rest, _ := strings.Cut(trimmed[1:], " ")
	if at := strings.IndexByte(head, '@'); at >= 0 {
		head = head[:at]
	}
	return Command{
		Name: strings.ToLower(head),
		Arg:  strings.TrimSpace(rest),
	}
}

// ParseCallback splits inline button data into its action and the id it acts on.
// Unknown or malformed data reports false rather than guessing, since the only
// buttons in play answer a permission prompt.
func ParseCallback(data string) (action, id string, ok bool) {
	action, id, found := strings.Cut(data, ":")
	if !found || id == "" {
		return "", "", false
	}
	switch action {
	case CallbackApprove, CallbackDeny:
		return action, id, true
	}
	return "", "", false
}

// HelpText is what /start and /help answer with. It doubles as the list of
// everything the bot can do, so it lives next to the command names rather than
// drifting in a handler somewhere.
const HelpText = `kunai

Send any message to prompt the current session.

/new <path>   start a session in a directory
/sessions     list running sessions
/use <id>     switch this chat to a session
/status       what the current session is doing
/stop         interrupt the running turn
/end          close the current session

File contents and command output stay on the machine. Open the kunai app to see
them in full.`
