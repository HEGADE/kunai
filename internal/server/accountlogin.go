package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
)

// In-app account login. Adding a second Claude account used to mean a terminal
// dance: run `CLAUDE_CONFIG_DIR=... claude auth login`, sign in, then hand-enter
// the name and folder in Settings. This drives that same login from the app.
//
// `claude auth login` is a full-screen TUI: it needs a real terminal (nothing
// prints on a plain pipe) and its subscription flow prints an OAuth URL, then
// waits at "Paste code here" for the code the browser hands back. So the driver
// runs it under a PTY, scrapes the one URL out, streams the one code in, and
// verifies the result with `auth status --json`. Two pieces of I/O, no scraping
// of a redrawing UI beyond the initial URL line.

const (
	loginURLTimeout  = 30 * time.Second // wait for the CLI to print its OAuth URL
	loginDoneTimeout = 90 * time.Second // wait for auth to complete after the code is sent
	loginFlowTTL     = 10 * time.Minute // abandon a flow the user walked away from
)

// oauthURL matches the authorize link the CLI prints. The class excludes control
// bytes and whitespace so it stops at the terminal escapes around the link (the
// CLI wraps it in an OSC-8 hyperlink, printing the URL twice back to back); we
// want exactly one URL, not the pair joined across the escape.
var oauthURL = regexp.MustCompile(`https://[^\x00-\x20'"<>]*oauth[^\x00-\x20'"<>]*`)

// loginFlow is one in-progress login: the CLI process under a PTY, waiting for a
// code, tied to the account name and config dir it is provisioning.
type loginFlow struct {
	id      string
	name    string
	dir     string
	cmd     *exec.Cmd
	tty     *os.File
	started time.Time
}

// loginManager owns in-progress login flows, keyed by a flow id. A flow holds a
// live subprocess, so flows are bounded by a TTL sweep and closed on shutdown.
type loginManager struct {
	mu    sync.Mutex
	flows map[string]*loginFlow
	// bin is the default Claude binary (from the default profile); a new account
	// logs in with the same binary, just a fresh config dir.
	bin      string
	accounts string // <dataDir>/accounts, where new account config dirs live
}

func newLoginManager(bin, dataDir string) *loginManager {
	return &loginManager{
		flows:    map[string]*loginFlow{},
		bin:      bin,
		accounts: filepath.Join(dataDir, "accounts"),
	}
}

// slug turns an account name into a filesystem-safe folder name.
var slugUnsafe = regexp.MustCompile(`[^a-z0-9]+`)

func accountSlug(name string) string {
	s := slugUnsafe.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
	return strings.Trim(s, "-")
}

// start provisions a config dir for `name`, spawns the login TUI under a PTY, and
// returns the flow id plus the OAuth URL the user opens in the browser. The
// process stays alive waiting for the code (see finish).
func (m *loginManager) start(ctx context.Context, name string) (id, url, dir string, err error) {
	slug := accountSlug(name)
	if slug == "" {
		return "", "", "", fmt.Errorf("give the account a name")
	}
	dir = filepath.Join(m.accounts, slug)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", "", fmt.Errorf("could not create the account folder: %w", err)
	}

	cmd := exec.Command(m.bin, "auth", "login", "--claudeai")
	cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+dir)
	tty, err := pty.Start(cmd)
	if err != nil {
		return "", "", "", fmt.Errorf("could not start the login: %w", err)
	}

	url, err = readOAuthURL(tty, loginURLTimeout)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = tty.Close()
		return "", "", "", err
	}

	id, _ = newSessionID() // an opaque handle for this flow; uniqueness is all that matters
	m.mu.Lock()
	m.flows[id] = &loginFlow{id: id, name: name, dir: dir, cmd: cmd, tty: tty, started: time.Now()}
	m.mu.Unlock()
	return id, url, dir, nil
}

// finish streams the pasted code into the waiting login process, waits for it to
// complete, and verifies the result. On success it returns the provisioned
// account's profile; the caller registers it.
func (m *loginManager) finish(id, code string) (CLIProfile, error) {
	m.mu.Lock()
	f := m.flows[id]
	delete(m.flows, id)
	m.mu.Unlock()
	if f == nil {
		return CLIProfile{}, fmt.Errorf("this login expired; start it again")
	}
	defer f.tty.Close()

	if _, err := f.tty.Write([]byte(strings.TrimSpace(code) + "\n")); err != nil {
		_ = f.cmd.Process.Kill()
		return CLIProfile{}, fmt.Errorf("could not submit the code: %w", err)
	}
	// Drain the PTY so the CLI isn't blocked writing, and let it exit.
	done := make(chan error, 1)
	go func() {
		_, _ = drain(f.tty)
		done <- f.cmd.Wait()
	}()
	select {
	case <-done:
	case <-time.After(loginDoneTimeout):
		_ = f.cmd.Process.Kill()
		return CLIProfile{}, fmt.Errorf("the login timed out; try again")
	}

	if !authOK(m.bin, f.dir) {
		return CLIProfile{}, fmt.Errorf("that code didn't complete the login; try again")
	}
	return CLIProfile{Name: f.name, Bin: m.bin, Dir: f.dir}, nil
}

// cancel kills an abandoned flow.
func (m *loginManager) cancel(id string) {
	m.mu.Lock()
	f := m.flows[id]
	delete(m.flows, id)
	m.mu.Unlock()
	if f != nil {
		_ = f.cmd.Process.Kill()
		_ = f.tty.Close()
	}
}

// sweep kills flows the user abandoned past the TTL, so a walked-away login never
// leaves a stuck subprocess.
func (m *loginManager) sweep() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, f := range m.flows {
		if time.Since(f.started) > loginFlowTTL {
			_ = f.cmd.Process.Kill()
			_ = f.tty.Close()
			delete(m.flows, id)
		}
	}
}

// readOAuthURL reads the PTY until the CLI prints its authorize URL or the timeout
// hits, whichever comes first.
func readOAuthURL(tty *os.File, timeout time.Duration) (string, error) {
	type res struct {
		url string
		err error
	}
	ch := make(chan res, 1)
	go func() {
		var buf bytes.Buffer
		chunk := make([]byte, 4096)
		for {
			n, err := tty.Read(chunk)
			if n > 0 {
				buf.Write(chunk[:n])
				// Accept a match only once a byte follows it (loc[1] < len), which
				// means the URL terminated at an escape/space and wasn't cut off by
				// a mid-read buffer boundary.
				if loc := oauthURL.FindIndex(buf.Bytes()); loc != nil && loc[1] < buf.Len() {
					ch <- res{url: string(buf.Bytes()[loc[0]:loc[1]])}
					return
				}
			}
			if err != nil {
				ch <- res{err: fmt.Errorf("the login exited before showing a link")}
				return
			}
		}
	}()
	select {
	case r := <-ch:
		return r.url, r.err
	case <-time.After(timeout):
		return "", fmt.Errorf("the login didn't produce a link in time")
	}
}

// drain reads a reader to EOF, discarding the bytes. Used to unblock the PTY.
func drain(r *os.File) (int64, error) {
	var total int64
	b := make([]byte, 4096)
	for {
		n, err := r.Read(b)
		total += int64(n)
		if err != nil {
			return total, err
		}
	}
}

// authOK reports whether the account in dir is signed in, via `auth status --json`.
// An empty dir means the default account (~/.claude): leave CLAUDE_CONFIG_DIR
// unset rather than blanking it, which the CLI would not read as "default".
func authOK(bin, dir string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "auth", "status", "--json")
	cmd.Env = os.Environ()
	if dir != "" {
		cmd.Env = append(cmd.Env, "CLAUDE_CONFIG_DIR="+dir)
	}
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	var st struct {
		LoggedIn      bool   `json:"loggedIn"`
		Authenticated bool   `json:"authenticated"`
		Status        string `json:"status"`
	}
	if json.Unmarshal(out, &st) != nil {
		return false
	}
	return st.LoggedIn || st.Authenticated ||
		strings.EqualFold(st.Status, "authenticated") || strings.EqualFold(st.Status, "logged_in")
}

// --- HTTP ---

func (s *Server) handleAccountLoginStart(w http.ResponseWriter, r *http.Request) {
	if s.login == nil {
		writeErr(w, http.StatusServiceUnavailable, "account login unavailable")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if s.accountNameTaken(req.Name) {
		writeErr(w, http.StatusConflict, "an account with that name already exists")
		return
	}
	id, url, _, err := s.login.start(r.Context(), req.Name)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"login_id": id, "url": url})
}

func (s *Server) handleAccountLoginFinish(w http.ResponseWriter, r *http.Request) {
	if s.login == nil {
		writeErr(w, http.StatusServiceUnavailable, "account login unavailable")
		return
	}
	var req struct {
		LoginID string `json:"login_id"`
		Code    string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Code) == "" {
		writeErr(w, http.StatusBadRequest, "paste the code from the browser")
		return
	}
	profile, err := s.login.finish(req.LoginID, req.Code)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.saveCLIs(append(s.cliList(), profile))
	writeJSON(w, http.StatusOK, map[string]string{"name": profile.Name})
}

func (s *Server) handleAccountLoginCancel(w http.ResponseWriter, r *http.Request) {
	if s.login != nil {
		var req struct {
			LoginID string `json:"login_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.login.cancel(req.LoginID)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) accountNameTaken(name string) bool {
	name = strings.TrimSpace(name)
	for _, c := range s.cliList() {
		if strings.EqualFold(c.Name, name) {
			return true
		}
	}
	return false
}

// AccountInfo is one account for the Accounts screen: its name, whether it is the
// default, and whether it is currently signed in.
type AccountInfo struct {
	Name    string `json:"name"`
	Default bool   `json:"default"`
	Ready   bool   `json:"ready"`
}

// handleAccounts lists the machine's accounts with their signed-in status, for the
// Accounts screen. Each status is a live `auth status` shell (~1s), so they run
// concurrently: the whole list resolves in about one check, not one per account.
func (s *Server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	list := s.cliList()
	out := make([]AccountInfo, len(list))
	var wg sync.WaitGroup
	for i, c := range list {
		out[i] = AccountInfo{Name: c.Name, Default: i == 0}
		wg.Add(1)
		go func(i int, c CLIProfile) {
			defer wg.Done()
			out[i].Ready = authOK(c.Bin, c.configDir()) // distinct index: no shared write
		}(i, c)
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, out)
}

// handleAccountRemove drops an account from the list. The default account can't be
// removed (a machine always needs one runnable Claude). The account's config dir
// is left on disk, so its transcripts and login survive a re-add.
func (s *Server) handleAccountRemove(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	list := s.cliList()
	if len(list) > 0 && strings.EqualFold(list[0].Name, name) {
		writeErr(w, http.StatusBadRequest, "the default account can't be removed")
		return
	}
	kept := make([]CLIProfile, 0, len(list))
	for _, c := range list {
		if !strings.EqualFold(c.Name, name) {
			kept = append(kept, c)
		}
	}
	writeJSON(w, http.StatusOK, s.saveCLIs(kept))
}

// handleSetAccount switches a live session to a different account, keeping its
// conversation. Claude ties a conversation's memory to the account's config dir,
// so the transcript is copied into the target account's projects folder first,
// then the session is respawned under that account with --resume. The resumed
// process authenticates and bills as the new account; its first turn re-reads the
// whole context uncached (the accepted cost of the switch). The id is unchanged.
func (s *Server) handleSetAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, ok := s.mgr.Get(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	target := s.resolveCLI(req.Name)
	if strings.EqualFold(target.Name, sess.Meta().CLI) {
		writeJSON(w, http.StatusOK, sess.Meta()) // already on it
		return
	}
	// Pre-flight: confirm the target account is actually signed in before touching
	// the live session. The restart below closes the running process first, so a
	// switch to a signed-out account would drop the session (the conversation
	// survives on disk, but the live tab would not) and only fail afterwards, since
	// the respawn is async. Checking here refuses cleanly and leaves the current
	// session running untouched. This shells `auth status` once (~1s), the price of
	// a deliberate switch, not a per-turn cost.
	if !authOK(target.Bin, target.configDir()) {
		writeErr(w, http.StatusConflict, "The "+target.Name+" account is signed out. Add it again from Accounts, then switch.")
		return
	}
	// Copy the transcript into the target account's folder so the resumed process
	// loads the full context under the new login. cid is the CLI-assigned id once a
	// turn has happened; before that the transcript (if any) lives under the handle.
	cid := sess.ClaudeSessionID()
	if cid == "" {
		cid = id
	}
	if src := s.transcriptForID(cid); src != "" {
		slug := filepath.Base(filepath.Dir(src))
		dst := filepath.Join(claudeRoot(target.configDir()), slug, cid+".jsonl")
		if err := copyFile(src, dst); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not move the conversation to that account: "+err.Error())
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	restarted, err := s.mgr.RestartWithAccount(ctx, id, target.Name, target.Bin, target.effectiveEnv(), loadTranscriptTurns)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.armSession(restarted)
	writeJSON(w, http.StatusOK, restarted.Meta())
}

// copyFile copies src to dst, creating dst's parent folder. Used to move a
// session's transcript into another account's config dir on an account switch.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// loginSweepLoop kills abandoned login flows on an interval.
func (s *Server) loginSweepLoop(ctx context.Context) {
	if s.login == nil {
		return
	}
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.login.sweep()
		}
	}
}
