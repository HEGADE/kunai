package telegram

import "testing"

func TestParseCommand(t *testing.T) {
	cases := []struct {
		in       string
		wantName string
		wantArg  string
	}{
		{"/new /srv/app", CmdNew, "/srv/app"},
		{"/sessions", CmdSessions, ""},
		{"  /help  ", CmdHelp, ""},
		{"/USE abc123", CmdUse, "abc123"},
		// Telegram appends the bot's username in groups; the same message has to
		// mean the same thing there as in a private chat.
		{"/new@kunai_bot /srv/app", CmdNew, "/srv/app"},
		{"/stop@kunai_bot", CmdStop, ""},
		// Anything that is not a command is a prompt, which is the common case.
		{"fix the failing test", "", "fix the failing test"},
		{"", "", ""},
	}
	for _, c := range cases {
		got := ParseCommand(c.in)
		if got.Name != c.wantName || got.Arg != c.wantArg {
			t.Errorf("ParseCommand(%q) = {%q, %q}, want {%q, %q}", c.in, got.Name, got.Arg, c.wantName, c.wantArg)
		}
	}
}

// A prompt beginning with a slash would otherwise be swallowed as an unknown
// command, so the distinction has to be explicit.
func TestIsPrompt(t *testing.T) {
	if !ParseCommand("just do it").IsPrompt() {
		t.Error("plain text should be a prompt")
	}
	if ParseCommand("/stop").IsPrompt() {
		t.Error("a command should not be a prompt")
	}
}

func TestParseCallback(t *testing.T) {
	cases := []struct {
		in         string
		wantAction string
		wantID     string
		wantOK     bool
	}{
		{"ok:req-1", CallbackApprove, "req-1", true},
		{"no:req-2", CallbackDeny, "req-2", true},
		// Anything unrecognised is refused rather than guessed at: the only
		// buttons in play answer a permission ask, and guessing means answering
		// one wrongly.
		{"delete:everything", "", "", false},
		{"ok:", "", "", false},
		{"ok", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		action, id, ok := ParseCallback(c.in)
		if action != c.wantAction || id != c.wantID || ok != c.wantOK {
			t.Errorf("ParseCallback(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.in, action, id, ok, c.wantAction, c.wantID, c.wantOK)
		}
	}
}

// Callback data is capped at 64 bytes by Telegram, and a session id already
// spends most of that, so the prefixes have to stay short.
func TestCallbackDataFitsTelegramsLimit(t *testing.T) {
	const uuidLen = 36
	for _, prefix := range []string{CallbackApprove, CallbackDeny} {
		if n := len(prefix) + 1 + uuidLen; n > 64 {
			t.Errorf("%q + a uuid is %d bytes, over Telegram's 64", prefix, n)
		}
	}
}
