package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hegade/kunai/internal/ghapp"
)

// big returns a patch long enough to push a change past smallDiffLines.
func bigPatch(lines int) ghapp.FileDiff {
	var b strings.Builder
	b.WriteString("@@ -1,1 +1," + "1" + " @@\n")
	for i := 0; i < lines; i++ {
		b.WriteString("+line\n")
	}
	return ghapp.FileDiff{Filename: "big.go", Status: "modified", Patch: b.String(), Additions: lines}
}

// The point of the layout: one file's diff is reachable on its own, at a path
// derivable from the file's own path. Without this the reviewer can only read
// the combined diff from the top, which is what cost 18M cache-read tokens on a
// real review.
func TestEachFileGetsItsOwnDiffAtAMirroredPath(t *testing.T) {
	dir := t.TempDir()
	got, err := writeDiff(dir, 7, []ghapp.FileDiff{
		{Filename: "internal/server/thing.go", Status: "modified", Patch: "@@ -1 +1 @@\n-a\n+b\n", Additions: 1, Deletions: 1},
		{Filename: "web/src/App.svelte", Status: "modified", Patch: "@@ -2 +2 @@\n+x\n", Additions: 1},
	})
	if err != nil {
		t.Fatalf("writeDiff() = %v", err)
	}

	want := filepath.Join(reviewPerFileDir, "internal/server/thing.go.diff")
	if got.Files[0].Diff != want {
		t.Fatalf("diff path = %q, want %q", got.Files[0].Diff, want)
	}
	body, err := os.ReadFile(filepath.Join(dir, want))
	if err != nil {
		t.Fatalf("reading the per-file diff: %v", err)
	}
	// A valid patch on its own, headers included, rather than a fragment.
	for _, s := range []string{"--- a/internal/server/thing.go", "+++ b/internal/server/thing.go", "+b"} {
		if !strings.Contains(string(body), s) {
			t.Errorf("per-file diff is missing %q:\n%s", s, body)
		}
	}
	// And it holds ONLY that file, or reading one file would still drag in the
	// other and the whole layout would buy nothing.
	if strings.Contains(string(body), "App.svelte") {
		t.Error("a per-file diff carries another file's patch")
	}
}

// A small change keeps the combined file too: there, one read of the lot is
// genuinely cheaper than a read per file to assemble the same picture.
func TestASmallChangeAlsoGetsTheWholeDiff(t *testing.T) {
	dir := t.TempDir()
	got, err := writeDiff(dir, 3, []ghapp.FileDiff{
		{Filename: "a.go", Status: "modified", Patch: "@@ -1 +1 @@\n+a\n", Additions: 1},
	})
	if err != nil {
		t.Fatalf("writeDiff() = %v", err)
	}
	if got.Whole == "" {
		t.Fatal("a small change got no combined diff")
	}
	if _, err := os.Stat(filepath.Join(dir, got.Whole)); err != nil {
		t.Fatalf("the combined diff was named but not written: %v", err)
	}
}

// A large one does not, and that is the whole point: the combined file is what
// forces the material the review will never look at into its context.
func TestALargeChangeGetsNoCombinedDiff(t *testing.T) {
	dir := t.TempDir()
	got, err := writeDiff(dir, 4, []ghapp.FileDiff{bigPatch(smallDiffLines + 1)})
	if err != nil {
		t.Fatalf("writeDiff() = %v", err)
	}
	if got.Whole != "" {
		t.Errorf("a %d-line change still wrote a combined diff at %q", smallDiffLines+1, got.Whole)
	}
	if got.Files[0].Diff == "" {
		t.Error("a large change wrote no per-file diff, so there is no way in at all")
	}
}

// A binary file has no patch to read. Reported as having no diff rather than
// silently pointing at a file that says nothing, so the prompt can say so and
// the reviewer does not go looking.
func TestABinaryFileHasNoDiffToRead(t *testing.T) {
	dir := t.TempDir()
	got, err := writeDiff(dir, 5, []ghapp.FileDiff{
		{Filename: "web/logo.png", Status: "added"},
	})
	if err != nil {
		t.Fatalf("writeDiff() = %v", err)
	}
	if got.Files[0].Diff != "" {
		t.Errorf("a binary file was given a diff path %q", got.Files[0].Diff)
	}
}

// The path comes from GitHub and is joined onto a directory kunai creates, so
// the cost of being wrong is a file written wherever the name says.
func TestADiffPathCannotClimbOutOfTheDiffDirectory(t *testing.T) {
	dir := t.TempDir()
	got, err := writeDiff(dir, 6, []ghapp.FileDiff{
		{Filename: "../../../etc/passwd", Status: "modified", Patch: "@@ -1 +1 @@\n+x\n", Additions: 1},
	})
	if err != nil {
		t.Fatalf("writeDiff() = %v", err)
	}
	rel := got.Files[0].Diff
	if strings.Contains(rel, "..") {
		t.Fatalf("diff path %q still climbs", rel)
	}
	abs, err := filepath.Abs(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(filepath.Join(dir, reviewPerFileDir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		t.Errorf("%q landed outside %q", abs, root)
	}
}
