package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// A real PTY subprocess, with no network or credentials. Both login and status
// record their account environment so a wrong-directory login cannot pass.
func reauthTestCLI(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude-test")
	script := `#!/bin/sh
case "$2" in
login)
  printf '%s' "${CLAUDE_CONFIG_DIR-unset}" > "$KUNAI_REAUTH_CAPTURE/login-dir"
  printf '%s' "$KUNAI_REAUTH_MARKER" > "$KUNAI_REAUTH_CAPTURE/login-marker"
  printf '%s\n' 'https://claude.ai/oauth/authorize?client_id=test&state=test-state'
  IFS= read -r code
  if [ "$code" = 'fail#test-state' ]; then exit 1; fi
  touch "$KUNAI_REAUTH_CAPTURE/refreshed"
  ;;
status)
  printf '%s' "${CLAUDE_CONFIG_DIR-unset}" > "$KUNAI_REAUTH_CAPTURE/status-dir"
  printf '%s' "$KUNAI_REAUTH_MARKER" > "$KUNAI_REAUTH_CAPTURE/status-marker"
  if [ -f "$KUNAI_REAUTH_CAPTURE/refreshed" ] || [ "$KUNAI_REAUTH_OLD_VALID" = 1 ]; then
    printf '%s\n' '{"loggedIn":true}'
  else
    printf '%s\n' '{"loggedIn":false}'
  fi
  ;;
*) exit 2 ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUNAI_REAUTH_CAPTURE", dir)
	t.Setenv("KUNAI_REAUTH_MARKER", "inherited")
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "wrong-parent-account"))
	old := loginURLSettle
	loginURLSettle = 20 * time.Millisecond
	t.Cleanup(func() { loginURLSettle = old })
	return bin, dir
}

func startReauthRequest(t *testing.T, s *Server, body string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleAccountLoginStart(rec, httptest.NewRequest("POST", "/api/accounts/login/start", strings.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	var response map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || response["login_id"] == "" {
		t.Fatalf("invalid start response: %s", rec.Body.String())
	}
	id := response["login_id"]
	t.Cleanup(func() { s.login.cancel(id) })
	return id
}

func TestReauthPreservesExistingAccount(t *testing.T) {
	for _, kind := range []string{"default", "dir", "env"} {
		t.Run(kind, func(t *testing.T) {
			bin, capture := reauthTestCLI(t)
			data := t.TempDir()
			target := CLIProfile{Name: "Work", Bin: bin, Env: map[string]string{"KUNAI_REAUTH_MARKER": "profile-value"}}
			wantDir := filepath.Join(data, "original-account")
			switch kind {
			case "default":
				target.Name, wantDir = "Claude", "unset"
			case "dir":
				target.Dir = wantDir
			case "env":
				target.Env["CLAUDE_CONFIG_DIR"] = wantDir
			}
			s := &Server{cfg: Config{DataDir: data}, clis: []CLIProfile{{Name: "Claude", Bin: bin}, target}}
			if kind == "default" {
				s.clis = []CLIProfile{target, {Name: "Other", Bin: bin, Dir: filepath.Join(data, "other-account")}}
			}
			s.saveCLIs(s.cliList())
			before := s.cliList()
			beforeFile, _ := os.ReadFile(filepath.Join(data, "clis.json"))
			// Wrongly using the add-account manager's binary must fail the test.
			registered := 0
			s.login = newLoginManager("missing-default-cli", data, func(CLIProfile) { registered++ })
			if authOKCached(target) {
				t.Fatal("expected expired login")
			}
			transcript := filepath.Join(data, "saved-transcript")
			if target.configDir() != "" {
				transcript = filepath.Join(target.configDir(), "projects", "project", "saved-transcript.jsonl")
				if err := os.MkdirAll(filepath.Dir(transcript), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			os.WriteFile(transcript, []byte("conversation stays here"), 0o600)
			body, _ := json.Marshal(map[string]any{"name": " " + strings.ToLower(target.Name) + " ", "reauth": true})
			id := startReauthRequest(t, s, string(body))
			finish := httptest.NewRecorder()
			s.handleAccountLoginFinish(finish, httptest.NewRequest("POST", "/api/accounts/login/finish", strings.NewReader(`{"login_id":"`+id+`","code":"valid#test-state"}`)))
			if finish.Code != 200 {
				t.Fatalf("finish: %d %s", finish.Code, finish.Body.String())
			}
			if registered != 0 || !reflect.DeepEqual(before, s.cliList()) {
				t.Fatalf("re-login changed the account list: %+v", s.cliList())
			}
			afterFile, _ := os.ReadFile(filepath.Join(data, "clis.json"))
			if string(beforeFile) != string(afterFile) {
				t.Fatal("re-login rewrote account configuration")
			}
			for _, action := range []string{"login", "status"} {
				got, _ := os.ReadFile(filepath.Join(capture, action+"-dir"))
				marker, _ := os.ReadFile(filepath.Join(capture, action+"-marker"))
				if string(got) != wantDir || string(marker) != "profile-value" {
					t.Fatalf("%s used wrong profile: dir=%q marker=%q", action, got, marker)
				}
			}
			if !authOKCached(target) {
				t.Fatal("signed-out status was not invalidated after re-login")
			}
			if _, err := os.Stat(filepath.Join(data, "accounts")); !os.IsNotExist(err) {
				t.Fatal("re-login created a new account directory")
			}
			if content, _ := os.ReadFile(transcript); string(content) != "conversation stays here" {
				t.Fatal("re-login damaged the transcript")
			}
		})
	}
}

func TestReauthFailureOrCancelDoesNotAcceptOldCredentials(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "cancel"}[cancel], func(t *testing.T) {
			bin, _ := reauthTestCLI(t)
			t.Setenv("KUNAI_REAUTH_OLD_VALID", "1")
			registered := 0
			m := newLoginManager(bin, t.TempDir(), func(CLIProfile) { registered++ })
			id, _, _, err := m.startProfile(t.Context(), CLIProfile{Name: "Work", Bin: bin, Dir: t.TempDir()}, true)
			if err != nil {
				t.Fatal(err)
			}
			m.mu.Lock()
			f := m.flows[id]
			m.mu.Unlock()
			defer m.cancel(id)
			if cancel {
				m.cancel(id)
			} else {
				_, _ = f.tty.Write([]byte("fail#test-state\n"))
			}
			if !f.awaitDone(3 * time.Second) {
				t.Fatal("failed login did not finish")
			}
			if _, err := f.outcome(); err == nil || registered != 0 {
				t.Fatal("old credentials incorrectly made a failed login successful")
			}
		})
	}
}

func TestReauthAutoCompletionDoesNotReAddRemovedAccount(t *testing.T) {
	bin, _ := reauthTestCLI(t)
	s := &Server{clis: []CLIProfile{{Name: "Claude", Bin: bin}, {Name: "Work", Bin: bin, Dir: t.TempDir()}}}
	s.login = newLoginManager(bin, t.TempDir(), func(p CLIProfile) { s.saveCLIs(append(s.cliList(), p)) })
	id := startReauthRequest(t, s, `{"name":"Work","reauth":true}`)
	s.login.mu.Lock()
	f := s.login.flows[id]
	s.login.mu.Unlock()
	s.saveCLIs(s.cliList()[:1])                      // user removed the account while OAuth was open
	_, _ = f.tty.Write([]byte("valid#test-state\n")) // browser completed; no finish request
	if !f.awaitDone(3 * time.Second) {
		t.Fatal("login did not complete")
	}
	done, p, err := s.login.poll(id)
	if !done || err != nil || p.Name != "Work" || len(s.cliList()) != 1 {
		t.Fatalf("auto-completion recreated removed account: done=%v profile=%+v error=%v", done, p, err)
	}
}

func TestReauthRequiresAnExistingAccount(t *testing.T) {
	s := &Server{clis: defaultCLIs(), login: newLoginManager("must-not-run", t.TempDir(), nil), providers: &providerStore{list: []Provider{{Name: "Codex"}}}}
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"name":"Claude"}`, 409},
		{`{"name":"unknown","reauth":true}`, 404},
		{`{"name":"Codex","reauth":true}`, 404},
	} {
		rec := httptest.NewRecorder()
		s.handleAccountLoginStart(rec, httptest.NewRequest("POST", "/api/accounts/login/start", strings.NewReader(tc.body)))
		if rec.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.body, rec.Code, rec.Body.String())
		}
	}
}
