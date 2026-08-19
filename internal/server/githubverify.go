package server

// Proving that a GitHub App will actually work, at the moment somebody can fix
// it.
//
// Saving used to check only that the PEM parsed. That check passes for a key
// belonging to a different App, for the right key with the wrong id, and for an
// App installed on nothing at all, so all three sat on disk reporting
// "Configured" in green and failed later as a raw GitHub error string on the
// dashboard. Setting this up is already the most tedious part of the feature;
// having it silently not work is what makes it feel broken rather than fiddly.
//
// Two questions are asked, and they fail differently:
//
//	Whoami        -> do this id and this key belong together?
//	Installations -> is the App installed anywhere it could review?
//
// The first is the credential, the second is the step everybody forgets, because
// registering an App and installing it are separate actions on GitHub and only
// the first one feels like setup.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/hegade/kunai/internal/ghapp"
)

// githubCheck is what verification found, and it is deliberately not a boolean.
// "The key is wrong", "the App is installed nowhere" and "GitHub did not answer"
// need three different sentences and lead to three different actions.
type githubCheck struct {
	// Name and InstallURL identify the App to a person. The URL is what turns
	// "install it on your organisation" from an instruction into a link.
	Name       string   `json:"name,omitempty"`
	InstallURL string   `json:"install_url,omitempty"`
	Orgs       []string `json:"orgs,omitempty"`
	// Partial marks an installation that covers only selected repositories,
	// which is the setting behind the confusing failure: everything reports
	// configured and one repository still refuses.
	Partial bool `json:"partial,omitempty"`
	// Warning is set when the credentials look right but something is not
	// finished, most often an App installed nowhere.
	Warning string `json:"warning,omitempty"`
}

// verifyTimeout bounds the two calls. Generous enough for a slow link and short
// enough that saving credentials never feels hung: this runs while somebody
// watches a button.
const verifyTimeout = 15 * time.Second

// verifyGitHubApp asks GitHub whether these credentials are usable.
//
// The returned error means "do not save this": the pair was rejected, and the
// person is looking at the two fields they just pasted. Anything softer comes
// back on the check as a Warning instead, because a warning that blocks saving
// is indistinguishable from a failure and would make an unreachable network
// look like a bad key.
func verifyGitHubApp(ctx context.Context, creds *ghapp.Credentials) (githubCheck, error) {
	return verifyGitHubAppWith(ctx, ghapp.New(creds))
}

// verifyGitHubAppWith is verifyGitHubApp against an App that has already been
// built, which is the seam a test uses to point it at an httptest server. Same
// reason ghapp exports NewWithBaseURL: the three outcomes below each need their
// own sentence, and asserting that means being able to produce all three
// without a real App and a real organisation.
func verifyGitHubAppWith(ctx context.Context, app *ghapp.App) (githubCheck, error) {
	ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()

	id, err := app.Whoami(ctx)
	if err != nil {
		if rejected(err) {
			return githubCheck{}, fmt.Errorf(
				"GitHub refused this App id and private key together. Check that the id matches the App the key was generated on: %w", err)
		}
		// Could not ask. The credentials may be perfect, so they are kept and the
		// uncertainty is reported rather than pretended away.
		return githubCheck{
			Warning: "Saved, but GitHub could not be reached to check it. If reviews fail, come back and press Save again.",
		}, nil
	}

	out := githubCheck{Name: id.Name, InstallURL: id.InstallURL()}

	installs, err := app.Installations(ctx)
	if err != nil {
		out.Warning = "Saved, but kunai could not read where this App is installed."
		return out, nil
	}
	for _, in := range installs {
		if in.Account.Login != "" {
			out.Orgs = append(out.Orgs, in.Account.Login)
		}
		if in.RepositorySelection == "selected" {
			out.Partial = true
		}
	}
	if len(out.Orgs) == 0 {
		// The step everybody misses. Registering an App and installing it are
		// separate actions, and only the first one feels like setup.
		out.Warning = "This App is not installed on any organisation yet, so there is nothing it can review. Install it, then come back."
	}
	return out, nil
}

// rejected reports whether GitHub actively refused the credentials, as opposed
// to not answering. 401 is the credential being wrong; 404 is GitHub declining
// to admit the App exists, which for /app means the same thing from the caller's
// side.
func rejected(err error) bool {
	var apiErr *ghapp.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.Status == http.StatusUnauthorized || apiErr.Status == http.StatusNotFound
}
