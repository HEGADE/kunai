// Package review holds the parts of a pull-request review that are pure: what a
// finding is, how the agent's answer is read, where a finding may be anchored in
// a diff, and what the posted review looks like.
//
// Kept away from the network and from the session machinery on purpose. Anchoring
// is the part that decides whether a review posts at all (GitHub rejects the WHOLE
// review, not the offending comment, when one line is wrong), so it is worth being
// able to exercise it against a diff fixture rather than against GitHub.
package review

import (
	"sort"
	"strings"
)

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
	// Severity is how much this matters if it is true, and Confidence is how
	// likely it is to be true. See severity.go for why those are two fields and
	// not one number.
	//
	// Severity is what gives a review its shape: without it a reader faces a
	// dozen identical cards in whatever order the model happened to emit them,
	// with no way to tell the data-loss bug from the observation.
	Severity Severity `json:"severity,omitempty"`
	// Confidence drives the verification phase as well as the display: anything
	// below high is handed back to be refuted before it may be posted.
	Confidence Confidence `json:"confidence,omitempty"`
	// Category is what the finding is about, for grouping and filtering. It never
	// affects ranking.
	Category string `json:"category,omitempty"`
	// Evidence is what the finding rests on, in the reviewer's own words: the
	// call site it followed, the invariant it checked. Recorded because the
	// verification phase judges the claim against it, and because a finding whose
	// reasoning is visible is one a person can overrule quickly.
	Evidence string `json:"evidence,omitempty"`
	// Verified is true when an independent pass tried to refute this and failed.
	//
	// Its absence is not a black mark: a finding can skip verification by being
	// demonstrated in the first place, and one can miss it because the pass could
	// not be read. But a reader must be able to tell "this was checked" from
	// "this was asserted", and only the flag can say so.
	Verified bool `json:"verified,omitempty"`
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
	f.Evidence = strings.TrimSpace(f.Evidence)
	// Repaired rather than rejected, for the reason given in severity.go: a real
	// blocker that arrived labelled "critical" must not be lost to vocabulary.
	f.Severity = normaliseSeverity(f.Severity)
	f.Confidence = normaliseConfidence(f.Confidence)
	f.Category = normaliseCategory(f.Category)
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
//
// It also SORTS, by severity, most serious first. The prompt asks for that order
// too, but asking is not a guarantee and the order is what a reader relies on to
// stop reading: whatever is at the top has to be the worst thing found. Since
// every finding now carries a severity, that promise can be kept here rather
// than hoped for. The sort is stable, so within one severity the reviewer's own
// ordering survives.
func (d Draft) Normalise() Draft {
	out := Draft{Summary: strings.TrimSpace(d.Summary)}
	for _, f := range d.Findings {
		f = f.Normalise()
		if f.Title == "" && f.Body == "" {
			continue
		}
		out.Findings = append(out.Findings, f)
	}
	sort.SliceStable(out.Findings, func(i, j int) bool {
		return out.Findings[i].Severity.Rank() < out.Findings[j].Severity.Rank()
	})
	return out
}

// Counts tallies the findings by severity, which is the review's headline: two
// blockers and five minors is a different pull request from seven minors, and a
// reader must be able to tell those apart before reading any of them.
func (d Draft) Counts() (blocker, major, minor int) {
	for _, f := range d.Findings {
		switch f.Severity {
		case SeverityBlocker:
			blocker++
		case SeverityMajor:
			major++
		default:
			minor++
		}
	}
	return blocker, major, minor
}
