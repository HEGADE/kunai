package review

import (
	"strings"
	"testing"
)

func fileLines(s string) []string { return strings.Split(strings.TrimSuffix(s, "\n"), "\n") }

// The ordinary case: the code the finding quoted is still there, and the
// suggestion takes its place.
func TestApplyReplacesTheLinesTheFindingQuoted(t *testing.T) {
	src := fileLines("package a\n\nfunc f() {\n\tuse(a)\n\treturn nil\n}\n")
	f := Finding{
		File: "a.go", Line: 4, EndLine: 4,
		Quote:      []string{"\tuse(a)"},
		Suggestion: "\tsafe := resolve(a)\n\tuse(safe)",
	}
	out, applied, err := ApplyTo(src, f)
	if err != nil {
		t.Fatalf("ApplyTo() = %v", err)
	}
	want := "package a\n\nfunc f() {\n\tsafe := resolve(a)\n\tuse(safe)\n\treturn nil\n}"
	if got := strings.Join(out, "\n"); got != want {
		t.Errorf("file =\n%s\nwant\n%s", got, want)
	}
	if applied.Line != 4 || applied.Removed != 1 || applied.Added != 2 {
		t.Errorf("applied = %+v, want line 4, -1 +2", applied)
	}
}

// The load-bearing refusal. The file has moved on since the review read it, so
// writing at the recorded line number would put a fix somewhere it was never
// meant to go. Nothing is written and the reason says so.
func TestApplyRefusesWhenTheCodeHasChanged(t *testing.T) {
	src := fileLines("func f() {\n\tsomethingElseEntirely()\n}\n")
	f := Finding{File: "a.go", Line: 2, Quote: []string{"\tuse(a)"}, Suggestion: "\tfixed()"}
	if _, _, err := ApplyTo(src, f); err == nil {
		t.Fatal("a change was applied to code the review never read")
	} else if !strings.Contains(err.Error(), "changed since the review") {
		t.Errorf("error = %q, want it to say the code has changed", err)
	}
}

// A line has SHIFTED rather than changed: an import added above moves everything
// down, and the quote is the thing that finds it again. This is the whole reason
// the apply matches on text.
func TestApplyFollowsCodeThatMovedDownTheFile(t *testing.T) {
	src := fileLines("import x\nimport y\nimport z\n\nfunc f() {\n\tuse(a)\n}\n")
	f := Finding{File: "a.go", Line: 2, Quote: []string{"\tuse(a)"}, Suggestion: "\tuse(safe)"}
	out, applied, err := ApplyTo(src, f)
	if err != nil {
		t.Fatalf("ApplyTo() = %v", err)
	}
	if applied.Line != 6 {
		t.Errorf("applied at line %d, want the line the code is on now", applied.Line)
	}
	if !strings.Contains(strings.Join(out, "\n"), "\tuse(safe)") {
		t.Error("the change did not land")
	}
}

// Where the quoted text occurs more than once (a bare closing brace matches
// almost everywhere) the run NEAREST where the finding was wins: lines shift by
// a few, they do not teleport.
func TestApplyPrefersTheOccurrenceNearestTheFinding(t *testing.T) {
	src := fileLines("func a() {\n\tx()\n}\n\nfunc b() {\n\tx()\n}\n")
	f := Finding{File: "a.go", Line: 6, Quote: []string{"\tx()"}, Suggestion: "\ty()"}
	out, applied, err := ApplyTo(src, f)
	if err != nil {
		t.Fatalf("ApplyTo() = %v", err)
	}
	if applied.Line != 6 {
		t.Fatalf("applied at line %d, want the occurrence nearest the finding", applied.Line)
	}
	if out[1] != "\tx()" {
		t.Error("the far occurrence was changed instead")
	}
}

// A review from before the quote was recorded cannot place its change, and says
// so rather than guessing from a line number.
func TestApplyRefusesAFindingWithNoQuote(t *testing.T) {
	f := Finding{File: "a.go", Line: 2, Suggestion: "\tfixed()"}
	if _, _, err := ApplyTo(fileLines("a\nb\n"), f); err == nil {
		t.Fatal("a change was placed with nothing to place it by")
	}
}

// Nothing to do is not an error to hide: the button would otherwise report
// success having written the same bytes back.
func TestApplyRefusesAChangeThatChangesNothing(t *testing.T) {
	src := fileLines("func f() {\n\tuse(a)\n}\n")
	f := Finding{File: "a.go", Line: 2, Quote: []string{"\tuse(a)"}, Suggestion: "\tuse(a)"}
	if _, _, err := ApplyTo(src, f); err == nil {
		t.Fatal("a no-op was reported as an applied change")
	}
}

// And a finding with no suggestion has nothing to apply.
func TestApplyRefusesAFindingWithNoSuggestion(t *testing.T) {
	f := Finding{File: "a.go", Line: 1, Quote: []string{"x"}}
	if _, _, err := ApplyTo(fileLines("x\n"), f); err == nil {
		t.Fatal("a finding with no suggestion was applied")
	}
}

// Trailing whitespace is not a change to the code, so a file a formatter has
// touched must still match. The line written back is the suggestion's own.
func TestApplyIgnoresTrailingWhitespaceWhenMatching(t *testing.T) {
	src := fileLines("func f() {\n\tuse(a)   \n}\n")
	f := Finding{File: "a.go", Line: 2, Quote: []string{"\tuse(a)"}, Suggestion: "\tuse(safe)"}
	out, _, err := ApplyTo(src, f)
	if err != nil {
		t.Fatalf("ApplyTo() = %v", err)
	}
	if out[1] != "\tuse(safe)" {
		t.Errorf("line = %q, want the suggestion written as it stands", out[1])
	}
}
