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

// guestFiles remembers what was issued to which guest.
//
// The whole ATTACHMENT, not just its id, and that is the difference between the
// images-only rule holding and merely appearing to. What decides whether bytes
// are inlined to the model or WRITTEN INTO THE PROJECT is the media type, and
// the media type buildContent reads is the one on the prompt frame -- which the
// guest writes. So checking the upload's Content-Type at upload time and then
// trusting the frame later left the rule enforced on a field nobody consults:
// upload anything as image/png, then attach it as text/plain, and the else
// branch copies it into the owner's repository under a name the guest chose and
// tells the agent where it is. The record is written here, from what the gate
// verified, and the frame is never read for anything but the id.
type guestFiles struct {
	mu sync.Mutex
	// token -> id -> what this gate actually staged.
	ids map[string]map[string]session.Attachment
	// token -> files written but not yet recorded. A slot is taken BEFORE the
	// bytes are, so the cap cannot be exceeded by a file that is already on disk
	// (see reserve).
	pending map[string]int
}

// reserve takes a slot for a file that has not been written yet, and reports
// whether there was one.
//
// Before the write, deliberately. The cap used to be spent after StageUpload
// returned, so a refused upload had already put its bytes in the uploads
// directory with no id recorded anywhere: nothing referenced it and nothing
// swept it, and a paired guest could go on posting 8MB files past the cap for
// ever. The cap exists to stop exactly that, so it has to be the thing that
// decides whether the write happens.
func (f *guestFiles) reserve(token string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pending == nil {
		f.pending = map[string]int{}
	}
	if len(f.ids[token])+f.pending[token] >= maxGuestFiles {
		return false
	}
	f.pending[token]++
	return true
}

// release gives a reserved slot back when the file was never written.
func (f *guestFiles) release(token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pending[token] > 0 {
		f.pending[token]--
	}
}

// commit turns a reserved slot into a staged file.
func (f *guestFiles) commit(token string, att session.Attachment) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pending[token] > 0 {
		f.pending[token]--
	}
	if f.ids == nil {
		f.ids = map[string]map[string]session.Attachment{}
	}
	set := f.ids[token]
	if set == nil {
		set = map[string]session.Attachment{}
		f.ids[token] = set
	}
	set[att.ID] = att
}

// issued returns what this gate staged under an id for this link, if anything.
func (f *guestFiles) issued(token, id string) (session.Attachment, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	att, ok := f.ids[token][id]
	return att, ok
}

// forget drops a share's ids when the share ends, so the map cannot grow with
// links that no longer exist.
func (f *guestFiles) forget(token string) {
	f.mu.Lock()
	delete(f.ids, token)
	delete(f.pending, token)
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

	// The slot before the bytes. Spending the cap afterwards left a refused
	// upload's file on disk with its id recorded nowhere, so nothing referenced
	// it and nothing swept it: the cap said no and the disk filled anyway.
	if !g.files.reserve(token) {
		writeErr(w, http.StatusTooManyRequests, "too many files have been sent through this link")
		return
	}
	att, err := g.uploads.StageUpload(header.Filename, mediaType, data)
	if err != nil {
		g.files.release(token)
		writeErr(w, http.StatusInternalServerError, "that file could not be stored")
		return
	}
	// Recorded as the GATE saw it, not as the frame will later describe it: the
	// media type here is the one this handler checked against guestImageTypes.
	att.MediaType = mediaType
	g.files.commit(token, att)
	writeJSON(w, http.StatusCreated, att)
}

// guestAttachments resolves the attachments on a guest's prompt, refusing any id
// this gate did not issue to this link. Returns the metadata to show and the
// content to send.
//
// The frame is read for its IDS AND NOTHING ELSE. Everything that decides what
// happens to the bytes is taken from what the gate staged, because the guest
// writes the frame: `buildContent` branches on the media type, and anything that
// is not an image is copied into the owner's working directory with the agent
// pointed at it. Passing the frame through meant the images-only rule was
// enforced against a field that no longer had any say -- upload as image/png,
// attach as text/plain, and a file of the guest's choosing lands in the
// repository under a name of their choosing.
//
// The type is then checked AGAIN against the same table, which is not
// redundant: it means the rule is enforced on the value buildContent actually
// reads, so any future path that stages a file has to satisfy it too rather
// than only the one handler above.
func (g *shareGate) guestAttachments(token, cwd, text string, atts []session.Attachment) ([]session.Attachment, any, error) {
	if len(atts) == 0 {
		return nil, nil, nil
	}
	if g.uploads == nil {
		return nil, nil, errGuestFiles
	}
	out := make([]session.Attachment, 0, len(atts))
	for _, a := range atts {
		issued, ok := g.files.issued(token, a.ID)
		if !ok {
			// Not "no such file": an id this link was never given is somebody
			// reaching for a file that is not theirs, and the answer is the same
			// whether or not it exists.
			return nil, nil, errGuestFiles
		}
		if !guestImageTypes[issued.MediaType] {
			return nil, nil, errGuestFiles
		}
		out = append(out, issued)
	}
	return out, g.uploads.BuildContent(cwd, text, out), nil
}
