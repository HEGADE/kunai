package review

// Putting a suggested change into the file it is about.
//
// The rule this replaces was "the patch is copied, never applied", and the
// reasoning was wrong in a way worth writing down: a review runs with Write,
// Edit and Bash withheld, which is what lets it read somebody else's branch
// unattended, and a button that wrote to the tree looked like a hole in that.
// But the actor is different. The withheld tools stop the MODEL editing on its
// own initiative, in the middle of a job nobody is watching. This is a person
// who has read the finding, read the diff, and pressed a button; refusing them
// on the model's behalf is not a safety property, it is a chore, and it left the
// one screen in kunai whose entire job is deciding what to do about code unable
// to do anything about it.
//
// What DOES have to hold is that the change lands on the code the finding is
// about and nowhere else. Line numbers cannot promise that: the file on disk has
// moved on since the commit that was read, which is the same problem posting has
// and is solved the same way, by the text. A finding records the lines it was
// anchored to (Finding.Quote), so applying means finding that text again and
// refusing when it is not there, rather than writing at a number and hoping.

import (
	"fmt"
	"strings"
)

// Applied is what a change did, so the person who pressed the button can be told
// something more useful than "done".
type Applied struct {
	// Line is where the replacement starts in the file, 1-based.
	Line int
	// Removed and Added are line counts.
	Removed int
	Added   int
}

// ApplyTo splices a finding's suggestion into the file's lines.
//
// Returns the new lines and what happened. It never writes anything: the caller
// owns the file, and keeping this pure is what makes the matching rules testable
// without a repository.
func ApplyTo(lines []string, f Finding) ([]string, Applied, error) {
	if strings.TrimSpace(f.Suggestion) == "" {
		return nil, Applied{}, fmt.Errorf("this finding suggests no change to make")
	}
	if len(f.Quote) == 0 {
		// Older reviews predate the quote, and a line number alone is not enough
		// to write into somebody's file with.
		return nil, Applied{}, fmt.Errorf("this review did not record the lines it was about, so its change cannot be placed; the fix can still be copied")
	}
	start, end, ok := locate(lines, f.Quote, f.StartLine())
	if !ok {
		return nil, Applied{}, fmt.Errorf("the code this is about has changed since the review, so nothing was written")
	}

	// The suggestion is written against the indentation the quoted lines had, so
	// it is spliced in as it stands. Trailing newline stripped: the join owns the
	// line breaks.
	repl := strings.Split(strings.TrimRight(f.Suggestion, "\n"), "\n")
	if same(lines[start:end+1], repl) {
		return nil, Applied{}, fmt.Errorf("that is already what the file says")
	}

	out := make([]string, 0, len(lines)-(end-start+1)+len(repl))
	out = append(out, lines[:start]...)
	out = append(out, repl...)
	out = append(out, lines[end+1:]...)
	return out, Applied{Line: start + 1, Removed: end - start + 1, Added: len(repl)}, nil
}

// locate finds the quoted run in a file, returning 0-based bounds.
//
// The same shape as relocate, which does this against a diff, and for the same
// reason: where the same text occurs more than once (a bare `}` matches almost
// everywhere) the run nearest where the finding was wins, because lines shift by
// a few rather than teleport. Compared with trailing whitespace ignored, since a
// formatter that stripped it has not changed the code.
func locate(lines, quote []string, near int) (start, end int, ok bool) {
	if len(quote) == 0 || len(lines) < len(quote) {
		return 0, 0, false
	}
	best := -1
	for i := 0; i+len(quote) <= len(lines); i++ {
		match := true
		for j, want := range quote {
			if strings.TrimRight(lines[i+j], " \t") != strings.TrimRight(want, " \t") {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		if best < 0 || abs((i+1)-near) < abs((best+1)-near) {
			best = i
		}
	}
	if best < 0 {
		return 0, 0, false
	}
	return best, best + len(quote) - 1, true
}

func same(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if strings.TrimRight(a[i], " \t") != strings.TrimRight(b[i], " \t") {
			return false
		}
	}
	return true
}
