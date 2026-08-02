package lanauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"time"
)

// A session is what the PIN buys: a device proves itself once and then carries a
// token, so the PIN is typed once rather than travelling on every request.
//
// Two choices worth stating, both of which a reader of this repository can check:
//
//   - The token is 32 bytes from crypto/rand. That is the real credential, and at
//     256 bits it is not guessable by anyone, ever, which is why the throttle
//     guards the PIN endpoint and does not need to guard this one.
//   - Only a SHA-256 of it is stored. The file on disk is therefore not a pile of
//     working credentials: someone who reads it cannot log in with what they
//     found. Hashing is cheap and unsalted here on purpose -- the input is
//     already 256 bits of randomness, so there is no dictionary to build and
//     nothing for a salt to defend against.

// SessionTTL is how long a device stays signed in without being used. Long,
// because re-typing a PIN on a tablet you use weekly is the kind of friction that
// makes people turn the lock off; refreshed on every use, so a device in regular
// use never expires.
const SessionTTL = 30 * 24 * time.Hour

// tokenBytes is the size of the random token handed to a device.
const tokenBytes = 32

// Session is one signed-in device.
type Session struct {
	// Hash is sha256(token), hex-free base64. The token itself is never stored.
	Hash string `json:"hash"`
	// Label is a human hint about which device this is, for the sign-out list.
	// Free text from the client, so it is displayed and never trusted.
	Label   string    `json:"label,omitempty"`
	Created time.Time `json:"created"`
	Seen    time.Time `json:"seen"`
}

// NewSession mints a token and the record to store for it. The token is returned
// once and cannot be recovered afterwards.
func NewSession(now time.Time, label string) (token string, s Session, err error) {
	raw := make([]byte, tokenBytes)
	if _, err = rand.Read(raw); err != nil {
		return "", Session{}, err
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, Session{
		Hash:    HashToken(token),
		Label:   trimLabel(label),
		Created: now,
		Seen:    now,
	}, nil
}

// HashToken is the stored form of a session token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// Expired reports whether this session has gone unused for longer than SessionTTL.
func (s Session) Expired(now time.Time) bool { return now.Sub(s.Seen) > SessionTTL }

// MatchesToken reports whether a presented token belongs to this session.
//
// Constant time out of habit rather than necessity: the compared value is a hash
// of 256 bits of randomness, so there is no feasible search to guide. It costs
// nothing and removes the need for the reader to work out whether it mattered.
func (s Session) MatchesToken(token string) bool {
	return subtle.ConstantTimeCompare([]byte(s.Hash), []byte(HashToken(token))) == 1
}

// maxLabel bounds what a client can store, since the label is attacker-supplied
// on a listener that has not authenticated yet.
const maxLabel = 40

func trimLabel(s string) string {
	out := make([]rune, 0, maxLabel)
	for _, r := range s {
		// Printable ASCII only. A label is shown in a list of devices, and letting
		// arbitrary runes in buys nothing and invites control characters.
		if r < 0x20 || r > 0x7e {
			continue
		}
		out = append(out, r)
		if len(out) == maxLabel {
			break
		}
	}
	return string(out)
}
