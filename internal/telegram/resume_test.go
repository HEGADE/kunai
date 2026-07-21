package telegram

import (
	"strings"
	"testing"
	"time"
)

// A closed session is not a lost session, and the chat has to say so. These
// tests hold the two ways back: a command that survives scrollback, and a button
// that does not require retyping a UUID on a phone.

func TestResumeOfferCarriesCommandAndButton(t *testing.T) {
	const id = "0f3c9a4e-1b2d-4c8f-9a7e-5d6b8c1f2a30"
	out := resumeOffer("That session ended.", id)

	if !strings.Contains(out.Text, "/resume "+id) {
		t.Fatalf("offer has no resume command: %q", out.Text)
	}
	if !strings.HasPrefix(out.Text, "That session ended.") {
		t.Errorf("offer lost its lead: %q", out.Text)
	}
	if out.Keyboard == nil || len(out.Keyboard.Rows) != 1 || len(out.Keyboard.Rows[0]) != 1 {
		t.Fatalf("want one resume button, got %+v", out.Keyboard)
	}
	action, got, ok := ParseCallback(out.Keyboard.Rows[0][0].Data)
	if !ok || action != CallbackResume || got != id {
		t.Errorf("button does not resume this session: %q", out.Keyboard.Rows[0][0].Data)
	}
}

// Telegram silently rejects callback data over 64 bytes, which would ship a
// button that does nothing. The command line has to keep working on its own.
func TestResumeOfferDropsButtonWhenIDIsTooLong(t *testing.T) {
	id := strings.Repeat("x", maxCallbackBytes)
	out := resumeOffer("Closed.", id)

	if out.Keyboard != nil {
		t.Errorf("want no button for an oversized id, got %+v", out.Keyboard)
	}
	if !strings.Contains(out.Text, "/resume "+id) {
		t.Errorf("the command must survive when the button cannot: %q", out.Text)
	}
}

// A real session id is a UUID. It has to fit, or the button is dead in normal
// use rather than in some edge case.
func TestResumeButtonFitsARealSessionID(t *testing.T) {
	const id = "0f3c9a4e-1b2d-4c8f-9a7e-5d6b8c1f2a30"
	if n := len(callbackData(CallbackResume, id)); n > maxCallbackBytes {
		t.Fatalf("callback data is %d bytes, over Telegram's %d", n, maxCallbackBytes)
	}
	if resumeKeyboard(id) == nil {
		t.Fatal("want a button for an ordinary session id")
	}
}

func TestResumeListOffersOnePerSession(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	past := []Past{
		{ID: "a", Cwd: "/home/ninja/coding/kunai", Title: "Telegram bot", When: now.Add(-30 * time.Minute)},
		{ID: "b", Cwd: "/srv/app", When: now.Add(-50 * time.Hour)},
	}
	out := resumeList(past, now)

	if out.Keyboard == nil || len(out.Keyboard.Rows) != 2 {
		t.Fatalf("want a button per session, got %+v", out.Keyboard)
	}
	for _, want := range []string{"Telegram bot", "/resume a", "30m ago", "/resume b", "2d ago"} {
		if !strings.Contains(out.Text, want) {
			t.Errorf("list is missing %q:\n%s", want, out.Text)
		}
	}
	// A session with no title is recognised by its directory, not its uuid.
	if !strings.Contains(out.Text, "app") {
		t.Errorf("untitled session lost its directory:\n%s", out.Text)
	}
}

func TestResumeListSaysSoWhenThereIsNothing(t *testing.T) {
	out := resumeList(nil, time.Now())
	if out.Keyboard != nil {
		t.Errorf("want no buttons, got %+v", out.Keyboard)
	}
	if !strings.Contains(out.Text, "/new") {
		t.Errorf("an empty list should point somewhere: %q", out.Text)
	}
}

func TestAgoIsCoarseButRight(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		d    time.Duration
		want string
	}{
		{10 * time.Second, "just now"},
		{5 * time.Minute, "5m ago"},
		{3 * time.Hour, "3h ago"},
		{30 * time.Hour, "yesterday"},
		{72 * time.Hour, "3d ago"},
	}
	for _, c := range cases {
		if got := ago(now.Add(-c.d), now); got != c.want {
			t.Errorf("ago(%v) = %q, want %q", c.d, got, c.want)
		}
	}
	if got := ago(time.Time{}, now); got != "unknown" {
		t.Errorf("a session with no timestamp should read %q, got %q", "unknown", got)
	}
}
