package server

// redactEvent is the single place that decides what leaves the machine through a
// share link, and it had no tests at all. Every case here is something that would
// otherwise be published to whoever holds the URL.

import (
	"encoding/json"
	"testing"

	"github.com/hegade/kunai/internal/session"
	"github.com/hegade/kunai/internal/share"
)

func strict() *share.Share { return &share.Share{Tier: share.TierView, Detail: share.StrictPolicy()} }
func full() *share.Share   { return &share.Share{Tier: share.TierView, Detail: share.FullPolicy()} }

// The owner's attachments are metadata about files, which is exactly the class a
// strict share exists to withhold: it drops what a tool read, so passing through
// the names of files the owner handed the model was inconsistent. A filename can
// say more than the conversation does.
func TestStrictShareDoesNotSendTheOwnersAttachmentNames(t *testing.T) {
	ev := session.AppEvent{
		Seq: 5, T: session.EvUser, Text: "have a look at this",
		Attachments: []session.Attachment{{Name: "acme-contract-final.pdf"}},
	}

	out, keep := redactEvent(ev, strict())
	if !keep {
		t.Fatal("the prompt itself was dropped; the conversation is the point of the share")
	}
	if out.Text != "have a look at this" {
		t.Errorf("the prompt text was lost: %q", out.Text)
	}
	if len(out.Attachments) != 0 {
		t.Errorf("a strict share sent the attachment names: %v", out.Attachments)
	}

	// Turning tool detail on is the owner saying "show them what was involved".
	if out, _ := redactEvent(ev, full()); len(out.Attachments) != 1 {
		t.Error("a full-detail share withheld attachments the owner chose to include")
	}
}

// Tool arguments are the contents of your files on an Edit, so a strict share
// keeps the fact a tool ran and drops what it was called with.
func TestStrictShareDropsToolInputsAndOutputs(t *testing.T) {
	secret := json.RawMessage(`{"file_path":"/home/me/.env","new_string":"AWS_KEY=live"}`)

	asst := session.AppEvent{Seq: 6, T: session.EvAssistant, Blocks: []session.AppBlock{
		{Type: "text", Text: "editing that now"},
		{Type: "tool_use", Name: "Edit", ID: "t1", Input: secret},
	}}
	out, keep := redactEvent(asst, strict())
	if !keep {
		t.Fatal("the assistant message was dropped entirely")
	}
	if len(out.Blocks) != 2 || out.Blocks[0].Text != "editing that now" {
		t.Fatalf("the reply text did not survive: %+v", out.Blocks)
	}
	if out.Blocks[1].Input != nil {
		t.Errorf("a strict share sent the tool's arguments: %s", out.Blocks[1].Input)
	}
	if out.Blocks[1].Name != "Edit" {
		t.Error("the tool's name was dropped, so the conversation stops making sense")
	}

	res := session.AppEvent{Seq: 7, T: session.EvToolResult, ToolUseID: "t1", Content: "AWS_KEY=live"}
	out, keep = redactEvent(res, strict())
	if !keep || out.ToolUseID != "t1" {
		t.Fatal("the tool result vanished, so the call reads as unanswered")
	}
	if out.Content != "" {
		t.Errorf("a strict share sent the tool's output: %q", out.Content)
	}
	if out, _ := redactEvent(res, full()); out.Content != "AWS_KEY=live" {
		t.Error("a full-detail share withheld output the owner chose to include")
	}
}

// What the owner's account is spending, and how close it is to its limits, is not
// part of the conversation being shared.
func TestShareNeverSendsTheOwnersMoneyOrQuota(t *testing.T) {
	res := session.AppEvent{Seq: 8, T: session.EvResult, DurationMs: 1200, CostUSD: 0.42,
		Tokens: 99, NewTokens: 50, CachedTokens: 49}
	for _, sh := range []*share.Share{strict(), full()} {
		out, keep := redactEvent(res, sh)
		if !keep {
			t.Fatal("the turn's end was dropped, so the guest never sees it finish")
		}
		if out.CostUSD != 0 || out.Tokens != 0 || out.NewTokens != 0 || out.CachedTokens != 0 {
			t.Errorf("a share leaked the turn's cost or token spend: %+v", out)
		}
		if out.DurationMs != 1200 {
			t.Error("the duration was dropped, which is the harmless half")
		}
	}

	if _, keep := redactEvent(session.AppEvent{Seq: 9, T: session.EvRateLimit, ResetsAt: 1}, full()); keep {
		t.Error("a share sent the owner's rate-limit state")
	}
}

// An event tag nobody has considered is withheld rather than forwarded, so a new
// one added to the protocol is invisible to guests until somebody decides
// otherwise. This is the property that keeps the redactor honest over time.
func TestAnUnknownEventIsWithheldNotForwarded(t *testing.T) {
	for _, tag := range []string{"some_new_event", session.EvProject, session.EvHello, ""} {
		if _, keep := redactEvent(session.AppEvent{Seq: 3, T: tag}, full()); keep {
			t.Errorf("event %q was forwarded to a guest without anyone deciding it should be", tag)
		}
	}
}

// The floor is enforced against the stored share. A guest asking for an earlier
// ?since= is clamped elsewhere; this is the second line, applied per event.
func TestEventsBeforeTheShareFloorAreWithheld(t *testing.T) {
	sh := strict()
	sh.FromSeq = 100
	for _, seq := range []uint64{1, 99, 100} {
		if _, keep := redactEvent(session.AppEvent{Seq: seq, T: session.EvUser}, sh); keep {
			t.Errorf("seq %d was sent although the share starts at %d", seq, sh.FromSeq)
		}
	}
	if _, keep := redactEvent(session.AppEvent{Seq: 101, T: session.EvUser}, sh); !keep {
		t.Error("an event after the floor was withheld")
	}
}

// A guest may see that the session is waiting on a decision, so it does not look
// stalled, but never what is being asked unless the owner shared inputs, and
// never the suggestions that would help answer it.
func TestAPermissionAskIsVisibleButNotReadable(t *testing.T) {
	ask := session.AppEvent{
		Seq: 11, T: session.EvPermission, RequestID: "r1", ToolName: "Write",
		Input:       json.RawMessage(`{"file_path":"/home/me/.ssh/config"}`),
		Description: "write to your ssh config",
		Suggestions: json.RawMessage(`[{"always":true}]`),
	}
	out, keep := redactEvent(ask, strict())
	if !keep || out.ToolName != "Write" {
		t.Fatal("the guest cannot tell the session is waiting, so it looks hung")
	}
	if out.Input != nil || out.Description != "" {
		t.Errorf("a strict share sent what was being asked: %s %q", out.Input, out.Description)
	}
	if out.Suggestions != nil {
		t.Error("the ask's suggestions were sent to somebody who can never answer it")
	}
}
