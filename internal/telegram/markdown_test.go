package telegram

import (
	"strings"
	"testing"
)

// An agent writes relative links constantly, and Telegram refuses a link it
// cannot open by refusing the whole message. The label is what the reader wanted
// anyway, so it is kept.
func TestSafeLinksKeepsTheLabelAndDropsWhatTelegramRefuses(t *testing.T) {
	cases := []struct{ in, want string }{
		{"see [config.ts](./src/config.ts) for it", "see config.ts for it"},
		{"[readme](README.md)", "readme"},
		{"[abs](/etc/hosts)", "abs"},
		{"[nope](file:///tmp/x)", "nope"},
		{"[empty]()", "empty"},

		// Anything Telegram can open is left exactly as written.
		{"[docs](https://example.com/a_b)", "[docs](https://example.com/a_b)"},
		{"[docs](HTTP://Example.com)", "[docs](HTTP://Example.com)"},
		{"[mail](mailto:a@b.c)", "[mail](mailto:a@b.c)"},
		{"[anchor](#section)", "[anchor](#section)"},
		{"[titled](https://x.dev \"why\")", "[titled](https://x.dev \"why\")"},

		// Not links at all, and must survive untouched.
		{"an array literal [1](x", "an array literal [1](x"},
		{"[bracketed] text", "[bracketed] text"},
		{"a [multi\nline](x) thing", "a [multi\nline](x) thing"},
	}
	for _, c := range cases {
		if got := safeLinks(c.in); got != c.want {
			t.Errorf("safeLinks(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

// Code is the one thing that must survive verbatim: it is shown precisely
// because the reader wants to see exactly what is there.
func TestSafeLinksNeverRewritesCode(t *testing.T) {
	fenced := "before [a](./x)\n\n```md\n[a](./x)\n[b](./y)\n```\n\nafter [c](./z)"
	got := safeLinks(fenced)
	if !strings.Contains(got, "```md\n[a](./x)\n[b](./y)\n```") {
		t.Errorf("the fenced block was rewritten:\n%s", got)
	}
	if strings.Contains(got, "before [a](./x)") || strings.Contains(got, "after [c](./z)") {
		t.Errorf("prose outside the fence was not rewritten:\n%s", got)
	}

	span := "use `[a](./x)` not [b](./y)"
	if got := safeLinks(span); got != "use `[a](./x)` not b" {
		t.Errorf("inline code span: got %q", got)
	}

	// A stray backtick must not swallow the rest of the reply into "code".
	stray := "a ` tick\nthen [b](./y)"
	if got := safeLinks(stray); !strings.HasSuffix(got, "then b") {
		t.Errorf("an unclosed span leaked past its line: %q", got)
	}
}

// --- splitting ----------------------------------------------------------------

func TestSplitRichLeavesAShortReplyAlone(t *testing.T) {
	got := splitRich("just a line", 100)
	if len(got) != 1 || got[0] != "just a line" {
		t.Errorf("got %q", got)
	}
}

// The old behaviour cut the reply and appended a notice, so the conclusion (the
// part at the end) was the part that got lost.
func TestSplitRichKeepsEveryWord(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString("paragraph number ")
		b.WriteString(strings.Repeat("x", 40))
		b.WriteString("\n\n")
	}
	full := strings.TrimSpace(b.String())

	pieces := splitRich(full, 500)
	if len(pieces) < 2 {
		t.Fatalf("expected several pieces, got %d", len(pieces))
	}
	for i, p := range pieces {
		if runeLen(p) > 500 {
			t.Errorf("piece %d is %d runes, over the limit", i, runeLen(p))
		}
	}
	rejoined := strings.Join(pieces, "\n\n")
	if normalise(rejoined) != normalise(full) {
		t.Errorf("text was lost or altered by splitting\nfirst diff around: %q",
			firstDiff(normalise(rejoined), normalise(full)))
	}
}

// A seam inside a code block must not turn the rest of the reply into code.
func TestSplitRichClosesAndReopensACodeFence(t *testing.T) {
	code := strings.Repeat("fmt.Println(\"hello\")\n", 60)
	full := "Here it is:\n\n```go\n" + code + "```\n\nAnd that is all."

	pieces := splitRich(full, 400)
	if len(pieces) < 2 {
		t.Fatalf("expected a split, got %d piece(s)", len(pieces))
	}
	for i, p := range pieces {
		if n := strings.Count(p, "```"); n%2 != 0 {
			t.Errorf("piece %d has an unbalanced fence (%d markers):\n%s", i, n, p)
		}
	}
	// The reopened fence keeps the language, which is what makes the second half
	// highlight the same as the first.
	if !strings.HasPrefix(pieces[1], "```go") {
		t.Errorf("the continuation did not reopen the fence with its language:\n%s", pieces[1])
	}
	last := pieces[len(pieces)-1]
	if !strings.HasSuffix(strings.TrimSpace(last), "And that is all.") {
		t.Errorf("the tail after the code block was lost:\n%s", last)
	}
}

// One enormous unbroken line has no seam to use; it must still be split rather
// than looping or being dropped.
func TestSplitRichHandlesAWallOfText(t *testing.T) {
	full := strings.Repeat("x", 2500)
	pieces := splitRich(full, 500)
	if len(pieces) < 5 {
		t.Fatalf("got %d pieces for 2500 runes at a 500 limit", len(pieces))
	}
	total := 0
	for _, p := range pieces {
		if runeLen(p) > 500 {
			t.Errorf("piece over the limit: %d", runeLen(p))
		}
		total += runeLen(p)
	}
	if total != 2500 {
		t.Errorf("kept %d of 2500 runes", total)
	}
}

// Multi-byte characters must not be sliced in half.
func TestSplitRichCutsOnRuneBoundaries(t *testing.T) {
	full := strings.Repeat("日本語テキスト", 200)
	for _, p := range splitRich(full, 100) {
		if strings.ContainsRune(p, '�') {
			t.Fatalf("a rune was cut in half: %q", p)
		}
	}
}

func normalise(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func firstDiff(a, b string) string {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			lo := max(0, i-40)
			return a[lo:min(len(a), i+40)]
		}
	}
	return ""
}

// --- through the stream ---------------------------------------------------------

// The split has to happen on the way out, not merely be available. A reply over
// the limit used to arrive with its ending replaced by "… (truncated)".
func TestALongReplyArrivesWholeAcrossMessages(t *testing.T) {
	f := &fakeSender{}
	s := newStream(f, 1)
	s.drafting = false

	// Distinct paragraphs so the last one is identifiable in what was sent.
	var b strings.Builder
	for i := 0; i < 4000; i++ {
		b.WriteString("paragraph ")
		b.WriteString(strings.Repeat("y", 8))
		b.WriteString("\n\n")
	}
	b.WriteString("THE CONCLUSION")
	full := b.String()
	if runeLen(full) <= maxRichRunes {
		t.Fatalf("test needs a reply over the limit, got %d runes", runeLen(full))
	}

	s.Append(t.Context(), full)
	// What the mid-stream push sent is a preview: it is superseded by the edit
	// below, so only what the finished reply produces is measured.
	previews := len(f.richSends)

	if err := s.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}

	final := f.richSends[previews:]
	if len(final) < 1 || len(f.edits) < 1 {
		t.Fatalf("expected the finished reply to span the existing message plus new ones, got %d edit(s) and %d send(s)",
			len(f.edits), len(final))
	}
	landed := append([]string{f.edits[len(f.edits)-1]}, final...)
	for i, m := range landed {
		if runeLen(m) > maxRichRunes {
			t.Errorf("message %d is %d runes, over Telegram's ceiling", i, runeLen(m))
		}
	}
	joined := strings.Join(landed, "\n")
	if strings.Contains(joined, "truncated") {
		t.Error("the reply was truncated rather than split")
	}
	if !strings.Contains(joined, "THE CONCLUSION") {
		t.Error("the end of the reply never arrived, which is the part that matters")
	}
}

// An unfinished reply is still growing and later pushes edit it in place, which
// cannot span messages, so only the finished one is split.
func TestAnUnfinishedReplyIsNotSplit(t *testing.T) {
	f := &fakeSender{}
	s := newStream(f, 1)
	s.drafting = false

	s.Append(t.Context(), strings.Repeat("z", maxRichRunes+500))
	if err := s.push(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	if len(f.richSends) != 1 {
		t.Errorf("a mid-stream push sent %d messages; it must stay one editable message", len(f.richSends))
	}
}

// The link guard has to be on the wire, not only in a helper nobody calls.
func TestRelativeLinksAreNeutralisedOnTheWay(t *testing.T) {
	if got := clampRich(safeLinks("see [x](./a/b.ts)")); got != "see x" {
		t.Errorf("got %q", got)
	}
}
