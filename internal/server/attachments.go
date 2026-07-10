package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/hegade/kunai/internal/claude"
	"github.com/hegade/kunai/internal/session"
)

const maxUpload = 20 << 20 // 20 MiB per file

// handleUpload accepts a single multipart file (field "file"), stages it under
// the uploads dir, and returns a handle the client attaches to a prompt.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid upload")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "missing file")
		return
	}
	defer file.Close()

	id := hexID()
	dst, err := os.Create(filepath.Join(s.uploadsDir, id))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot store upload")
		return
	}
	defer dst.Close()
	if _, err := io.Copy(dst, io.LimitReader(file, maxUpload)); err != nil {
		writeErr(w, http.StatusInternalServerError, "write failed")
		return
	}

	mediaType := header.Header.Get("Content-Type")
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"id":         id,
		"name":       filepath.Base(header.Filename),
		"media_type": mediaType,
	})
}

// buildContent turns a prompt + attachments into the value sent to Claude. With
// no attachments it returns the plain string; with attachments it returns an
// API-style content-block array (images inline as base64; other files are copied
// into the session cwd and referenced by path so Claude can Read them).
func (s *Server) buildContent(cwd, text string, atts []session.Attachment) any {
	if len(atts) == 0 {
		return text
	}
	blocks := []claude.ContentBlock{}
	extraText := text

	for _, a := range atts {
		data, err := os.ReadFile(filepath.Join(s.uploadsDir, a.ID))
		if err != nil {
			continue
		}
		if strings.HasPrefix(a.MediaType, "image/") {
			blocks = append(blocks, claude.ContentBlock{
				Type: "image",
				Source: &claude.ImageSource{
					Type:      "base64",
					MediaType: a.MediaType,
					Data:      base64.StdEncoding.EncodeToString(data),
				},
			})
			continue
		}
		// Non-image: drop it into the project so Claude can open it with Read.
		dir := filepath.Join(cwd, ".kunai-uploads")
		if os.MkdirAll(dir, 0o755) == nil {
			dest := filepath.Join(dir, safeName(a.Name, a.ID))
			if os.WriteFile(dest, data, 0o644) == nil {
				extraText += "\n\n[Attached file: " + dest + "]"
			}
		}
	}

	if len(blocks) == 0 {
		return extraText // only non-image files; a plain string with paths
	}
	content := []claude.ContentBlock{{Type: "text", Text: extraText}}
	return append(content, blocks...)
}

// safeName keeps the basename but strips path separators; falls back to the id.
func safeName(name, id string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return id
	}
	return name
}

func hexID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
