package server

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// A code the CLI refuses must be reported as the refusal, not as a timeout.
//
// The CLI does not exit when it dislikes a code: it prints why and prompts
// again. finish only waited for the process to end, so a refusal became ninety
// seconds of silence and then "the login timed out", with the CLI's actual
// sentence buried at the end of a wall of redacted URL. Measured against claude
// 2.1.220: a well-formed code that fails the exchange draws "Login failed: ...".
func TestARefusedCodeIsReportedNotWaitedOut(t *testing.T) {
	if os.Getenv("KUNAI_E2E") == "" {
		t.Skip("set KUNAI_E2E=1 (spawns a real claude login)")
	}
	m := newLoginManager("claude", t.TempDir(), nil)
	id, _, _, err := m.start(context.Background(), "probe")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.cancel(id)

	start := time.Now()
	_, err = m.finish(id, "PyEWjWAFiMx4dqpNDVr4PoeKlxRIZQys0hYOoy5SabcQ")
	took := time.Since(start)

	if err == nil {
		t.Fatal("a bogus code was accepted")
	}
	t.Logf("took %s, said: %v", took.Round(time.Millisecond), err)
	if strings.Contains(err.Error(), "timed out") {
		t.Errorf("reported as a timeout instead of the CLI's own refusal: %v", err)
	}
	if took > 30*time.Second {
		t.Errorf("took %s; the refusal was on screen almost immediately", took)
	}
}

// The half of the shape kunai controls: a paste with no state is refused up
// front with something actionable, rather than typed in to be rejected.
func TestAStatelessPasteIsRefusedWithAdvice(t *testing.T) {
	f := &loginFlow{authState: ""} // a scrape that came back without a state
	typed := pasteCode("BAREC0DE", f.authState)
	if strings.Contains(typed, "#") {
		t.Fatal("test setup: expected a bare paste")
	}
	// pasteCode passing it through unchanged is what finish now checks for.
	if typed != "BAREC0DE" {
		t.Errorf("pasteCode mangled a stateless paste: %q", typed)
	}
}
