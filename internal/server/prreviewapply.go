package server

// Applying a suggested change to the code it is about.
//
// See internal/review/apply.go for why this exists at all (the "copied, never
// applied" rule was reasoning about the wrong actor). This half is the file
// handling, and it has three jobs: find the file the finding names inside the
// repository the review was of, refuse anything that resolves outside it, and
// write only where the text still matches what the review read.
//
// It writes to the WORKING CHECKOUT, not the review's throwaway one, and it does
// not commit. The throwaway checkout is deleted, so a change there would be a
// change to nothing; and leaving the edit unstaged is what keeps the person in
// charge of it, since `git diff` is then the whole record of what this button
// did and undoing it is one command they already know.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/hegade/kunai/internal/pathguard"
	"github.com/hegade/kunai/internal/review"
)

func (s *Server) handleApplyReviewFix(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Index int `json:"index"`
		// File is echoed back purely as a check on Index, the same guard the
		// verification pass uses on its verdicts: an index is a fragile way to
		// name a finding, and the failure it protects against is silent and
		// writes to the wrong file.
		File string `json:"file"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "malformed request")
		return
	}
	if s.prReviews == nil {
		writeErr(w, http.StatusNotFound, "this session is not a pull-request review")
		return
	}
	rec, ok := s.prReviews.get(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "this session is not a pull-request review")
		return
	}
	if rec.Draft == nil {
		writeErr(w, http.StatusBadRequest, "this review has not finished yet")
		return
	}
	// The same ordering the client is looking at, which is the plan's: Build
	// walks the normalised draft one for one, so position i means the same
	// finding on both sides.
	findings := rec.Draft.Normalise().Findings
	if req.Index < 0 || req.Index >= len(findings) {
		writeErr(w, http.StatusBadRequest, "no such finding in this review")
		return
	}
	f := findings[req.Index]
	if req.File != "" && req.File != f.File {
		writeErr(w, http.StatusConflict, "this review has changed since the page was loaded; reload it before applying")
		return
	}
	if rec.RepoDir == "" {
		writeErr(w, http.StatusBadRequest, "this review does not record which checkout it read, so it cannot write to it")
		return
	}

	// Confined to the repository, with symlinks resolved BEFORE the containment
	// check, so no spelling of a path the model produced can write outside it.
	path, err := pathguard.Resolve(rec.RepoDir, f.File)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "that file is not inside the repository this review read")
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "that file is not in the checkout any more")
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// A file's own line ending survives: rewriting a CRLF file as LF would show
	// up as every line changed in the diff this is meant to keep readable.
	nl := "\n"
	if strings.Contains(string(b), "\r\n") {
		nl = "\r\n"
	}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	trailing := strings.HasSuffix(text, "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")

	out, applied, err := review.ApplyTo(lines, f)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	body := strings.Join(out, nl)
	if trailing {
		body += nl
	}
	if err := writeFileAtomic(path, []byte(body), info.Mode().Perm()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"file":    f.File,
		"path":    path,
		"line":    applied.Line,
		"removed": applied.Removed,
		"added":   applied.Added,
	})
}

// writeFileAtomic replaces a file's contents without ever leaving a half-written
// one behind, the same temp-and-rename idiom the stores use. Somebody's source
// file is the last place a partial write is acceptable.
func writeFileAtomic(path string, b []byte, mode os.FileMode) error {
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".kunai-tmp")
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
