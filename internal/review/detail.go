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

import "strings"

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
func PatchFor(f Finding, hunk []HunkLine) *Patch {
	if strings.TrimSpace(f.Suggestion) == "" {
		return nil
	}
	var lines []PatchLine
	// The anchored lines come out as removals, in the order they appear.
	for _, l := range hunk {
		if !l.Focus {
			continue
		}
		lines = append(lines, PatchLine{Sign: "-", Text: l.Text})
	}
	for _, t := range strings.Split(strings.TrimRight(f.Suggestion, "\n"), "\n") {
		lines = append(lines, PatchLine{Sign: "+", Text: t})
	}
	if len(lines) == 0 {
		return nil
	}
	title := strings.TrimSpace(f.FixTitle)
	if title == "" {
		// A patch with no name still beats no patch, and the anchor is the most
		// useful thing to fall back to.
		title = "Suggested change"
	}
	return &Patch{Title: title, Lines: lines}
}

// normaliseGrounds tidies what the model returned: labels uppercased and short,
// empty rows dropped, and the list capped so one verbose finding cannot push a
// panel past the height of the screen.
func normaliseGrounds(in []Ground) []Ground {
	const maxGrounds = 4
	out := make([]Ground, 0, len(in))
	for _, g := range in {
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
