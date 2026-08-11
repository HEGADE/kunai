package server

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/hegade/kunai/internal/session"
)

// errGuestFiles is the one answer to every bad attachment, so a guest cannot
// learn which ids exist by the shape of the refusal.
var errGuestFiles = errors.New("those files are not available on this link")

// A guest sending a picture.
//
// Sharing already lets somebody work in a session, and a screenshot is most of
// what people send when they are describing a problem, so "here, look at this"
// was the obvious next thing and it simply was not possible: the prompt path
// refused attachments outright, on the correct reasoning that no upload route
// existed on this listener and an id arriving from one could only be somebody
// probing the reader that resolves it.
//
// That reasoning is what this file has to replace rather than remove. Three
// rules do it, and each closes a hole the others do not.
//
// IMAGES ONLY. An owner's non-image upload is copied into the session's working
// directory so the agent can read it, which for a guest is writing a file into
// somebody else's repository. An image is never written there: it is inlined to
// the model as base64 and exists only in the conversation. So the safe subset is
// exactly the one that never touches the project, and that is the whole subset
// offered.
//
// ONLY THE PAIRED GUEST. Holding the link is enough to watch; sending takes the
// pairing the owner approved. Uploading is sending, so it takes the same.
//
// AND ONLY IDS THIS GUEST WAS GIVEN. The uploads directory is shared with the
// OWNER's uploads, and an id is the only thing that names a file in it. Without
// this a guest could send any 32-hex id and have whatever it happened to name
// inlined into the conversation and read back to them -- the owner's last
// screenshot, fetched by guessing. So the gate remembers what it issued to whom,
// and the prompt path accepts nothing else.

// maxGuestUpload bounds one file from a guest. Smaller than the owner's own cap:
// this listener answers the public internet, and a picture of a screen is a
// couple of megabytes.
const maxGuestUpload = 8 << 20

// maxGuestFiles bounds how many one guest may stage over the life of a share, so
// a link cannot be used to fill somebody's disk a few megabytes at a time.
const maxGuestFiles = 40

// guestImageTypes is what may be sent. Deliberately the same raster set the file
// route serves rather than "anything image/*", so a guest cannot hand the model
// an SVG (a scriptable document) or a format nothing here can show.
var guestImageTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
	"image/avif": true,
	"image/bmp":  true,
}

// guestUploads is the narrow slice of the server the gate needs to stage a file,
// for the same reason sessionLookup is narrow: it keeps "what can a guest reach"
// answerable by reading this file.
type guestUploads interface {
	// StageUpload writes bytes to the uploads directory and returns the
	// attachment that names them.
	StageUpload(name, mediaType string, data []byte) (session.Attachment, error)
	// BuildContent turns staged attachments into the model-facing content.
	BuildContent(cwd, text string, atts []session.Attachment) any
}

// guestFiles remembers which upload ids were issued to which guest.
type guestFiles struct {
	mu  sync.Mutex
	ids map[string]map[string]bool // token -> id set
}

func (f *guestFiles) add(token, id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ids == nil {
		f.ids = map[string]map[string]bool{}
	}
	set := f.ids[token]
	if set == nil {
		set = map[string]bool{}
		f.ids[token] = set
	}
	if len(set) >= maxGuestFiles {
		return false
	}
	set[id] = true
	return true
}

func (f *guestFiles) has(token, id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ids[token][id]
}

// forget drops a share's ids when the share ends, so the map cannot grow with
// links that no longer exist.
func (f *guestFiles) forget(token string) {
	f.mu.Lock()
	delete(f.ids, token)
	f.mu.Unlock()
}

// handleGuestUpload stages one image sent by a paired guest.
func (g *shareGate) handleGuestUpload(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	sh, err := g.shares.Get(token)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if g.uploads == nil {
		writeErr(w, http.StatusNotImplemented, "this machine cannot take files")
		return
	}
	// Watching is not sending, and uploading is sending.
	if !sh.Tier.CanPrompt() {
		writeErr(w, http.StatusForbidden, "this link is read-only")
		return
	}
	if !sh.Paired(deviceOf(r)) {
		writeErr(w, http.StatusForbidden, "ask the owner to let you in first")
		return
	}

	if err := r.ParseMultipartForm(maxGuestUpload); err != nil {
		writeErr(w, http.StatusBadRequest, "that file could not be read")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "no file was sent")
		return
	}
	defer file.Close()

	mediaType := strings.ToLower(strings.TrimSpace(header.Header.Get("Content-Type")))
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = strings.TrimSpace(mediaType[:i])
	}
	if !guestImageTypes[mediaType] {
		writeErr(w, http.StatusUnsupportedMediaType, "only images can be sent through a shared link")
		return
	}
	// LimitReader+1 so an oversize file is REFUSED rather than silently truncated
	// to the cap, which is what the owner's own upload path does and is the wrong
	// behaviour to copy: a picture cut in half is worse than one that did not send.
	data, err := io.ReadAll(io.LimitReader(file, maxGuestUpload+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "that file could not be read")
		return
	}
	if len(data) > maxGuestUpload {
		writeErr(w, http.StatusRequestEntityTooLarge, "that image is too large to send")
		return
	}
	if len(data) == 0 {
		writeErr(w, http.StatusBadRequest, "that file is empty")
		return
	}

	att, err := g.uploads.StageUpload(header.Filename, mediaType, data)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "that file could not be stored")
		return
	}
	if !g.files.add(token, att.ID) {
		writeErr(w, http.StatusTooManyRequests, "too many files have been sent through this link")
		return
	}
	writeJSON(w, http.StatusCreated, att)
}

// guestAttachments resolves the attachments on a guest's prompt, refusing any id
// this gate did not issue to this link. Returns the metadata to show and the
// content to send.
func (g *shareGate) guestAttachments(token, cwd, text string, atts []session.Attachment) ([]session.Attachment, any, error) {
	if len(atts) == 0 {
		return nil, nil, nil
	}
	if g.uploads == nil {
		return nil, nil, errGuestFiles
	}
	for _, a := range atts {
		if !g.files.has(token, a.ID) {
			// Not "no such file": an id this link was never given is somebody
			// reaching for a file that is not theirs, and the answer is the same
			// whether or not it exists.
			return nil, nil, errGuestFiles
		}
	}
	return atts, g.uploads.BuildContent(cwd, text, atts), nil
}
