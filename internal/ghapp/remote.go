package ghapp

// Turning a local clone's remote into an owner and a repository.
//
// This is how kunai learns which GitHub repository a session's folder belongs to:
// it already knows the checkout on disk, and the remote is the only thing that
// says what that checkout IS on GitHub. Everything downstream (listing pull
// requests, finding the installation, posting a review) is keyed on the answer.
//
// Pure and free of git and of the network, so the parsing is exercised directly
// rather than through a repository fixture.

import (
	"errors"
	"fmt"
	"strings"
)

// ErrNotGitHub reports a remote that is not on GitHub. Its own error, because it
// is a perfectly ordinary state (a GitLab checkout, a bare local repo) that the
// UI should answer by not offering review at all, rather than by showing a
// failure.
var ErrNotGitHub = errors.New("not a GitHub remote")

// ParseRemote reads a git remote URL into a Repo.
//
// The three spellings below are all in daily use and none is more correct than
// the others, so all are accepted:
//
//	git@github.com:owner/repo.git
//	https://github.com/owner/repo.git
//	ssh://git@github.com/owner/repo
//
// A host that is not github.com is ErrNotGitHub rather than a parse failure, so a
// caller can tell "this repo is somewhere else" apart from "this remote is
// malformed". GitHub Enterprise is deliberately not guessed at: it needs its own
// API base URL, and silently treating an Enterprise host as github.com would send
// a review to the wrong place.
func ParseRemote(remote string) (Repo, error) {
	raw := strings.TrimSpace(remote)
	if raw == "" {
		return Repo{}, errors.New("this folder has no git remote, so kunai cannot tell which repository it is")
	}

	host, path, err := splitRemote(raw)
	if err != nil {
		return Repo{}, err
	}
	if !strings.EqualFold(host, "github.com") {
		return Repo{}, fmt.Errorf("%w: %s", ErrNotGitHub, host)
	}

	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	owner, name, ok := strings.Cut(path, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return Repo{}, fmt.Errorf("could not read an owner and repository out of %q", remote)
	}
	return Repo{Owner: owner, Name: name}, nil
}

// splitRemote separates the host from the path for the scp-like and URL forms.
// Written by hand rather than through net/url because the scp-like spelling
// (git@github.com:owner/repo.git) is not a URL at all: url.Parse accepts it and
// puts the whole thing in Opaque or Path depending on the colon, which is a
// subtler wrong answer than a plain parse.
func splitRemote(raw string) (host, path string, err error) {
	if i := strings.Index(raw, "://"); i >= 0 {
		rest := raw[i+3:]
		if at := strings.LastIndex(rest, "@"); at >= 0 {
			rest = rest[at+1:] // drop any userinfo
		}
		host, path, _ = strings.Cut(rest, "/")
		if host == "" || path == "" {
			return "", "", fmt.Errorf("could not read a host and path out of %q", raw)
		}
		return host, path, nil
	}

	// scp-like: [user@]host:path
	rest := raw
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		rest = rest[at+1:]
	}
	host, path, ok := strings.Cut(rest, ":")
	if !ok || host == "" || path == "" {
		return "", "", fmt.Errorf("could not read a host and path out of %q", raw)
	}
	return host, path, nil
}
