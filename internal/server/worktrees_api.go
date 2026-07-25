package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"strings"

	"github.com/hegade/kunai/internal/session"
	"github.com/hegade/kunai/internal/worktree"
)

// The worktree API. Creating one is its own endpoint rather than a field on
// session create, because setup can take minutes and because "prepare a worktree
// now, start a session in it later" then costs nothing extra.

// worktreeView is a record plus what only the server knows: who is working in it
// and how it stands against its base.
type worktreeView struct {
	worktreeRecord
	Sessions []string         `json:"sessions,omitempty"`
	Status   *worktree.Status `json:"status,omitempty"`
}

func (s *Server) handleWorktrees(w http.ResponseWriter, r *http.Request) {
	if s.worktrees == nil {
		writeJSON(w, http.StatusOK, []worktreeView{})
		return
	}
	records := s.worktrees.all()
	out := make([]worktreeView, 0, len(records))
	for _, rec := range records {
		view := worktreeView{worktreeRecord: rec, Sessions: s.worktrees.sessionsIn(rec.Path)}
		if st, err := worktree.StatusOf(rec.Info); err == nil {
			view.Status = &st
		}
		out = append(out, view)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateWorktree(w http.ResponseWriter, r *http.Request) {
	if s.worktrees == nil {
		writeErr(w, http.StatusServiceUnavailable, "worktrees need a data directory")
		return
	}
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if strings.TrimSpace(req.Repo) == "" {
		writeErr(w, http.StatusBadRequest, "repo required")
		return
	}
	rec, err := s.worktrees.create(req)
	if err != nil {
		writeErr(w, worktreeErrStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, worktreeView{worktreeRecord: rec})
}

// handleWorktreeSetup answers "what would be run in a new worktree of this
// repository, and where did that come from". The client shows it before creating
// anything: the command is arbitrary shell run with the server's privileges, so
// it is never inferred and executed without a person having seen it.
func (s *Server) handleWorktreeSetup(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	if repo == "" {
		writeErr(w, http.StatusBadRequest, "repo required")
		return
	}
	root, err := worktree.Root(repo)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "not a git repository")
		return
	}
	if s.worktrees == nil {
		writeJSON(w, http.StatusOK, worktree.ProposeSetup(root))
		return
	}
	writeJSON(w, http.StatusOK, s.worktrees.setupFor(root))
}

// handleWorktreeBranches lists the branches a new worktree could start from.
func (s *Server) handleWorktreeBranches(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	if repo == "" {
		writeErr(w, http.StatusBadRequest, "repo required")
		return
	}
	root, err := worktree.Root(repo)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "not a git repository")
		return
	}
	refs, err := worktree.Branches(root)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	fromOrigin := true
	if s.worktrees != nil {
		fromOrigin = s.worktrees.getSettings().FromOrigin
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"refs":        refs,
		"default":     worktree.DefaultBranch(root),
		"from_origin": fromOrigin,
		"repo":        root,
	})
}

func (s *Server) handleMergeWorktree(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.worktreeFromBody(w, r)
	if !ok {
		return
	}
	res, err := worktree.Merge(rec.Info)
	if err != nil {
		writeErr(w, worktreeErrStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handlePullRequestWorktree pushes the branch and opens a pull request with gh.
// gh is used rather than a GitHub API call because it already holds the user's
// credentials; kunai never sees them.
func (s *Server) handlePullRequestWorktree(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.worktreeFromBody(w, r)
	if !ok {
		return
	}
	if _, err := exec.LookPath("gh"); err != nil {
		writeErr(w, http.StatusPreconditionFailed, "gh is not installed on this machine")
		return
	}
	if out, err := runIn(rec.Path, "git", "push", "-u", "origin", rec.Branch); err != nil {
		writeErr(w, http.StatusBadRequest, "push failed: "+firstLine(strings.TrimSpace(out)))
		return
	}
	out, err := runIn(rec.Path, "gh", "pr", "create",
		"--base", worktree.BaseBranchName(rec.Base), "--head", rec.Branch, "--fill")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "gh pr create failed: "+firstLine(strings.TrimSpace(out)))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": firstURL(out)})
}

// handleDeleteWorktree removes a worktree and, unless asked otherwise, its
// branch. It refuses while any session is working in it: a worktree may hold more
// than one, so the question is "is anyone here", not "did its session end".
func (s *Server) handleDeleteWorktree(w http.ResponseWriter, r *http.Request) {
	if s.worktrees == nil {
		writeErr(w, http.StatusServiceUnavailable, "worktrees need a data directory")
		return
	}
	path := r.URL.Query().Get("path")
	rec, ok := s.worktrees.get(path)
	if !ok {
		writeErr(w, http.StatusNotFound, "no such worktree")
		return
	}
	if live := s.worktrees.sessionsIn(path); len(live) > 0 {
		writeErr(w, http.StatusConflict, "close the session working in this worktree first")
		return
	}
	force := r.URL.Query().Get("force") == "1"
	keepBranch := r.URL.Query().Get("keep_branch") == "1"

	if err := worktree.Remove(rec.Info, force, !keepBranch); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.worktrees.forget(path)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleWorktreeSettings reads and writes the machine-wide preferences.
func (s *Server) handleWorktreeSettings(w http.ResponseWriter, r *http.Request) {
	if s.worktrees == nil {
		writeJSON(w, http.StatusOK, defaultWorktreeSettings())
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, s.worktrees.getSettings())
		return
	}
	var v worktreeSettings
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	s.worktrees.setSettings(v)
	writeJSON(w, http.StatusOK, s.worktrees.getSettings())
}

// applyWorktree points a session at a worktree: its path becomes the cwd, and the
// brief describing it becomes an appended system prompt.
//
// It waits for the worktree's setup to finish first. A session that started
// against a half-installed tree would have the agent debugging missing
// dependencies as though they were bugs in the code, which is a worse failure
// than a slow create. This is the same shape as ensureCLIProxyReady, and for the
// same reason: some things have to exist before the CLI is spawned, not after.
func (s *Server) applyWorktree(ctx context.Context, opts *session.CreateOptions, path string) error {
	if s.worktrees == nil {
		return errors.New("worktrees need a data directory")
	}
	if _, ok := s.worktrees.get(path); !ok {
		return errors.New("no such worktree")
	}
	s.worktrees.waitReady(ctx, path)

	// Re-read after the wait: the setup outcome and the shared paths it left
	// behind are exactly what the brief has to report, and neither was known
	// when the worktree was created.
	rec, ok := s.worktrees.get(path)
	if !ok {
		return errors.New("no such worktree")
	}
	opts.Cwd = rec.Path
	opts.AppendSystemPrompt = rec.brief()
	return nil
}

// --- helpers ------------------------------------------------------------------

// worktreeFromBody reads {"path": ...} and looks the worktree up, writing the
// error response itself so the handlers above stay to the point.
func (s *Server) worktreeFromBody(w http.ResponseWriter, r *http.Request) (worktreeRecord, bool) {
	if s.worktrees == nil {
		writeErr(w, http.StatusServiceUnavailable, "worktrees need a data directory")
		return worktreeRecord{}, false
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return worktreeRecord{}, false
	}
	rec, ok := s.worktrees.get(body.Path)
	if !ok {
		writeErr(w, http.StatusNotFound, "no such worktree")
		return worktreeRecord{}, false
	}
	return rec, true
}

// worktreeErrStatus maps the errors a caller acts on differently to status codes,
// so the client can tell "you need to do something first" from "that failed".
func worktreeErrStatus(err error) int {
	switch {
	case errors.Is(err, worktree.ErrNotGit):
		return http.StatusBadRequest
	case errors.Is(err, worktree.ErrDirtyRepo), errors.Is(err, worktree.ErrNotOnBase):
		return http.StatusConflict
	case errors.Is(err, worktree.ErrConflict):
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}

func runIn(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// firstURL picks the pull request URL out of gh's output, which prints it on its
// own line among other chatter.
func firstURL(s string) string {
	for _, f := range strings.Fields(s) {
		if strings.HasPrefix(f, "https://") {
			return f
		}
	}
	return strings.TrimSpace(s)
}
