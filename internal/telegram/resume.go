package telegram

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// Bringing a closed session back.
//
// Closing a session does not destroy it: the transcript is on disk and kunai can
// reattach the CLI to it. But a chat had no way to say so, so ending a session
// from the app left the phone stuck on "No session yet", with the conversation
// still sitting there, unreachable. Every place a session goes away now hands
// back the two ways to return to it: a button to tap now, and a command to keep.
//
// The command matters as much as the button. A chat scrolls, and a message from
// last night is easier to act on if you can copy one line than if you have to
// find the right old bubble to tap.

// resumeButtonLabel is short on purpose: it sits under a message that has
// already explained what it does.
const resumeButtonLabel = "Resume"

// resumeCommand is the line to keep, so a session can be brought back from any
// point in the chat later.
func resumeCommand(id string) string { return "/" + CmdResume + " " + id }

// resumeOffer is what to say when a session has gone away. lead states what
// happened in the caller's words; the offer is the same either way.
func resumeOffer(lead, id string) Rendered {
	text := resumeCommand(id)
	if lead != "" {
		text = lead + "\n\n" + text
	}
	return Rendered{Text: text, Keyboard: resumeKeyboard(id)}
}

// resumeKeyboard is the one-tap version of the command. Nil when the id would
// not fit in Telegram's callback budget, in which case the command line is still
// there and still works.
func resumeKeyboard(id string) *InlineKeyboard {
	data := callbackData(CallbackResume, id)
	if id == "" || len(data) > maxCallbackBytes {
		return nil
	}
	return &InlineKeyboard{Rows: [][]InlineButton{{
		{Text: resumeButtonLabel, Data: data},
	}}}
}

// resumeList is the answer to a bare /resume: the sessions worth offering, each
// with its own button. Buttons rather than a list of ids because the point is
// not to make someone retype a UUID on a phone.
func resumeList(past []Past, now time.Time) Rendered {
	if len(past) == 0 {
		return Rendered{Text: "Nothing to bring back yet. Start one with /new <path>."}
	}
	var sb strings.Builder
	sb.WriteString("Sessions you can bring back:\n")
	rows := make([][]InlineButton, 0, len(past))
	for _, p := range past {
		fmt.Fprintf(&sb, "\n%s\n  %s · %s\n  %s",
			pastTitle(p), filepath.Base(p.Cwd), ago(p.When, now), resumeCommand(p.ID))
		if kb := resumeKeyboard(p.ID); kb != nil {
			rows = append(rows, []InlineButton{{
				Text: pastTitle(p) + " · " + ago(p.When, now),
				Data: callbackData(CallbackResume, p.ID),
			}})
		}
	}
	var kb *InlineKeyboard
	if len(rows) > 0 {
		kb = &InlineKeyboard{Rows: rows}
	}
	return Rendered{Text: sb.String(), Keyboard: kb}
}

// pastTitle is what a session is called in a list. A session that never got a
// title falls back to its directory, which is how it is recognised anyway.
func pastTitle(p Past) string {
	if t := strings.TrimSpace(p.Title); t != "" {
		return oneLine(t)
	}
	if p.Cwd != "" {
		return filepath.Base(p.Cwd)
	}
	return p.ID
}

// ago is a coarse age. Precision is not the point: "yesterday" is what picks the
// right session out of a list, not a timestamp.
func ago(t, now time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
