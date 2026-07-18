package server

import (
	"strconv"
	"strings"
)

// A parsed git diff, structured so the client renders it directly without
// re-diffing anything: the heavy lifting (LCS, hunking) is git's, and we only
// reshape its text output into typed rows. Kept deliberately flat — hunk headers
// ride the same line stream as their rows — so the client is a single loop.

// DiffLine is one row of a file's diff. Kind is "hunk" (a @@ header), "ctx"
// (unchanged), "add", or "del". Old/New are 1-based line numbers in the old and
// new file; each is 0 when it doesn't apply to that kind (an add has no old
// line, a del has no new line, a hunk header has neither).
type DiffLine struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
	Old  int    `json:"old,omitempty"`
	New  int    `json:"new,omitempty"`
}

// FileDiff is one file's worth of changes.
type FileDiff struct {
	Path      string     `json:"path"`
	Old       string     `json:"old,omitempty"` // rename source, when Status == "renamed"
	Status    string     `json:"status"`        // added | modified | deleted | renamed
	Binary    bool       `json:"binary,omitempty"`
	Lines     []DiffLine `json:"lines,omitempty"`
	Truncated bool       `json:"truncated,omitempty"`
}

// maxDiffLines caps how many rows one file's diff yields, so a machine-generated
// megafile can't balloon a response or the client's DOM. Beyond it the file is
// marked truncated and the rest is dropped. maxLineLen bounds a single row's
// text, so a minified one-line bundle can't ship megabytes in one row.
const (
	maxDiffLines = 6000
	maxLineLen   = 2000
)

// parseUnifiedDiff turns `git diff` output (one or many files) into FileDiffs.
// It reads only what git already emitted; there is no second pass over file
// contents, so memory is bounded by the diff text itself.
func parseUnifiedDiff(out []byte) []FileDiff {
	var files []FileDiff
	var cur *FileDiff
	var oldNo, newNo int

	flush := func() {
		if cur != nil {
			files = append(files, *cur)
		}
		cur = nil
	}

	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flush()
			cur = &FileDiff{Status: "modified"}
			// Seed the path from the header so binary files (no ---/+++ lines)
			// still get one; the +++ line overrides it for text files.
			cur.Path = gitHeaderPath(line)
			oldNo, newNo = 0, 0

		case cur == nil:
			// Preamble before the first file header (there isn't normally any).
			continue

		case strings.HasPrefix(line, "new file mode"):
			cur.Status = "added"
		case strings.HasPrefix(line, "deleted file mode"):
			cur.Status = "deleted"
		case strings.HasPrefix(line, "rename from "):
			cur.Status = "renamed"
			cur.Old = strings.TrimPrefix(line, "rename from ")
		case strings.HasPrefix(line, "rename to "):
			cur.Path = strings.TrimPrefix(line, "rename to ")

		case strings.HasPrefix(line, "Binary files "):
			cur.Binary = true

		case strings.HasPrefix(line, "--- "):
			if p := diffPath(line[4:]); p != "" && cur.Path == "" {
				cur.Path = p // deleted files carry their name only on the --- side
			}
		case strings.HasPrefix(line, "+++ "):
			if p := diffPath(line[4:]); p != "" {
				cur.Path = p
			}

		case strings.HasPrefix(line, "@@"):
			o, n := parseHunkStarts(line)
			oldNo, newNo = o, n
			cur.append(DiffLine{Kind: "hunk", Text: line})

		case strings.HasPrefix(line, "+"):
			cur.append(DiffLine{Kind: "add", Text: line[1:], New: newNo})
			newNo++
		case strings.HasPrefix(line, "-"):
			cur.append(DiffLine{Kind: "del", Text: line[1:], Old: oldNo})
			oldNo++
		case strings.HasPrefix(line, " "):
			cur.append(DiffLine{Kind: "ctx", Text: line[1:], Old: oldNo, New: newNo})
			oldNo++
			newNo++
		case strings.HasPrefix(line, "\\"):
			// "\ No newline at end of file" — metadata, not a row.
			continue
		}
	}
	flush()
	return files
}

// append adds a row unless the file has hit the row cap, in which case it flips
// Truncated and drops the rest.
func (f *FileDiff) append(l DiffLine) {
	if f.Truncated {
		return
	}
	if len(f.Lines) >= maxDiffLines {
		f.Truncated = true
		return
	}
	if len(l.Text) > maxLineLen {
		l.Text = l.Text[:maxLineLen] + "…"
	}
	f.Lines = append(f.Lines, l)
}

// gitHeaderPath pulls the new path out of a "diff --git a/x b/y" line via the
// " b/" marker — the reliable fallback when a file (e.g. binary) has no +++ line.
func gitHeaderPath(line string) string {
	rest := strings.TrimPrefix(line, "diff --git ")
	if i := strings.Index(rest, " b/"); i >= 0 {
		return strings.TrimRight(rest[i+3:], "\r")
	}
	return ""
}

// diffPath strips the a//b/ prefix from a --- / +++ path, returning "" for
// /dev/null (an add's old side or a delete's new side).
func diffPath(s string) string {
	s = strings.TrimRight(s, "\r")
	if s == "/dev/null" {
		return ""
	}
	if len(s) > 2 && (s[:2] == "a/" || s[:2] == "b/") {
		return s[2:]
	}
	return s
}

// parseHunkStarts reads the old and new starting line numbers from a hunk header
// like "@@ -12,7 +12,8 @@ func foo()". Returns 1/1 if it can't parse.
func parseHunkStarts(line string) (old, new int) {
	old, new = 1, 1
	fields := strings.Fields(line)
	for _, f := range fields {
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case '-':
			old = leadingInt(f[1:])
		case '+':
			new = leadingInt(f[1:])
		}
	}
	return old, new
}

// leadingInt reads the integer before an optional ",count" suffix.
func leadingInt(s string) int {
	if i := strings.IndexByte(s, ','); i >= 0 {
		s = s[:i]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 1
	}
	return n
}
