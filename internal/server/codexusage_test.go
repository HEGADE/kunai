package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The token and account id are read from either the nested "tokens" form or the
// older flat form, and account id has three spellings.
func TestCodexCredsShapes(t *testing.T) {
	cases := []struct{ raw, tok, acc string }{
		{`{"tokens":{"access_token":"t1","account_id":"a1"}}`, "t1", "a1"},
		{`{"access_token":"t2","chatgpt_account_id":"a2"}`, "t2", "a2"},
		{`{"access_token":"t3","account_id":"a3"}`, "t3", "a3"},
		{`{"tokens":{"access_token":"t4"},"account_id":"a4"}`, "t4", "a4"},
	}
	for _, c := range cases {
		var a codexAuthFile
		if err := json.Unmarshal([]byte(c.raw), &a); err != nil {
			t.Fatalf("unmarshal %s: %v", c.raw, err)
		}
		if tok, acc := a.creds(); tok != c.tok || acc != c.acc {
			t.Errorf("%s -> (%q,%q), want (%q,%q)", c.raw, tok, acc, c.tok, c.acc)
		}
	}
}

// Codex usage is fetched only for a ChatGPT/Codex model, never for Grok/Kimi
// (which would otherwise show the codex account's numbers from the fallback).
func TestIsCodexModel(t *testing.T) {
	for _, m := range []string{"gpt-5.5", "gpt-5.6-terra", "codex-1", "o3-mini", "chatgpt-4o"} {
		if !isCodexModel(m) {
			t.Errorf("%q should be codex", m)
		}
	}
	for _, m := range []string{"grok-4.5", "kimi-k3", "moonshot-v1", "gemini-2.5-pro"} {
		if isCodexModel(m) {
			t.Errorf("%q should NOT be codex", m)
		}
	}
}

// The two windows map onto session (primary) and weekly (secondary), and the auth
// headers are sent.
func TestFetchCodexUsageMaps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("missing bearer: %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("ChatGPT-Account-Id") != "acc" {
			t.Errorf("missing account: %q", r.Header.Get("ChatGPT-Account-Id"))
		}
		// primary is a short (5h) window -> session; secondary a long (7d) -> weekly.
		_, _ = io.WriteString(w, `{"rate_limit":{"primary_window":{"used_percent":42.5,"reset_at":1000,"limit_window_seconds":18000},"secondary_window":{"used_percent":9,"reset_at":2000,"limit_window_seconds":604800}}}`)
	}))
	defer srv.Close()
	old := codexUsageURL
	codexUsageURL = srv.URL
	defer func() { codexUsageURL = old }()

	u, err := fetchCodexUsage(context.Background(), "tok", "acc")
	if err != nil {
		t.Fatal(err)
	}
	if u.Session == nil || u.Session.Percent != 42.5 || u.Session.ResetsAt != 1000 {
		t.Errorf("session window = %+v", u.Session)
	}
	if u.Weekly == nil || u.Weekly.Percent != 9 || u.Weekly.ResetsAt != 2000 {
		t.Errorf("weekly window = %+v", u.Weekly)
	}
}

// A ChatGPT Go plan reports a single ~30-day window; it lands in the weekly row
// (a long window), not session, so the reset time the client shows is honest.
func TestFetchCodexUsageGoPlanLongWindow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"rate_limit":{"primary_window":{"used_percent":17,"reset_at":9000,"limit_window_seconds":2592000},"secondary_window":null}}`)
	}))
	defer srv.Close()
	old := codexUsageURL
	codexUsageURL = srv.URL
	defer func() { codexUsageURL = old }()
	u, err := fetchCodexUsage(context.Background(), "tok", "acc")
	if err != nil {
		t.Fatal(err)
	}
	if u.Session != nil {
		t.Errorf("30-day window should not be session, got %+v", u.Session)
	}
	if u.Weekly == nil || u.Weekly.Percent != 17 {
		t.Errorf("weekly window = %+v", u.Weekly)
	}
}

// A non-200 (e.g. an expired token) is an error, not a bogus empty meter.
func TestFetchCodexUsageRejectsNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	old := codexUsageURL
	codexUsageURL = srv.URL
	defer func() { codexUsageURL = old }()
	if _, err := fetchCodexUsage(context.Background(), "tok", "acc"); err == nil {
		t.Error("expected error on 401")
	}
}
