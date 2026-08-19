package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A key that belongs to a different App used to save cleanly, report
// "Configured" in green, and fail days later as an unexplained error on the
// dashboard. GitHub refusing the pair has to be refused here, while the two
// fields somebody just pasted are still on screen.
func TestARejectedKeyIsRefusedRatherThanSaved(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"A JSON web token could not be decoded"}`))
	}))
	defer srv.Close()

	_, err := verifyGitHubAppWith(context.Background(), testGitHubApp(t, srv.URL))
	if err == nil {
		t.Fatal("a key GitHub refused was accepted")
	}
	if !strings.Contains(err.Error(), "id matches the App") {
		t.Errorf("the error does not say what to check: %v", err)
	}
}

// The step everybody misses: registering an App and installing it are separate
// actions on GitHub, and only the first one feels like setup. An App installed
// nowhere is not a bad credential, so it saves, but it cannot review anything
// and the screen has to say so with somewhere to go.
func TestAnAppInstalledNowhereWarnsWithALink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app":
			writeTestJSON(w, map[string]any{"name": "kunai", "slug": "kunai-bot"})
		case "/app/installations":
			writeTestJSON(w, []any{})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	check, err := verifyGitHubAppWith(context.Background(), testGitHubApp(t, srv.URL))
	if err != nil {
		t.Fatalf("an uninstalled App was refused rather than warned: %v", err)
	}
	if !strings.Contains(check.Warning, "not installed") {
		t.Errorf("warning = %q, want it to say the App is installed nowhere", check.Warning)
	}
	if check.InstallURL != "https://github.com/apps/kunai-bot/installations/new" {
		t.Errorf("install url = %q, want a link to install it", check.InstallURL)
	}
}

// The happy path names where it is installed, because "Configured" alone is
// exactly the message that was already being shown when nothing worked.
func TestAWorkingAppNamesWhereItIsInstalled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app":
			writeTestJSON(w, map[string]any{"name": "kunai", "slug": "kunai-bot"})
		case "/app/installations":
			writeTestJSON(w, []any{
				map[string]any{"id": 1, "account": map[string]any{"login": "hegade"}, "repository_selection": "all"},
				map[string]any{"id": 2, "account": map[string]any{"login": "lyzr"}, "repository_selection": "selected"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	check, err := verifyGitHubAppWith(context.Background(), testGitHubApp(t, srv.URL))
	if err != nil {
		t.Fatalf("a working App was refused: %v", err)
	}
	if check.Warning != "" {
		t.Errorf("a working App produced a warning: %q", check.Warning)
	}
	if len(check.Orgs) != 2 || check.Orgs[0] != "hegade" {
		t.Errorf("orgs = %v, want both accounts named", check.Orgs)
	}
	// "selected" is the setting behind the confusing failure: everything reports
	// configured and one repository still refuses because it was never ticked.
	if !check.Partial {
		t.Error("a selected-repositories installation was not flagged as partial")
	}
}

// GitHub being unreachable is not the user's fault and must not look like a bad
// key. The credentials are kept and the uncertainty is reported, because
// refusing here would send somebody to re-paste a key that was always correct.
func TestAnUnreachableGitHubKeepsTheCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // nothing is listening, so the request fails at the transport

	check, err := verifyGitHubAppWith(context.Background(), testGitHubApp(t, srv.URL))
	if err != nil {
		t.Fatalf("an unreachable GitHub was treated as a bad key: %v", err)
	}
	if !strings.Contains(check.Warning, "could not be reached") {
		t.Errorf("warning = %q, want it to say GitHub could not be reached", check.Warning)
	}
}
