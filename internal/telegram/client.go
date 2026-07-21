package telegram

import (
	"bytes"
	"context"
	"encoding/json"
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
}

// NewClient returns a client for the given bot token. The timeout has to clear
// the long poll, or every getUpdates would cancel itself.
func NewClient(token string) *Client {
	return &Client{
		token: token,
		http:  &http.Client{Timeout: (longPollSeconds + 15) * time.Second},
	}
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
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	var r apiResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("%s: unreadable reply: %w", method, err)
	}
	if !r.OK {
		// Telegram reports its own failures in a 200 body, so the status code
		// alone would call a rejected request a success.
		return &APIError{Method: method, Code: r.ErrorCode, Description: r.Description}
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
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram %s: %d %s", e.Method, e.Code, e.Description)
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
