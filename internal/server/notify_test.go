package server

import (
	"strings"
	"testing"

	"github.com/hegade/kunai/internal/session"
)

// The detail every raiser already computes used to be accepted and dropped, so
// each kind said the same thing whatever happened. These are the sentences that
// have to reach the phone.
func TestWakeupTextUsesTheDetail(t *testing.T) {
	cases := []struct {
		name, kind, detail, want string
	}{
		{"loop names the limit that ended it", session.NotifyLoop, "the $5.00 budget ran out", "Loop finished: the $5.00 budget ran out"},
		{"permission names the tool waiting", session.NotifyPermission, "Bash", "Needs approval: Bash"},
		{"thermal says why it tripped", session.NotifyThermal, "cpu 92C for 3 reads", "Stopped everything: cpu 92C for 3 reads"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			title, body := wakeupText(c.kind, c.detail)
			if title != "Kunai" {
				t.Errorf("title = %q, want Kunai", title)
			}
			if body != c.want {
				t.Errorf("body = %q, want %q", body, c.want)
			}
		})
	}
}

// A caller with nothing to add must not produce a dangling "Loop finished:".
func TestWakeupTextFallsBackWithoutDetail(t *testing.T) {
	cases := map[string]string{
		session.NotifyLoop:       "A loop finished",
		session.NotifyPermission: "A session needs your approval",
		session.NotifyThermal:    "Stopped everything: the host got too hot",
		session.NotifyDone:       "A task finished",
		session.NotifyFailed:     "A turn failed",
		"something-new":          "A session needs your attention",
	}
	for kind, want := range cases {
		if _, body := wakeupText(kind, ""); body != want {
			t.Errorf("%s: body = %q, want %q", kind, body, want)
		}
		if strings.HasSuffix(want, ": ") {
			t.Errorf("%s: fallback ends in a dangling separator", kind)
		}
	}
}

// A failed turn used to send the same words as a successful one, so the one
// outcome worth acting on differently was indistinguishable on a lock screen.
func TestWakeupTextDistinguishesFailureFromSuccess(t *testing.T) {
	_, ok := wakeupText(session.NotifyDone, "4m 12s · $0.42")
	_, bad := wakeupText(session.NotifyFailed, "1m 3s")
	if ok != "Task finished: 4m 12s · $0.42" {
		t.Errorf("done body = %q", ok)
	}
	if bad != "Turn failed: 1m 3s" {
		t.Errorf("failed body = %q", bad)
	}
	if ok == bad {
		t.Error("a failed turn reads the same as a finished one")
	}
}

// A detail is the last place a stray newline could reach the OS, so the body is
// always one line.
func TestCleanDetailFlattensWhitespace(t *testing.T) {
	got := cleanDetail("  the $5.00\n budget\tran   out  ")
	if got != "the $5.00 budget ran out" {
		t.Errorf("got %q, want the whitespace collapsed to one line", got)
	}
	if strings.ContainsAny(got, "\n\r\t") {
		t.Errorf("got %q, want no control whitespace", got)
	}
}

// Long details are cut here, where the ellipsis is honest, rather than clipped
// mid-word by the phone.
func TestCleanDetailBoundsLength(t *testing.T) {
	got := cleanDetail(strings.Repeat("a", detailMax*2))
	if r := []rune(got); len(r) != detailMax+1 || r[len(r)-1] != '…' {
		t.Fatalf("got %d runes ending %q, want %d plus an ellipsis", len(r), string(r[len(r)-1]), detailMax)
	}
}

// Cutting on bytes would split a multi-byte character into mojibake.
func TestCleanDetailCutsOnRuneBoundaries(t *testing.T) {
	got := cleanDetail(strings.Repeat("é", detailMax*2))
	trimmed := strings.TrimSuffix(got, "…")
	if strings.ContainsRune(trimmed, '�') {
		t.Fatalf("got %q, want no replacement characters", got)
	}
	if len([]rune(trimmed)) != detailMax {
		t.Errorf("got %d runes, want %d", len([]rune(trimmed)), detailMax)
	}
}

// Nothing a raiser passes may exceed what a notification body should hold, so a
// long detail is bounded no matter which kind carries it.
func TestWakeupBodyStaysBounded(t *testing.T) {
	long := strings.Repeat("why ", 200)
	for _, kind := range []string{session.NotifyLoop, session.NotifyPermission, session.NotifyThermal} {
		_, body := wakeupText(kind, long)
		if n := len([]rune(body)); n > detailMax+40 {
			t.Errorf("%s: body is %d runes, want it bounded", kind, n)
		}
	}
}
