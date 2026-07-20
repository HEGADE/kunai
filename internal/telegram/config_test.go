package telegram

import (
	"os"
	"path/filepath"
	"testing"
)

// Talking to this bot is equivalent to a shell on the machine, so an empty
// allow list has to mean nobody rather than everybody. This is the one default
// that would be dangerous to get backwards.
func TestEmptyAllowListPermitsNobody(t *testing.T) {
	var c Config
	if c.permits(12345) {
		t.Fatal("an unconfigured bot must refuse every user")
	}
}

func TestAllowListPermitsOnlyItsMembers(t *testing.T) {
	c := Config{Allowed: []int64{111, 222}}
	if !c.permits(111) || !c.permits(222) {
		t.Error("an allowed user was refused")
	}
	if c.permits(333) || c.permits(0) || c.permits(-111) {
		t.Error("a user outside the list was allowed in")
	}
}

func TestEnabledNeedsAToken(t *testing.T) {
	if (Config{}).Enabled() {
		t.Error("no token should mean no bot")
	}
	if !(Config{Token: "t"}).Enabled() {
		t.Error("a token should enable the bot")
	}
}

// A restart should resume the conversation rather than making you say /use
// again, and it must not replay updates it already handled.
func TestStateSurvivesAReload(t *testing.T) {
	dir := t.TempDir()

	s := loadState(dir)
	s.bind(-1001234, "sess-a")
	s.setOffset(99)

	reloaded := loadState(dir)
	if got := reloaded.boundTo(-1001234); got != "sess-a" {
		t.Errorf("binding lost: got %q", got)
	}
	if reloaded.offset() != 99 {
		t.Errorf("offset lost: got %d", reloaded.offset())
	}
}

// Group chat ids are negative, which is why they are stored as strings.
func TestStateHandlesGroupChatIDs(t *testing.T) {
	s := loadState(t.TempDir())
	s.bind(-1009876543210, "sess-g")
	if got := s.boundTo(-1009876543210); got != "sess-g" {
		t.Errorf("got %q", got)
	}
}

func TestUnbindForgetsTheSession(t *testing.T) {
	s := loadState(t.TempDir())
	s.bind(5, "sess-x")
	s.unbind(5)
	if got := s.boundTo(5); got != "" {
		t.Errorf("still bound to %q", got)
	}
}

// A corrupt file must not stop the bot starting: losing which session a chat
// was on is a small thing next to the bot not coming up at all.
func TestCorruptStateFileIsSurvived(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "telegram.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := loadState(dir)
	if s.Bound == nil {
		t.Fatal("state should still be usable")
	}
	s.bind(1, "sess-y") // must not panic
	if got := s.boundTo(1); got != "sess-y" {
		t.Errorf("got %q", got)
	}
}

// Without a data dir (a dev run) the bot still works, just without memory.
func TestStateWithoutADataDirIsUsable(t *testing.T) {
	s := loadState("")
	s.bind(1, "sess-z")
	if got := s.boundTo(1); got != "sess-z" {
		t.Errorf("got %q", got)
	}
	s.setOffset(3) // must not panic without a path
}

func TestChatKey(t *testing.T) {
	cases := map[int64]string{0: "0", 7: "7", -1001234567890: "-1001234567890"}
	for in, want := range cases {
		if got := chatKey(in); got != want {
			t.Errorf("chatKey(%d) = %q, want %q", in, got, want)
		}
	}
}
