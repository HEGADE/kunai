package ghapp

// Reading pull requests as an installation.
//
// Deliberately a thin, named surface rather than a general GitHub client: every
// call kunai makes is here, so what this feature can reach is a list you can read
// in one sitting. Adding a capability means adding a method, which is the point.
// The App's permissions are the real boundary (pull requests read and write,
// contents read, metadata read), and this mirrors them so the code cannot quietly
// want more than the installation was granted.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Repo is a GitHub repository as kunai learns it: parsed from a local clone's
// origin remote.
type Repo struct {
	Owner string
	Name  string
}

func (r Repo) String() string { return r.Owner + "/" + r.Name }

// path builds an escaped /repos/{owner}/{repo}/... path.
func (r Repo) path(format string, args ...any) string {
	base := "/repos/" + url.PathEscape(r.Owner) + "/" + url.PathEscape(r.Name)
	if format == "" {
		return base
	}
	return base + fmt.Sprintf(format, args...)
}

// PullRequest is one open pull request, reduced to what the review surface shows
// and what a review needs to run: which commit to check out, and whether the head
// is a fork (which decides the agent's toolset).
type PullRequest struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Draft     bool      `json:"draft"`
	UpdatedAt time.Time `json:"updated_at"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
	Head struct {
		SHA  string `json:"sha"`
		Ref  string `json:"ref"`
		Repo *struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
}

// FromFork reports that this PR's head lives in a different repository, which is
// the signal that its diff is untrusted input written by someone outside the
// repo. The agent reviewing it gets no Bash.
//
// Read from the head repo's identity rather than from the author's login: a
// maintainer can open a PR from their own fork, and a stranger cannot push a
// branch to the base repo, so the repository is what actually says who controlled
// the code. A head repo GitHub declines to name (a deleted fork) is treated as a
// fork, because the safe reading of "unknown" here is "not ours".
func (p PullRequest) FromFork(base Repo) bool {
	if p.Head.Repo == nil {
		return true
	}
	return !strings.EqualFold(p.Head.Repo.FullName, base.String())
}

// OpenPullRequests lists the open pull requests on a repository, most recently
// updated first, as the installation.
//
// Not paginated: the review surface offers what is open right now, and a repo
// with more than a page of open PRs is not one where you scroll a dashboard card
// looking for the right one. If that changes it is a page parameter, not a
// redesign.
func (a *App) OpenPullRequests(ctx context.Context, repo Repo) ([]PullRequest, error) {
	token, err := a.repoToken(ctx, repo)
	if err != nil {
		return nil, err
	}
	var out []PullRequest
	path := repo.path("/pulls?state=open&sort=updated&direction=desc&per_page=50")
	if err := a.doAuthed(ctx, http.MethodGet, path, authInstallation, token, nil, &out); err != nil {
		return nil, fmt.Errorf("could not list open pull requests on %s: %w", repo, err)
	}
	return out, nil
}

// PullRequest reads one pull request. The list endpoint omits the diff totals, so
// this is what the review run uses.
func (a *App) PullRequest(ctx context.Context, repo Repo, number int) (PullRequest, error) {
	token, err := a.repoToken(ctx, repo)
	if err != nil {
		return PullRequest{}, err
	}
	var out PullRequest
	if err := a.doAuthed(ctx, http.MethodGet, repo.path("/pulls/%d", number), authInstallation, token, nil, &out); err != nil {
		return PullRequest{}, fmt.Errorf("could not read %s#%d: %w", repo, number, err)
	}
	return out, nil
}

// Review is a review already on a pull request. kunai reads these for two
// reasons: to avoid repeating a point a human has already made, and to notice
// that this very commit has already been reviewed by the bot, possibly on a
// colleague's machine.
type Review struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	User struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"user"`
	State       string    `json:"state"`
	CommitID    string    `json:"commit_id"`
	SubmittedAt time.Time `json:"submitted_at"`
}

// ByBot reports a review posted by this App rather than by a person. GitHub
// appends "[bot]" to an App's login and sets the account type to Bot; the type is
// checked because a human is free to name themselves anything.
func (r Review) ByBot() bool { return r.User.Type == "Bot" }

// Reviews lists the reviews on a pull request.
func (a *App) Reviews(ctx context.Context, repo Repo, number int) ([]Review, error) {
	token, err := a.repoToken(ctx, repo)
	if err != nil {
		return nil, err
	}
	var out []Review
	path := repo.path("/pulls/%d/reviews?per_page=100", number)
	if err := a.doAuthed(ctx, http.MethodGet, path, authInstallation, token, nil, &out); err != nil {
		return nil, fmt.Errorf("could not read the reviews on %s#%d: %w", repo, number, err)
	}
	return out, nil
}

// ReviewedAt reports whether this App has already reviewed exactly this commit,
// and when. This is the whole of the duplicate-work defence, and it works across
// machines that cannot see each other because GitHub is the only state two
// colleagues' installs share.
//
// Matched on the commit rather than the pull request: a review of an older commit
// says nothing about the code as it stands now, and offering to skip on that basis
// would hide a review the new commits genuinely need.
func (a *App) ReviewedAt(ctx context.Context, repo Repo, number int, headSHA string) (time.Time, bool, error) {
	reviews, err := a.Reviews(ctx, repo, number)
	if err != nil {
		return time.Time{}, false, err
	}
	var latest time.Time
	found := false
	for _, r := range reviews {
		if !r.ByBot() || !strings.EqualFold(r.CommitID, headSHA) {
			continue
		}
		if r.SubmittedAt.After(latest) {
			latest, found = r.SubmittedAt, true
		}
	}
	return latest, found, nil
}

// repoToken resolves the installation covering a repository and mints (or reuses)
// its token. The installation lookup is not cached: an App can be uninstalled or
// have a repo removed from its selection between two reviews, and finding that
// out at the moment of use is better than acting on a stale yes.
func (a *App) repoToken(ctx context.Context, repo Repo) (string, error) {
	inst, err := a.InstallationFor(ctx, repo.Owner, repo.Name)
	if err != nil {
		return "", err
	}
	return a.InstallationToken(ctx, inst.ID)
}
