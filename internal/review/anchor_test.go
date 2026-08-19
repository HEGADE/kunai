package review

import (
	"strings"
	"testing"
)

func testAnchors() *Anchors {
	return ParseDiff([]FileDiff{{Filename: "run.go", Patch: samplePatch}})
}

// Nothing the agent produced may be silently dropped. A finding that cannot be a
// line comment is demoted to the summary, and the plan says why, so the draft can
// show it before you post.
func TestBuildPlacesEveryFinding(t *testing.T) {
	draft := Draft{
		Summary: "Looks reasonable.",
		Findings: []Finding{
			{File: "run.go", Line: 11, Title: "shiny() is not checked"},          // added line: inline
			{File: "other.go", Line: 4, Title: "the caller still expects old()"}, // untouched file: summary
			{File: "run.go", Line: 99, Title: "outside the hunk"},                // wrong line: summary
			{Title: "no location at all"},                                        // summary
		},
	}

	plan := Build(draft, testAnchors())
	total, inline, summary := plan.Counts()
	if total != 4 || inline != 1 || summary != 3 {
		t.Fatalf("counts = %d total, %d inline, %d summary; want 4/1/3", total, inline, summary)
	}

	// Each demotion explains itself in terms the reader can act on, and the most
	// specific true reason wins: "not in this pull request" beats "line 4 is not
	// commentable", though both are true.
	reasons := map[string]string{}
	for _, pl := range plan.Demoted() {
		reasons[pl.Finding.Title] = pl.Why
	}
	if got := reasons["the caller still expects old()"]; !strings.Contains(got, "other.go") {
		t.Errorf("untouched-file reason = %q, want it to name the file", got)
	}
	if got := reasons["outside the hunk"]; !strings.Contains(got, "99") {
		t.Errorf("bad-line reason = %q, want it to name the line", got)
	}
}

// A suggestion replaces the lines it is anchored to, so it only means anything
// against the new file. On a deleted line it would propose editing code that no
// longer exists.
func TestSuggestionOnADeletedLineIsDemoted(t *testing.T) {
	draft := Draft{Findings: []Finding{{
		File: "run.go", Line: 11, Side: SideLeft,
		Title: "restore this", Suggestion: "\told()",
	}}}
	plan := Build(draft, testAnchors())
	if len(plan.Demoted()) != 1 {
		t.Fatalf("a suggestion on a deleted line should be demoted, got %+v", plan.Placements)
	}
	if why := plan.Demoted()[0].Why; !strings.Contains(why, "deleted") {
		t.Errorf("reason = %q, want it to explain the deleted line", why)
	}
}

// A range must be commentable at BOTH ends. Only checking the start posts a
// comment whose end is outside the diff, which GitHub rejects, taking the whole
// review with it.
func TestBuildChecksBothEndsOfARange(t *testing.T) {
	ok := Draft{Findings: []Finding{{File: "run.go", Line: 11, EndLine: 12, Title: "both added"}}}
	if got := len(Build(ok, testAnchors()).Inline()); got != 1 {
		t.Errorf("a range inside the hunk should be inline, got %d inline", got)
	}

	spill := Draft{Findings: []Finding{{File: "run.go", Line: 12, EndLine: 40, Title: "runs off the end"}}}
	if got := len(Build(spill, testAnchors()).Inline()); got != 0 {
		t.Errorf("a range ending outside the diff must not be posted inline")
	}
}

// A model writing a range backwards is the common slip, and it is unambiguous.
// Losing a real finding to a typo is a worse outcome than agreeing with what was
// obviously meant.
func TestNormaliseRepairsWhatModelsGetWrong(t *testing.T) {
	f := Finding{File: "./run.go", Line: 12, EndLine: 11, Side: "right", Title: "  spaced  "}.Normalise()
	if f.Line != 11 || f.EndLine != 12 {
		t.Errorf("backwards range not corrected: %d-%d", f.Line, f.EndLine)
	}
	if f.File != "run.go" {
		t.Errorf("file = %q, want the ./ prefix stripped", f.File)
	}
	if f.Side != SideRight {
		t.Errorf("side = %q, want it upper-cased to RIGHT", f.Side)
	}
	if f.Title != "spaced" {
		t.Errorf("title = %q, want it trimmed", f.Title)
	}

	// A one-line range written as start == end is just a single line.
	if got := (Finding{Line: 7, EndLine: 7}).Normalise(); got.EndLine != 0 {
		t.Errorf("EndLine = %d for a single line, want 0", got.EndLine)
	}
	// An unset side defaults to the new file, which is what nearly every finding
	// is about.
	if got := (Finding{Title: "x"}).Normalise(); got.Side != SideRight {
		t.Errorf("default side = %q, want RIGHT", got.Side)
	}
}

// A finding with nothing to say is not a finding, and an empty comment on
// somebody's line is worse than no comment.
func TestNormaliseDropsEmptyFindings(t *testing.T) {
	d := Draft{Findings: []Finding{
		{File: "run.go", Line: 11, Title: "real"},
		{File: "run.go", Line: 12},
	}}.Normalise()
	if len(d.Findings) != 1 || d.Findings[0].Title != "real" {
		t.Fatalf("got %+v, want only the finding that says something", d.Findings)
	}
}
