package review

import "testing"

// A finding travels with the code it is about, or judging it means going and
// finding that code yourself, which is how a review becomes a chore.
func TestHunkForCarriesTheLinesAroundAFinding(t *testing.T) {
	files := []FileDiff{{Filename: "run.go", Patch: samplePatch}}

	got := HunkFor(files, Finding{File: "run.go", Line: 11, Side: SideRight})
	if len(got) == 0 {
		t.Fatal("no hunk returned for a line that is in the diff")
	}

	var focused []string
	for _, l := range got {
		if l.Focus {
			focused = append(focused, l.Text)
		}
	}
	if len(focused) != 1 || focused[0] != "\tshiny()" {
		t.Errorf("focused %v, want just the added line the finding is about", focused)
	}
	// Context comes along, so the card shows the shape of the code.
	if len(got) < 3 {
		t.Errorf("got %d lines, want the finding plus surrounding context", len(got))
	}
}

// A range marks every line it covers, not only its first.
func TestHunkForMarksAWholeRange(t *testing.T) {
	got := HunkFor([]FileDiff{{Filename: "run.go", Patch: samplePatch}},
		Finding{File: "run.go", Line: 11, EndLine: 12, Side: SideRight})

	n := 0
	for _, l := range got {
		if l.Focus {
			n++
		}
	}
	if n != 2 {
		t.Errorf("%d focused lines, want both lines of the range", n)
	}
}

// A finding about a file the pull request never touched has no hunk by
// definition. That is the case demoted to the summary, and it must return
// nothing rather than guessing.
func TestHunkForReturnsNothingWhenNotInTheDiff(t *testing.T) {
	files := []FileDiff{{Filename: "run.go", Patch: samplePatch}}
	if got := HunkFor(files, Finding{File: "other.go", Line: 3, Side: SideRight}); got != nil {
		t.Errorf("got %d lines for an untouched file", len(got))
	}
	if got := HunkFor(files, Finding{File: "run.go", Line: 900, Side: SideRight}); got != nil {
		t.Errorf("got %d lines for a line outside the diff", len(got))
	}
}

// The deleted side is reachable, which is the only way to show a finding about a
// line the pull request removed.
func TestHunkForReadsTheDeletedSide(t *testing.T) {
	got := HunkFor([]FileDiff{{Filename: "run.go", Patch: samplePatch}},
		Finding{File: "run.go", Line: 11, Side: SideLeft})
	var focused string
	for _, l := range got {
		if l.Focus {
			focused = l.Text
		}
	}
	if focused != "\told()" {
		t.Errorf("focused %q, want the deleted line", focused)
	}
}
