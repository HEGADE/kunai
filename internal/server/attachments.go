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
		if !stagedID(a.ID) {
			continue
		}
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
			dest := filepath.Join(dir, safeName(a.Name))
			if os.WriteFile(dest, data, 0o644) == nil {
				extraText += "\n\n[Attached file: " + dest + "]"
			}
		}
	}

	if len(blocks) == 0 {
		// Only non-image files (referenced by path), or nothing readable at all. An
		// empty string would stall the turn just like an empty text block does, so
		// say something rather than nothing.
		if strings.TrimSpace(extraText) == "" {
			return attachOnlyPrompt
		}
		return extraText
	}
	// An EMPTY text block is rejected by the API, and the turn then hangs on
	// "Working…" forever -- which is what sending a screenshot with no message did.
	// So the text block is included only when there is actually text; an
	// image-only message is just the image blocks, which is valid.
	content := make([]claude.ContentBlock, 0, len(blocks)+1)
	if strings.TrimSpace(extraText) != "" {
		content = append(content, claude.ContentBlock{Type: "text", Text: extraText})
	}
	return append(content, blocks...)
}

// attachOnlyPrompt stands in when a message carries attachments but no words and
// nothing could be inlined: the model still needs a sentence to act on.
const attachOnlyPrompt = "Take a look at the attached file(s)."

// stagedID reports whether an attachment id is one this server actually minted,
// by shape: exactly what hexID produces and nothing else.
//
// It has to be checked, because the id arrives verbatim on the websocket and is
// then joined onto the uploads directory. filepath.Join CLEANS the result, so an
// id of "../../../../home/you/.ssh/id_rsa" resolves clean out of the uploads dir
// and buildContent reads whatever is there. Worse than a read: a non-image media
// type sends it down the branch that copies the bytes into <cwd>/.kunai-uploads
// and appends the destination to the prompt, so the file lands INSIDE the folder
// the agent is confined to and the model is told where to find it. No tool
// restriction can prevent that, because kunai does the read itself, before the
// model runs.
//
// A bare 32-char lowercase hex string cannot contain a separator, a dot, or a
// leading dash, so this closes the shape rather than blacklisting sequences.
// history.go:596 guards the identical shape for transcript ids.
func stagedID(id string) bool {
	if len(id) != 2*idBytes {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// safeName keeps the basename but strips path separators. The fallback used to be
// the attachment's id, which is caller-supplied: that made the WRITE destination
// escape by the same trick as the read, out of a directory at a different depth
// from the uploads dir. It takes no id at all now, so it cannot come back.
func safeName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "attachment"
	}
	return name
}

// idBytes is the entropy behind an attachment id; the hex form is twice this.
const idBytes = 16

func hexID() string {
	b := make([]byte, idBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
