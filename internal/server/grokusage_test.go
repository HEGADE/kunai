package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFetchGrokBillingPaid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("missing bearer: %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"config":{"monthlyLimit":{"val":10000},"used":{"val":2500},"billingPeriodEnd":"2026-08-01T00:00:00+00:00"}}`))
	}))
	defer srv.Close()
	old := grokBillingURL
	grokBillingURL = srv.URL
	defer func() { grokBillingURL = old }()

	u, err := fetchGrokBilling(context.Background(), "tok")
	if err != nil {
		t.Fatal(err)
	}
	if u == nil || u.Weekly == nil || u.Weekly.Percent != 25 {
		t.Errorf("billing usage = %+v, want 25%% weekly", u)
	}
}

func TestFetchGrokBillingFreeReturnsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"config":{"monthlyLimit":{"val":0},"used":{"val":0}}}`))
	}))
	defer srv.Close()
	old := grokBillingURL
	grokBillingURL = srv.URL
	defer func() { grokBillingURL = old }()

	u, err := fetchGrokBilling(context.Background(), "tok")
	if err != nil || u != nil {
		t.Errorf("free tier should map to nil billing, got %+v err=%v", u, err)
	}
}

// The wiring, not just the helper: a refused quota request must not be repeated
// by the next caller, and must be logged once rather than once per caller.
//
// This is the shape that filled the journal. grokUsageCache stored only success,
// so a stale login meant every /api/stats hit xAI again and wrote another line.
func TestGrokQuotaStopsAskingAfterARefusal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".grok"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".grok", "auth.json"),
		[]byte(`{"iss::1":{"key":"tok"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	prev := grokBillingURL
	grokBillingURL = srv.URL
	defer func() { grokBillingURL = prev }()

	var c grokUsageCache
	for i := 0; i < 10; i++ {
		if u := c.get(context.Background()); u != nil {
			t.Fatalf("call %d returned a quota although the endpoint refused", i)
		}
	}
	if hits != 1 {
		t.Errorf("asked xAI %d times for a credential it refused on the first try; want 1", hits)
	}
}

// The reason a quota poll failed must reach the client, not just the log.
//
// Both provider caches held a precise sentence, reported it to the journal, and
// returned a bare nil -- so the handler answered with a generic "usage not
// available for this provider" and the dashboard could only print "no quota".
// That reads as "this provider has no quota to show", which is the wrong
// conclusion and the one nobody can act on.
func TestAFailedQuotaPollKeepsItsReasonForTheClient(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".grok"), 0o700); err != nil {
		t.Fatal(err)
	}
	// A key whose expiry has passed and which cannot be refreshed: a dead login.
	body := `{"iss::1":{"key":"tok","expires_at":"2020-01-01T00:00:00Z"}}`
	if err := os.WriteFile(filepath.Join(home, ".grok", "auth.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	var c grokUsageCache
	if u := c.get(context.Background()); u != nil {
		t.Fatal("a dead login reported a quota")
	}
	why := c.reason()
	if why == "" {
		t.Fatal("the failure reason was discarded; the client has nothing to show")
	}
	// It has to name the thing to do, not the thing that happened.
	if !strings.Contains(strings.ToLower(why), "sign in") {
		t.Errorf("reason = %q, want it to say how to fix the login", why)
	}
}

// And a poll that never failed has nothing to say, so a healthy provider does not
// grow a permanent error line.
func TestASuccessfulQuotaPollHasNoReason(t *testing.T) {
	var c grokUsageCache
	if got := c.reason(); got != "" {
		t.Errorf("reason = %q on a fresh cache, want empty", got)
	}
}

// A caller that hangs up is not a provider failure.
//
// The quota fetch runs on the HTTP request's context, so a client that navigates
// away or supersedes its own poll cancels it mid-flight. Recording that parked
// the quota for a whole minute over a request nobody was waiting for, and once
// failures became visible it printed `context canceled` on the dashboard as
// though the account were broken.
func TestACancelledPollIsNotRememberedAsAFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".grok"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".grok", "auth.json"),
		[]byte(`{"iss::1":{"key":"tok"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// A server that never answers, so the cancellation is what ends the request.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	prev := grokBillingURL
	grokBillingURL = srv.URL
	defer func() { grokBillingURL = prev }()

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()

	var c grokUsageCache
	if u := c.get(ctx); u != nil {
		t.Fatal("a cancelled poll returned a quota")
	}
	if why := c.reason(); why != "" {
		t.Errorf("reason = %q, want empty: hanging up is not something to report", why)
	}
	// And it must not be holding, or the next poll is blocked for a minute over a
	// request the client abandoned.
	if c.fail.holding(time.Now()) {
		t.Error("a cancelled poll backed the cache off; the next one must be free to try")
	}
}
