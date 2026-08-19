// Package imageprep makes an uploaded image into something the model can
// actually be sent.
//
// It exists because kunai was sending whatever arrived. An attachment whose
// media type began with "image/" was base64'd straight into the message, and the
// API accepts exactly four formats -- JPEG, PNG, GIF and WebP -- so an iPhone
// photo (HEIC), a screenshot saved as BMP, an AVIF from a website, or an SVG all
// went up and came back as "an image in the conversation could not be processed
// and was removed". Nothing said which image, or why, or that anything was
// wrong: the picture simply was not there, three minutes into a turn that had
// already been paid for.
//
// Size was the same failure with a different number. The upload cap was 20 MiB
// and base64 adds a third, so a large photo could arrive at the API well over
// its 10 MB per-image limit, and a 48-megapixel one over the 8000x8000 cap.
//
// So: sniff what the bytes actually ARE (never the declared type, which is
// whatever the browser guessed or the sender chose), pass through what is
// already fine, downscale what is merely too big, and refuse what cannot be
// converted with a sentence that says what to do about it. The refusal happens
// at upload time, where the person is looking, rather than inside a turn.
//
// Stdlib only, deliberately. JPEG, PNG and GIF are decodable with what Go ships,
// and they are what real uploads are: screenshots are PNG, photos are JPEG.
// Adding an image library to convert the long tail would cost a dependency and
// still not decode HEIC, which needs cgo.
package imageprep

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif" // registered so a GIF decodes; encoding one is never wanted
	"image/jpeg"
	"image/png"
	"strings"
)

// The API's own limits, from the vision documentation.
const (
	// maxBase64 is the per-image cap the API applies to the ENCODED bytes, so the
	// raw file has to stay under three quarters of it. The margin below that is
	// deliberate: the request also carries the conversation.
	maxRawBytes = 5 << 20
	// maxEdge is what a long edge is reduced to. Not the API's 8000px hard limit
	// but its own high-resolution downscale target, so nothing is lost that the
	// model would have kept: an image above this is resized on their side anyway,
	// and doing it here saves the upload, the tokens and the latency.
	maxEdge = 2576
)

// Result is an image ready to send.
type Result struct {
	Data      []byte
	MediaType string
	// Changed says the bytes are not the ones that were uploaded, so a caller can
	// say so rather than silently handing back something else.
	Changed bool
	// Note is what was done, in one phrase, when anything was.
	Note string
}

// ErrUnsupported is a format that cannot be sent and cannot be converted here.
var ErrUnsupported = errors.New("unsupported image format")

// Prepare returns the bytes to send and the media type to declare.
//
// The declared type is ignored entirely. It is whatever the browser guessed from
// a file extension or whatever a sender chose to write, and both are wrong often
// enough that trusting either is how the wrong thing gets sent -- the same
// lesson the share gate learned when a guest could relabel its own upload.
func Prepare(data []byte) (Result, error) {
	switch kind := Sniff(data); kind {
	case "image/webp":
		// Supported by the API but not decodable with the standard library, so it
		// can be passed on and cannot be shrunk. Refused when it is too large,
		// which is the honest answer: better than sending something that will be
		// dropped without explanation.
		if len(data) > maxRawBytes {
			return Result{}, fmt.Errorf("%w: this WebP is %s, and the limit is 5 MB. Re-save it smaller, or send a PNG or JPEG", ErrUnsupported, size(len(data)))
		}
		return Result{Data: data, MediaType: kind}, nil

	case "image/jpeg", "image/png", "image/gif":
		return fit(data, kind)

	case "image/heic":
		return Result{}, fmt.Errorf("%w: HEIC is what iPhones save photos as, and Claude cannot read it. Either set Settings > Camera > Formats to Most Compatible, or open the photo and share it as a screenshot", ErrUnsupported)
	case "image/avif":
		return Result{}, fmt.Errorf("%w: AVIF cannot be read here. Re-save it as PNG or JPEG", ErrUnsupported)
	case "image/svg+xml":
		return Result{}, fmt.Errorf("%w: an SVG is a document rather than a picture, and Claude cannot see it. Export it as a PNG", ErrUnsupported)
	case "image/bmp", "image/tiff":
		return Result{}, fmt.Errorf("%w: %s cannot be read here. Re-save it as PNG or JPEG", ErrUnsupported, strings.ToUpper(strings.TrimPrefix(kind, "image/")))
	}
	return Result{}, fmt.Errorf("%w: these bytes are not an image Claude can read", ErrUnsupported)
}

// fit passes an image through untouched when it is already sendable, and
// otherwise shrinks it until it is.
func fit(data []byte, kind string) (Result, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Result{}, fmt.Errorf("%w: the file is damaged or is not really %s", ErrUnsupported, kind)
	}
	long := max(cfg.Width, cfg.Height)
	if long <= maxEdge && len(data) <= maxRawBytes {
		return Result{Data: data, MediaType: kind}, nil
	}

	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Result{}, fmt.Errorf("%w: the file is damaged", ErrUnsupported)
	}
	if long > maxEdge {
		src = scale(src, maxEdge)
	}

	// PNG first when it started as one: a screenshot re-encoded as JPEG grows
	// artefacts exactly where the text is, which is usually the reason it was
	// sent. JPEG is the fallback when PNG is still too heavy, which for a
	// photograph it always is.
	var out bytes.Buffer
	mediaType := kind
	if kind == "image/png" || kind == "image/gif" {
		if err := png.Encode(&out, src); err != nil {
			return Result{}, err
		}
		mediaType = "image/png"
	}
	if out.Len() == 0 || out.Len() > maxRawBytes {
		out.Reset()
		if err := jpeg.Encode(&out, flatten(src), &jpeg.Options{Quality: 85}); err != nil {
			return Result{}, err
		}
		mediaType = "image/jpeg"
	}
	if out.Len() > maxRawBytes {
		return Result{}, fmt.Errorf("%w: this image is %s even after shrinking it, and the limit is 5 MB", ErrUnsupported, size(out.Len()))
	}

	note := "resized"
	if mediaType != kind {
		note = "resized and re-encoded"
	}
	return Result{Data: out.Bytes(), MediaType: mediaType, Changed: true, Note: note}, nil
}

// scale reduces an image so its long edge is exactly edge, keeping the aspect
// ratio.
//
// Bilinear by hand rather than by dependency. It is thirty lines, it runs once
// per upload on an image nobody is waiting on, and the alternative is adding a
// library to kunai's single binary for one function.
func scale(src image.Image, edge int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return src
	}
	nw, nh := w, h
	if w >= h {
		nw = edge
		nh = max(1, h*edge/w)
	} else {
		nh = edge
		nw = max(1, w*edge/h)
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	xr := float64(w) / float64(nw)
	yr := float64(h) / float64(nh)
	for y := 0; y < nh; y++ {
		for x := 0; x < nw; x++ {
			// The source box this destination pixel covers, averaged. Box sampling
			// rather than nearest: downscaling by nearest drops every other row of a
			// screenshot's text and makes it unreadable, which defeats the point.
			x0, y0 := int(float64(x)*xr), int(float64(y)*yr)
			x1, y1 := min(w, int(float64(x+1)*xr)+1), min(h, int(float64(y+1)*yr)+1)
			var r, g, bl, a, n uint32
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					pr, pg, pb, pa := src.At(b.Min.X+sx, b.Min.Y+sy).RGBA()
					r += pr >> 8
					g += pg >> 8
					bl += pb >> 8
					a += pa >> 8
					n++
				}
			}
			if n == 0 {
				continue
			}
			dst.Set(x, y, color.RGBA{uint8(r / n), uint8(g / n), uint8(bl / n), uint8(a / n)})
		}
	}
	return dst
}

// flatten puts a transparent image on white before JPEG encoding, which has no
// alpha channel: without it, everything transparent comes out black.
func flatten(src image.Image) image.Image {
	b := src.Bounds()
	out := image.NewRGBA(b)
	draw.Draw(out, b, image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(out, b, src, b.Min, draw.Over)
	return out
}

// Sniff names the format from the bytes themselves.
//
// The magic numbers rather than a decoder, because half the point is to
// recognise the formats that cannot be decoded here, and to say their names when
// refusing them. An extension and a Content-Type are both claims by whoever sent
// the file.
func Sniff(b []byte) string {
	switch {
	case len(b) < 12:
		return ""
	case bytes.HasPrefix(b, []byte("\xff\xd8\xff")):
		return "image/jpeg"
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case bytes.HasPrefix(b, []byte("GIF87a")), bytes.HasPrefix(b, []byte("GIF89a")):
		return "image/gif"
	case bytes.HasPrefix(b, []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return "image/webp"
	case bytes.HasPrefix(b, []byte("BM")):
		return "image/bmp"
	case bytes.HasPrefix(b, []byte("II*\x00")), bytes.HasPrefix(b, []byte("MM\x00*")):
		return "image/tiff"
	}
	// ISO base media (HEIC, AVIF): a `ftyp` box whose brand names the format.
	if len(b) >= 16 && bytes.Equal(b[4:8], []byte("ftyp")) {
		switch brand := string(b[8:12]); brand {
		case "heic", "heix", "hevc", "heim", "heis", "hevm", "mif1", "msf1":
			return "image/heic"
		case "avif", "avis":
			return "image/avif"
		}
	}
	if i := bytes.Index(b[:min(len(b), 256)], []byte("<svg")); i >= 0 {
		return "image/svg+xml"
	}
	return ""
}

// Sendable reports whether a media type is one the API accepts, which is the
// last gate before an image block is built. Four formats, and the list is short
// enough to state rather than pattern-match: "image/" as a prefix is what sent
// HEIC in the first place.
func Sendable(mediaType string) bool {
	switch mediaType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	}
	return false
}

func size(n int) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d KB", n/1024)
}
