package imageprep

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func pngOf(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 90, 255})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func jpegOf(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8((x * y) % 256), 40, 200, 255})
		}
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, nil)
	return b.Bytes()
}

// An image that is already fine goes through untouched. Re-encoding a screenshot
// for no reason costs quality exactly where the text is.
func TestASendableImagePassesThrough(t *testing.T) {
	in := pngOf(800, 600)
	got, err := Prepare(in)
	if err != nil {
		t.Fatalf("Prepare() = %v", err)
	}
	if got.MediaType != "image/png" || got.Changed {
		t.Errorf("got %q changed=%v, want an untouched PNG", got.MediaType, got.Changed)
	}
	if !bytes.Equal(got.Data, in) {
		t.Error("the bytes were rewritten when nothing needed doing")
	}
}

// The failure this package exists for. An iPhone photo is HEIC, the API accepts
// four formats and that is not one of them, and kunai sent it anyway: the turn
// came back with "an image in the conversation could not be processed and was
// removed" and nothing said which image or why.
func TestAnUnsendableFormatIsRefusedWithSomethingToDo(t *testing.T) {
	heic := append([]byte{0, 0, 0, 24}, []byte("ftypheic")...)
	heic = append(heic, make([]byte, 32)...)
	_, err := Prepare(heic)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want unsupported", err)
	}
	// The message has to name the format and say what to do about it: this is the
	// only moment anybody can act on it.
	if !strings.Contains(err.Error(), "HEIC") || !strings.Contains(err.Error(), "Most Compatible") {
		t.Errorf("error = %q, want it to name HEIC and the setting that fixes it", err)
	}

	for _, c := range []struct {
		name, want string
		data       []byte
	}{
		{"avif", "AVIF", append(append([]byte{0, 0, 0, 24}, []byte("ftypavif")...), make([]byte, 32)...)},
		{"svg", "SVG", []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"></svg>`)},
		{"bmp", "BMP", append([]byte("BM"), make([]byte, 64)...)},
	} {
		if _, err := Prepare(c.data); !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want a refusal naming %s", c.name, err, c.want)
		}
	}
}

// The declared type is never trusted, because it is a claim: the browser guesses
// it from an extension and a sender can write whatever they like. A HEIC called
// image/png is still a HEIC.
func TestTheBytesDecideNotTheLabel(t *testing.T) {
	heic := append([]byte{0, 0, 0, 24}, []byte("ftypheic")...)
	heic = append(heic, make([]byte, 32)...)
	if got := Sniff(heic); got != "image/heic" {
		t.Errorf("Sniff() = %q, want image/heic whatever it was called", got)
	}
	if got := Sniff(pngOf(4, 4)); got != "image/png" {
		t.Errorf("Sniff() = %q, want image/png", got)
	}
}

// Too many pixels is shrunk rather than refused, and the aspect ratio survives.
// The API downscales above this anyway, so nothing is lost that the model would
// have seen -- and the upload, the tokens and the wait all get smaller.
func TestAnOversizeImageIsShrunkToSomethingSendable(t *testing.T) {
	got, err := Prepare(jpegOf(5000, 2500))
	if err != nil {
		t.Fatalf("Prepare() = %v", err)
	}
	if !got.Changed || got.Note == "" {
		t.Error("a resized image does not report that it was changed")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(got.Data))
	if err != nil {
		t.Fatalf("the result does not decode: %v", err)
	}
	if cfg.Width != maxEdge {
		t.Errorf("width = %d, want the long edge at %d", cfg.Width, maxEdge)
	}
	if cfg.Height != maxEdge/2 {
		t.Errorf("height = %d, want the aspect ratio kept", cfg.Height)
	}
	if len(got.Data) > maxRawBytes {
		t.Errorf("still %d bytes, over the limit", len(got.Data))
	}
	if !Sendable(got.MediaType) {
		t.Errorf("media type %q is not one the API accepts", got.MediaType)
	}
}

// The last gate before an image block is built. "image/" as a prefix is what
// sent HEIC in the first place, so the four are named.
func TestOnlyTheFourFormatsAreSendable(t *testing.T) {
	for _, ok := range []string{"image/jpeg", "image/png", "image/gif", "image/webp"} {
		if !Sendable(ok) {
			t.Errorf("%s should be sendable", ok)
		}
	}
	for _, no := range []string{"image/heic", "image/avif", "image/bmp", "image/tiff", "image/svg+xml", "image/", "text/plain"} {
		if Sendable(no) {
			t.Errorf("%s should not be sendable", no)
		}
	}
}
