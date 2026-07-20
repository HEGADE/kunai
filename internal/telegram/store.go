package telegram

import (
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Everything the bot knows that outlives a restart: the token, who is allowed to
// use it, who has asked to be, and which chat is driving which session.
//
// It is UI-editable, which is why it lives here rather than in flags. Flags only
// seed it on first run.

// pairTTL is how long a pairing code is good for. Short, because an unapproved
// code is a standing invitation to whoever holds it.
const pairTTL = time.Hour

// Person is someone allowed to drive kunai from a chat.
type Person struct {
	ID       int64  `json:"id"`
	Name     string `json:"name,omitempty"`
	Username string `json:"username,omitempty"`
	AddedAt  int64  `json:"added_at,omitempty"`
}

// Pending is an unapproved request to use the bot. It is created when a stranger
// messages it, and it carries who asked so the owner is approving a person
// rather than a number.
type Pending struct {
	Code     string `json:"code"`
	ID       int64  `json:"id"`
	Name     string `json:"name,omitempty"`
	Username string `json:"username,omitempty"`
	AskedAt  int64  `json:"asked_at"`
}

// Store is the bot's persisted state.
type Store struct {
	mu   sync.Mutex
	path string

	Token   string    `json:"token,omitempty"`
	People  []Person  `json:"people"`
	Waiting []Pending `json:"waiting,omitempty"`
	// Detail lets tool inputs and outputs leave the machine. Off unless asked
	// for; see render.go for what that means.
	Detail bool              `json:"detail"`
	Offset int64             `json:"offset"`
	Bound  map[string]string `json:"bound"` // chat id -> session id
}

// LoadStore reads the bot's state, seeding the token and allow list from flags
// the first time so an existing command line keeps working.
func LoadStore(dataDir, seedToken string, seedAllowed []int64) *Store {
	s := &Store{Bound: map[string]string{}}
	if dataDir != "" {
		s.path = filepath.Join(dataDir, "telegram.json")
		if b, err := os.ReadFile(s.path); err == nil {
			// A corrupt file must not stop the bot: starting with an empty
			// state is recoverable, refusing to start is not.
			if json.Unmarshal(b, s) != nil {
				s = &Store{path: s.path}
			}
		}
	}
	if s.Bound == nil {
		s.Bound = map[string]string{}
	}
	dirty := false
	if s.Token == "" && seedToken != "" {
		s.Token, dirty = seedToken, true
	}
	for _, id := range seedAllowed {
		if !s.allows(id) {
			s.People = append(s.People, Person{ID: id, AddedAt: time.Now().Unix()})
			dirty = true
		}
	}
	if dirty {
		s.save()
	}
	return s
}

// --- reads ---

func (s *Store) token() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Token
}

func (s *Store) detail() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Detail
}

// Allows reports whether a user may drive kunai. An empty list means nobody:
// a chat with this bot can run commands on the machine, so the safe direction is
// closed.
func (s *Store) Allows(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.allows(id)
}

func (s *Store) allows(id int64) bool {
	for _, p := range s.People {
		if p.ID == id {
			return true
		}
	}
	return false
}

// Snapshot returns the state the UI renders, with expired requests dropped.
func (s *Store) Snapshot() (token string, people []Person, waiting []Pending, detail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	return s.Token, append([]Person(nil), s.People...), append([]Pending(nil), s.Waiting...), s.Detail
}

// --- writes ---

// SetToken replaces the bot token. Returns whether it changed, which is the
// bot's cue to reconnect with the new identity.
func (s *Store) SetToken(t string) bool {
	s.mu.Lock()
	changed := s.Token != t
	s.Token = t
	if changed {
		// A new bot is a new identity, so the old update cursor means nothing.
		s.Offset = 0
	}
	s.mu.Unlock()
	if changed {
		s.save()
	}
	return changed
}

func (s *Store) SetDetail(v bool) {
	s.mu.Lock()
	s.Detail = v
	s.mu.Unlock()
	s.save()
}

// Ask records a stranger's request to use the bot and returns the code the owner
// approves. Asking twice returns the same code rather than filling the list.
func (s *Store) Ask(id int64, name, username string) string {
	s.mu.Lock()
	s.expireLocked()
	for _, w := range s.Waiting {
		if w.ID == id {
			code := w.Code
			s.mu.Unlock()
			return code
		}
	}
	p := Pending{Code: pairCode(), ID: id, Name: name, Username: username, AskedAt: time.Now().Unix()}
	s.Waiting = append(s.Waiting, p)
	s.mu.Unlock()
	s.save()
	return p.Code
}

// Approve turns a pending request into an allowed person.
func (s *Store) Approve(code string) (Person, bool) {
	s.mu.Lock()
	s.expireLocked()
	idx := -1
	for i, w := range s.Waiting {
		if w.Code == code {
			idx = i
			break
		}
	}
	if idx < 0 {
		s.mu.Unlock()
		return Person{}, false
	}
	w := s.Waiting[idx]
	s.Waiting = append(s.Waiting[:idx], s.Waiting[idx+1:]...)
	person := Person{ID: w.ID, Name: w.Name, Username: w.Username, AddedAt: time.Now().Unix()}
	if !s.allows(w.ID) {
		s.People = append(s.People, person)
	}
	s.mu.Unlock()
	s.save()
	return person, true
}

// Deny drops a pending request without allowing it.
func (s *Store) Deny(code string) bool {
	s.mu.Lock()
	for i, w := range s.Waiting {
		if w.Code == code {
			s.Waiting = append(s.Waiting[:i], s.Waiting[i+1:]...)
			s.mu.Unlock()
			s.save()
			return true
		}
	}
	s.mu.Unlock()
	return false
}

// Revoke removes someone's access.
func (s *Store) Revoke(id int64) bool {
	s.mu.Lock()
	for i, p := range s.People {
		if p.ID == id {
			s.People = append(s.People[:i], s.People[i+1:]...)
			s.mu.Unlock()
			s.save()
			return true
		}
	}
	s.mu.Unlock()
	return false
}

// expireLocked drops pairing requests older than the TTL. Caller holds the lock.
func (s *Store) expireLocked() {
	cutoff := time.Now().Add(-pairTTL).Unix()
	kept := s.Waiting[:0]
	for _, w := range s.Waiting {
		if w.AskedAt >= cutoff {
			kept = append(kept, w)
		}
	}
	s.Waiting = kept
}

// --- chat bindings ---

func (s *Store) bind(chatID int64, sessionID string) {
	s.mu.Lock()
	s.Bound[chatKey(chatID)] = sessionID
	s.mu.Unlock()
	s.save()
}

func (s *Store) boundTo(chatID int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Bound[chatKey(chatID)]
}

func (s *Store) unbind(chatID int64) {
	s.mu.Lock()
	delete(s.Bound, chatKey(chatID))
	s.mu.Unlock()
	s.save()
}

func (s *Store) setOffset(v int64) {
	s.mu.Lock()
	changed := v != s.Offset
	s.Offset = v
	s.mu.Unlock()
	if changed {
		s.save()
	}
}

func (s *Store) offset() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Offset
}

// save writes the state out, via a temp file and a rename so a crash mid-write
// cannot leave a half-file that loses the token. Failures are logged nowhere and
// survived: the bot keeps working on what it has in memory.
func (s *Store) save() {
	s.mu.Lock()
	if s.path == "" {
		s.mu.Unlock()
		return
	}
	b, err := json.MarshalIndent(s, "", "  ")
	path := s.path
	s.mu.Unlock()
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// pairCode is a short, unambiguous code the owner reads off a screen. Digits and
// uppercase letters only, with the shapes that get misread left out.
func pairCode() string {
	const alphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "PAIR00"
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

func chatKey(id int64) string { return itoa(id) }

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
