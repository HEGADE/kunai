package worktree

import (
	"errors"
	"fmt"
)

// The errors below are the ones a caller acts on differently. Everything else
// git refuses is passed through with git's own wording, which is almost always
// more specific than anything this package could invent.
var (
	// ErrNotGit means the directory is not inside a git work tree, so a caller
	// can treat "no worktrees here" as an ordinary answer rather than a fault.
	ErrNotGit = errors.New("worktree: not a git repository")

	// errNoBase means the repository has no branch to start from, which happens
	// in a repo with no commits yet.
	errNoBase = errors.New("worktree: this repository has no branch to start from")

	// ErrDirtyRepo means the main checkout has uncommitted changes, so a merge
	// would mix them into the result. Refusing beats stashing on the user's
	// behalf: a stash they did not ask for is a surprise they have to remember.
	ErrDirtyRepo = errors.New("worktree: the main checkout has uncommitted changes")

	// ErrNotOnBase means the main checkout is on some other branch and the merge
	// could not be done without switching it, which is not kunai's to do.
	ErrNotOnBase = errors.New("worktree: the main checkout is not on the base branch")

	// ErrConflict means the merge stopped on a real conflict, left in place for
	// the user to resolve.
	ErrConflict = errors.New("worktree: merge stopped on a conflict")
)

func errUnknownBase(base string) error {
	return fmt.Errorf("worktree: no branch named %q", base)
}
