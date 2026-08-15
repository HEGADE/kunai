package review

import (
	"strings"
	"testing"
)

func hunkOf(texts ...string) []HunkLine {
	out := make([]HunkLine, 0, len(texts))
	for i, t := range texts {
		out = append(out, HunkLine{Kind: "+", New: 100 + i, Text: t, Focus: true})
	}
	return out
}

// The bug this exists to prevent, seen in a real review: a model asked to
// replace eight lines hands back all eight with two words different, and the
// naive patch printed eight removals and eight near-identical additions. Thirty
// wrapped lines in a narrow rail, with the reader left to spot the difference
// that a diff exists to point at.
func TestAPatchShowsOnlyWhatChanged(t *testing.T) {
	f := Finding{
		File: "a.go", Line: 100, EndLine: 103, FixTitle: "Resolve the id first",
		Suggestion: "for _, a := range atts {\n\tsafe := resolve(a)\n\treturn nil\n}",
	}
	p := PatchFor(f, hunkOf("for _, a := range atts {", "\tuse(a)", "\treturn nil", "}"))
	if p == nil {
		t.Fatal("PatchFor() = nil, want a patch")
	}
	if len(p.Lines) != 2 {
		t.Fatalf("patch has %d lines, want the one changed line each way:\n%+v", len(p.Lines), p.Lines)
	}
	// Unindented, because once the surrounding lines are trimmed away the tab
	// they all shared is a margin in front of a two-line patch. See
	// stripCommonIndent.
	if p.Lines[0].Sign != "-" || p.Lines[0].Text != "use(a)" {
		t.Errorf("removal = %+v, want the line that actually went", p.Lines[0])
	}
	if p.Lines[1].Sign != "+" || p.Lines[1].Text != "safe := resolve(a)" {
		t.Errorf("addition = %+v, want the line that actually arrived", p.Lines[1])
	}
}

// A suggestion that is the code already there is not a change, and drawing it as
// one sends somebody looking for a difference that does not exist.
func TestAnUnchangedSuggestionIsNotAPatch(t *testing.T) {
	f := Finding{File: "a.go", Suggestion: "x := 1\ny := 2"}
	if p := PatchFor(f, hunkOf("x := 1", "y := 2")); p != nil {
		t.Fatalf("PatchFor() = %+v, want nil for a suggestion that changes nothing", p)
	}
}

// Re-indentation is not a change either. A model reformats as it quotes, and a
// diff reporting eight changed lines because the tabs moved is worse than none.
func TestReindentationAloneIsNotAChange(t *testing.T) {
	f := Finding{File: "a.go", Suggestion: "  x := 1\n  y := 2"}
	if p := PatchFor(f, hunkOf("x := 1", "y := 2")); p != nil {
		t.Fatalf("PatchFor() = %+v, want nil when only the indentation moved", p)
	}
}

// Past a point a suggestion is not a small local fix, and rendering it in a
// 344px rail is a wall of wrapped code nobody reads.
func TestAnEnormousSuggestionIsNotOffered(t *testing.T) {
	before := make([]string, 0, 12)
	var after string
	for i := 0; i < 12; i++ {
		before = append(before, "old line "+string(rune('a'+i)))
		after += "new line " + string(rune('a'+i)) + "\n"
	}
	f := Finding{File: "a.go", Suggestion: after}
	if p := PatchFor(f, hunkOf(before...)); p != nil {
		t.Fatalf("PatchFor() offered %d lines, want nothing past the cap", len(p.Lines))
	}
}

// A row in the grounds panel is a phrase, not a paragraph: it is 344px wide with
// a 70px label column, so an unbounded value is a forty-line ribbon and the rows
// beside it become unfindable.
func TestALongGroundIsCutToTheShapeOfItsPanel(t *testing.T) {
	long := "Confirmed end to end. " + strings.Repeat("guestAttachments passes atts verbatim to BuildContent. ", 12)
	got := normaliseGrounds([]Ground{{Key: "trace", Value: long}})
	if len(got) != 1 {
		t.Fatalf("grounds = %+v, want the row kept", got)
	}
	if len(got[0].Value) > maxGroundValue+4 {
		t.Errorf("value is %d chars, want it cut to about %d", len(got[0].Value), maxGroundValue)
	}
	if got[0].Key != "TRACE" {
		t.Errorf("key = %q, want it uppercased", got[0].Key)
	}
}

// The margin the whole patch shares is spent on nothing.
//
// Real code is three or four levels deep before it says anything, and in a 344px
// rail that indentation takes a quarter of every line and pushes each one into
// two or three wrapped ones. The relative indentation inside the patch is the
// part that carries meaning, and it has to survive exactly.
func TestThePatchDropsTheIndentationItAllShares(t *testing.T) {
	f := Finding{File: "a.go", Suggestion: "\t\t\tif !ok {\n\t\t\t\treturn errGuestFiles\n\t\t\t}"}
	p := PatchFor(f, hunkOf("\t\t\tif bad {", "\t\t\t\treturn nil", "\t\t\t}"))
	if p == nil {
		t.Fatal("PatchFor() = nil, want a patch")
	}
	for _, l := range p.Lines {
		if strings.HasPrefix(l.Text, "\t\t\t") {
			t.Fatalf("line %q kept the shared margin", l.Text)
		}
	}
	var nested string
	for _, l := range p.Lines {
		if l.Sign == "+" && strings.Contains(l.Text, "errGuestFiles") {
			nested = l.Text
		}
	}
	if nested != "\treturn errGuestFiles" {
		t.Errorf("nested line = %q, want one tab of relative indentation kept", nested)
	}
}

// Mixed indentation must not be sliced through the middle of a character run: a
// tab and a space are not the same amount of anything, so the common prefix is
// compared character by character and stops at the first difference.
func TestMixedIndentationIsLeftAlone(t *testing.T) {
	f := Finding{File: "a.go", Suggestion: "\tif ok {\n  deep()\n\t}"}
	p := PatchFor(f, hunkOf("\tif bad {", "  shallow()", "\t}"))
	if p == nil {
		t.Fatal("PatchFor() = nil, want a patch")
	}
	for _, l := range p.Lines {
		if l.Text == "" {
			t.Fatalf("a line was emptied out by the dedent: %+v", p.Lines)
		}
	}
}
