package share

import (
	"crypto/rand"
	"encoding/base64"
)

// tokenBytes is the entropy behind a share link.
//
// 32 bytes, where the rest of kunai uses 8 or 16, because this is the only secret
// it hands to the open internet. Everything else generated here (an upload id, a
// queue id) is unguessable only in the sense that nobody can reach it without
// already being on the tailnet. A share token is reachable by anybody, forever, so
// it has to be long enough that guessing is not a strategy rather than long enough
// to avoid a collision.
const tokenBytes = 32

// NewToken returns the secret half of a share link: URL-safe, unpadded, so it
// drops into a path without escaping and without a trailing "=" that people cut
// off when they copy it by hand.
func NewToken() string {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand does not fail on any platform kunai runs on, and a share with
		// a predictable token would be worse than no share, so there is no sensible
		// fallback to reach for.
		panic("share: no entropy for a token: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// NewDevice returns the key a guest's browser keeps to prove it is the one that
// was approved. Separate from the token because they answer different questions:
// the token says which conversation, the device says which person, and everybody
// with the link has the first.
func NewDevice() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("share: no entropy for a device key: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// pairAlphabet excludes the glyphs people misread aloud or mistype from a screen:
// no 0/O, no 1/I/L. The owner reads this code off the guest's screen (or hears it
// over a call), so the cost of an ambiguous character is a failed pairing and a
// second attempt.
const pairAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// pairLen is short enough to read out and long enough that guessing inside the
// one-hour window is hopeless: 31^6 is about 887 million.
const pairLen = 6

// NewPairCode returns the short code an owner approves to let one guest drive the
// session. Unbiased: rejection sampling rather than a modulo, which would make the
// first few letters of the alphabet more likely and quietly shrink the space.
func NewPairCode() string {
	out := make([]byte, 0, pairLen)
	buf := make([]byte, pairLen)
	// 248 is the largest multiple of 31 below 256; anything above it is discarded
	// so every letter stays equally likely.
	const limit = 248
	for len(out) < pairLen {
		if _, err := rand.Read(buf); err != nil {
			panic("share: no entropy for a pairing code: " + err.Error())
		}
		for _, b := range buf {
			if b >= limit {
				continue
			}
			out = append(out, pairAlphabet[int(b)%len(pairAlphabet)])
			if len(out) == pairLen {
				break
			}
		}
	}
	return string(out)
}
