package server

import (
	"strings"

	"github.com/hegade/kunai/internal/session"
)

// Turning a wake-up into the words a phone shows.
//
// A push carries no session content: not what you asked, not what the model
// said, not what a tool was given to run. What it does carry is the detail the
// raiser already computed about kunai's own state, which used to be accepted and
// then dropped on the floor. That detail is the difference between "a loop
// finished" at 3am and "loop finished: the $5.00 budget ran out", which is the
// one thing you actually wanted to know without unlocking the phone.

// detailMax bounds a detail in the notification body. A push payload is small
// and a phone truncates anyway, so cut it here where an ellipsis can be honest
// about it rather than letting the OS clip mid-word.
const detailMax = 80

// wakeupText renders a notification's title and body for a kind and its detail.
// A kind with nothing useful to add ignores the detail, and any kind falls back
// to its bare wording when the detail is empty, so a caller that has nothing to
// say never produces a dangling "Loop finished:".
func wakeupText(kind, detail string) (title, body string) {
	d := cleanDetail(detail)
	return "Kunai", wakeupBody(kind, d)
}

func wakeupBody(kind, detail string) string {
	switch kind {
	case session.NotifyPermission:
		// The tool's name, never its input: "Bash" tells you a command wants to
		// run, which is worth waking up for, while the command itself is content.
		return withDetail("A session needs your approval", "Needs approval: ", detail)
	case session.NotifyDone:
		// How long it ran and what it cost: measurements kunai took of its own
		// work. Which session finished stays out, because naming it means showing
		// its title, and a title is the prompt you typed.
		return withDetail("A task finished", "Task finished: ", detail)
	case session.NotifyFailed:
		// Worth its own wording. A failed turn used to report as finished, so the
		// one outcome you would act on differently read exactly like success.
		return withDetail("A turn failed", "Turn failed: ", detail)
	case session.NotifyLoop:
		return withDetail("A loop finished", "Loop finished: ", detail)
	case session.NotifyThermal:
		return withDetail("Stopped everything: the host got too hot", "Stopped everything: ", detail)
	default:
		return "A session needs your attention"
	}
}

// withDetail returns prefix+detail when there is a detail, and the plain
// fallback when there is not.
func withDetail(fallback, prefix, detail string) string {
	if detail == "" {
		return fallback
	}
	return prefix + detail
}

// cleanDetail makes a detail fit a notification body: one line, bounded, and cut
// on a rune boundary so a multi-byte character is never split into mojibake.
// Details are written as single-line phrases, but this is the boundary where a
// stray newline would otherwise reach the OS, so it is enforced rather than
// assumed.
func cleanDetail(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > detailMax {
		return strings.TrimRight(string(r[:detailMax]), " ") + "…"
	}
	return s
}

// pushNotifier returns the callback the session manager and the thermal guard
// use to wake a phone. On a peer (HubURL set) it forwards to the hub, which owns
// the subscription; on the hub or a standalone machine it pushes directly.
func (s *Server) pushNotifier() func(kind, detail string) {
	return func(kind, detail string) {
		title, body := wakeupText(kind, detail)
		if s.cfg.HubURL != "" {
			s.forwardWake(title, body)
			return
		}
		if s.push != nil {
			s.push.Notify(title, body)
		}
	}
}
