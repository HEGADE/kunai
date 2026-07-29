// Package review holds the parts of a pull-request review that are pure: what a
// finding is, how the agent's answer is read, where a finding may be anchored in
// a diff, and what the posted review looks like.
//
// Kept away from the network and from the session machinery on purpose. Anchoring
// is the part that decides whether a review posts at all (GitHub rejects the WHOLE
// review, not the offending comment, when one line is wrong), so it is worth being
// able to exercise it against a diff fixture rather than against GitHub.
package review

import "strings"

// Side is which version of the file a line belongs to. GitHub's names are kept
// rather than translated, because they are what goes on the wire and a mismatch
// here is a rejected review.
const (
	// SideRight is the new file: added and context lines. Almost everything.
	SideRight = "RIGHT"
	// SideLeft is the old file, which is the only way to comment on a line the
	// pull request DELETED.
	SideLeft = "LEFT"
)

// Finding is one thing the review has to say.
type Finding struct {
	// File is the path as the diff gives it, relative to the repository root.
	File string `json:"file"`
	// Line is the line the finding is about, numbered in the file named by Side.
	Line int `json:"line"`
	// EndLine extends the finding over a range. Zero means a single line.
	// GitHub wants the range the other way round from how people write it, so
	// this is normalised in Normalise rather than trusted.
	EndLine int `json:"end_line,omitempty"`
	// Side defaults to RIGHT when unset, because a finding about deleted code is
	// rare and everything else is about the new file.
	Side string `json:"side,omitempty"`
	// Title is the claim in one line: what is wrong, not what to do.
	Title string `json:"title"`
	// Body is the explanation, including why it matters.
	Body string `json:"body"`
	// Suggestion, when set, is the literal replacement for the anchored lines.
	// GitHub renders it as a diff with an Apply button, so it is offered only for
	// small, local, unambiguous fixes: a suggestion on a design opinion means
	// somebody clicks Apply and the bot has written code nobody reviewed.
	Suggestion string `json:"suggestion,omitempty"`
}

// Normalise fills in the defaults and repairs the shapes a model gets wrong,
// so the anchoring rules below are the only place that has to reason about
// validity.
func (f Finding) Normalise() Finding {
	f.File = strings.TrimPrefix(strings.TrimSpace(f.File), "./")
	f.Side = strings.ToUpper(strings.TrimSpace(f.Side))
	if f.Side != SideLeft {
		f.Side = SideRight
	}
	f.Title = strings.TrimSpace(f.Title)
	f.Body = strings.TrimSpace(f.Body)
	// A range written backwards is the common slip and is unambiguous, so it is
	// corrected rather than rejected: losing a real finding to a typo would be a
	// worse outcome than quietly agreeing with what was obviously meant.
	if f.EndLine != 0 && f.EndLine < f.Line {
		f.Line, f.EndLine = f.EndLine, f.Line
	}
	if f.EndLine == f.Line {
		f.EndLine = 0
	}
	if f.Suggestion != "" {
		// A trailing newline inside a suggestion block adds a blank line to the
		// file when applied, which is a real (small) defect in somebody's tree.
		f.Suggestion = strings.TrimRight(f.Suggestion, "\n")
	}
	return f
}

// StartLine and LastLine give the range a finding covers, whether or not it set
// an end.
func (f Finding) StartLine() int { return f.Line }
func (f Finding) LastLine() int {
	if f.EndLine == 0 {
		return f.Line
	}
	return f.EndLine
}

// Draft is what the agent produced: an overall summary plus its findings.
type Draft struct {
	// Summary is the review's opening: what the change does and whether it looks
	// right, in a few lines.
	Summary string `json:"summary"`
	// Findings are ordered as the agent ranked them, most serious first.
	Findings []Finding `json:"findings"`
}

// Normalise cleans every finding and drops the ones carrying nothing to say. A
// finding with no title is not a finding, and posting an empty comment on a
// colleague's line is worse than posting nothing.
func (d Draft) Normalise() Draft {
	out := Draft{Summary: strings.TrimSpace(d.Summary)}
	for _, f := range d.Findings {
		f = f.Normalise()
		if f.Title == "" && f.Body == "" {
			continue
		}
		out.Findings = append(out.Findings, f)
	}
	return out
}
