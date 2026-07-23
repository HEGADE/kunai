package grok

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPersistLocked_WritesRotatedTokenBack proves the fix for the revoked-token
// bug: xAI rotates refresh tokens, so a refreshed token must be written back or the
// next process reads the revoked one. persistLocked must update key/refresh_token/
// expires_at under the same "<issuer>::<id>" entry and preserve every other field.
func TestPersistLocked_WritesRotatedTokenBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	orig := `{"https://auth.x.ai::abc":{"key":"old-key","refresh_token":"old-rt","expires_at":"2020-01-01T00:00:00Z","oidc_issuer":"https://auth.x.ai","oidc_client_id":"cid","extra_field":"keep-me"}}`
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	m := newTokenManager(path)
	if err := m.readLocked(); err != nil {
		t.Fatal(err)
	}
	// Simulate a successful refresh rotating the token.
	m.tok.Key = "new-key"
	m.tok.RefreshToken = "new-rt"
	m.exp = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	m.persistLocked()

	b, _ := os.ReadFile(path)
	var raw map[string]map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("auth.json no longer valid: %v", err)
	}
	ent := raw["https://auth.x.ai::abc"]
	if ent["key"] != "new-key" || ent["refresh_token"] != "new-rt" {
		t.Errorf("rotated token not written back: %v", ent)
	}
	if ent["extra_field"] != "keep-me" {
		t.Errorf("write-back clobbered an unknown field: %v", ent["extra_field"])
	}
	if ent["oidc_issuer"] != "https://auth.x.ai" {
		t.Errorf("write-back dropped oidc_issuer")
	}
}

// TestDeadLogin_ReturnsReauthError: an expired token with no way to refresh must
// return the actionable "sign in again" error, not a doomed stale key.
func TestDeadLogin_ReturnsReauthError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	// Expired, and no refresh token: cannot refresh, key is past expiry.
	body := `{"https://auth.x.ai::abc":{"key":"stale","expires_at":"2020-01-01T00:00:00Z"}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m := newTokenManager(path)
	_, err := m.token(nil)
	if err == nil {
		t.Fatal("expected an error for a dead login, got a token")
	}
	if !containsAll(err.Error(), "sign in again") {
		t.Errorf("error should tell the user to sign in again, got: %v", err)
	}
}

func TestRefreshReason_ExtractsDescription(t *testing.T) {
	err := errors.New(`grok token refresh: HTTP 400: {"error":"invalid_grant","error_description":"Refresh token has been revoked"}`)
	if got := refreshReason(err); got != "Refresh token has been revoked" {
		t.Errorf("refreshReason = %q, want the revoked description", got)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
