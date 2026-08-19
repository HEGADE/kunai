package server

// Getting a pull request's diff onto disk in a shape that does not cost a
// fortune to read.
//
// The original wrote every file's patch into ONE file and told the prompt where
// it was. That is right in spirit (a path costs nothing; the diff pasted into
// the prompt costs the whole diff before the model has decided anything is worth
// reading) and wrong in practice as soon as a pull request is large, because a
// single file can only be read from the top.
//
// Measured on a real review of a 41-file, 9,800-line pull request: the model
// read that one file four times at byte offsets 2020, 3019, 7623 and 9836,
// hunting for the parts it wanted. Every one of those chunks then sat in the
// context for the remaining eighty-odd model calls and was billed again on each
// of them. The run cost 18.25M cache-read tokens and produced two findings.
//
// The fix is not to read less, it is to make the interesting part addressable.
// One diff per changed file, at a path mirroring the file's own, so the reviewer
// that wants `internal/server/sharetier.go` reads exactly that and never has the
// lockfile in its context at all. The path is derivable from the file path, so
// nothing has to be looked up to construct it.
//
// A small pull request keeps the single combined file, because there the old
// shape genuinely is cheaper: one read of 400 lines beats eleven reads of forty.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hegade/kunai/internal/ghapp"
	"github.com/hegade/kunai/internal/review"
)

const (
	// reviewDiffDir is where a review's diff is written inside its worktree.
	// Dotted and namespaced so it is obviously kunai's and obviously not part of
	// the change being reviewed; the whole checkout is thrown away regardless.
	reviewDiffDir = ".kunai-review"
	// reviewPerFileDir holds the per-file diffs, under the same root.
	reviewPerFileDir = reviewDiffDir + "/diff"
	// smallDiffLines is where reading the whole change in one go stops being the
	// cheaper option.
	//
	// Below it, the combined file is one read of something the reviewer was going
	// to read all of anyway, and splitting it would cost a read per file to
	// assemble the same picture. Above it, most of the diff is material this
	// particular review will never look at, and the combined file is the thing
	// that forces it into the context anyway.
	smallDiffLines = 800
)

// reviewDiff is where a review's material ended up on disk. Paths are relative
// to the worktree, so the prompt can name them the way the agent will type them.
type reviewDiff struct {
	// Whole is the combined diff, or empty when the change was too big for one.
	Whole string
	// Dir is the root of the per-file diffs.
	Dir string
	// Files is the changed files, each carrying the path to its own diff.
	Files []review.FileSummary
}

// writeDiff lays a pull request's patches out inside the worktree.
func writeDiff(worktreePath string, number int, files []ghapp.FileDiff) (reviewDiff, error) {
	out := reviewDiff{Dir: reviewPerFileDir, Files: make([]review.FileSummary, 0, len(files))}

	total := 0
	for _, f := range files {
		total += f.Additions + f.Deletions
	}

	for _, f := range files {
		sum := review.FileSummary{
			Path: f.Filename, Status: f.Status,
			Additions: f.Additions, Deletions: f.Deletions,
		}
		// No patch means binary, or too large for GitHub to render. There is
		// nothing to write and nothing to read, and saying so beside the file is
		// what stops the reviewer going looking for a file that is not there.
		if f.Patch != "" {
			rel, err := writeFileDiff(worktreePath, f)
			if err != nil {
				return reviewDiff{}, err
			}
			sum.Diff = rel
		}
		out.Files = append(out.Files, sum)
	}

	if total <= smallDiffLines {
		whole, err := writeWholeDiff(worktreePath, number, files)
		if err != nil {
			return reviewDiff{}, err
		}
		out.Whole = whole
	}
	return out, nil
}

// writeFileDiff writes one file's patch and returns its worktree-relative path.
func writeFileDiff(worktreePath string, f ghapp.FileDiff) (string, error) {
	rel := filepath.Join(reviewPerFileDir, safeDiffPath(f.Filename)+".diff")
	dst := filepath.Join(worktreePath, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", fmt.Errorf("could not prepare the review diff: %w", err)
	}
	if err := os.WriteFile(dst, []byte(fileDiffText(f)), 0o644); err != nil {
		return "", fmt.Errorf("could not write the review diff: %w", err)
	}
	return rel, nil
}

// writeWholeDiff writes the combined diff for a change small enough to be worth
// reading in one go.
func writeWholeDiff(worktreePath string, number int, files []ghapp.FileDiff) (string, error) {
	var b strings.Builder
	for _, f := range files {
		b.WriteString(fileDiffText(f))
	}
	rel := filepath.Join(reviewDiffDir, fmt.Sprintf("pr-%d.diff", number))
	dst := filepath.Join(worktreePath, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", fmt.Errorf("could not prepare the review diff: %w", err)
	}
	if err := os.WriteFile(dst, []byte(b.String()), 0o644); err != nil {
		return "", fmt.Errorf("could not write the review diff: %w", err)
	}
	return rel, nil
}

// fileDiffText is one file's section of a unified diff, headers included so each
// per-file diff is a valid patch on its own rather than a fragment.
func fileDiffText(f ghapp.FileDiff) string {
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", f.Filename, f.Filename)
	if f.Patch == "" {
		fmt.Fprintf(&b, "(%s, no textual diff available)\n", f.Status)
		return b.String()
	}
	b.WriteString(f.Patch)
	if !strings.HasSuffix(f.Patch, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

// safeDiffPath keeps a path from GitHub inside the diff directory.
//
// A repository path cannot normally climb out of its own root, but this one is
// joined onto a directory kunai creates and the cost of being wrong is writing a
// file wherever the name says. Cleaned and stripped of any leading climb, so
// traversal is inexpressible rather than merely unexpected.
func safeDiffPath(name string) string {
	clean := filepath.Clean("/" + filepath.ToSlash(name))
	return strings.TrimPrefix(clean, "/")
}
