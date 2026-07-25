package telegram

import "strings"

// Callback data prefixes for inline buttons. Kept short because Telegram caps
// callback data at 64 bytes and a request id already eats most of that.
const (
	CallbackApprove = "ok"
	CallbackDeny    = "no"
	CallbackResume  = "rs" // bring a closed session back; the arg is its id
	CallbackModel   = "md" // switch the session's model; the arg is the alias
	CallbackMode    = "pm" // switch the permission mode; the arg is the mode
)

// ChatModels are the model aliases offered as buttons. Aliases, not pinned ids,
// so the newest model of each family is what you get -- the same reason the app's
// picker sends an alias (see web/src/lib/models.ts).
var ChatModels = []string{"opus", "sonnet", "haiku", "fable"}

// ChatModes are the permission modes offered as buttons, with the plain-language
// label first: a chat is where you are least able to babysit a prompt, so what each
// mode will DO to you matters more than its internal name.
var ChatModes = []struct{ ID, Label string }{
	{"auto", "Auto - run safe things, ask otherwise"},
	{"acceptEdits", "Edits - auto-approve file edits"},
	{"default", "Ask - stop for every tool"},
	{"plan", "Plan - read-only, no changes"},
}

// Command names the bot understands.
const (
	CmdStart    = "start"
	CmdHelp     = "help"
	CmdNew      = "new"
	CmdSessions = "sessions"
	CmdUse      = "use"
	CmdResume   = "resume"
	CmdStatus   = "status"
	CmdStop     = "stop"
	CmdEnd      = "end"
	CmdModel    = "model"
	CmdMode     = "mode"
	CmdGet      = "get"
)

// callbackData builds the payload for an inline button. Telegram caps it at 64
// bytes, which a two-letter action plus a session id or a request id fits inside
// with room to spare; anything longer would be silently rejected by the API, so
// the cap is asserted in a test rather than trusted.
func callbackData(action, arg string) string { return action + ":" + arg }

// maxCallbackBytes is Telegram's ceiling on callback_data.
const maxCallbackBytes = 64

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
// Unknown or malformed data reports false rather than guessing: a button from an
// older build must be refused, not misread as a different action.
func ParseCallback(data string) (action, id string, ok bool) {
	action, id, found := strings.Cut(data, ":")
	if !found || id == "" {
		return "", "", false
	}
	switch action {
	case CallbackApprove, CallbackDeny, CallbackResume:
		return action, id, true
	}
	return "", "", false
}

// HelpText is what /start and /help answer with. It doubles as the list of
// everything the bot can do, so it lives next to the command names rather than
// drifting in a handler somewhere.
const HelpText = `kunai

Send any message to prompt the current session. Send a photo or a file and it
goes to the session too, with your caption as the prompt.

/new <path>    start a session in a directory
/sessions      list running sessions
/use <id>      switch this chat to a session
/resume <id>   bring a closed session back, with its conversation
/resume        list sessions you can bring back
/status        what the current session is doing
/model         switch the model for this session
/mode          switch what needs your approval
/get <path>    send a file from the session's machine to this chat
/stop          interrupt the running turn
/end           close the current session

Closing a session never loses it. Ending one here, or in the app, leaves you a
/resume command you can send later.

File contents and command output stay on the machine. Open the kunai app to see
them in full.`
