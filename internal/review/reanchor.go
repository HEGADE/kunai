package review

// Posting a review onto a pull request that moved while you were reading it.
//
// This replaces a refusal, and the refusal was the wrong answer to a real
// problem. Somebody pushes, the head changes, and posting stopped dead with
// "#5 has moved on since this review (now 8c802e4d); review it again before
// posting" -- throwing away a review that cost minutes of work and dollars of
// tokens, over a commit that in the ordinary case did not touch a single line
// any of the findings are about. Reviewing it again is exactly the same work for
// exactly the same answer.
//
// A finding is not really about a line NUMBER, it is about a line of CODE. The
// number is how GitHub is told where to put the comment, and it is the only part
// a push invalidates. So the fix is to remember what each finding quoted, find
// that text again in the new diff, and move the comment onto wherever it lives
// now. A rebase that shifts every line by twelve then costs nothing at all.
//
// What cannot be found has genuinely changed under the review, and that finding
// is demoted to the summary and says so, rather than being posted onto whatever
// happens to occupy its old line number. That is the case the old refusal was
// really protecting against, and it is a property of one finding rather than of
// the whole review.

import (
	"strconv"
	"strings"
)

// Quote is the text of the lines a finding is anchored to, captured from the
// diff that was read.
//
// Stored on the finding rather than recomputed later because later is exactly
// when it is gone: the whole point is to compare what the finding was about
// against what is there now, and by then only the new diff is in hand.
func Quote(files []FileDiff, f Finding) []string {
	for _, file := range files {
		if file.Filename != f.File || file.Patch == "" {
			continue
		}
		var out []string
		for _, l := range parsePatch(file.Patch) {
			if n := l.number(f.Side); n != 0 && n >= f.StartLine() && n <= f.LastLine() {
				out = append(out, l.Text)
			}
		}
		return out
	}
	return nil
}

// number is a line's position on one side, 0 when it does not exist there.
func (l HunkLine) number(side string) int {
	if side == SideLeft {
		return l.Old
	}
	return l.New
}

// Reanchor moves a draft's findings onto the lines they quote in a newer diff.
//
// Findings with no quote are left exactly as they are. That is not a gap: a
// finding with no quote was never anchored in the diff in the first place (it is
// about a file the pull request does not change, which is often the most
// valuable kind), so its line number refers to the file rather than to the
// patch, and a push that does not touch that file has not moved it.
func Reanchor(d Draft, now []FileDiff) (Draft, ReanchorReport) {
	patches := make(map[string][]HunkLine, len(now))
	for _, f := range now {
		if f.Patch != "" {
			patches[f.Filename] = parsePatch(f.Patch)
		}
	}

	out := Draft{Summary: d.Summary, Findings: make([]Finding, 0, len(d.Findings))}
	var rep ReanchorReport
	for _, f := range d.Findings {
		if len(f.Quote) == 0 {
			out.Findings = append(out.Findings, f)
			continue
		}
		start, end, ok := relocate(patches[f.File], f.Side, f.Quote, f.StartLine())
		switch {
		case !ok:
			// The code this finding is about is no longer in the diff. Kept and
			// demoted rather than dropped: it may still be true, and the person
			// posting has already decided to send it.
			f.Stale = true
			rep.Stale++
		case start != f.StartLine() || end != f.LastLine():
			f.Line, f.EndLine = start, end
			if f.EndLine == f.Line {
				f.EndLine = 0
			}
			rep.Moved++
		default:
			rep.Unchanged++
		}
		out.Findings = append(out.Findings, f)
	}
	return out, rep
}

// ReanchorReport is what happened, for the log and for the note in the posted
// review. A reader has to be able to tell "it moved cleanly" from "two findings
// are about code that no longer exists".
type ReanchorReport struct {
	Unchanged int
	Moved     int
	Stale     int
}

// Any reports whether the pull request moving made any difference at all.
func (r ReanchorReport) Any() bool { return r.Moved > 0 || r.Stale > 0 }

// relocate finds where a quoted run of lines lives in a new patch.
//
// Matched on text, on the finding's own side, and the run must be contiguous in
// the same order. Where the same text occurs more than once (a bare `}` matches
// almost everywhere) the run nearest the original line wins, which is right for
// the case this exists for: lines shift by a few, they do not teleport.
func relocate(lines []HunkLine, side string, quote []string, near int) (start, end int, ok bool) {
	// Only the lines that exist on the side this finding is about, since a
	// comment on the old file can only be placed against a deleted line.
	var nums []int
	var texts []string
	for _, l := range lines {
		if n := l.number(side); n != 0 {
			nums = append(nums, n)
			texts = append(texts, l.Text)
		}
	}
	if len(quote) == 0 || len(texts) < len(quote) {
		return 0, 0, false
	}

	best := -1
	for i := 0; i+len(quote) <= len(texts); i++ {
		match := true
		for j, want := range quote {
			if texts[i+j] != want {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		if best < 0 || abs(nums[i]-near) < abs(nums[best]-near) {
			best = i
		}
	}
	if best < 0 {
		return 0, 0, false
	}
	return nums[best], nums[best+len(quote)-1], true
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// MovedNote is the line added to a posted review whose pull request moved after
// it was read.
//
// Said out loud rather than left implicit. The author is entitled to know that
// the reviewer read an older commit, because it is the one thing that could make
// an otherwise correct finding wrong, and a bot that quietly comments on code it
// did not read is exactly the kind that gets muted.
func MovedNote(readSHA string, rep ReanchorReport) string {
	var b strings.Builder
	b.WriteString("This review read ")
	b.WriteString(shortSHA(readSHA))
	b.WriteString(", and the branch has moved since. ")
	switch {
	case rep.Moved > 0 && rep.Stale > 0:
		b.WriteString("Comments were re-attached to the code they quote; ")
		b.WriteString(plural(rep.Stale, "finding", "findings"))
		b.WriteString(" below are about code that has changed since, so they may already be fixed.")
	case rep.Moved > 0:
		b.WriteString("Every comment was re-attached to the exact code it quotes, so the lines are current.")
	default:
		b.WriteString(plural(rep.Stale, "finding", "findings"))
		b.WriteString(" below are about code that has changed since, so they may already be fixed.")
	}
	return b.String()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
