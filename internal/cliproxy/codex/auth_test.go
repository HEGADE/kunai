package codex

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

// fakeJWT builds a token whose payload carries the given exp claim, enough for the
// expiry/account parsing to read without a real signature.
func fakeJWT(t *testing.T, payload string) string {
	t.Helper()
	seg := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	return seg(`{"alg":"none"}`) + "." + seg(payload) + "." + "sig"
}

func TestReadToken_NestedCodexCLIFormat(t *testing.T) {
	// The real ~/.codex/auth.json nests the token under "tokens" with last_refresh,
	// unlike the flat sidecar-login shape. Both must load. exp far in the future so
	// creds() returns without attempting a network refresh.
	access := fakeJWT(t, `{"exp":9999999999,"https://api.openai.com/auth":{"chatgpt_account_id":"acc-123"}}`)
	body := `{"auth_mode":"chatgpt","tokens":{"access_token":"` + access +
		`","refresh_token":"refresh-xyz","id_token":"` + access + `","account_id":"acc-123"},"last_refresh":"2026-07-23T00:00:00Z"}`
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m := newTokenManager(path, false)
	got, account, err := m.creds(context.Background())
	if err != nil {
		t.Fatalf("creds on nested token: %v", err)
	}
	if got != access {
		t.Errorf("access token = %q, want the nested one", got)
	}
	if account != "acc-123" {
		t.Errorf("account = %q, want acc-123", account)
	}
}

func TestReadToken_FlatSidecarFormat(t *testing.T) {
	access := fakeJWT(t, `{"exp":9999999999}`)
	body := `{"access_token":"` + access + `","refresh_token":"r","account_id":"acc-flat","expired":"2099-01-01T00:00:00Z"}`
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m := newTokenManager(path, false)
	got, account, err := m.creds(context.Background())
	if err != nil {
		t.Fatalf("creds on flat token: %v", err)
	}
	if got != access || account != "acc-flat" {
		t.Errorf("flat token = %q/%q, want access/acc-flat", got, account)
	}
}
