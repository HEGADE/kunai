package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// apiBase is Telegram's endpoint. A variable so a test can point the client at
// an httptest server instead of the internet.
var apiBase = "https://api.telegram.org"

// longPollSeconds is how long getUpdates parks waiting for an update. Telegram
// holds the request open and answers the moment something arrives, so a long
// wait means fewer round trips rather than a slower bot.
const longPollSeconds = 50

// Client is a thin Bot API caller. It owns HTTP and JSON and nothing else: no
// session knowledge, no policy, so it can be exercised against a stub server.
type Client struct {
	token string
	http  *http.Client
	tr    *http.Transport
	dial  *familyDialer
}

// NewClient returns a client for the given bot token. The timeout has to clear
// the long poll, or every getUpdates would cancel itself.
func NewClient(token string) *Client {
	tr, dial := newTransport()
	return &Client{
		token: token,
		tr:    tr,
		dial:  dial,
		http:  &http.Client{Timeout: (longPollSeconds + 15) * time.Second, Transport: tr},
	}
}

// brokenRoute is what to do when a request never reached Telegram: throw the
// pooled connection away and put new ones on IPv4 for a while.
//
// Both halves matter. Without the first, Go keeps reusing the connection it
// already chose, so one bad route stays bad for as long as that connection
// lives. Without the second, the next race can simply pick the bad family
// again. See transport.go for the measurements behind it.
func (c *Client) brokenRoute() {
	c.tr.CloseIdleConnections()
	c.dial.pin()
}

// call posts params to a Bot API method and decodes result into out (which may
// be nil when the reply is not needed).
func (c *Client) call(ctx context.Context, method string, params any, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/bot%s/%s", apiBase, c.token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		// The request never got an answer. That is a route problem, not a
		// refusal: Telegram's own refusals arrive as a 200 with ok:false.
		c.brokenRoute()
		return redact(err, c.token)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		// Headers arrived and then the body did not, which is the same broken
		// route wearing a different hat.
		c.brokenRoute()
		return redact(err, c.token)
	}
	var r apiResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("%s: unreadable reply: %w", method, err)
	}
	if !r.OK {
		// Telegram reports its own failures in a 200 body, so the status code
		// alone would call a rejected request a success.
		e := &APIError{Method: method, Code: r.ErrorCode, Description: r.Description}
		if r.Parameters != nil {
			e.RetryAfter = r.Parameters.RetryAfter
		}
		return e
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(r.Result, out)
}

// APIError is a refusal from Telegram itself, which arrives with HTTP 200 and
// ok:false rather than as a transport error.
type APIError struct {
	Method      string
	Code        int
	Description string
	// RetryAfter is the cool-off Telegram asks for on a 429, in seconds. It is
	// the difference between "you are going too fast" and "you cannot do this",
	// which is a distinction a streaming reply has to get right.
	RetryAfter int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram %s: %d %s", e.Method, e.Code, e.Description)
}

// unsupported reports whether an error means "this chat cannot do that", as
// opposed to "that did not work just now".
//
// The distinction is the whole safety of degrading a capability, because a
// downgrade is permanent for the chat. A timeout on a flaky route, or a 429 for
// pushing too fast, says nothing about whether rich messages or drafts work
// here; treating either as a refusal drops the chat to the slowest path for
// good, on a hiccup. Only a flat rejection from Telegram counts, and 429 is
// explicitly excluded even though it arrives the same way.
func unsupported(err error) bool {
	var api *APIError
	if !errors.As(err, &api) {
		return false // a transport failure: the request never got an answer
	}
	if api.Code == http.StatusTooManyRequests || api.RetryAfter > 0 {
		return false
	}
	return api.Code >= 400 && api.Code < 500
}

// GetUpdates long-polls for updates after offset. It returns as soon as
// Telegram has something, or empty when the poll window expires.
func (c *Client) GetUpdates(ctx context.Context, offset int64) ([]Update, error) {
	var out []Update
	err := c.call(ctx, "getUpdates", map[string]any{
		"offset":  offset,
		"timeout": longPollSeconds,
		// Only what the bot acts on. Anything else is bandwidth and noise.
		"allowed_updates": []string{"message", "callback_query"},
	}, &out)
	return out, err
}

// SendOptions are the optional extras on a send.
type SendOptions struct {
	Keyboard *InlineKeyboard
	// Markdown asks Telegram to parse the text as MarkdownV2. Off by default,
	// because unescaped user or model text breaks the parse and Telegram then
	// rejects the whole message.
	Markdown bool
}

// Send posts a message and returns the id, which a streaming reply needs so it
// can edit what it already sent.
func (c *Client) Send(ctx context.Context, chatID int64, text string, opts *SendOptions) (int64, error) {
	params := map[string]any{"chat_id": chatID, "text": clampText(text)}
	if opts != nil {
		if opts.Keyboard != nil {
			params["reply_markup"] = opts.Keyboard
		}
		if opts.Markdown {
			params["parse_mode"] = "MarkdownV2"
		}
	}
	var out sentMessage
	if err := c.call(ctx, "sendMessage", params, &out); err != nil {
		return 0, err
	}
	return out.MessageID, nil
}

// Edit replaces the text of a message already sent. Telegram rejects an edit
// that would not change anything, which is expected during streaming and left
// for the caller to ignore.
func (c *Client) Edit(ctx context.Context, chatID, messageID int64, text string, opts *SendOptions) error {
	params := map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       clampText(text),
	}
	if opts != nil {
		if opts.Keyboard != nil {
			params["reply_markup"] = opts.Keyboard
		}
		if opts.Markdown {
			params["parse_mode"] = "MarkdownV2"
		}
	}
	return c.call(ctx, "editMessageText", params, nil)
}

// AnswerCallback acknowledges a button tap. Telegram spins the button until
// this arrives, so it is sent even when the answer is empty.
func (c *Client) AnswerCallback(ctx context.Context, id, text string) error {
	return c.call(ctx, "answerCallbackQuery", map[string]any{
		"callback_query_id": id,
		"text":              text,
	}, nil)
}

// Rich messages (Bot API 10.1) are how a reply keeps its formatting.
//
// The model writes Markdown, and sending that as plain text is why a heading
// arrived as literal ** and a code fence as three backticks. The older
// alternatives are both bad for this: MarkdownV2 rejects the entire message over
// a single unescaped character, of which model output is full, and HTML means
// writing a Markdown-to-HTML converter that then has to be kept honest against
// partial text mid-stream. A rich message takes GitHub Flavored Markdown
// directly, which is the dialect already in hand, and renders headings, lists,
// tables and fenced code natively.
//
// Both calls are used only for the model's own reply. Everything the bot says
// itself stays plain text: those lines carry file paths and tool names, and
// running them through a Markdown parser would italicise a path with
// underscores in it for no gain.

// SendRich posts the reply with its formatting intact.
func (c *Client) SendRich(ctx context.Context, chatID int64, markdown string, opts *SendOptions) (int64, error) {
	params := map[string]any{
		"chat_id":      chatID,
		"rich_message": InputRichMessage{Markdown: clampRich(markdown)},
	}
	if opts != nil && opts.Keyboard != nil {
		params["reply_markup"] = opts.Keyboard
	}
	var out sentMessage
	if err := c.call(ctx, "sendRichMessage", params, &out); err != nil {
		return 0, err
	}
	return out.MessageID, nil
}

// DraftRich streams a partial rich message, the formatted twin of Draft. The
// same rules apply: ephemeral, and equal draft ids animate.
func (c *Client) DraftRich(ctx context.Context, chatID, draftID int64, markdown string) error {
	return c.call(ctx, "sendRichMessageDraft", map[string]any{
		"chat_id":      chatID,
		"draft_id":     draftID,
		"rich_message": InputRichMessage{Markdown: clampRich(markdown)},
	}, nil)
}

// Draft streams a partial message, which is how a reply animates in as it is
// written instead of arriving in edited chunks.
//
// Three things about it decide how it has to be used, and all three are the
// API's, not ours. The draft is **ephemeral**: Telegram shows it for about
// thirty seconds and then it is gone, so a finished reply must still be sent as
// a real message to persist. Successive calls with the same draftID **animate**,
// so one id per reply and it must be non-zero. And it is a **private-chat**
// method, so a bot used in a group has to fall back to editing a real message.
func (c *Client) Draft(ctx context.Context, chatID, draftID int64, text string) error {
	return c.call(ctx, "sendMessageDraft", map[string]any{
		"chat_id":  chatID,
		"draft_id": draftID,
		"text":     clampText(text),
	}, nil)
}

// SendChatAction shows "typing" while a turn runs, so a slow reply does not
// look like a dead bot.
func (c *Client) SendChatAction(ctx context.Context, chatID int64, action string) error {
	return c.call(ctx, "sendChatAction", map[string]any{
		"chat_id": chatID,
		"action":  action,
	}, nil)
}

// maxMessageRunes is Telegram's per-message ceiling. Going over is a hard
// rejection, so text is cut here rather than losing the whole message.
const maxMessageRunes = 4096

// maxRichRunes is the ceiling on a rich message, which is far higher than a
// plain one: a long answer that would have been truncated now arrives whole.
const maxRichRunes = 32768

// clampRich trims a rich message to what Telegram will accept. Same rune-safe
// cut as clampText, against the rich limit.
func clampRich(s string) string {
	const notice = "\n\n… (truncated)"
	r := []rune(s)
	if len(r) <= maxRichRunes {
		return s
	}
	keep := maxRichRunes - len([]rune(notice))
	return strings.TrimRight(string(r[:keep]), " \n") + notice
}

// clampText trims a message to what Telegram will accept, on a rune boundary so
// a multi-byte character is never sliced in half, and says that it did.
func clampText(s string) string {
	const notice = "\n… (truncated)"
	r := []rune(s)
	if len(r) <= maxMessageRunes {
		return s
	}
	keep := maxMessageRunes - len([]rune(notice))
	return strings.TrimRight(string(r[:keep]), " \n") + notice
}
