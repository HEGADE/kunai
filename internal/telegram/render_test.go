package telegram

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hegade/kunai/internal/session"
)

// The promise this whole interface rests on: a third party gets the
// conversation and the controls, never the contents of a file or the output of
// a command. These tests are the promise, so they use payloads that would be
// genuinely damaging to leak.

const secret = "AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG"

func TestStrictPolicyKeepsToolOutputOffTheWire(t *testing.T) {
	ev := session.AppEvent{
		T:         session.EvToolResult,
		ToolUseID: "t1",
		Content:   "cat .env\n" + secret,
	}
	out, ok := RenderEvent(ev, StrictPolicy())
	if ok && strings.Contains(out.Text, secret) {
		t.Fatalf("tool output reached the message: %q", out.Text)
	}
	if ok {
		t.Fatalf("a successful tool result should say nothing under the strict policy, got %q", out.Text)
	}
}

func TestStrictPolicyKeepsToolInputOffTheWire(t *testing.T) {
	ev := session.AppEvent{
		T:         session.EvPermission,
		ToolName:  "Bash",
		RequestID: "r1",
		Input:     json.RawMessage(`{"command":"echo ` + secret + `"}`),
	}
	out, ok := RenderEvent(ev, StrictPolicy())
	if !ok {
		t.Fatal("a permission ask must always be sent, it is the whole point of the gate")
	}
	if strings.Contains(out.Text, secret) {
		t.Fatalf("the command reached the message: %q", out.Text)
	}
	if !strings.Contains(out.Text, "Bash") {
		t.Errorf("the ask must still name the tool, got %q", out.Text)
	}
}

// A failure is worth knowing about even when the output stays home, otherwise a
// broken turn looks like a quiet one.
func TestStrictPolicyStillReportsAFailedTool(t *testing.T) {
	ev := session.AppEvent{T: session.EvToolResult, IsError: true, Content: secret}
	out, ok := RenderEvent(ev, StrictPolicy())
	if !ok {
		t.Fatal("a failed tool call should be reported")
	}
	if strings.Contains(out.Text, secret) {
		t.Fatalf("failure output leaked: %q", out.Text)
	}
}

// Approving something you cannot identify is worse than not being asked, so the
// path is sent even when the arguments are not.
func TestPermissionNamesTheFileWithoutItsContents(t *testing.T) {
	ev := session.AppEvent{
		T:         session.EvPermission,
		ToolName:  "Edit",
		RequestID: "r2",
		Input: json.RawMessage(`{"file_path":"/srv/app/auth.go",` +
			`"old_string":"` + secret + `","new_string":"redacted"}`),
	}
	out, _ := RenderEvent(ev, StrictPolicy())
	if !strings.Contains(out.Text, "/srv/app/auth.go") {
		t.Errorf("the ask should name the file, got %q", out.Text)
	}
	if strings.Contains(out.Text, secret) {
		t.Fatalf("the edited text leaked: %q", out.Text)
	}
}

// Every permission ask carries the two buttons that answer it, tagged with the
// request id so the answer reaches the right ask.
func TestPermissionCarriesApproveAndDenyButtons(t *testing.T) {
	ev := session.AppEvent{T: session.EvPermission, ToolName: "Bash", RequestID: "req-9"}
	out, _ := RenderEvent(ev, StrictPolicy())
	if out.Keyboard == nil || len(out.Keyboard.Rows) != 1 || len(out.Keyboard.Rows[0]) != 2 {
		t.Fatalf("want one row of two buttons, got %+v", out.Keyboard)
	}
	for _, btn := range out.Keyboard.Rows[0] {
		if !strings.HasSuffix(btn.Data, ":req-9") {
			t.Errorf("button %q does not carry the request id: %q", btn.Text, btn.Data)
		}
	}
}

// The permissive policy is opt-in and does what it says, so that turning it on
// is a real choice rather than a setting that quietly does nothing.
func TestPermissivePolicySendsWhatItPromises(t *testing.T) {
	loose := Policy{ToolInputs: true, ToolOutputs: true}

	ask := session.AppEvent{
		T: session.EvPermission, ToolName: "Bash", RequestID: "r3",
		Input: json.RawMessage(`{"command":"ls -la /srv"}`),
	}
	if out, _ := RenderEvent(ask, loose); !strings.Contains(out.Text, "ls -la /srv") {
		t.Errorf("want the command shown, got %q", out.Text)
	}

	res := session.AppEvent{T: session.EvToolResult, Content: "total 4\ndrwx"}
	out, ok := RenderEvent(res, loose)
	if !ok || !strings.Contains(out.Text, "total 4") {
		t.Errorf("want the output shown, got %q (ok=%v)", out.Text, ok)
	}
}

// The reply itself is the conversation, so it goes through as written.
func TestAssistantProseIsSentAndMarkedForStreaming(t *testing.T) {
	ev := session.AppEvent{T: session.EvAssistant, Blocks: []session.AppBlock{
		{Type: "thinking", Text: "hmm"},
		{Type: "text", Text: "Fixed the failing test."},
		{Type: "tool_use", Name: "Edit"},
	}}
	out, ok := RenderEvent(ev, StrictPolicy())
	if !ok || out.Text != "Fixed the failing test." {
		t.Fatalf("got %q (ok=%v)", out.Text, ok)
	}
	if !out.Stream {
		t.Error("an assistant reply should stream into one message")
	}
	if strings.Contains(out.Text, "hmm") {
		t.Error("thinking blocks must not be sent")
	}
}

// A turn of pure tool work has nothing to say on its own.
func TestAssistantWithNoProseSaysNothing(t *testing.T) {
	ev := session.AppEvent{T: session.EvAssistant, Blocks: []session.AppBlock{{Type: "tool_use", Name: "Read"}}}
	if _, ok := RenderEvent(ev, StrictPolicy()); ok {
		t.Error("a tool-only assistant message should not post an empty bubble")
	}
}

// The end of a turn reports the same facts the push notification does: how long
// and how much, and nothing that was said.
func TestResultReportsDurationAndCost(t *testing.T) {
	out, ok := RenderEvent(session.AppEvent{
		T: session.EvResult, DurationMs: 252_000, CostUSD: 0.42,
	}, StrictPolicy())
	if !ok || !strings.Contains(out.Text, "4m 12s") || !strings.Contains(out.Text, "$0.42") {
		t.Fatalf("got %q", out.Text)
	}
}

func TestResultReportsFailure(t *testing.T) {
	out, _ := RenderEvent(session.AppEvent{T: session.EvResult, IsError: true, DurationMs: 63_000}, StrictPolicy())
	if !strings.Contains(strings.ToLower(out.Text), "failed") {
		t.Errorf("a failed turn should say so, got %q", out.Text)
	}
}

// A warning is not a wall, the same rule the loop and the banner already follow.
func TestOnlyARejectedWindowIsReported(t *testing.T) {
	for _, status := range []string{"", "allowed", "allowed_warning"} {
		ev := session.AppEvent{T: session.EvRateLimit, LimitStatus: status}
		if _, ok := RenderEvent(ev, StrictPolicy()); ok {
			t.Errorf("status %q should not be reported as a spent window", status)
		}
	}
	if _, ok := RenderEvent(session.AppEvent{T: session.EvRateLimit, LimitStatus: "rejected"}, StrictPolicy()); !ok {
		t.Error("a rejected window should be reported")
	}
}

// Noise that would otherwise arrive once per keystroke or state change.
func TestNoisyEventsAreNotSent(t *testing.T) {
	for _, tag := range []string{session.EvState, session.EvHello, session.EvThinking, session.EvMode} {
		if _, ok := RenderEvent(session.AppEvent{T: tag}, StrictPolicy()); ok {
			t.Errorf("%s should not become a chat message", tag)
		}
	}
}

// A model answering in a table must not be able to explode one field across the
// message.
func TestOneLineFlattensMultilineText(t *testing.T) {
	if got := oneLine("a\nb\t c  \n d"); got != "a b c d" {
		t.Errorf("got %q", got)
	}
}
