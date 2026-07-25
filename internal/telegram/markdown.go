package telegram

import (
	"strings"
)

// Preparing a model's Markdown for Telegram.
//
// kunai sends GFM straight to Bot API rich messages rather than converting it,
// which is why there is no renderer here. But "straight" still needs two guards,
// both learned from openclaw's Telegram extension, which converts and therefore
// hit these first:
//
//   - Telegram refuses a link whose target it cannot open, and refuses the whole
//     message with it. An agent writes `[foo.ts](./src/foo.ts)` constantly.
//   - A reply longer than the limit was being cut, losing its tail, and cut
//     without regard for Markdown, so it could end inside a code fence.
//
// Both are handled by rewriting the Markdown before it is sent, which keeps the
// no-converter promise: what goes out is still the model's own Markdown, minus
// the parts Telegram would reject.

// linkSchemes are the targets Telegram will accept in a formatted message.
// Anything else (a relative path, a bare filename, a file:// URL) is not a link
// it can open, and offering it costs the message.
var linkSchemes = []string{"https://", "http://", "tg://", "mailto:", "tel:"}

// safeLinks strips the link syntax from targets Telegram would refuse, keeping
// the label so nothing the model wrote is lost.
//
// It never touches code: a link inside a fence or a backtick span is text the
// user asked to see verbatim, and rewriting it would corrupt the very thing code
// formatting exists to preserve.
func safeLinks(md string) string {
	var b strings.Builder
	b.Grow(len(md))

	s := newMDScanner(md)
	for !s.done() {
		if s.inCode() {
			b.WriteString(s.advance())
			continue
		}
		if s.peek() != '[' {
			b.WriteString(s.advance())
			continue
		}
		label, href, width, ok := parseLink(md[s.i:])
		if !ok {
			b.WriteString(s.advance())
			continue
		}
		if allowedHref(href) {
			b.WriteString(md[s.i : s.i+width])
		} else {
			// The label alone. An agent's `[config.ts](./src/config.ts)` becomes
			// "config.ts", which reads as it was meant to and cannot be refused.
			b.WriteString(label)
		}
		s.skip(width)
	}
	return b.String()
}

func allowedHref(href string) bool {
	h := strings.TrimSpace(href)
	if h == "" {
		return false
	}
	if strings.HasPrefix(h, "#") {
		return true
	}
	lower := strings.ToLower(h)
	for _, scheme := range linkSchemes {
		if strings.HasPrefix(lower, scheme) {
			return true
		}
	}
	return false
}

// parseLink reads a `[label](href)` starting at the beginning of s, returning
// the label, the href, and how many bytes the whole thing occupied.
//
// Deliberately strict: a label containing a newline, or a target with a space
// outside a title, is not treated as a link at all, and is passed through
// untouched. Guessing at malformed syntax risks mangling text that was never
// meant to be a link.
func parseLink(s string) (label, href string, width int, ok bool) {
	if len(s) == 0 || s[0] != '[' {
		return "", "", 0, false
	}
	depth := 0
	end := -1
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\n':
			return "", "", 0, false
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 || end+1 >= len(s) || s[end+1] != '(' {
		return "", "", 0, false
	}
	close := strings.IndexByte(s[end+2:], ')')
	if close < 0 {
		return "", "", 0, false
	}
	target := s[end+2 : end+2+close]
	if strings.ContainsAny(target, "\n") {
		return "", "", 0, false
	}
	// A title ("...") after the target is part of the link; the href is the
	// first field.
	href = target
	if sp := strings.IndexAny(target, " \t"); sp >= 0 {
		href = target[:sp]
	}
	return s[1:end], href, end + 2 + close + 1, true
}

// --- splitting ----------------------------------------------------------------

// splitRich breaks Markdown into pieces no longer than limit runes each.
//
// Splitting rather than truncating is the point: a long reply used to lose its
// tail to a "… (truncated)" notice, and the part cut off is usually the
// conclusion. The seams are chosen so each piece is valid Markdown on its own:
// paragraph boundaries first, then lines, and a code fence left open by a seam is
// closed at the end of one piece and reopened at the start of the next, so a
// split never turns the rest of a reply into code.
func splitRich(md string, limit int) []string {
	if limit <= 0 || runeLen(md) <= limit {
		return []string{md}
	}

	var out []string
	rest := md
	for runeLen(rest) > limit {
		cut := seam(rest, limit)
		piece := strings.TrimRight(rest[:cut], " \n")
		rest = strings.TrimLeft(rest[cut:], "\n")

		// A fence the seam left open is closed here and reopened below, so
		// neither piece leaks code formatting into the other.
		if fence, open := openFence(piece); open {
			piece += "\n```"
			rest = fence + "\n" + rest
		}
		if piece != "" {
			out = append(out, piece)
		}
		if cut == 0 {
			break // no progress is possible; fall through to the tail
		}
	}
	if rest != "" {
		out = append(out, rest)
	}
	if len(out) == 0 {
		return []string{md}
	}
	return out
}

// seam picks the byte offset to cut at: the last paragraph break within the
// limit, else the last line break, else the limit itself on a rune boundary.
func seam(s string, limit int) int {
	max := byteAt(s, limit)
	window := s[:max]
	if i := strings.LastIndex(window, "\n\n"); i > 0 {
		return i
	}
	if i := strings.LastIndexByte(window, '\n'); i > 0 {
		return i
	}
	return max
}

// openFence reports whether s ends inside a fenced code block, and with what
// opening line, so the next piece can reopen it identically (the info string
// carries the language, which is what makes the highlighting right).
func openFence(s string) (fence string, open bool) {
	var current string
	inside := false
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if !strings.HasPrefix(trimmed, "```") {
			continue
		}
		if inside {
			inside = false
			current = ""
			continue
		}
		inside = true
		current = trimmed
	}
	return current, inside
}

// byteAt returns the byte offset of the nth rune, or len(s) when the string is
// shorter. Cutting on a rune boundary is what keeps a multi-byte character from
// being sliced in half.
func byteAt(s string, n int) int {
	count := 0
	for i := range s {
		if count == n {
			return i
		}
		count++
	}
	return len(s)
}

func runeLen(s string) int { return len([]rune(s)) }

// --- scanning -----------------------------------------------------------------

// mdScanner walks Markdown while tracking whether the cursor is inside code,
// which is the one thing safeLinks must never rewrite.
type mdScanner struct {
	s        string
	i        int
	inFence  bool
	inSpan   bool
	atLine   bool // the cursor is at the start of a line
	spanTick int  // how many backticks opened the current inline span
}

func newMDScanner(s string) *mdScanner { return &mdScanner{s: s, atLine: true} }

func (m *mdScanner) done() bool   { return m.i >= len(m.s) }
func (m *mdScanner) peek() byte   { return m.s[m.i] }
func (m *mdScanner) inCode() bool { return m.inFence || m.inSpan }

// advance consumes the next token (a fence marker, a backtick run, or one byte)
// and returns its text, updating the code state as it goes.
func (m *mdScanner) advance() string {
	if m.atLine && strings.HasPrefix(m.s[m.i:], "```") {
		line := m.s[m.i:]
		if n := strings.IndexByte(line, '\n'); n >= 0 {
			line = line[:n]
		}
		m.inFence = !m.inFence
		m.i += len(line)
		m.atLine = false
		return line
	}
	if !m.inFence && m.s[m.i] == '`' {
		ticks := 0
		for m.i+ticks < len(m.s) && m.s[m.i+ticks] == '`' {
			ticks++
		}
		run := m.s[m.i : m.i+ticks]
		switch {
		case !m.inSpan:
			m.inSpan, m.spanTick = true, ticks
		case ticks == m.spanTick:
			m.inSpan, m.spanTick = false, 0
		}
		m.i += ticks
		m.atLine = false
		return run
	}
	c := m.s[m.i]
	m.i++
	m.atLine = c == '\n'
	if c == '\n' {
		// An inline span cannot cross a line; without this a stray backtick
		// would swallow the rest of the reply.
		m.inSpan, m.spanTick = false, 0
	}
	return string(c)
}

func (m *mdScanner) skip(n int) {
	m.i += n
	m.atLine = false
}
