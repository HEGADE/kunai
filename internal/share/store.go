package share

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"
)

// Store holds every live share, keyed by token, and persists them so a link
// survives a kunai restart. Modelled on internal/server's sessionMetaStore:
// mutex, map, temp-file-and-rename.
//
// The file is the record of what is currently reachable from the public
// internet, so it is written on every change rather than on a timer. A share
// that exists only in memory would come back after a crash as a link that
// answers 404, which reads as "kunai is broken" rather than "that expired".
type Store struct {
	mu      sync.Mutex
	path    string
	byToken map[string]*Share
	// now is injectable so expiry can be tested without sleeping. Nil means
	// time.Now.
	now func() time.Time
}

// ErrNotFound is returned for a token that does not exist, has expired, or has
// been revoked. Deliberately one error for all three: an error that distinguishes
// "wrong token" from "expired token" tells a stranger probing the endpoint that
// they found a real one.
var ErrNotFound = errors.New("no such share")

// ErrNoRoom is returned when a session already has a share. One per session keeps
// the mental model honest: "this session is shared, here is the link, here is who
// has it", rather than a set of links with different powers that have to be
// reasoned about together.
var ErrNoRoom = errors.New("this session is already shared")

// NewStore loads the shares from path, dropping any that expired while kunai was
// not running. A corrupt file starts empty rather than refusing to boot: losing
// the shares is recoverable and annoying, not starting is neither.
func NewStore(path string) *Store {
	s := &Store{path: path, byToken: map[string]*Share{}}
	if path == "" {
		return s
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	var list []*Share
	if json.Unmarshal(b, &list) != nil {
		return s
	}
	now := time.Now()
	for _, sh := range list {
		if sh == nil || sh.Token == "" || sh.Expired(now) {
			continue
		}
		s.byToken[sh.Token] = sh
	}
	return s
}

func (s *Store) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// SetClock replaces the store's idea of now. Tests only.
func (s *Store) SetClock(fn func() time.Time) {
	s.mu.Lock()
	s.now = fn
	s.mu.Unlock()
}

// saveLocked writes the whole set atomically. Callers hold s.mu.
func (s *Store) saveLocked() {
	if s.path == "" {
		return
	}
	list := make([]*Share, 0, len(s.byToken))
	for _, sh := range s.byToken {
		list = append(list, sh)
	}
	b, err := json.Marshal(list)
	if err != nil {
		return
	}
	tmp := s.path + ".tmp"
	// 0600: the file is a list of live bearer tokens for the public internet.
	if os.WriteFile(tmp, b, 0o600) != nil {
		return
	}
	_ = os.Rename(tmp, s.path)
}

// sweepLocked drops expired shares. Called on every read path, so an expired
// share stops answering the moment its time is up rather than at the next write.
func (s *Store) sweepLocked(now time.Time) {
	for tok, sh := range s.byToken {
		if sh.Expired(now) {
			delete(s.byToken, tok)
		}
	}
	// A pairing request that nobody approved is dropped on its own, shorter clock:
	// leaving one open is leaving an invitation open.
	for _, sh := range s.byToken {
		if sh.Pending != nil && now.Sub(time.Unix(sh.Pending.AskedAt, 0)) > PairTTL {
			sh.Pending = nil
		}
	}
}

// Create records a new share and returns it. The caller supplies everything the
// share is about; the token, timestamps and expiry are set here so no caller can
// forget one.
func (s *Store) Create(sh Share, ttl time.Duration) (*Share, error) {
	if sh.SessionID == "" {
		return nil, errors.New("a share needs a session")
	}
	if !sh.Tier.Valid() {
		return nil, errors.New("unknown tier: " + string(sh.Tier))
	}
	now := s.clock()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)
	for _, existing := range s.byToken {
		if existing.SessionID == sh.SessionID {
			return nil, ErrNoRoom
		}
	}

	out := sh
	// Copy the roots rather than keeping the caller's slice. Sharing the backing
	// array means a caller that reuses or mutates it afterwards silently rewrites
	// the boundary a guest is confined to, which is the one field here that must
	// not move after the share is made.
	out.Roots = append([]string(nil), sh.Roots...)
	out.Token = NewToken()
	out.CreatedAt = now.Unix()
	out.ExpiresAt = now.Add(ClampTTL(ttl)).Unix()
	out.Turns = 0
	out.Guest = nil
	out.Pending = nil
	s.byToken[out.Token] = &out
	s.saveLocked()

	return out.clone(), nil
}

// Get returns a copy of the share for token, or ErrNotFound. A copy, because the
// caller reads it across a request while another goroutine may be approving a
// guest or spending a turn on it.
func (s *Store) Get(token string) (*Share, error) {
	now := s.clock()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)
	sh, ok := s.byToken[token]
	if !ok {
		return nil, ErrNotFound
	}
	return sh.clone(), nil
}

// BySession returns the share for a session, if it has one.
func (s *Store) BySession(sessionID string) (*Share, bool) {
	now := s.clock()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)
	for _, sh := range s.byToken {
		if sh.SessionID == sessionID {
			return sh.clone(), true
		}
	}
	return nil, false
}

// All returns every live share, for the owner's own listing.
func (s *Store) All() []Share {
	now := s.clock()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)
	out := make([]Share, 0, len(s.byToken))
	for _, sh := range s.byToken {
		out = append(out, *sh.clone())
	}
	return out
}

// Ask records a device's request to drive the session and returns the code the
// owner approves. Asking twice from the same device returns the same code rather
// than inventing a second one the owner would also have to deal with.
func (s *Store) Ask(token, device, name string) (string, error) {
	if device == "" {
		return "", errors.New("a pairing request needs a device")
	}
	now := s.clock()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)
	sh, ok := s.byToken[token]
	if !ok {
		return "", ErrNotFound
	}
	if !sh.Tier.CanPrompt() {
		return "", errors.New("this link is view-only")
	}
	if sh.Guest != nil {
		// Somebody already holds this share. Saying so is the point: the second
		// person needs to know the link is spent, not sit waiting for an approval
		// that would take the first person's place.
		return "", errors.New("someone is already paired with this link")
	}
	if sh.Pending != nil {
		if sh.Pending.Device == device {
			// Asking twice from the same browser returns the same code rather than
			// inventing a second one the owner would also have to deal with.
			return sh.Pending.Code, nil
		}
		// Somebody else is already waiting, and their request must NOT be replaced.
		//
		// A single pending slot that the newest asker overwrites is worse than it
		// looks: the first person reads their code to the owner, a second person
		// opens the link, and the code the owner was given no longer matches. If
		// the owner approves from a button bound to "whoever is waiting" rather
		// than by typing the code, they approve the second person while believing
		// they approved the first. So the queue is one deep and first come first
		// served; the owner denies to move on, and an unanswered request expires
		// on its own after PairTTL.
		return "", errors.New("someone else is already waiting to be let in; ask them to try again shortly")
	}
	sh.Pending = &Pending{Code: NewPairCode(), Device: device, Name: name, AskedAt: now.Unix()}
	s.saveLocked()
	return sh.Pending.Code, nil
}

// Approve turns the outstanding pairing request into this share's one guest. The
// code is checked rather than the device, because the owner is approving what
// they can see on the guest's screen.
func (s *Store) Approve(token, code string) (*Share, error) {
	now := s.clock()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)
	sh, ok := s.byToken[token]
	if !ok {
		return nil, ErrNotFound
	}
	if sh.Pending == nil || sh.Pending.Code != code {
		return nil, errors.New("that code is not the one waiting")
	}
	sh.Guest = &Guest{Device: sh.Pending.Device, Name: sh.Pending.Name, PairedAt: now.Unix()}
	sh.Pending = nil
	s.saveLocked()
	return sh.clone(), nil
}

// Deny drops the outstanding request without granting anything.
func (s *Store) Deny(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sh, ok := s.byToken[token]
	if !ok {
		return ErrNotFound
	}
	sh.Pending = nil
	s.saveLocked()
	return nil
}

// Unpair removes the current guest but keeps the link alive, so the owner can
// hand it to somebody else without minting a new one.
func (s *Store) Unpair(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sh, ok := s.byToken[token]
	if !ok {
		return ErrNotFound
	}
	sh.Guest = nil
	sh.Pending = nil
	s.saveLocked()
	return nil
}

// SpendTurn records that the guest used a turn and reports whether it was
// allowed. The check and the increment happen under one lock, or two prompts
// arriving together both see the last remaining turn.
func (s *Store) SpendTurn(token, device string) error {
	now := s.clock()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)
	sh, ok := s.byToken[token]
	if !ok {
		return ErrNotFound
	}
	if !sh.MayPrompt(device, now) {
		return errors.New("this link cannot send right now")
	}
	sh.Turns++
	s.saveLocked()
	return nil
}

// Revoke ends a share immediately. The link stops working on the next request and
// the guest's socket is closed by the caller.
func (s *Store) Revoke(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byToken[token]; !ok {
		return ErrNotFound
	}
	delete(s.byToken, token)
	s.saveLocked()
	return nil
}

// RevokeSession drops whatever share a session had, and reports the token so the
// caller can hang up on anyone still connected. Called when a session ends: a
// share outliving its session would be a link pointing at a conversation that no
// longer exists, and worse, at an id that could be reused.
func (s *Store) RevokeSession(sessionID string) (token string, had bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for tok, sh := range s.byToken {
		if sh.SessionID == sessionID {
			delete(s.byToken, tok)
			s.saveLocked()
			return tok, true
		}
	}
	return "", false
}

// Empty reports whether anything is shared at all, which is what decides if the
// public listener needs to be running.
func (s *Store) Empty() bool {
	now := s.clock()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)
	return len(s.byToken) == 0
}
