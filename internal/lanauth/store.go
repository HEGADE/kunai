package lanauth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Store is the persisted lock: the PIN, the devices it has admitted, and the
// throttle's memory of failed attempts.
//
// All three are saved together and for the same reason. A restart must not hand
// an attacker a fresh guessing budget, and must not sign out every device the
// owner has already admitted; an update that did either would train people to
// turn the feature off.

// ErrLocked is returned when the throttle is refusing attempts. RetryAfter says
// for how long.
type ErrLocked struct{ RetryAfter time.Duration }

func (e *ErrLocked) Error() string { return "too many attempts" }

var (
	ErrNoPIN   = errors.New("no PIN is set")
	ErrBadPIN  = errors.New("wrong PIN")
	ErrNoStore = errors.New("auth store unavailable")
)

// state is the on-disk shape.
type state struct {
	PIN      Hashed    `json:"pin"`
	Sessions []Session `json:"sessions,omitempty"`
	Throttle Throttle  `json:"throttle"`
	Updated  time.Time `json:"updated"`
}

// Store guards the state and persists every change.
type Store struct {
	path string
	mu   sync.Mutex
	st   state
	// now is the clock, injectable so the throttle's behaviour over time can be
	// tested without sleeping.
	now func() time.Time
}

// Open loads the store at path, creating an empty one if it does not exist.
func Open(path string) *Store {
	s := &Store{path: path, now: time.Now}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &s.st)
	}
	return s
}

// SetClock replaces the time source. Tests only.
func (s *Store) SetClock(fn func() time.Time) {
	s.mu.Lock()
	s.now = fn
	s.mu.Unlock()
}

// HasPIN reports whether a PIN has been set, which is what decides whether the
// network listener may run at all.
func (s *Store) HasPIN() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.PIN.Set()
}

// SetPIN stores a new PIN and signs out every device.
//
// Signing everything out is the point rather than a side effect: changing the PIN
// is what you do when you think somebody else has it, and a change that left
// their session working would be worse than useless. Clearing the throttle with
// it is safe, because setting a PIN can only be done from a listener that is
// already trusted.
func (s *Store) SetPIN(pin string) error {
	if err := ValidatePIN(pin); err != nil {
		return err
	}
	h, err := HashPIN(pin)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.PIN = h
	s.st.Sessions = nil
	s.st.Throttle = Throttle{}
	return s.saveLocked()
}

// ClearPIN removes the lock and every session with it.
func (s *Store) ClearPIN() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st = state{}
	return s.saveLocked()
}

// Login checks a PIN and, on success, returns a fresh session token.
//
// source identifies where the attempt came from, for the per-source half of the
// throttle. The order here is deliberate: the throttle is consulted BEFORE the
// PIN is checked, so a locked-out attacker cannot use the timing of the answer to
// learn anything, and cannot spend our CPU on argon2 either.
func (s *Store) Login(pin, source, label string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()

	if wait := s.st.Throttle.RetryAfter(now, source); wait > 0 {
		return "", &ErrLocked{RetryAfter: wait}
	}
	if !s.st.PIN.Set() {
		// Same work as a real check, so an unauthenticated caller cannot tell an
		// unarmed lock from a wrong guess by how long the answer took.
		BurnEquivalentWork()
		s.st.Throttle.Fail(now, source)
		_ = s.saveLocked()
		return "", ErrNoPIN
	}
	if !s.st.PIN.Verify(pin) {
		s.st.Throttle.Fail(now, source)
		_ = s.saveLocked()
		return "", ErrBadPIN
	}

	token, sess, err := NewSession(now, label)
	if err != nil {
		return "", err
	}
	s.st.Throttle.Succeed(now, source)
	s.st.Sessions = append(pruneExpired(s.st.Sessions, now), sess)
	if err := s.saveLocked(); err != nil {
		return "", err
	}
	return token, nil
}

// Valid reports whether a token names a live session, refreshing its last-seen
// time so a device in regular use never expires.
//
// The refresh is only persisted when it is meaningfully newer, so a busy client
// does not rewrite the file on every request.
func (s *Store) Valid(token string) bool {
	if token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	want := HashToken(token)
	for i := range s.st.Sessions {
		if s.st.Sessions[i].Hash != want {
			continue
		}
		if s.st.Sessions[i].Expired(now) {
			s.st.Sessions = append(s.st.Sessions[:i], s.st.Sessions[i+1:]...)
			_ = s.saveLocked()
			return false
		}
		if now.Sub(s.st.Sessions[i].Seen) > time.Hour {
			s.st.Sessions[i].Seen = now
			_ = s.saveLocked()
		}
		return true
	}
	return false
}

// Forget signs out the device holding this token.
func (s *Store) Forget(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := HashToken(token)
	for i := range s.st.Sessions {
		if s.st.Sessions[i].Hash == want {
			s.st.Sessions = append(s.st.Sessions[:i], s.st.Sessions[i+1:]...)
			_ = s.saveLocked()
			return
		}
	}
}

// ForgetAll signs out every device, keeping the PIN.
func (s *Store) ForgetAll() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.Sessions = nil
	return s.saveLocked()
}

// Devices lists the signed-in devices, newest first, for the owner to review.
func (s *Store) Devices() []Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Session, len(s.st.Sessions))
	copy(out, s.st.Sessions)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// LockedFor reports the current wait for a source, so a client can be told when
// to try again rather than guessing.
func (s *Store) LockedFor(source string) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Throttle.RetryAfter(s.now(), source)
}

func pruneExpired(in []Session, now time.Time) []Session {
	out := in[:0]
	for _, sess := range in {
		if !sess.Expired(now) {
			out = append(out, sess)
		}
	}
	return out
}

// saveLocked writes the state atomically, owner-readable only. The caller holds
// the mutex.
func (s *Store) saveLocked() error {
	if s.path == "" {
		return ErrNoStore
	}
	s.st.Updated = s.now()
	b, err := json.MarshalIndent(s.st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
