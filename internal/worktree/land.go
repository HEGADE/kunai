package worktree

import (
	"fmt"
	"strconv"
	"strings"
)

// MergeResult reports what a merge did, so the UI can say something true rather
// than "done".
type MergeResult struct {
	// Branch and Base name what was merged into what.
	Branch string `json:"branch"`
	Base   string `json:"base"`
	// FastForward is true when the base simply advanced, which is the common and
	// uneventful case.
	FastForward bool `json:"fast_forward"`
	// Commits is how many commits the base gained.
	Commits int `json:"commits"`
	// AlreadyMerged is true when there was nothing to do, which is worth saying
	// explicitly: silence here reads as failure.
	AlreadyMerged bool `json:"already_merged,omitempty"`
}

// Merge lands a worktree's branch on its base, in the main checkout.
//
// It refuses rather than improvises. A dirty main checkout is refused instead of
// stashed, because a stash nobody asked for is a surprise to remember later. A
// main checkout sitting on some other branch is refused instead of switched,
// because switching someone's checkout under them is not this feature's to do,
// with one exception: when the merge would be a plain fast-forward, the base ref
// can be advanced without touching any working tree at all, so that is done
// directly and nothing is disturbed.
//
// A conflict is left exactly where git put it, in the main checkout, and
// reported as ErrConflict. Resolving it is a human's job, and half-resolving it
// automatically would be worse than not trying.
func Merge(info Info) (MergeResult, error) {
	repo, err := Root(info.Repo)
	if err != nil {
		return MergeResult{}, err
	}
	base := BaseBranchName(info.Base)
	res := MergeResult{Branch: info.Branch, Base: base}

	ahead, err := commitsAhead(repo, base, info.Branch)
	if err != nil {
		return res, err
	}
	if ahead == 0 {
		res.AlreadyMerged = true
		return res, nil
	}
	res.Commits = ahead

	// A fast-forward can be done on the ref alone, with no checkout involved, so
	// it works whatever the main checkout happens to be doing right now. Only
	// worth the special case when the checkout is elsewhere; when it is already
	// on the base, the ordinary merge below fast-forwards it anyway.
	if canFastForward(repo, base, info.Branch) && !onBranch(repo, base) {
		if _, err := git(repo, "fetch", ".", info.Branch+":"+base); err != nil {
			return res, err
		}
		res.FastForward = true
		return res, nil
	}

	if dirty(repo) {
		return res, ErrDirtyRepo
	}
	if !onBranch(repo, base) {
		return res, fmt.Errorf("%w: it is on %q, switch it to %q to merge", ErrNotOnBase, currentBranch(repo), base)
	}

	out, code, err := gitCode(repo, "merge", "--no-edit", info.Branch)
	if code != 0 {
		if isConflict(repo) {
			return res, fmt.Errorf("%w: %s", ErrConflict, firstMeaningfulLine(out))
		}
		return res, err
	}
	res.FastForward = strings.Contains(out, "Fast-forward")
	return res, nil
}

// Remove deletes a worktree, and its branch when the caller asks. force is
// required when the worktree has uncommitted changes; without it git refuses,
// which is the behaviour we want by default.
//
// deleteBranch uses -D rather than -d deliberately: a caller that has decided to
// discard the work has already been shown what is unmerged (see UnmergedCommits),
// so a second refusal from git here would only be noise. The confirmation belongs
// in front of this call, not inside it.
func Remove(info Info, force, deleteBranch bool) error {
	repo, err := Root(info.Repo)
	if err != nil {
		return err
	}
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, info.Path)
	if _, err := git(repo, args...); err != nil {
		return err
	}
	if deleteBranch && info.Branch != "" {
		if _, err := git(repo, "branch", "-D", info.Branch); err != nil {
			return err
		}
	}
	return nil
}

// UnmergedCommits counts what discarding a worktree would throw away: commits on
// its branch that its base does not have. The number a confirmation dialog needs.
func UnmergedCommits(info Info) int {
	repo, err := Root(info.Repo)
	if err != nil {
		return 0
	}
	n, _ := commitsAhead(repo, BaseBranchName(info.Base), info.Branch)
	return n
}

// --- git predicates ----------------------------------------------------------

func commitsAhead(repo, base, branch string) (int, error) {
	out, err := git(repo, "rev-list", "--count", base+".."+branch)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("worktree: unreadable commit count %q", out)
	}
	return n, nil
}

// canFastForward reports whether base is an ancestor of branch, which is exactly
// the condition for advancing base without a merge commit.
func canFastForward(repo, base, branch string) bool {
	_, code, _ := gitCode(repo, "merge-base", "--is-ancestor", base, branch)
	return code == 0
}

func onBranch(repo, branch string) bool { return currentBranch(repo) == branch }

func currentBranch(repo string) string {
	out, err := git(repo, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func dirty(repo string) bool {
	out, err := git(repo, "status", "--porcelain")
	return err == nil && len(lines(out)) > 0
}

// isConflict distinguishes a merge that stopped on conflicting content from one
// that failed for another reason: MERGE_HEAD exists only while a merge is
// unfinished.
func isConflict(repo string) bool {
	return refExists(repo, "MERGE_HEAD")
}
