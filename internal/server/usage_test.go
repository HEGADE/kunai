package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A real /api/oauth/usage body, trimmed. The endpoint reports far more than we
// show (per-model windows, overage, spend), so the parse must ignore the rest
// and must not care that most windows are null.
const usageBody = `{
  "five_hour": {"utilization": 7.0, "resets_at": "2026-07-17T17:00:00.327623+00:00"},
  "seven_day": {"utilization": 17.0, "resets_at": "2026-07-18T09:00:00.327644+00:00"},
  "seven_day_opus": null,
  "extra_usage": {"is_enabled": false},
  "limits": [{"kind": "session", "percent": 7}],
  "member_dashboard_available": false
}`

func TestUsageParse(t *testing.T) {
	var ur usageResponse
	if err := json.Unmarshal([]byte(usageBody), &ur); err != nil {
		t.Fatalf("decode: %v", err)
	}
	s := ur.FiveHour.window()
	if s == nil || s.Percent != 7 {
		t.Fatalf("five_hour = %+v, want 7%%", s)
	}
	if want := time.Date(2026, 7, 17, 17, 0, 0, 0, time.UTC).Unix(); s.ResetsAt != want {
		t.Errorf("five_hour resets_at = %d, want %d", s.ResetsAt, want)
	}
	if w := ur.SevenDay.window(); w == nil || w.Percent != 17 {
		t.Fatalf("seven_day = %+v, want 17%%", w)
	}
	// A window the account does not have stays nil: an unknown limit and an
	// empty one are different claims, and the UI shows nil as absent.
	var absent *usageEntry
	if absent.window() != nil {
		t.Error("a null window must decode to nil, not an empty meter")
	}
}

func writeCreds(t *testing.T, dir string, c map[string]any) {
	t.Helper()
	b, _ := json.MarshalIndent(map[string]any{"claudeAiOauth": c}, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readCredsRaw(t *testing.T, dir string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, ".credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	var whole map[string]any
	if err := json.Unmarshal(b, &whole); err != nil {
		t.Fatal(err)
	}
	return whole["claudeAiOauth"].(map[string]any)
}

// A live token is used as-is: kunai must never refresh a token the CLI is
// happily using, because refreshing is what can rotate it out from under the CLI.
func TestTokenUsesLiveTokenWithoutRefreshing(t *testing.T) {
	dir := t.TempDir()
	writeCreds(t, dir, map[string]any{
		"accessToken":  "live",
		"refreshToken": "r1",
		"expiresAt":    time.Now().Add(time.Hour).UnixMilli(),
	})
	u := newUsageCache()
	u.http = &http.Client{Transport: noHTTP{t}} // any HTTP call fails the test

	c, err := u.token(context.Background(), dir)
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if c.AccessToken != "live" {
		t.Fatalf("access token = %q, want the on-disk one", c.AccessToken)
	}
}

// An expired token is refreshed, the new one persisted, and every field we do
// not model preserved: this file is the account's login, so a round trip must
// not drop scopes/tier or the user is silently logged out.
func TestTokenRefreshesAndPreservesUnknownFields(t *testing.T) {
	dir := t.TempDir()
	writeCreds(t, dir, map[string]any{
		"accessToken":           "stale",
		"refreshToken":          "r1",
		"expiresAt":             time.Now().Add(-time.Hour).UnixMilli(),
		"refreshTokenExpiresAt": float64(1234),
		"scopes":                []any{"user:inference"},
		"subscriptionType":      "max",
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["grant_type"] != "refresh_token" || body["refresh_token"] != "r1" {
			t.Errorf("refresh body = %+v", body)
		}
		if body["client_id"] != oauthClientID {
			t.Errorf("client_id = %q", body["client_id"])
		}
		json.NewEncoder(w).Encode(tokenResponse{AccessToken: "fresh", RefreshToken: "r2", ExpiresIn: 3600})
	}))
	defer srv.Close()
	defer swapURL(&tokenURL, srv.URL)()

	c, err := refreshToken(context.Background(), srv.Client(), "r1")
	if err != nil {
		t.Fatalf("refreshToken: %v", err)
	}
	if c.AccessToken != "fresh" || c.RefreshToken != "r2" {
		t.Fatalf("refreshed = %+v", c)
	}
	if !time.UnixMilli(c.ExpiresAt).After(time.Now().Add(50 * time.Minute)) {
		t.Errorf("expiresAt = %v, want ~1h out", time.UnixMilli(c.ExpiresAt))
	}

	// Persist it the way token() does, then check nothing else was lost.
	_, whole, err := readCreds(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAccessToken(dir, whole, c); err != nil {
		t.Fatalf("writeAccessToken: %v", err)
	}
	got := readCredsRaw(t, dir)
	if got["accessToken"] != "fresh" || got["refreshToken"] != "r2" {
		t.Fatalf("tokens not persisted: %+v", got)
	}
	if got["refreshTokenExpiresAt"] != float64(1234) || got["subscriptionType"] != "max" {
		t.Errorf("unmodelled fields dropped on write: %+v", got)
	}
	if _, ok := got["scopes"]; !ok {
		t.Error("scopes dropped on write")
	}
	// The login must stay private.
	fi, err := os.Stat(filepath.Join(dir, ".credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("credentials mode = %v, want 0600", fi.Mode().Perm())
	}
}

// A refresh that fails must not wipe or corrupt the credentials, and must not
// take the machine down with it: no usage is a quiet tile, not a broken account.
func TestRefreshFailureLeavesCredentialsIntact(t *testing.T) {
	dir := t.TempDir()
	writeCreds(t, dir, map[string]any{
		"accessToken":  "stale",
		"refreshToken": "r1",
		"expiresAt":    time.Now().Add(-time.Hour).UnixMilli(),
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer srv.Close()
	defer swapURL(&tokenURL, srv.URL)()

	if _, err := refreshToken(context.Background(), srv.Client(), "r1"); err == nil {
		t.Fatal("want an error from a 401 refresh")
	}
	got := readCredsRaw(t, dir)
	if got["accessToken"] != "stale" || got["refreshToken"] != "r1" {
		t.Fatalf("failed refresh mutated credentials: %+v", got)
	}
}

// A logged-out account is a normal state, not a server error.
func TestTokenOnLoggedOutAccount(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	u := newUsageCache()
	if _, err := u.token(context.Background(), dir); err == nil {
		t.Fatal("want an error for an account with no claudeAiOauth")
	}
}

func TestExpiredSkew(t *testing.T) {
	if (oauthCreds{ExpiresAt: time.Now().Add(time.Hour).UnixMilli()}).expired() {
		t.Error("an hour of life is not expired")
	}
	if !(oauthCreds{ExpiresAt: time.Now().Add(30 * time.Second).UnixMilli()}).expired() {
		t.Error("inside the skew must count as expired, so a fetch never races the wall")
	}
	if (oauthCreds{}).expired() {
		t.Error("no recorded expiry: let the API judge, do not force a refresh")
	}
}

// swapURL points a real endpoint at a test server and returns the undo, so a
// test can never reach the live account endpoints.
func swapURL(target *string, url string) func() {
	old := *target
	*target = url
	return func() { *target = old }
}

// noHTTP makes any outbound call a test failure, so a test can assert that a
// code path stayed entirely local.
type noHTTP struct{ t *testing.T }

func (n noHTTP) RoundTrip(r *http.Request) (*http.Response, error) {
	n.t.Errorf("unexpected HTTP call to %s", r.URL)
	return nil, errors.New("no HTTP expected")
}
