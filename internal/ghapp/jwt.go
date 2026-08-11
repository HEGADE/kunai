package ghapp

// Signing the App JWT.
//
// Written out rather than pulled from a JWT library, and that is a deliberate
// trade. kunai ships as one binary and adds dependencies reluctantly; the whole
// of what is needed here is one algorithm (RS256), three claims, and no parsing
// at all, because we only ever sign. A library would bring a verifier, a claims
// framework and an algorithm registry to do forty lines of work.
//
// The two timing details below are the ones that actually break in the field.

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	// jwtLifetime is how long the App JWT claims to be valid. GitHub rejects
	// anything over ten minutes, and rejects it against ITS clock rather than
	// ours, so the last minute is left unclaimed: a laptop running a little fast
	// would otherwise mint a token that is already too long-lived on arrival.
	jwtLifetime = 9 * time.Minute
	// jwtBackdate pulls `iat` into the past for the same reason from the other
	// side. A machine whose clock is a few seconds ahead issues a token that
	// GitHub reads as created in the future and refuses outright, which presents
	// as an authentication failure that fixes itself and then comes back.
	jwtBackdate = 60 * time.Second
)

// signJWT returns a signed RS256 JWT identifying the App at time now.
func (c *Credentials) signJWT(now time.Time) (string, error) {
	if c == nil || c.key == nil {
		return "", ErrNoCredentials
	}
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	claims := map[string]any{
		"iat": now.Add(-jwtBackdate).Unix(),
		"exp": now.Add(jwtLifetime).Unix(),
		"iss": c.AppID,
	}

	encoded, err := encodeSegments(header, claims)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(encoded))
	sig, err := rsa.SignPKCS1v15(rand.Reader, c.key, crypto.SHA256, digest[:])
	if err != nil {
		// Deliberately not wrapped with anything about the key itself.
		return "", fmt.Errorf("signing the GitHub App token failed: %w", err)
	}
	return encoded + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// encodeSegments builds the signing input: base64url(header).base64url(claims).
// Raw (unpadded) base64url is required by the JWT spec; the padded variant is
// accepted by nothing.
func encodeSegments(parts ...any) (string, error) {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		b, err := json.Marshal(p)
		if err != nil {
			return "", fmt.Errorf("encoding the GitHub App token: %w", err)
		}
		out = append(out, base64.RawURLEncoding.EncodeToString(b))
	}
	return strings.Join(out, "."), nil
}
