package review

// Which lines GitHub will accept an inline comment on.
//
// This is the single most failure-prone part of posting a review, and the failure
// mode is unusually harsh: an inline comment on a line that is not part of the
// diff makes GitHub reject the ENTIRE review with a 422, not just that comment.
// One bad line number and a review that cost real quota is lost.
//
// So the diff is parsed once into the set of positions that are actually
// commentable, and every finding is checked against it before anything is sent.
// A finding that does not fit is demoted to the summary rather than risking the
// whole submission.
//
// The rules, which are easy to state and easy to get subtly wrong:
//
//	context line  ' '  exists in both files: commentable on RIGHT and on LEFT
//	added line    '+'  exists only in the new file: RIGHT only
//	removed line  '-'  exists only in the old file: LEFT only
//
// Line numbers come from the hunk header, `@@ -old,count +new,count @@`, and
// advance per line according to which file that line is in.

import (
	"strconv"
	"strings"
)

// Anchors is the set of commentable positions in one pull request's diff.
type Anchors struct {
	// files maps path -> side -> line -> true.
	files map[string]map[string]map[int]bool
}

// FileDiff is one file's patch, as GitHub's "list pull request files" returns it.
// Patch is empty for a binary file or one too large to diff, which is a real case
// and simply means nothing in that file is commentable.
type FileDiff struct {
	Filename string `json:"filename"`
	Status   string `json:"status"`
	Patch    string `json:"patch"`
}

// ParseDiff builds the commentable set from the files in a pull request.
func ParseDiff(files []FileDiff) *Anchors {
	a := &Anchors{files: map[string]map[string]map[int]bool{}}
	for _, f := range files {
		if f.Patch == "" {
			// Binary, or too large for GitHub to render. The file is still part of
			// the pull request, but no line in it can carry a comment.
			a.ensure(f.Filename)
			continue
		}
		a.addPatch(f.Filename, f.Patch)
	}
	return a
}

func (a *Anchors) ensure(file string) map[string]map[int]bool {
	if a.files[file] == nil {
		a.files[file] = map[string]map[int]bool{
			SideRight: {},
			SideLeft:  {},
		}
	}
	return a.files[file]
}

func (a *Anchors) addPatch(file, patch string) {
	sides := a.ensure(file)
	var oldLine, newLine int
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "@@") {
			oldLine, newLine = parseHunkHeader(line)
			continue
		}
		if oldLine == 0 && newLine == 0 {
			continue // anything before the first hunk header
		}
		switch {
		case strings.HasPrefix(line, "+"):
			sides[SideRight][newLine] = true
			newLine++
		case strings.HasPrefix(line, "-"):
			sides[SideLeft][oldLine] = true
			oldLine++
		case strings.HasPrefix(line, `\`):
			// "\ No newline at end of file" annotates the previous line and is not
			// a line of its own in either file.
		default:
			// A context line, including the empty string git emits for a blank
			// context line with its leading space stripped by a transport.
			sides[SideRight][newLine] = true
			sides[SideLeft][oldLine] = true
			oldLine++
			newLine++
		}
	}
}

// parseHunkHeader reads the starting line numbers out of `@@ -12,7 +12,9 @@`.
// A malformed header yields zeros, which makes the hunk contribute nothing rather
// than contributing wrong positions.
func parseHunkHeader(header string) (oldStart, newStart int) {
	body := header
	if i := strings.Index(body[2:], "@@"); i >= 0 {
		body = body[2 : i+2]
	}
	for _, field := range strings.Fields(body) {
		if len(field) < 2 {
			continue
		}
		sign, rest := field[0], field[1:]
		if sign != '-' && sign != '+' {
			continue
		}
		numStr, _, _ := strings.Cut(rest, ",")
		n, err := strconv.Atoi(numStr)
		if err != nil {
			continue
		}
		if sign == '-' {
			oldStart = n
		} else {
			newStart = n
		}
	}
	return oldStart, newStart
}

// Commentable reports whether GitHub will accept an inline comment at this
// position.
func (a *Anchors) Commentable(file, side string, line int) bool {
	sides, ok := a.files[file]
	if !ok {
		return false
	}
	return sides[side][line]
}

// Touches reports whether the pull request changed this file at all. Used to tell
// two different failures apart in what the reader is told: a finding about a file
// the PR never touched is expected and worth explaining, while a finding on a
// changed file at an uncommentable line is the agent picking a line just outside
// the hunk.
func (a *Anchors) Touches(file string) bool {
	_, ok := a.files[file]
	return ok
}

// Files lists the changed paths, for the prompt that tells the agent what is in
// the pull request.
func (a *Anchors) Files() []string {
	out := make([]string, 0, len(a.files))
	for f := range a.files {
		out = append(out, f)
	}
	return out
}
