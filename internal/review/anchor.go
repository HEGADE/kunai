package review

// Deciding where each finding lands.
//
// Two destinations, and which one a finding gets is not a preference: GitHub only
// accepts an inline comment on a line the diff touches. The most valuable finding
// is often about a file the pull request never changed ("this breaks the caller
// over here"), and that one can never be inline. So rather than dropping it or
// risking the submission, it is demoted to the summary with a permalink, which is
// still precise and still clickable.
//
// Nothing is ever silently discarded. Every finding the agent produced appears
// somewhere in the posted review, and Plan says exactly where each one went so the
// UI can show it before you post.

import "fmt"

// Placement is where one finding will appear.
type Placement struct {
	Finding Finding
	// Inline is true when it will be a comment on the line itself.
	Inline bool
	// Why explains a demotion, in words meant for the person reading the draft
	// rather than for a log.
	Why string
}

// Plan is the whole posted review, decided.
type Plan struct {
	Summary    string
	Placements []Placement
}

// Inline returns the findings that will be posted as line comments.
func (p Plan) Inline() []Finding {
	var out []Finding
	for _, pl := range p.Placements {
		if pl.Inline {
			out = append(out, pl.Finding)
		}
	}
	return out
}

// Demoted returns the findings that will appear in the summary body instead.
func (p Plan) Demoted() []Placement {
	var out []Placement
	for _, pl := range p.Placements {
		if !pl.Inline {
			out = append(out, pl)
		}
	}
	return out
}

// Counts is what the draft card's header promises: seven findings, five inline,
// two in the summary.
func (p Plan) Counts() (total, inline, summary int) {
	for _, pl := range p.Placements {
		total++
		if pl.Inline {
			inline++
		} else {
			summary++
		}
	}
	return total, inline, summary
}

// Build decides each finding's placement against the diff.
//
// The checks are ordered from the most explanatory failure to the least, because
// the reason shown to the reader should be the most specific true thing: "this
// file is not in the pull request" is more useful than "line 88 is not
// commentable", even though both are true of the same finding.
func Build(draft Draft, anchors *Anchors) Plan {
	draft = draft.Normalise()
	plan := Plan{Summary: draft.Summary}

	for _, f := range draft.Findings {
		if why := demotionReason(f, anchors); why != "" {
			plan.Placements = append(plan.Placements, Placement{Finding: f, Why: why})
			continue
		}
		plan.Placements = append(plan.Placements, Placement{Finding: f, Inline: true})
	}
	return plan
}

// demotionReason returns "" when the finding can be an inline comment, and
// otherwise says why it cannot in one readable line.
func demotionReason(f Finding, anchors *Anchors) string {
	if f.File == "" || f.Line <= 0 {
		return "no file and line, so it belongs in the summary"
	}
	// Checked before the anchor questions because it is the more specific truth
	// and leads somewhere different: this line may well still be commentable, and
	// commenting on it would put the finding against code it was never about.
	if f.Stale {
		return "the code this is about has changed since the review read it"
	}
	if anchors == nil || !anchors.Touches(f.File) {
		return "this pull request does not change " + f.File
	}
	if !anchors.Commentable(f.File, f.Side, f.StartLine()) {
		return fmt.Sprintf("line %d is not part of the diff", f.StartLine())
	}
	if f.EndLine != 0 && !anchors.Commentable(f.File, f.Side, f.LastLine()) {
		return fmt.Sprintf("line %d is not part of the diff", f.LastLine())
	}
	// A suggestion replaces the lines it is anchored to, so it only makes sense
	// against the new file. Anchored to the old side it would propose editing
	// lines that no longer exist, which GitHub rejects and which would be
	// meaningless if it did not.
	if f.Suggestion != "" && f.Side != SideRight {
		return "a suggested change cannot be attached to a deleted line"
	}
	return ""
}
