package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/hegade/kunai/internal/claude"
	"github.com/hegade/kunai/internal/imageprep"
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

	data, err := io.ReadAll(io.LimitReader(file, maxUpload))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "that file could not be read")
		return
	}

	mediaType := header.Header.Get("Content-Type")
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}

	// An image is made sendable HERE, or refused here, and both are the point.
	//
	// kunai used to inline anything whose media type began with "image/", and the
	// API accepts four formats. So an iPhone photo (HEIC), an AVIF from a
	// website, a BMP, or simply a photo too large for the 10 MB per-image cap all
	// went up and came back as "an image in the conversation could not be
	// processed and was removed" -- minutes into a turn, with nothing saying
	// which image or why, and the turn already paid for. Refusing at the upload
	// is the difference between a sentence somebody can act on and a picture that
	// silently was not there.
	note := ""
	if kind := imageprep.Sniff(data); kind != "" || strings.HasPrefix(mediaType, "image/") {
		prepared, perr := imageprep.Prepare(data)
		if perr != nil {
			writeErr(w, http.StatusUnsupportedMediaType, perr.Error())
			return
		}
		data, mediaType, note = prepared.Data, prepared.MediaType, prepared.Note
	}

	id := hexID()
	if err := os.WriteFile(filepath.Join(s.uploadsDir, id), data, 0o600); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot store upload")
		return
	}

	out := map[string]any{
		"id":         id,
		"name":       filepath.Base(header.Filename),
		"media_type": mediaType,
	}
	if note != "" {
		// Said rather than done silently: these are not the bytes that were
		// handed over, and somebody sending a screenshot to be read closely is
		// entitled to know it was scaled.
		out["note"] = note
	}
	writeJSON(w, http.StatusCreated, out)
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
		// Named formats, not an "image/" prefix. The prefix is exactly what sent
		// HEIC to an API that accepts four types, and a media type on a record is
		// still only a claim about the bytes: this is the last place to check it
		// before a block is built that the API will refuse.
		if imageprep.Sendable(a.MediaType) && imageprep.Sendable(imageprep.Sniff(data)) {
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

// StageUpload writes bytes into the uploads directory under a fresh id, the same
// place and shape handleUpload produces. Exported on the server so the share gate
// can stage a guest's picture without learning where uploads live or how an id is
// made; see shareupload.go for why the gate is kept that narrow.
func (s *Server) StageUpload(name, mediaType string, data []byte) (session.Attachment, error) {
	if s.uploadsDir == "" {
		return session.Attachment{}, errors.New("no uploads directory")
	}
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	// A guest's picture goes through the same preparation as the owner's, and
	// for the same reason: the gate checks the Content-Type it was handed, which
	// is a claim, and an image the API cannot read fails silently three minutes
	// later inside somebody else's turn.
	if prepared, err := imageprep.Prepare(data); err == nil {
		data, mediaType = prepared.Data, prepared.MediaType
	} else if imageprep.Sniff(data) != "" || strings.HasPrefix(mediaType, "image/") {
		return session.Attachment{}, err
	}
	id := hexID()
	// 0600 like every other file kunai writes into its data dir: these are
	// somebody's screenshots, not world-readable scratch.
	if err := os.WriteFile(filepath.Join(s.uploadsDir, id), data, 0o600); err != nil {
		return session.Attachment{}, err
	}
	return session.Attachment{ID: id, Name: safeName(name), MediaType: mediaType}, nil
}

// BuildContent exposes buildContent to the share gate under the interface it
// asks for. Same function the owner's own prompt uses, so a guest's picture
// reaches the model in exactly the same shape.
func (s *Server) BuildContent(cwd, text string, atts []session.Attachment) any {
	return s.buildContent(cwd, text, atts)
}
