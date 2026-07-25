package worktree

import (
	"strconv"
	"strings"
)

// Status is how far a worktree has come: what it has committed relative to its
// base, and what is still loose in the working tree.
type Status struct {
	// Ahead and Behind count commits since the branch and its base diverged.
	Ahead  int `json:"ahead"`
	Behind int `json:"behind"`
	// Dirty counts entries git status reports, so a card can say "3 uncommitted
	// changes" without listing them.
	Dirty int `json:"dirty"`
	// Files are the paths this branch changed against its base, committed and
	// uncommitted together, because from the outside that distinction is not
	// what you want to know first.
	Files []string `json:"files"`
	// Staleness: the base has moved on since the worktree was made. Worth
	// surfacing before a merge, since it predicts a conflict.
	BaseMoved bool `json:"base_moved,omitempty"`
}

// StatusOf reads a worktree's position against its base. It never fails on an
// empty or unusual state: a branch with no commits yet is simply zero ahead.
func StatusOf(info Info) (Status, error) {
	if !IsRepo(info.Path) {
		return Status{}, ErrNotGit
	}
	var st Status

	// Three dots: count from where the two actually diverged, not from the tip of
	// the base, or every commit made on main since would read as "behind".
	if out, err := git(info.Path, "rev-list", "--left-right", "--count", info.Base+"..."+"HEAD"); err == nil {
		st.Behind, st.Ahead = parseCounts(out)
	}

	if out, err := git(info.Path, "status", "--porcelain"); err == nil {
		st.Dirty = len(lines(out))
	}

	st.Files = changedFiles(info)

	if info.BaseSHA != "" {
		if now, err := git(info.Path, "rev-parse", "--verify", "-q", info.Base+"^{commit}"); err == nil {
			st.BaseMoved = strings.TrimSpace(now) != info.BaseSHA
		}
	}
	return st, nil
}

// changedFiles unions the committed diff against the base with whatever is still
// uncommitted, de-duplicated and in a stable order.
func changedFiles(info Info) []string {
	seen := map[string]bool{}
	// Non-nil so the JSON is [] rather than null. A nil slice marshals to null,
	// and a client reading .files.length off it throws rather than seeing zero
	// files, which is the ordinary case for a worktree nobody has edited yet.
	out := []string{}
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}

	if diff, err := git(info.Path, "diff", "--name-only", info.Base+"...HEAD"); err == nil {
		for _, p := range lines(diff) {
			add(p)
		}
	}
	// Porcelain v1 lines are "XY path", and a rename is "XY old -> new"; the new
	// name is the one that exists now, so that is the one reported.
	if st, err := git(info.Path, "status", "--porcelain"); err == nil {
		for _, l := range lines(st) {
			if len(l) < 4 {
				continue
			}
			path := strings.TrimSpace(l[3:])
			if _, after, found := strings.Cut(path, " -> "); found {
				path = after
			}
			add(strings.Trim(path, `"`))
		}
	}
	return out
}

// parseCounts reads git's "<left>\t<right>" counter output.
func parseCounts(out string) (left, right int) {
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return 0, 0
	}
	l, _ := strconv.Atoi(fields[0])
	r, _ := strconv.Atoi(fields[1])
	return l, r
}
