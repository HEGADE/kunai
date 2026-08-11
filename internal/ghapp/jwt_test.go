package ghapp

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// testKey is a small RSA key: real signing, fast enough for a unit test. 2048 is
// what GitHub issues, but nothing here depends on the size and generating one per
// test package would dominate the run time.
func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func testCreds(t *testing.T) *Credentials {
	t.Helper()
	return &Credentials{AppID: "123456", key: testKey(t)}
}

// The JWT has to be a real RS256 token: three base64url segments, verifiable
// against the public key. If this drifts, every call fails with an opaque 401
// from GitHub and nothing local says why.
func TestSignJWTProducesAVerifiableToken(t *testing.T) {
	creds := testCreds(t)
	now := time.Unix(1_700_000_000, 0)

	token, err := creds.signJWT(now)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d segments, want 3", len(parts))
	}

	// The signature must verify over "header.claims" with the matching public key.
	signed := parts[0] + "." + parts[1]
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("signature is not raw base64url: %v", err)
	}
	digest := sha256.Sum256([]byte(signed))
	if err := rsa.VerifyPKCS1v15(&creds.key.PublicKey, crypto.SHA256, digest[:], sig); err != nil {
		t.Fatalf("signature does not verify: %v", err)
	}

	var header map[string]string
	decodeSegment(t, parts[0], &header)
	if header["alg"] != "RS256" || header["typ"] != "JWT" {
		t.Fatalf("header = %v, want RS256/JWT", header)
	}
}

// The two timing rules, which are the ones that actually break in the field.
// GitHub judges both against its own clock, so a machine that is slightly fast
// must still produce a token GitHub accepts.
func TestJWTTimingLeavesRoomForClockSkew(t *testing.T) {
	creds := testCreds(t)
	now := time.Unix(1_700_000_000, 0)

	token, err := creds.signJWT(now)
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
		Iss string `json:"iss"`
	}
	decodeSegment(t, strings.Split(token, ".")[1], &claims)

	if claims.Iss != "123456" {
		t.Errorf("iss = %q, want the App id", claims.Iss)
	}
	if claims.Iat >= now.Unix() {
		t.Errorf("iat = %d is not backdated against now = %d; a fast clock would look like the future", claims.Iat, now.Unix())
	}
	if life := time.Duration(claims.Exp-claims.Iat) * time.Second; life > 10*time.Minute {
		t.Errorf("token claims %v of life; GitHub rejects anything over 10 minutes", life)
	}
	if claims.Exp <= now.Unix() {
		t.Error("token is already expired when issued")
	}
}

// Signing without a key is an ordinary "not set up yet" rather than a crash, so
// a machine with no App configured can be asked and answer.
func TestSignJWTWithoutCredentials(t *testing.T) {
	var creds *Credentials
	if _, err := creds.signJWT(time.Now()); err == nil {
		t.Fatal("signing with no credentials reported success")
	}
	if _, err := (&Credentials{AppID: "1"}).signJWT(time.Now()); err == nil {
		t.Fatal("signing with no key reported success")
	}
}

func decodeSegment(t *testing.T, seg string, out any) {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		t.Fatalf("segment is not raw base64url: %v", err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("segment is not JSON: %v", err)
	}
}
