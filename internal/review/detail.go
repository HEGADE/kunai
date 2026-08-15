package review

// What a finding carries beyond the claim itself.
//
// The review surface asks four questions a reader has in a fixed order once
// they believe a finding: what would I change, what checked this, who can reach
// it, and how big is the fix. Answered as prose they are a paragraph nobody
// reads; answered as named fields they are a panel you scan in three seconds.
//
// Two of the four cost the model nothing extra, and that is deliberate. The
// PATCH is computed rather than asked for: the lines a finding is anchored to
// are the "before" and the suggestion it already produces is the "after", so a
// diff can be built from data that was on the record before this file existed.
// Only the fix's TITLE, the impact, and the grounds are new asks, and each is
// one short line.

import (
	"fmt"
	"strings"
)

// Ground is one row of what checked a claim: a label and what was found.
//
// Labelled rather than prose because the labels are the same three questions
// every time (what did you follow, who else calls it, is it tested), and a
// reader comparing two findings can only do so when both answer in the same
// shape.
type Ground struct {
	// Key is the short label: TRACE, CALLERS, TESTS. Uppercased for display.
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Impact is who can reach a finding, what it costs, and what fixing it costs.
//
// The three together are what turns a list of true statements into an order to
// work in: a two-line fix reachable by anyone outranks a rewrite reachable only
// by an admin, and severity alone cannot say that.
type Impact struct {
	// Who can trigger it: "any paired guest", "an admin", "only CI".
	Who string `json:"who"`
	// Radius is what it reaches when it goes wrong.
	Radius string `json:"radius"`
	// Size is roughly what fixing it costs: "3 lines", "a rewrite of X".
	Size string `json:"size"`
}

// PatchLine is one line of a suggested fix, in the diff's own vocabulary.
type PatchLine struct {
	// Sign is "+", "-" or " ".
	Sign string `json:"sign"`
	Text string `json:"text"`
}

// Patch is the fix as a diff, with a line saying what it does.
type Patch struct {
	Title string      `json:"title"`
	Lines []PatchLine `json:"lines"`
}

// PatchFor builds the before/after diff for a finding.
//
// Derived rather than asked for. The finding is anchored to lines, those lines
// are in the hunk it already carries, and the replacement is the suggestion it
// already produced: everything needed for a diff is on the record, and asking
// the model to write one as well would be paying twice for the same fact and
// giving it a second chance to disagree with itself.
//
// nil when there is no suggestion, which is the common case and correct: the
// prompt asks for one only when the fix is small, local and unambiguous.
// maxPatchLines is where a patch stops being a patch.
//
// Not a display cap: a suggestion this large is not a small, local, unambiguous
// fix, which is the only kind the prompt asks for, and rendering it in a 344px
// rail produces a wall of wrapped code nobody reads. Better to show nothing and
// leave the argument in the pane to make the case.
const maxPatchLines = 14

func PatchFor(f Finding, hunk []HunkLine) *Patch {
	if strings.TrimSpace(f.Suggestion) == "" {
		return nil
	}
	before := make([]string, 0, len(hunk))
	for _, l := range hunk {
		if l.Focus {
			before = append(before, l.Text)
		}
	}
	after := strings.Split(strings.TrimRight(f.Suggestion, "\n"), "\n")

	// Only what CHANGED.
	//
	// This is the whole difference between a patch and a restatement. A model
	// asked to replace lines 196 to 203 hands back all eight of them with two
	// words different, so the naive version printed eight removals and eight
	// near-identical additions: thirty wrapped lines in a narrow rail, and the
	// reader left to spot the difference themselves. That is precisely the job a
	// diff exists to do.
	before, after = trimCommon(before, after)
	if len(before) == 0 && len(after) == 0 {
		return nil // the suggestion is the code that is already there
	}
	if len(before)+len(after) > maxPatchLines {
		return nil // see maxPatchLines
	}

	lines := make([]PatchLine, 0, len(before)+len(after))
	for _, t := range before {
		lines = append(lines, PatchLine{Sign: "-", Text: t})
	}
	for _, t := range after {
		lines = append(lines, PatchLine{Sign: "+", Text: t})
	}

	title := strings.TrimSpace(f.FixTitle)
	if title == "" {
		// Named from the anchor rather than "Suggested change", which is what the
		// panel heading already says and so tells a reader nothing at all.
		title = "Replace " + f.File
		if f.Line > 0 {
			title = fmt.Sprintf("Replace %s:%d", f.File, f.Line)
			if f.EndLine > f.Line {
				title = fmt.Sprintf("Replace %s:%d-%d", f.File, f.Line, f.EndLine)
			}
		}
	}
	return &Patch{Title: title, Lines: lines}
}

// trimCommon drops the lines the two sides already agree on, at both ends.
//
// Whitespace-insensitive at the edges, because a suggestion is frequently the
// same code re-indented by the model's own formatting, and a diff that reports
// eight changed lines when only the indentation moved is worse than no diff:
// it sends a reader looking for a difference that is not there.
func trimCommon(before, after []string) ([]string, []string) {
	same := func(a, b string) bool { return strings.TrimSpace(a) == strings.TrimSpace(b) }

	head := 0
	for head < len(before) && head < len(after) && same(before[head], after[head]) {
		head++
	}
	before, after = before[head:], after[head:]

	tail := 0
	for tail < len(before) && tail < len(after) && same(before[len(before)-1-tail], after[len(after)-1-tail]) {
		tail++
	}
	return before[:len(before)-tail], after[:len(after)-tail]
}

// maxGroundValue is how long a labelled row may be.
//
// A row in that panel is a phrase, not a paragraph. The panel is 344px wide with
// a 70px label column, so a 400-word value is a forty-line ribbon of text and the
// three rows it sits among become unfindable. Anything longer than this belongs
// in the argument, in the pane, where there is a measure to read it at.
const maxGroundValue = 240

// normaliseGrounds tidies what the model returned: labels uppercased and short,
// empty rows dropped, over-long values cut back to the shape the panel is, and
// the list capped so one verbose finding cannot push a panel past the screen.
func normaliseGrounds(in []Ground) []Ground {
	const maxGrounds = 4
	out := make([]Ground, 0, len(in))
	for _, g := range in {
		if len(g.Value) > maxGroundValue {
			// Cut at a sentence where there is one, so the row ends somewhere a
			// person would have stopped rather than mid-word.
			cut := strings.LastIndex(g.Value[:maxGroundValue], ". ")
			if cut < maxGroundValue/2 {
				cut = strings.LastIndex(g.Value[:maxGroundValue], " ")
			}
			if cut <= 0 {
				cut = maxGroundValue
			}
			g.Value = strings.TrimRight(g.Value[:cut], " .,;") + "…"
		}
		key := strings.ToUpper(strings.TrimSpace(g.Key))
		val := strings.TrimSpace(g.Value)
		if key == "" || val == "" {
			continue
		}
		if len(key) > 12 {
			key = key[:12]
		}
		out = append(out, Ground{Key: key, Value: val})
		if len(out) == maxGrounds {
			break
		}
	}
	return out
}

// CleanAreas are the places the survey said to look that produced no finding.
//
// This is the other half of a review and it is normally thrown away: "I checked
// the index contract and it is fine" is information, and a reviewer that only
// ever lists problems is one you cannot tell from a reviewer that stopped
// looking. Derived rather than recorded, because it is exactly the survey's
// areas minus the files anything was found in.
func CleanAreas(s Survey, findings []Finding) []string {
	if len(s.Areas) == 0 {
		return nil
	}
	hit := make(map[string]bool, len(findings))
	for _, f := range findings {
		hit[f.File] = true
	}
	var out []string
	for _, a := range s.Areas {
		if len(a.Files) == 0 {
			continue // nothing to check it against, so nothing can be claimed
		}
		clean := true
		for _, file := range a.Files {
			if hit[file] {
				clean = false
				break
			}
		}
		if clean {
			out = append(out, strings.TrimSpace(a.What))
		}
	}
	return out
}
