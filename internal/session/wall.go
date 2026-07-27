package session

// Reading the subscription wall out of a turn that failed.
//
// The CLI has two ways of telling us a usage window is spent, and kunai only
// ever listened to one of them. The tidy one is a `rate_limit_event` control
// frame carrying `status: "rejected"` and a reset time, which pump handles in
// its EventRateLimit case. The other, which is what a session actually hits in
// practice, is the turn simply ending in an error whose text says so:
//
//	Claude AI usage limit reached|1753849800
//
// The web client already reads that text (it flips its own banner on an
// error matching /usage limit/), which is why the wall was visible in the chat
// while nothing server-side knew about it. Everything that acts on a wall lives
// on this side: auto-failover asks the turn-end hook whether the window is
// spent, a loop stops on it, and the scheduler pins a reset trigger to it. All
// three sat out the exact case the message above describes.
//
// So the text is parsed here, in one place, and latched onto the session the
// same way a rejected control frame is.

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// wallRe matches the CLI's ways of saying the window is spent. Deliberately
// narrow: an ordinary turn failure must not be mistaken for a wall, because
// failover would then roll a perfectly healthy session onto another account and
// spend its quota instead.
var wallRe = regexp.MustCompile(`(?i)(usage limit reached|rate limit(ed)? exceeded|exceeded your rate limit|quota exceeded)`)

// wallResetRe pulls the reset epoch the CLI appends after a pipe. It is the only
// place the reset time appears on this path, and without it a pinned reset job
// or a "resets in ..." line has nothing to say.
var wallResetRe = regexp.MustCompile(`\|\s*(\d{9,11})\b`)

// parseWall reports whether this text is the CLI saying the subscription window
// is spent, and the reset epoch when it carries one (0 otherwise).
func parseWall(text string) (resetsAt int64, ok bool) {
	if text == "" || !wallRe.MatchString(text) {
		return 0, false
	}
	if m := wallResetRe.FindStringSubmatch(text); m != nil {
		if n, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			return n, true
		}
	}
	return 0, true
}

// recordWall latches a wall seen anywhere other than a rejected control frame,
// and tells everyone a rejected frame would have told: attached clients, and the
// reset handler the scheduler pins its triggers to.
//
// The window is left as whatever a real rate_limit frame last reported: the
// error text names no window, and guessing "5-hour" over an observed seven_day
// would put the wrong reset on the banner and on any job pinned to it. The reset
// time is likewise only overwritten when the text actually carried one.
func (s *Session) recordWall(resetsAt int64) {
	s.mu.Lock()
	first := !s.rateLimited
	s.rateLimited, s.wallFromText = true, true
	if resetsAt > 0 {
		s.limitResetsAt = resetsAt
	}
	window, reset, fn := s.limitWindow, s.limitResetsAt, s.onRateLimit
	s.mu.Unlock()
	if !first {
		return // already walled; nothing new to announce
	}
	if fn != nil && reset > 0 {
		go fn(window, reset)
	}
	s.broadcast(AppEvent{T: EvRateLimit, Window: window, ResetsAt: reset, LimitStatus: "rejected"})
}

// clearWall drops a text-derived latch when a turn actually completes, because a
// turn that ran is proof the window is not spent.
//
// Needed because that latch has nothing to unset it: a rejected control frame is
// followed by an "allowed" one on the next attempt, which the EventRateLimit
// case already acts on, but error text arrives once and never retracts itself. A
// session left latched after its window reset would have auto-failover roll it
// onto another account on its next perfectly healthy turn.
//
// Only the text-derived latch is cleared. A control frame is the CLI's own
// considered answer and outranks an inference drawn from a turn that happened to
// succeed, so it keeps its existing behaviour untouched.
func (s *Session) clearWall() {
	s.mu.Lock()
	if s.wallFromText {
		s.rateLimited, s.wallFromText = false, false
	}
	s.mu.Unlock()
}

// resultText digs out the human-readable message a result frame carries, which
// is where the CLI puts "Claude AI usage limit reached|...". Both `result` and
// `error` are read because neither spelling is a documented contract, and each
// is decoded defensively: `result` is a string on a normal turn but not
// necessarily on a failed one, and a type mismatch must not lose the other
// field.
func resultText(raw json.RawMessage) string {
	var r struct {
		Result  json.RawMessage `json:"result"`
		Error   json.RawMessage `json:"error"`
		Subtype string          `json:"subtype"`
	}
	if json.Unmarshal(raw, &r) != nil {
		return ""
	}
	parts := []string{r.Subtype}
	for _, f := range []json.RawMessage{r.Result, r.Error} {
		var s string
		if len(f) > 0 && json.Unmarshal(f, &s) == nil && s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " ")
}
