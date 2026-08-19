package worktree

// Checking out a commit to look at, rather than a branch to work on.
//
// Every other worktree here exists to hold work: it gets a `kunai/` branch, it
// can be merged, and it is listed as somewhere an agent is doing something. A
// pull-request review wants the opposite of all three. It reads code at one
// commit that somebody else wrote, it will never be merged, and giving it a
// branch would put a permanent entry in `git branch` for a review that lasts
// minutes.
//
// So a review worktree is DETACHED: checked out at a SHA, with no branch, and
// removed when the review ends. Kunai() lists only branch worktrees under the
// prefix, so these never appear as work in progress either.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// reviewSegment namespaces review checkouts under the repository's worktree
// directory, so they are recognisable on disk and cannot collide with a worktree
// named after a piece of work.
const reviewSegment = "review"

// FetchRef fetches one ref from a remote into the local repository.
//
// The reason this exists rather than a plain `git fetch`: a pull request from a
// FORK is not on any branch of the repository you have cloned, so nothing you can
// fetch normally will produce its code. GitHub publishes every pull request's head
// on the base repository as `refs/pull/<n>/head`, which means one fetch reaches
// both a colleague's branch and a stranger's fork with no extra remotes and no
// second clone.
func FetchRef(repo, remote, ref string) error {
	root, err := Root(repo)
	if err != nil {
		return err
	}
	// Fetched into a local ref of our own rather than left as FETCH_HEAD, because
	// FETCH_HEAD is overwritten by the next fetch anywhere in the repository and a
	// review that takes minutes would lose the commit it is reading.
	local := "refs/kunai/" + strings.TrimPrefix(ref, "refs/")
	if _, err := git(root, "fetch", "--no-tags", remote, "+"+ref+":"+local); err != nil {
		return fmt.Errorf("could not fetch %s from %s: %w", ref, remote, err)
	}
	return nil
}

// RemoteURL reads a remote's URL, which is how kunai learns which GitHub
// repository a local checkout is. Read from git rather than remembered, because a
// remote can be re-pointed and a stale answer would send a review to the wrong
// repository.
func RemoteURL(repo, remote string) (string, error) {
	root, err := Root(repo)
	if err != nil {
		return "", err
	}
	out, err := git(root, "remote", "get-url", remote)
	if err != nil {
		return "", fmt.Errorf("this repository has no %q remote, so kunai cannot tell which GitHub repository it is", remote)
	}
	return strings.TrimSpace(out), nil
}

// ResolveRef returns the commit a ref points at.
func ResolveRef(repo, ref string) (string, error) {
	root, err := Root(repo)
	if err != nil {
		return "", err
	}
	out, err := git(root, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// ReviewOptions describe a detached checkout to read.
type ReviewOptions struct {
	// Repo is any directory inside the repository; the main checkout is resolved
	// from it.
	Repo string
	// Root is the directory worktrees live under, usually <dataDir>/worktrees.
	Root string
	// Name distinguishes this checkout from other reviews of the same repository,
	// conventionally the pull request number.
	Name string
	// SHA is the commit to check out. Required: a review is always of a specific
	// commit, never of a moving ref, or the code reviewed would not be the code
	// the findings are posted against.
	SHA string
}

// CreateReview makes a detached worktree at a commit.
//
// Reusing an existing checkout at the same path is deliberate rather than an
// error: reviewing the same pull request twice is ordinary (a colleague pushes a
// fix and you look again), and the second review should not fail because the
// first one left a directory behind. The checkout is moved to the new commit
// instead, which is exactly what re-reviewing means.
func CreateReview(opts ReviewOptions) (Info, error) {
	repo, err := Root(opts.Repo)
	if err != nil {
		return Info{}, err
	}
	if opts.Root == "" {
		return Info{}, fmt.Errorf("worktree: no root directory configured")
	}
	if strings.TrimSpace(opts.SHA) == "" {
		return Info{}, fmt.Errorf("worktree: a review checkout needs a commit")
	}

	path := filepath.Join(opts.Root, RepoName(repo), reviewSegment, slug(opts.Name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Info{}, fmt.Errorf("worktree: prepare %s: %w", filepath.Dir(path), err)
	}

	if _, statErr := os.Stat(path); statErr == nil {
		// Already there from a previous review: move it to the new commit. If that
		// fails the directory is stale or broken, so it is torn down and remade
		// rather than left to fail every future review of this pull request.
		if _, err := git(path, "checkout", "--detach", opts.SHA); err == nil {
			return Info{Path: path, Repo: repo, BaseSHA: opts.SHA}, nil
		}
		_, _ = git(repo, "worktree", "remove", "--force", path)
		_ = os.RemoveAll(path)
		_, _ = git(repo, "worktree", "prune")
	}

	if _, err := git(repo, "worktree", "add", "--detach", path, opts.SHA); err != nil {
		return Info{}, err
	}
	return Info{Path: path, Repo: repo, BaseSHA: opts.SHA}, nil
}

// RemoveReview tears down a detached review checkout. There is no branch to
// delete, which is the point of it being detached.
func RemoveReview(info Info) error {
	if info.Path == "" || info.Repo == "" {
		return nil
	}
	if _, err := git(info.Repo, "worktree", "remove", "--force", info.Path); err != nil {
		// git refuses if the directory has already gone; tidy up regardless so a
		// removed checkout never blocks the next review of the same pull request.
		_ = os.RemoveAll(info.Path)
		_, _ = git(info.Repo, "worktree", "prune")
		return nil
	}
	return nil
}

// slug makes a name safe to use as one path segment.
func slug(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "pr"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
