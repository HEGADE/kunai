package telegram

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Config is what the bot needs to run. The token comes from a flag or the
// environment rather than this file, so a token is never written to disk by us.
type Config struct {
	Token string
	// Allowed is the set of Telegram user ids permitted to drive kunai. Empty
	// means the bot refuses everyone, which is the only safe default: a bot
	// token is public the moment it leaks, and anyone who can talk to the bot
	// can run commands on this machine.
	Allowed []int64
	// Policy decides how much tool detail may leave the machine.
	Policy Policy
	// DataDir is where chat bindings persist.
	DataDir string
}

// Enabled reports whether the bot should start at all.
func (c Config) Enabled() bool { return c.Token != "" }

// permits reports whether a user may drive kunai.
func (c Config) permits(userID int64) bool {
	for _, id := range c.Allowed {
		if id == userID {
			return true
		}
	}
	return false
}

// state is the bot's memory across restarts: which session each chat is talking
// to, and the update offset so a restart does not replay old messages.
//
// It is small and rewritten whole, matching how the rest of kunai keeps its
// little JSON files next to each other in the data dir.
type state struct {
	mu     sync.Mutex
	path   string
	Offset int64             `json:"offset"`
	Bound  map[string]string `json:"bound"` // chat id -> session id
}

func loadState(dataDir string) *state {
	s := &state{Bound: map[string]string{}}
	if dataDir == "" {
		return s
	}
	s.path = filepath.Join(dataDir, "telegram.json")
	b, err := os.ReadFile(s.path)
	if err != nil {
		return s
	}
	// A corrupt file must not stop the bot: an empty state just means the chat
	// picks its session again.
	if json.Unmarshal(b, s) != nil || s.Bound == nil {
		s.Bound = map[string]string{}
	}
	return s
}

// save writes the state out. Failures are ignored on purpose: losing which
// session a chat was bound to is a small inconvenience, and it is not worth
// taking the bot down for.
func (s *state) save() {
	if s.path == "" {
		return
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	tmp := s.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) != nil {
		return
	}
	_ = os.Rename(tmp, s.path)
}

// bind remembers which session a chat is driving.
func (s *state) bind(chatID int64, sessionID string) {
	s.mu.Lock()
	s.Bound[chatKey(chatID)] = sessionID
	s.mu.Unlock()
	s.save()
}

// boundTo returns the session a chat is driving, if any.
func (s *state) boundTo(chatID int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Bound[chatKey(chatID)]
}

// unbind forgets a chat's session, used when the session ends.
func (s *state) unbind(chatID int64) {
	s.mu.Lock()
	delete(s.Bound, chatKey(chatID))
	s.mu.Unlock()
	s.save()
}

// setOffset records how far through the update stream we are, so a restart
// resumes rather than reprocessing.
func (s *state) setOffset(v int64) {
	s.mu.Lock()
	changed := v != s.Offset
	s.Offset = v
	s.mu.Unlock()
	if changed {
		s.save()
	}
}

func (s *state) offset() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Offset
}

// chatKey renders a chat id as a JSON object key. Chat ids are negative for
// groups, which is why this is a string rather than a number key.
func chatKey(id int64) string {
	return itoa(id)
}

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
