package review

import "testing"

// A patch where `func run()` sits at line 10 in the new file.
const patchAt10 = `@@ -8,4 +8,6 @@
 package thing

+func run() {
+	do()
+}
 // end
`

// The same code after somebody pushed a commit that inserts two lines above it,
// so `func run()` is now at line 12 and nothing about it has changed.
const patchAt12 = `@@ -8,4 +8,8 @@
 package thing

+// added by a later commit
+// and another
+func run() {
+	do()
+}
 // end
`

func fileAt(patch string) []FileDiff {
	return []FileDiff{{Filename: "a.go", Status: "modified", Patch: patch}}
}

func TestQuoteCapturesTheAnchoredLines(t *testing.T) {
	got := Quote(fileAt(patchAt10), Finding{File: "a.go", Side: SideRight, Line: 10, EndLine: 12})
	want := []string{"func run() {", "\tdo()", "}"}
	if len(got) != len(want) {
		t.Fatalf("Quote() = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Quote() = %q, want %q", got, want)
		}
	}
}

// The whole point. A push that shifts the code down two lines must not cost the
// review: the comment follows the code it quotes.
func TestAFindingFollowsTheCodeItQuotes(t *testing.T) {
	f := Finding{File: "a.go", Side: SideRight, Line: 10, EndLine: 12, Title: "x", Severity: SeverityMajor}
	f.Quote = Quote(fileAt(patchAt10), f)

	out, rep := Reanchor(Draft{Findings: []Finding{f}}, fileAt(patchAt12))
	if rep.Moved != 1 || rep.Stale != 0 {
		t.Fatalf("report = %+v, want 1 moved and none stale", rep)
	}
	got := out.Findings[0]
	if got.Line != 12 || got.LastLine() != 14 {
		t.Fatalf("re-anchored to %d-%d, want 12-14", got.Line, got.LastLine())
	}
	if got.Stale {
		t.Error("a finding that was found again was marked stale")
	}
	// And it is now postable inline, which is the outcome that matters.
	if why := demotionReason(got, ParseDiff(fileAt(patchAt12))); why != "" {
		t.Errorf("a re-anchored finding was demoted anyway: %s", why)
	}
}

// Nothing moved: the report says so, so a review whose branch merely gained an
// unrelated commit adds no caveat to what it posts.
func TestUnmovedCodeIsReportedAsUnchanged(t *testing.T) {
	f := Finding{File: "a.go", Side: SideRight, Line: 10, EndLine: 12}
	f.Quote = Quote(fileAt(patchAt10), f)

	_, rep := Reanchor(Draft{Findings: []Finding{f}}, fileAt(patchAt10))
	if rep.Unchanged != 1 || rep.Any() {
		t.Fatalf("report = %+v, want 1 unchanged and nothing to report", rep)
	}
}

// The case the old refusal actually existed to prevent, and the only one worth
// stopping: the code a finding is about is gone, so commenting on its old line
// number would attach the claim to something it was never about.
func TestCodeThatChangedIsHeldBackRatherThanMisplaced(t *testing.T) {
	f := Finding{File: "a.go", Side: SideRight, Line: 10, EndLine: 12, Title: "x"}
	f.Quote = Quote(fileAt(patchAt10), f)

	fixed := `@@ -8,4 +8,6 @@
 package thing

+func run() error {
+	return do()
+}
 // end
`
	out, rep := Reanchor(Draft{Findings: []Finding{f}}, fileAt(fixed))
	if rep.Stale != 1 {
		t.Fatalf("report = %+v, want the finding marked stale", rep)
	}
	why := demotionReason(out.Findings[0], ParseDiff(fileAt(fixed)))
	if why == "" {
		t.Fatal("a stale finding was still posted inline")
	}
	if want := "changed since"; !contains(why, want) {
		t.Errorf("demotion reason %q does not say the code changed", why)
	}
}

// A finding about a file the pull request never touches has no quote, because it
// was never anchored in a patch. Its line number refers to the FILE, so a push
// elsewhere has not moved it and re-anchoring must leave it completely alone.
func TestAFindingOutsideTheDiffIsLeftAlone(t *testing.T) {
	f := Finding{File: "elsewhere.go", Side: SideRight, Line: 400, Title: "the caller breaks"}
	out, rep := Reanchor(Draft{Findings: []Finding{f}}, fileAt(patchAt12))
	if rep.Any() || rep.Unchanged != 0 {
		t.Fatalf("report = %+v, want the finding untouched and uncounted", rep)
	}
	if out.Findings[0].Line != 400 || out.Findings[0].Stale {
		t.Errorf("a finding outside the diff was moved or marked stale: %+v", out.Findings[0])
	}
}

// Text that occurs several times must land on the nearest occurrence: lines
// shift by a few, they do not teleport, and `}` matches almost everywhere.
func TestAmbiguousTextTakesTheNearestMatch(t *testing.T) {
	repeated := `@@ -1,1 +1,9 @@
+}
+a
+}
+b
+}
+c
+}
`
	f := Finding{File: "a.go", Side: SideRight, Line: 5, Quote: []string{"}"}}
	out, _ := Reanchor(Draft{Findings: []Finding{f}}, fileAt(repeated))
	if out.Findings[0].Line != 5 {
		t.Errorf("re-anchored to %d, want the occurrence at 5", out.Findings[0].Line)
	}
}

// The note is the honesty half: an author must be able to tell that the reviewer
// read an older commit, since that is the one thing that could make an otherwise
// correct finding wrong.
func TestTheMovedNoteNamesTheCommitThatWasRead(t *testing.T) {
	note := MovedNote("8c802e4d1234", ReanchorReport{Moved: 2})
	if !contains(note, "8c802e4d") {
		t.Errorf("the note does not name the commit that was read: %s", note)
	}
	if !contains(note, "re-attached") {
		t.Errorf("the note does not say the comments were re-attached: %s", note)
	}
	stale := MovedNote("8c802e4d1234", ReanchorReport{Stale: 1})
	if !contains(stale, "1 finding") {
		t.Errorf("the note does not count the stale findings: %s", stale)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
