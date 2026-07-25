package worktree

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

// maxBranchAttempts bounds the collision search below. A hundred worktrees named
// the same thing in one repo is not a case worth serving; failing with a clear
// message beats looping.
const maxBranchAttempts = 100

// Slug turns whatever the user typed into a branch-safe segment: lowercase,
// words joined by a single dash, nothing git would reject or a shell would need
// quoted. An empty or entirely unusable name falls back to "work" rather than
// producing an invalid ref.
func Slug(name string) string {
	var b strings.Builder
	lastDash := true // leading dashes are dropped
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case r == '.' || r == '_':
			// git allows these but they read badly in a path; treat as separators.
			fallthrough
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "work"
	}
	// git refuses a ref component ending in ".lock" and one that is exactly "@".
	s = strings.TrimSuffix(s, "-lock")
	if s == "" || s == "@" {
		return "work"
	}
	return s
}

// BranchFor is the branch name a worktree named `name` would use.
func BranchFor(name string) string { return BranchPrefix + Slug(name) }

// PathFor is where a branch's worktree lives under root: grouped by repository,
// then the branch with its prefix folded into the directory name. Slashes cannot
// survive into a single path segment, so `kunai/fix-auth` becomes `fix-auth`
// under the repo's directory, which also keeps the path short enough to read in
// a UI.
func PathFor(root, repo, branch string) string {
	segment := strings.ReplaceAll(strings.TrimPrefix(branch, BranchPrefix), "/", "-")
	return filepath.Join(root, RepoName(repo), segment)
}

// AvailableBranch returns desired, or the first free `desired-N` when it is
// taken. Without this, starting a second worktree with a name you have used
// before simply fails, which is the common case rather than an edge one (you
// finish a piece of work, keep the branch, and start another like it).
//
// Borrowed from t3code's resolveAvailableBranchName, including where it starts
// counting: the second one is "-2", because it is the second, and a "-1" beside
// an unsuffixed original reads as though the original were the zeroth.
func AvailableBranch(repo, desired string) (string, error) {
	if !branchExists(repo, desired) {
		return desired, nil
	}
	for n := 2; n < maxBranchAttempts+2; n++ {
		candidate := fmt.Sprintf("%s-%d", desired, n)
		if !branchExists(repo, candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("worktree: no free branch name for %q after %d attempts", desired, maxBranchAttempts)
}

// Rename moves a worktree's branch to a new name, keeping its recorded merge
// base. Used to replace a placeholder once the user has said what they are doing.
//
// The directory is deliberately left where it is. git can move a worktree, but a
// session's claude process is running with that directory as its cwd, and moving
// it out from under a live process is a good way to break a turn in a way that is
// very hard to explain. The branch is the identity here; the directory is just
// where the files happen to sit.
func Rename(info Info, name string) (Info, error) {
	repo, err := Root(info.Repo)
	if err != nil {
		return info, err
	}
	target, err := AvailableBranch(repo, BranchFor(name))
	if err != nil {
		return info, err
	}
	if target == info.Branch {
		return info, nil
	}
	if _, err := git(repo, "branch", "-m", info.Branch, target); err != nil {
		return info, err
	}
	renamed := info
	renamed.Branch = target
	// The merge base is stored per branch, so it does not follow a rename.
	recordMergeBase(repo, renamed)
	return renamed, nil
}

func branchExists(repo, branch string) bool {
	return refExists(repo, "refs/heads/"+branch)
}
