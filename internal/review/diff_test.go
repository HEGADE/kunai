package review

import "testing"

// A patch with all three line kinds, so the walking rules are exercised rather
// than assumed. Hunk header says the new file starts at line 10, the old at 10.
const samplePatch = `@@ -10,6 +10,7 @@ func run() {
 	setup()
-	old()
+	shiny()
+	extra()
 	teardown()
 }`

// The rules that decide whether a review posts at all:
//
//	context ' ' exists in both files, so it is commentable on either side
//	added   '+' exists only in the new file, so RIGHT only
//	removed '-' exists only in the old file, so LEFT only
func TestParseDiffMapsEachLineKindToItsSide(t *testing.T) {
	a := ParseDiff([]FileDiff{{Filename: "run.go", Patch: samplePatch}})

	// Walking the hunk: old 10 / new 10 is the context line "setup()".
	if !a.Commentable("run.go", SideRight, 10) || !a.Commentable("run.go", SideLeft, 10) {
		t.Error("a context line should be commentable on both sides")
	}
	// old 11 is "old()", removed: LEFT only.
	if !a.Commentable("run.go", SideLeft, 11) {
		t.Error("a removed line should be commentable on the left")
	}
	// The old file ends at 13 in this hunk while the new one reaches 14, so 14 is
	// a position that exists on the right and cannot exist on the left.
	if a.Commentable("run.go", SideLeft, 14) {
		t.Error("a line past the end of the old file was reported as commentable")
	}
	if !a.Commentable("run.go", SideRight, 14) {
		t.Error("the last context line should be commentable on the right")
	}
	// new 11 and 12 are "shiny()" and "extra()", added: RIGHT only.
	for _, line := range []int{11, 12} {
		if !a.Commentable("run.go", SideRight, line) {
			t.Errorf("added line %d should be commentable on the right", line)
		}
	}
	// The old file only ever had 13 lines in this hunk; line 99 is in neither.
	if a.Commentable("run.go", SideRight, 99) {
		t.Error("a line outside the hunk was reported as commentable")
	}
	// A file the pull request never touched has no positions at all.
	if a.Commentable("other.go", SideRight, 1) {
		t.Error("an untouched file was reported as commentable")
	}
}

// A binary file (or one too big for GitHub to diff) is genuinely part of the pull
// request but has no commentable line. Both facts have to be true at once, or a
// finding about it is either lost or posted into a 422.
func TestParseDiffHandlesAPatchlessFile(t *testing.T) {
	a := ParseDiff([]FileDiff{{Filename: "logo.png", Status: "modified"}})
	if !a.Touches("logo.png") {
		t.Error("a binary file should still count as changed by the pull request")
	}
	if a.Commentable("logo.png", SideRight, 1) {
		t.Error("a file with no patch has no commentable lines")
	}
}

// Several hunks in one file, and several files, keep their own line counters.
// Getting this wrong shifts every finding after the first hunk.
func TestParseDiffTracksEachHunkSeparately(t *testing.T) {
	patch := `@@ -1,2 +1,3 @@
 first
+second
@@ -50,2 +51,3 @@
 fiftieth
+fifty-first`
	a := ParseDiff([]FileDiff{{Filename: "a.txt", Patch: patch}})

	if !a.Commentable("a.txt", SideRight, 2) {
		t.Error("the addition in the first hunk should be at new line 2")
	}
	if !a.Commentable("a.txt", SideRight, 52) {
		t.Error("the addition in the second hunk should be at new line 52")
	}
	// The gap between hunks is not in the diff and must not be commentable.
	for _, line := range []int{20, 40, 49} {
		if a.Commentable("a.txt", SideRight, line) {
			t.Errorf("line %d is between hunks and must not be commentable", line)
		}
	}
}

// A header we cannot read contributes nothing rather than contributing wrong
// positions, because a wrong position is a rejected review.
func TestParseDiffIgnoresAMalformedHunkHeader(t *testing.T) {
	a := ParseDiff([]FileDiff{{Filename: "a.txt", Patch: "@@ nonsense @@\n+added\n"}})
	if a.Commentable("a.txt", SideRight, 1) {
		t.Error("a malformed hunk header produced a position")
	}
	if !a.Touches("a.txt") {
		t.Error("the file should still be known to the pull request")
	}
}
