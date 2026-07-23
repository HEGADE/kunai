package codex

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenPKCE(t *testing.T) {
	v, c, err := genPKCE()
	if err != nil {
		t.Fatal(err)
	}
	if len(v) < 43 {
		t.Errorf("verifier too short: %d", len(v))
	}
	// challenge must be base64url(sha256(verifier)), no padding.
	sum := sha256.Sum256([]byte(v))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if c != want {
		t.Errorf("challenge mismatch")
	}
	if strings.ContainsAny(c, "=+/") {
		t.Errorf("challenge not URL-safe / unpadded: %q", c)
	}
}

func TestCodeFromPasted(t *testing.T) {
	cases := map[string]string{
		"abc123":           "abc123",
		"code=xyz&state=s": "xyz",
		"http://localhost:1455/auth/callback?code=q&state=s": "q",
		"?code=w":    "w",
		"abc#frag":   "abc",
		"  spaced  ": "spaced",
		"":           "",
	}
	for in, want := range cases {
		if got := codeFromPasted(in); got != want {
			t.Errorf("codeFromPasted(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeSlug(t *testing.T) {
	cases := map[string]string{
		"user@example.com": "user-example-com",
		"Acct_123":         "acct_123",
		"!!!":              "account",
		"a b c":            "abc",
	}
	for in, want := range cases {
		if got := sanitizeSlug(in); got != want {
			t.Errorf("sanitizeSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

// The authorize URL carries the right client, PKCE method, and registered redirect.
func TestStartLoginAuthURL(t *testing.T) {
	f, authURL, err := StartLogin(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer f.Cancel()
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("client_id") != oauthClientID {
		t.Errorf("client_id = %q", q.Get("client_id"))
	}
	if q.Get("redirect_uri") != oauthRedirectURI {
		t.Errorf("redirect_uri = %q", q.Get("redirect_uri"))
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q", q.Get("code_challenge_method"))
	}
	if q.Get("code_challenge") == "" || q.Get("state") == "" {
		t.Error("missing challenge/state")
	}
}

// Finish exchanges a pasted code against a mock token endpoint and writes a
// codex-*.json the native proxy would find.
func TestLoginFinishExchangeAndSave(t *testing.T) {
	// A mock OAuth token endpoint.
	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		// id_token with a chatgpt_account_id claim.
		claims := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acct-77"}}`))
		idTok := "h." + claims + ".s"
		_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","id_token":"` + idTok + `","expires_in":3600}`))
	}))
	defer srv.Close()
	old := oauthTokenURL
	oauthTokenURL = srv.URL
	defer func() { oauthTokenURL = old }()

	dir := t.TempDir()
	f, _, err := StartLogin(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Cancel()

	if err := f.Finish(context.Background(), "http://localhost:1455/auth/callback?code=THECODE&state=s"); err != nil {
		t.Fatalf("finish: %v", err)
	}
	// The exchange sent the code + verifier.
	if gotForm.Get("code") != "THECODE" {
		t.Errorf("exchanged code = %q", gotForm.Get("code"))
	}
	if gotForm.Get("code_verifier") == "" {
		t.Error("missing code_verifier in exchange")
	}
	if gotForm.Get("grant_type") != "authorization_code" {
		t.Errorf("grant_type = %q", gotForm.Get("grant_type"))
	}
	// A codex-*.json was written, named by the account id, readable by the proxy.
	matches, _ := filepath.Glob(filepath.Join(dir, "codex-*.json"))
	if len(matches) != 1 {
		t.Fatalf("expected one codex-*.json, got %v", matches)
	}
	if !strings.Contains(matches[0], "acct-77") {
		t.Errorf("token file not named by account: %s", matches[0])
	}
	b, _ := os.ReadFile(matches[0])
	if !strings.Contains(string(b), `"access_token": "at"`) || !strings.Contains(string(b), `"account_id": "acct-77"`) {
		t.Errorf("token file contents wrong: %s", b)
	}
	// Status reflects completion.
	if done, err := f.Status(); !done || err != nil {
		t.Errorf("status = %v, %v", done, err)
	}
}
