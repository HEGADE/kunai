package server

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The review API: what a session's agent changed on disk, so you can read the
// diff and browse the files from a phone. It shells `git` in the session's cwd
// (the first git usage in the tree) through gitRun, which is injectable exactly
// like usageRun so a test asserts the arguments instead of spawning git. Nothing
// here writes: it is diff/status/read only, so a review can never mutate the
// working tree it is reporting on.

const (
	gitTimeout      = 8 * time.Second
	maxChangedFiles = 800                                        // cap the tree so a huge sweep can't balloon a response
	maxUntrackedLC  = 4 << 20                                    // stop line-counting an untracked blob past this many bytes
	emptyTree       = "4b825dc642cb6eb9a060e54bf8d69288fbee4904" // git's canonical empty tree, the base when there is no HEAD yet
)

// gitRun shells git in dir and returns its stdout (populated even when git exits
// non-zero, e.g. `diff --no-index` signalling "files differ" with code 1).
// Injectable so a test asserts the command without a real git.
var gitRun = func(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	return cmd.Output()
}

// gitDiffOut runs a diff-style command and treats exit code 1 (git's "there were
// differences") as success, so `--no-index` and `--exit-code` don't read as errors.
func gitDiffOut(ctx context.Context, dir string, args ...string) ([]byte, error) {
	out, err := gitRun(ctx, dir, args...)
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
		return out, nil
	}
	return out, err
}

func isGitRepo(ctx context.Context, dir string) bool {
	out, err := gitRun(ctx, dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// diffBase is HEAD when the repo has a commit, else the empty tree — so a repo
// with no commits yet reports its whole working set as additions instead of erroring.
func diffBase(ctx context.Context, dir string) string {
	if _, err := gitRun(ctx, dir, "rev-parse", "--verify", "-q", "HEAD"); err == nil {
		return "HEAD"
	}
	return emptyTree
}

// sessionBase is the commit that was HEAD when the session started, so the review
// shows everything the session changed — its commits AND its uncommitted work —
// not just what is still uncommitted. Diffing the working tree against this base
// means committing the work does not empty the panel: "what did this session do"
// stays answerable after a commit. It is derived from the session's start time
// (git rev-list -1 --before), so it needs no capture at create and works for
// every already-running session. Falls back to HEAD when the session start is
// unknown or predates the first commit (so it never explodes to a whole-repo diff).
func sessionBase(ctx context.Context, dir string, since time.Time) string {
	base := diffBase(ctx, dir)
	if since.IsZero() || base == emptyTree {
		return base
	}
	out, err := gitRun(ctx, dir, "rev-list", "-1", "--before=@"+strconv.FormatInt(since.Unix(), 10), "HEAD")
	if sha := strings.TrimSpace(string(out)); err == nil && sha != "" {
		return sha
	}
	return base
}

// ChangedFile is one row of the changed-files tree.
type ChangedFile struct {
	Path    string `json:"path"`
	Status  string `json:"status"` // added | modified | deleted | renamed
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Binary  bool   `json:"binary,omitempty"`
}

// ChangesResp is the summary the tree is built from: every path the working tree
// differs from its last commit by, plus the fleet totals.
type ChangesResp struct {
	Repo      bool          `json:"repo"`
	Files     []ChangedFile `json:"files"`
	Added     int           `json:"added"`
	Removed   int           `json:"removed"`
	Truncated bool          `json:"truncated,omitempty"`
}

var statusWord = map[byte]string{
	'M': "modified", 'A': "added", 'D': "deleted",
	'R': "renamed", 'C': "added", 'T': "modified",
}

// changes lists what the working tree differs from base by: tracked changes (with
// line counts from numstat, statuses from name-status) plus untracked files as
// additions. Base is the session-start commit, so this is the session's whole
// footprint (committed and uncommitted), not just the uncommitted remainder.
// Three cheap git reads and a bounded read of each untracked file, so it stays
// fast on the common handful-of-files case.
func changes(ctx context.Context, dir, base string) (*ChangesResp, error) {

	// added \t removed \t path  (binary shows "-\t-\tpath")
	counts := map[string][2]int{}
	binary := map[string]bool{}
	numOut, err := gitRun(ctx, dir, "-c", "core.quotepath=false", "diff", "--numstat", base, "--")
	if err != nil {
		return nil, err
	}
	for _, ln := range splitLines(numOut) {
		f := strings.SplitN(ln, "\t", 3)
		if len(f) != 3 {
			continue
		}
		if f[0] == "-" {
			binary[f[2]] = true
			continue
		}
		a, _ := strconv.Atoi(f[0])
		r, _ := strconv.Atoi(f[1])
		counts[f[2]] = [2]int{a, r}
	}

	files := make([]ChangedFile, 0, len(counts))
	nsOut, err := gitRun(ctx, dir, "-c", "core.quotepath=false", "diff", "--name-status", base, "--")
	if err != nil {
		return nil, err
	}
	for _, ln := range splitLines(nsOut) {
		f := strings.SplitN(ln, "\t", 2)
		if len(f) != 2 || f[0] == "" {
			continue
		}
		path := f[1]
		c := counts[path]
		files = append(files, ChangedFile{
			Path: path, Status: statusWordFor(f[0][0]),
			Added: c[0], Removed: c[1], Binary: binary[path],
		})
	}

	// Untracked files: git doesn't diff them, so list and count them ourselves.
	utOut, err := gitRun(ctx, dir, "-c", "core.quotepath=false", "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	for _, path := range splitLines(utOut) {
		added, bin := countAdded(filepath.Join(dir, path))
		files = append(files, ChangedFile{Path: path, Status: "added", Added: added, Binary: bin})
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	resp := &ChangesResp{Repo: true}
	if len(files) > maxChangedFiles {
		files = files[:maxChangedFiles]
		resp.Truncated = true
	}
	resp.Files = files
	for _, f := range files {
		resp.Added += f.Added
		resp.Removed += f.Removed
	}
	return resp, nil
}

func statusWordFor(c byte) string {
	if w, ok := statusWord[c]; ok {
		return w
	}
	return "modified"
}

// countAdded returns the line count of an untracked file (its additions) and
// whether it's binary, reading through a small fixed buffer so a large file
// never costs more than that buffer in memory.
func countAdded(path string) (int, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	buf := make([]byte, 32*1024)
	lines, total := 0, 0
	var last byte
	for {
		n, err := f.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if bytes.IndexByte(chunk, 0) >= 0 {
				return 0, true // NUL byte: treat as binary, no line count
			}
			lines += bytes.Count(chunk, []byte{'\n'})
			last = chunk[n-1]
			total += n
			if total >= maxUntrackedLC {
				break
			}
		}
		if err != nil {
			break
		}
	}
	if total > 0 && last != '\n' {
		lines++ // a final line without a trailing newline still counts
	}
	return lines, false
}

// fileDiff returns the structured diff for one path, or for every changed
// tracked file when path is empty. Untracked files aren't in `git diff`, so they
// diff against /dev/null instead.
func fileDiff(ctx context.Context, dir, path, base string) ([]FileDiff, error) {
	args := []string{"-c", "core.quotepath=false", "diff", base, "--"}
	if path != "" {
		if !isTracked(ctx, dir, path) {
			out, err := gitDiffOut(ctx, dir, "-c", "core.quotepath=false", "diff", "--no-index", "--", os.DevNull, path)
			if err != nil {
				return nil, err
			}
			return parseUnifiedDiff(out), nil
		}
		args = append(args, path)
	}
	out, err := gitDiffOut(ctx, dir, args...)
	if err != nil {
		return nil, err
	}
	return parseUnifiedDiff(out), nil
}

func isTracked(ctx context.Context, dir, path string) bool {
	_, err := gitRun(ctx, dir, "ls-files", "--error-unmatch", "--", path)
	return err == nil
}

func splitLines(b []byte) []string {
	s := strings.TrimRight(string(b), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func (s *Server) handleChanges(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.mgr.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), gitTimeout)
	defer cancel()
	if !isGitRepo(ctx, sess.Cwd) {
		writeJSON(w, http.StatusOK, ChangesResp{Repo: false})
		return
	}
	resp, err := changes(ctx, sess.Cwd, sessionBase(ctx, sess.Cwd, s.sessionStart(sess.ID, sess.CreatedAt)))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleDiff(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.mgr.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), gitTimeout)
	defer cancel()
	if !isGitRepo(ctx, sess.Cwd) {
		writeJSON(w, http.StatusOK, map[string]any{"repo": false})
		return
	}
	files, err := fileDiff(ctx, sess.Cwd, r.URL.Query().Get("path"), sessionBase(ctx, sess.Cwd, s.sessionStart(sess.ID, sess.CreatedAt)))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repo": true, "files": files})
}
