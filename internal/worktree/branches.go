package worktree

import (
	"strings"
)

// Ref is a branch offered as a base for new work.
type Ref struct {
	Name string `json:"name"` // "main", or "origin/main" for a remote-only branch
	// Remote is true for a remote-tracking ref, which is the one to prefer as a
	// base: your local copy of a shared branch is often behind.
	Remote bool `json:"remote"`
	// Current marks the branch checked out in the main checkout.
	Current bool `json:"current,omitempty"`
	// Default marks the repository's default branch (what origin/HEAD points at).
	Default bool `json:"default,omitempty"`
	// InUse names the worktree already holding this branch, if any. git refuses
	// to check the same branch out twice, so the picker must be able to say why.
	InUse string `json:"in_use,omitempty"`
}

// Branches lists the local and origin branches of repo, ordered so the useful
// ones come first: the current branch, then the default branch, then the rest
// alphabetically. Remote-only branches follow the locals.
func Branches(repo string) ([]Ref, error) {
	if !IsRepo(repo) {
		return nil, ErrNotGit
	}
	inUse := branchesInUse(repo)
	def := DefaultBranch(repo)

	var refs []Ref
	locals, _ := git(repo, "for-each-ref", "--format=%(refname:short)%09%(HEAD)", "refs/heads")
	for _, line := range lines(locals) {
		name, head, _ := strings.Cut(line, "\t")
		if name == "" || strings.HasPrefix(name, BranchPrefix) {
			continue // kunai's own worktree branches are not bases for more work
		}
		refs = append(refs, Ref{
			Name:    name,
			Current: strings.TrimSpace(head) == "*",
			Default: name == def,
			InUse:   inUse[name],
		})
	}

	local := make(map[string]bool, len(refs))
	for _, r := range refs {
		local[r.Name] = true
	}
	remotes, _ := git(repo, "for-each-ref", "--format=%(refname:short)", "refs/remotes/origin")
	for _, name := range lines(remotes) {
		short := strings.TrimPrefix(name, "origin/")
		// origin/HEAD is a symbolic alias, not a branch someone would pick.
		if short == "HEAD" || local[short] {
			continue
		}
		refs = append(refs, Ref{Name: name, Remote: true, Default: short == def})
	}

	sortRefs(refs)
	return refs, nil
}

// DefaultBranch is what origin/HEAD points at, falling back to whichever of the
// usual names exists. Empty when the repository has neither.
func DefaultBranch(repo string) string {
	if out, err := git(repo, "symbolic-ref", "--short", "-q", "refs/remotes/origin/HEAD"); err == nil {
		if name := strings.TrimPrefix(strings.TrimSpace(out), "origin/"); name != "" {
			return name
		}
	}
	for _, name := range []string{"main", "master"} {
		if branchExists(repo, name) {
			return name
		}
	}
	return ""
}

// ResolveBase turns the base the user picked into the ref a worktree should
// actually branch from.
//
// When fromOrigin is set (the default, matching t3code's
// newWorktreesStartFromOrigin) a local name is upgraded to its origin
// counterpart, so new work starts from what the team has rather than from a
// local branch that may be weeks stale. An explicit "origin/x" is left alone,
// and a branch with no remote counterpart falls back to the local ref rather
// than failing.
func ResolveBase(repo, base string, fromOrigin bool) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		base = DefaultBranch(repo)
	}
	if base == "" {
		return "", errNoBase
	}
	if fromOrigin && !strings.Contains(base, "/") {
		if refExists(repo, "refs/remotes/origin/"+base) {
			return "origin/" + base, nil
		}
	}
	if !refExists(repo, base) && !refExists(repo, "refs/heads/"+base) && !refExists(repo, "refs/remotes/"+base) {
		return "", errUnknownBase(base)
	}
	return base, nil
}

// BaseBranchName strips a remote prefix, giving the branch a merge or a pull
// request should target. "origin/main" and "main" both land on "main".
func BaseBranchName(base string) string {
	if _, after, found := strings.Cut(base, "/"); found && strings.HasPrefix(base, "origin/") {
		return after
	}
	return base
}

// branchesInUse maps a branch to the worktree that has it checked out. git
// refuses to check one branch out in two worktrees, so this is what lets the
// picker explain a refusal before it happens.
func branchesInUse(repo string) map[string]string {
	out := map[string]string{}
	entries, err := List(repo)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.Branch != "" {
			out[e.Branch] = e.Path
		}
	}
	return out
}

// sortRefs puts current first, then default, then locals before remotes, then
// alphabetical. An insertion sort keeps it dependency-free and the list is short.
func sortRefs(refs []Ref) {
	rank := func(r Ref) int {
		switch {
		case r.Current:
			return 0
		case r.Default:
			return 1
		case !r.Remote:
			return 2
		default:
			return 3
		}
	}
	for i := 1; i < len(refs); i++ {
		for j := i; j > 0; j-- {
			a, b := refs[j-1], refs[j]
			if rank(a) < rank(b) || (rank(a) == rank(b) && a.Name <= b.Name) {
				break
			}
			refs[j-1], refs[j] = b, a
		}
	}
}
