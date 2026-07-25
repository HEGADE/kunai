package telegram

import (
	"context"
	"testing"
)

// A capability is off for the life of the chat, so what may cost one matters
// more than almost anything else in this package. Telegram uses the same shape
// for "this chat cannot do that" and "I could not render THIS message", and
// conflating them meant a single reply with a relative link or an unclosed
// entity dropped the chat to plain text forever: every later reply, however
// ordinary, arrived unformatted.
//
// The error list comes from openclaw, which verified it live against Bot API
// 10.2. Rediscovering it by watching a bot misbehave in production is the
// reinvention worth skipping.
func TestContentRejectionDoesNotCostTheChatItsFormatting(t *testing.T) {
	rejections := []string{
		"Bad Request: can't parse entities: unexpected end of the entity",
		"Bad Request: can't find end of the entity starting at byte offset 42",
		"Bad Request: RICH_MESSAGE_ENTITIES_INVALID",
		"Bad Request: RICH_MESSAGE_TEXT_TOO_LONG",
		"Bad Request: RICH_MESSAGE_BLOCKS_TOO_MANY",
		"Bad Request: RICH_MESSAGE_CONTENT_REQUIRED",
		"Bad Request: can't parse InputRichBlock",
	}
	for _, desc := range rejections {
		err := &APIError{Method: "sendRichMessage", Code: 400, Description: desc}
		if !contentRejected(err) {
			t.Errorf("%q should be read as a content problem", desc)
		}
		if unsupported(err) {
			t.Errorf("%q would cost the chat its rich messages", desc)
		}
	}
}

// The other direction still has to work, or the fallback never happens at all
// and a chat that genuinely cannot do rich messages pays a failed request every
// single reply.
func TestARealRefusalStillCostsTheCapability(t *testing.T) {
	refusals := []*APIError{
		{Code: 400, Description: "Bad Request: method not found"},
		{Code: 403, Description: "Forbidden: bot was blocked by the user"},
		{Code: 400, Description: "Bad Request: RICH_MESSAGE_NOT_SUPPORTED"},
	}
	for _, err := range refusals {
		if contentRejected(err) {
			t.Errorf("%q is not a content problem", err.Description)
		}
		if !unsupported(err) {
			t.Errorf("%q should count as a refusal", err.Description)
		}
	}
	// A throttle and a broken route say nothing either way, as before.
	if unsupported(&APIError{Code: 429, RetryAfter: 5, Description: "Too Many Requests"}) {
		t.Error("a 429 must never cost a capability")
	}
}

// End to end through the stream: one bad reply, then an ordinary one, and the
// ordinary one must still be rich.
func TestOneBadReplyDoesNotDowngradeTheChat(t *testing.T) {
	f := &fakeSender{richSendErr: &APIError{Code: 400, Description: "Bad Request: can't parse entities"}}
	s := newStream(f, 1)
	s.drafting = false // isolate the final send from the draft path

	s.Append(context.Background(), "here is [a file](./src/foo.ts)")
	_ = s.Flush(context.Background())
	if len(f.sends) != 1 {
		t.Fatalf("the rejected reply should still have landed as plain text, got %d plain sends", len(f.sends))
	}

	// A perfectly ordinary second reply in the same chat.
	f.richSendErr = nil
	s.Reset()
	s.drafting = false
	s.Append(context.Background(), "all done")
	_ = s.Flush(context.Background())

	if len(f.richSends) != 1 {
		t.Errorf("the second reply went as plain text; one malformed reply cost the chat its formatting")
	}
}
