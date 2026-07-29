package ghapp

// Trading the App JWT for an installation token.
//
// The App JWT can only ask GitHub which installations exist. To read a pull
// request or post a review you need an INSTALLATION token, which is scoped to one
// installation of the App (typically one org) and lives an hour.
//
// Those tokens are cached, because a single review makes several calls and
// re-minting per call would be both slow and rude. The cache refreshes early
// rather than on expiry: a token that lapses between the check and the request
// fails a review that has already spent real quota, and the only cost of being
// early is one extra exchange an hour.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// tokenRefreshMargin is how long before real expiry a cached token is considered
// spent. Generous on purpose: an hour-long token refreshed two minutes early
// costs nothing, and the failure it prevents lands in the middle of work.
const tokenRefreshMargin = 2 * time.Minute

type cachedToken struct {
	value     string
	expiresAt time.Time
}

// live reports whether this token is still worth using at time now.
func (t cachedToken) live(now time.Time) bool {
	return t.value != "" && now.Before(t.expiresAt.Add(-tokenRefreshMargin))
}

// InstallationToken returns a token scoped to one installation, minting a new one
// only when the cached one is missing or close to expiry.
func (a *App) InstallationToken(ctx context.Context, installationID int64) (string, error) {
	a.mu.Lock()
	cached := a.tokens[installationID]
	a.mu.Unlock()
	if cached.live(a.now()) {
		return cached.value, nil
	}

	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	path := fmt.Sprintf("/app/installations/%d/access_tokens", installationID)
	if err := a.do(ctx, http.MethodPost, path, authApp, nil, &out); err != nil {
		return "", fmt.Errorf("could not get an access token for installation %d: %w", installationID, err)
	}
	if out.Token == "" {
		return "", fmt.Errorf("GitHub returned no access token for installation %d", installationID)
	}

	a.mu.Lock()
	a.tokens[installationID] = cachedToken{value: out.Token, expiresAt: out.ExpiresAt}
	a.mu.Unlock()
	return out.Token, nil
}

// Installation is one place the App is installed: an org or a user account.
type Installation struct {
	ID      int64 `json:"id"`
	Account struct {
		Login string `json:"login"`
	} `json:"account"`
}

// Installations lists everywhere this App is installed. Authenticated as the App
// itself, which is the only thing the App JWT is allowed to do.
func (a *App) Installations(ctx context.Context) ([]Installation, error) {
	var out []Installation
	if err := a.do(ctx, http.MethodGet, "/app/installations", authApp, nil, &out); err != nil {
		return nil, fmt.Errorf("could not list this App's installations: %w", err)
	}
	return out, nil
}

// InstallationFor finds the installation covering a repository, which is what a
// caller actually has in hand: an owner and a repo parsed out of a git remote.
//
// Asked of GitHub directly rather than matched against the Installations list by
// owner name, because an App can be installed on an org while a repo inside it is
// excluded from the selection, and the list cannot see that. A "not installed"
// answer here is the honest one and is the error the user needs to read.
func (a *App) InstallationFor(ctx context.Context, owner, repo string) (Installation, error) {
	var out Installation
	path := fmt.Sprintf("/repos/%s/%s/installation", url.PathEscape(owner), url.PathEscape(repo))
	if err := a.do(ctx, http.MethodGet, path, authApp, nil, &out); err != nil {
		return Installation{}, fmt.Errorf("kunai is not installed on %s/%s (install the App on that repository): %w", owner, repo, err)
	}
	return out, nil
}
