// Package lanauth is the lock on kunai's network listener: a PIN the owner
// sets, the sessions it grants, and the throttle that makes guessing one
// pointless.
//
// It is deliberately pure. Nothing here imports net/http, so every rule can be
// tested directly and the decisions live in one place instead of being spread
// through handlers.
//
// This project is open source, so everything below is public knowledge to an
// attacker. That is a design constraint, not a worry: nothing here is protected
// by being hard to find. The PIN's secrecy, the size of the random tokens, and
// the throttle are what hold, and each is chosen to survive an attacker who has
// read this file. Where a choice is weak on its own -- six digits is only a
// million possibilities -- the comment says which other layer covers it, because
// a reader deserves to know which parts are load-bearing.
package lanauth

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"strings"

	"golang.org/x/crypto/argon2"
)

// PIN length bounds. Six is the floor because it is what people will actually
// type on a phone; longer is allowed and strictly better, since every extra
// digit multiplies the guessing cost by ten.
const (
	MinPINLength = 6
	MaxPINLength = 12
)

var (
	ErrPINLength = errors.New("a PIN must be 6 to 12 digits")
	ErrPINDigits = errors.New("a PIN must be digits only")
	ErrPINWeak   = errors.New("that PIN is one of the first an attacker tries; pick another")
)

// weakPINs are refused outright.
//
// Six digits has a million combinations, but real choices are not spread evenly
// over them: a small set of patterns (repeats, runs, years, keypad shapes)
// accounts for a large share of PINs people pick. An attacker with any sense
// tries these first, and against that head start the throttle's few attempts are
// spent before it can help. Publishing the list costs nothing -- an attacker
// would try these first whether or not we said so -- and it removes the choices
// that would make everything else here decorative.
//
// Deliberately short. It is not an attempt to enumerate bad PINs, only to catch
// the handful that are chosen far more often than chance.
var weakPINs = map[string]bool{
	"000000": true, "111111": true, "222222": true, "333333": true,
	"444444": true, "555555": true, "666666": true, "777777": true,
	"888888": true, "999999": true, "123456": true, "654321": true,
	"012345": true, "543210": true, "121212": true, "112233": true,
	"123123": true, "696969": true, "159753": true, "147258": true,
	"111222": true, "123321": true, "789456": true, "010203": true,
}

// ValidatePIN reports whether a proposed PIN may be used.
//
// Rejection happens when the PIN is SET, never when one is checked: telling a
// visitor at login that their guess was "too short" would hand them the length,
// which is the one thing about a PIN worth keeping to yourself.
func ValidatePIN(pin string) error {
	if len(pin) < MinPINLength || len(pin) > MaxPINLength {
		return ErrPINLength
	}
	for _, r := range pin {
		if r < '0' || r > '9' {
			return ErrPINDigits
		}
	}
	if weakPINs[pin] || allSameDigit(pin) || isRun(pin) {
		return ErrPINWeak
	}
	return nil
}

func allSameDigit(pin string) bool {
	return pin != "" && strings.Count(pin, pin[:1]) == len(pin)
}

// isRun catches ascending and descending sequences of any length, which the
// fixed list above can only cover for six digits.
func isRun(pin string) bool {
	if len(pin) < 3 {
		return false
	}
	up, down := true, true
	for i := 1; i < len(pin); i++ {
		if pin[i] != pin[i-1]+1 {
			up = false
		}
		if pin[i] != pin[i-1]-1 {
			down = false
		}
	}
	return up || down
}

// Argon2 parameters. Memory-hard on purpose: a PIN has little entropy, so if the
// hash file is ever read the only thing standing between an attacker and the PIN
// is how expensive each guess is. 64 MiB per guess is what turns a million
// candidates from seconds on a GPU into something not worth starting.
//
// This is defence in depth rather than the main line. Reading the file means
// already having the owner's account on this machine, at which point running
// `claude` directly is easier than cracking anything.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 2
	argonKeyLen  = 32
	saltLen      = 16
)

// Hashed is a stored PIN: a salt and the argon2id key derived from it. The PIN
// itself is never written anywhere.
type Hashed struct {
	Salt    []byte `json:"salt"`
	Key     []byte `json:"key"`
	Time    uint32 `json:"t"`
	Memory  uint32 `json:"m"`
	Threads uint8  `json:"p"`
}

// HashPIN derives a stored form of pin with a fresh random salt.
func HashPIN(pin string) (Hashed, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return Hashed{}, err
	}
	return Hashed{
		Salt:    salt,
		Key:     argon2.IDKey([]byte(pin), salt, argonTime, argonMemory, argonThreads, argonKeyLen),
		Time:    argonTime,
		Memory:  argonMemory,
		Threads: argonThreads,
	}, nil
}

// Set reports whether this is a real stored PIN rather than a zero value.
func (h Hashed) Set() bool { return len(h.Salt) > 0 && len(h.Key) > 0 }

// Verify reports whether pin matches.
//
// The comparison is constant time, and the derivation runs with the STORED
// parameters rather than the current constants, so raising the cost later does
// not lock out an existing PIN.
func (h Hashed) Verify(pin string) bool {
	if !h.Set() {
		return false
	}
	got := argon2.IDKey([]byte(pin), h.Salt, h.Time, h.Memory, h.Threads, uint32(len(h.Key)))
	return subtle.ConstantTimeCompare(got, h.Key) == 1
}

// BurnEquivalentWork does the same derivation against a throwaway salt.
//
// Called when there is no PIN to check, so that "no PIN is set" and "the PIN is
// wrong" take the same time to answer. Without it the difference between an
// instant refusal and a 60ms one tells an unauthenticated caller whether the
// lock is even armed, which is a small leak and a completely free one to close.
func BurnEquivalentWork() {
	var salt [saltLen]byte
	_, _ = rand.Read(salt[:])
	_ = argon2.IDKey([]byte("burn"), salt[:], argonTime, argonMemory, argonThreads, argonKeyLen)
}
