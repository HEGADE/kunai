package review

// The lines around a finding, so a card can carry its own evidence.
//
// A finding read as a claim with a file and a number attached is not reviewable:
// deciding whether it is right means looking at the code, and making somebody go
// and find that code themselves is how a review becomes a chore. So each finding
// travels with the hunk it is about, which the diff already contains.
//
// Cut from the patch rather than read from disk, because the patch is what the
// finding's line numbers refer to. Reading the file would drift the moment the
// pull request moved, and the numbers would quietly point at the wrong lines.

import (
	"strconv"
	"strings"
)

// HunkLine is one line of evidence, numbered as GitHub numbers it.
type HunkLine struct {
	// Kind is " ", "+" or "-", the diff's own vocabulary.
	Kind string `json:"kind"`
	// Old and New are the line's number in each file, 0 where it does not exist.
	Old int `json:"old,omitempty"`
	New int `json:"new,omitempty"`
	// Text is the line without its marker.
	Text string `json:"text"`
	// Focus marks the lines the finding is actually about, so a card can show
	// context without letting it drown the point.
	Focus bool `json:"focus,omitempty"`
}

// hunkContext is how many lines either side of a finding are worth showing. Wide
// enough to see the shape of the code, narrow enough that a card stays a card.
const hunkContext = 4

// HunkFor returns the diff lines around a finding, or nil when the finding is
// not anchored in this diff (which is exactly the case that gets demoted to the
// summary, and which has no hunk to show by definition).
func HunkFor(files []FileDiff, f Finding) []HunkLine {
	for _, file := range files {
		if file.Filename != f.File || file.Patch == "" {
			continue
		}
		return sliceHunk(parsePatch(file.Patch), f)
	}
	return nil
}

// parsePatch walks a patch into numbered lines, which is the same walk ParseDiff
// does for anchoring. Kept separate rather than shared because that one answers a
// yes/no question as cheaply as possible and this one keeps the text.
func parsePatch(patch string) []HunkLine {
	var out []HunkLine
	var oldLine, newLine int
	for _, raw := range strings.Split(patch, "\n") {
		if strings.HasPrefix(raw, "@@") {
			oldLine, newLine = parseHunkHeader(raw)
			continue
		}
		if oldLine == 0 && newLine == 0 {
			continue
		}
		switch {
		case strings.HasPrefix(raw, "+"):
			out = append(out, HunkLine{Kind: "+", New: newLine, Text: raw[1:]})
			newLine++
		case strings.HasPrefix(raw, "-"):
			out = append(out, HunkLine{Kind: "-", Old: oldLine, Text: raw[1:]})
			oldLine++
		case strings.HasPrefix(raw, `\`):
			// "\ No newline at end of file" is an annotation, not a line.
		default:
			text := raw
			if strings.HasPrefix(raw, " ") {
				text = raw[1:]
			}
			out = append(out, HunkLine{Kind: " ", Old: oldLine, New: newLine, Text: text})
			oldLine++
			newLine++
		}
	}
	return out
}

// sliceHunk takes the window around the finding and marks the lines it is about.
func sliceHunk(lines []HunkLine, f Finding) []HunkLine {
	at := func(l HunkLine) int {
		if f.Side == SideLeft {
			return l.Old
		}
		return l.New
	}
	start, end := f.StartLine(), f.LastLine()

	first, last := -1, -1
	for i, l := range lines {
		n := at(l)
		if n == 0 {
			continue // this line does not exist on the side the finding is about
		}
		if n >= start && n <= end {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return nil // the finding's lines are not in this patch
	}
	for i := first; i <= last; i++ {
		lines[i].Focus = true
	}

	lo := max(0, first-hunkContext)
	hi := min(len(lines)-1, last+hunkContext)
	return lines[lo : hi+1]
}

// Label renders a line's number for the gutter: the side the finding is about,
// so the numbers a card shows are the numbers the finding quotes.
func (l HunkLine) Label(side string) string {
	n := l.New
	if side == SideLeft {
		n = l.Old
	}
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}
