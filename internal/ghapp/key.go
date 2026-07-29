package ghapp

// Loading the App's private key.
//
// This key is the whole credential: anyone holding it can act as kunai[bot] on
// every repository the App is installed on. So it is read from disk with the same
// care kunai gives an account login and, unlike an account login, it is never
// echoed anywhere. No method returns it, no error message quotes it, and nothing
// logs it. The failure messages below are deliberately about the SHAPE of what
// was found, never its contents.
//
// Each machine gets its own key for the same App rather than a copy of one
// shared key, because a GitHub App can hold several private keys and revoke them
// individually. A lost laptop then costs one key instead of forcing a
// redistribution to everyone.

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Credentials are an App's identity: its numeric id and its RSA private key.
type Credentials struct {
	// AppID is the App's numeric id as GitHub shows it. Kept as a string because
	// it is only ever used as a JWT claim and never arithmetic.
	AppID string

	key *rsa.PrivateKey
}

// ErrNoCredentials reports that this machine has not been given an App key yet,
// which is an ordinary state rather than a failure: kunai runs perfectly well
// without one, it just cannot review a pull request.
var ErrNoCredentials = errors.New("no GitHub App credentials on this machine")

// LoadCredentials parses an App id and a PEM private key.
//
// Both PKCS#1 ("RSA PRIVATE KEY") and PKCS#8 ("PRIVATE KEY") are accepted.
// GitHub hands out PKCS#1 today, but a key that has been round-tripped through
// openssl or a secrets manager often comes back as PKCS#8, and refusing it would
// be an inscrutable failure for someone who did nothing wrong.
func LoadCredentials(appID string, pemBytes []byte) (*Credentials, error) {
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return nil, fmt.Errorf("%w: the App id is missing", ErrNoCredentials)
	}
	if len(pemBytes) == 0 {
		return nil, fmt.Errorf("%w: the private key is missing", ErrNoCredentials)
	}

	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("the GitHub App key is not PEM: expected a file beginning -----BEGIN RSA PRIVATE KEY-----")
	}
	key, err := parsePrivateKey(block)
	if err != nil {
		return nil, err
	}
	return &Credentials{AppID: appID, key: key}, nil
}

// LoadCredentialsFile is LoadCredentials over a key on disk. A missing file is
// ErrNoCredentials rather than an error, so callers can treat "not set up" and
// "set up" the same way without stat-ing first.
func LoadCredentialsFile(appID, path string) (*Credentials, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: no key at %s", ErrNoCredentials, path)
	}
	if err != nil {
		// The path is safe to quote; the contents are not, and are not read yet.
		return nil, fmt.Errorf("reading the GitHub App key at %s: %w", path, err)
	}
	return LoadCredentials(appID, b)
}

// parsePrivateKey accepts either PEM encoding of an RSA key. A PKCS#8 block
// holding some other algorithm is rejected by name, because "invalid key" would
// send someone hunting through a file that is perfectly valid and simply the
// wrong kind (GitHub Apps sign with RSA).
func parsePrivateKey(block *pem.Block) (*rsa.PrivateKey, error) {
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("the GitHub App key could not be parsed as an RSA private key (PEM block %q)", block.Type)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("the GitHub App key is a %T, but GitHub Apps sign with RSA", parsed)
	}
	return key, nil
}
