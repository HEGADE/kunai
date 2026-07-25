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
// Borrowed from t3code's resolveAvailableBranchName, which does the same thing
// for the same reason.
func AvailableBranch(repo, desired string) (string, error) {
	if !branchExists(repo, desired) {
		return desired, nil
	}
	for n := 1; n <= maxBranchAttempts; n++ {
		candidate := fmt.Sprintf("%s-%d", desired, n)
		if !branchExists(repo, candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("worktree: no free branch name for %q after %d attempts", desired, maxBranchAttempts)
}

func branchExists(repo, branch string) bool {
	return refExists(repo, "refs/heads/"+branch)
}
