package server

// Where this machine's GitHub App credentials live.
//
// Two files in the data dir, deliberately not one: the id is configuration and
// the key is a secret, and keeping them apart means the key file can be handled
// with the care it needs (0600, never read into an API response) without dragging
// the id along.
//
// Each machine holds its OWN key for the same App. A GitHub App can carry several
// private keys and revoke them one at a time, so a colleague's laptop being lost
// costs one key rather than forcing a redistribution to everybody.

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/hegade/kunai/internal/ghapp"
)

const (
	githubAppIDFile = "github-app-id"
	githubKeyFile   = "github-app.pem"
)

func githubAppIDPath(dataDir string) string { return filepath.Join(dataDir, githubAppIDFile) }
func githubKeyPath(dataDir string) string   { return filepath.Join(dataDir, githubKeyFile) }

// loadGitHubCredentials reads the App this machine acts as. Returns
// ghapp.ErrNoCredentials when none is configured, which is an ordinary state:
// kunai works fine without one, it simply cannot review a pull request.
func loadGitHubCredentials(dataDir string) (*ghapp.Credentials, error) {
	if dataDir == "" {
		return nil, ghapp.ErrNoCredentials
	}
	id, err := os.ReadFile(githubAppIDPath(dataDir))
	if err != nil {
		return nil, ghapp.ErrNoCredentials
	}
	return ghapp.LoadCredentialsFile(strings.TrimSpace(string(id)), githubKeyPath(dataDir))
}

// saveGitHubCredentials writes the App id and key, after checking they are
// usable together. Validating first means a paste with a stray character fails at
// the moment somebody can fix it, rather than the first time they click Review.
func saveGitHubCredentials(dataDir, appID, keyPEM string) error {
	if _, err := ghapp.LoadCredentials(appID, []byte(keyPEM)); err != nil {
		return err
	}
	if err := os.WriteFile(githubAppIDPath(dataDir), []byte(strings.TrimSpace(appID)+"\n"), 0o600); err != nil {
		return err
	}
	// 0600 and never anything else. This key grants pull-request write on every
	// repository the App is installed on.
	return os.WriteFile(githubKeyPath(dataDir), []byte(keyPEM), 0o600)
}

// clearGitHubCredentials removes them, for a machine that should stop being able
// to post as the bot.
func clearGitHubCredentials(dataDir string) {
	_ = os.Remove(githubAppIDPath(dataDir))
	_ = os.Remove(githubKeyPath(dataDir))
}

// githubConfigured reports whether this machine can act as the App, without
// loading or exposing the key.
func githubConfigured(dataDir string) bool {
	if dataDir == "" {
		return false
	}
	if _, err := os.Stat(githubKeyPath(dataDir)); err != nil {
		return false
	}
	_, err := os.Stat(githubAppIDPath(dataDir))
	return err == nil
}
