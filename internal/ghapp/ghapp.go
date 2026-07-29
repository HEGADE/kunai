// Package ghapp authenticates as a GitHub App and makes the handful of REST
// calls kunai's pull-request review needs.
//
// A GitHub App is used for one reason: attribution. Reviews have to appear as
// kunai[bot] on an org's pull requests, not under the name of whoever happens to
// be running kunai, because posting a machine's review under a colleague's
// account misrepresents who reviewed the code. That rules out shelling `gh`,
// which authenticates as the human.
//
// The App deliberately has NO WEBHOOK. People assume App means webhooks, but an
// App registered with no webhook URL is perfectly valid and is purely an identity
// plus a credential. That matters here more than anywhere: kunai exposes nothing
// to the internet, and a review is triggered by a person clicking a button in
// kunai rather than by GitHub calling in. Nothing in this package listens.
//
// Authentication is two hops, which is the shape GitHub requires:
//
//	private key --(RS256 JWT, <=10 min)--> "as the App"
//	           --(POST installations/N/access_tokens)--> "as this installation"
//
// Only the second token can touch a repository. The first can only ask which
// installations exist. Both are short-lived, so nothing durable is ever written
// down except the private key itself.
package ghapp

import (
	"net/http"
	"sync"
	"time"
)

// defaultBaseURL is github.com's REST API. Held as a field on App rather than a
// constant so tests can point at an httptest server, and so a GitHub Enterprise
// host could be supported later without touching call sites.
const defaultBaseURL = "https://api.github.com"

// App is an authenticated GitHub App. It turns a private key into short-lived
// installation tokens and caches them, so a review that makes several calls does
// not re-sign and re-exchange for each one.
//
// Safe for concurrent use: two machines-worth of reviews never run in one
// process, but a single review and a PR list refresh easily overlap.
type App struct {
	creds *Credentials

	http *http.Client
	base string
	// now is the clock, injected so token expiry is testable without sleeping.
	now func() time.Time

	mu     sync.Mutex
	tokens map[int64]cachedToken
}

// New returns an App that authenticates with these credentials.
func New(creds *Credentials) *App {
	return &App{
		creds: creds,
		// A bounded client rather than http.DefaultClient: this talks to a third
		// party over the internet, and a review that hangs for ever on a stalled
		// connection is worse than one that fails and says so.
		http:   &http.Client{Timeout: 30 * time.Second},
		base:   defaultBaseURL,
		now:    time.Now,
		tokens: map[int64]cachedToken{},
	}
}

// AppID is the numeric id of the App these credentials belong to, for display.
// The key itself is never exposed, by this or any other method.
func (a *App) AppID() string {
	if a.creds == nil {
		return ""
	}
	return a.creds.AppID
}
