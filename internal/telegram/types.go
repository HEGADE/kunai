// Package telegram drives a kunai session from a Telegram bot.
//
// The bot long-polls Telegram's API outbound, so kunai still exposes nothing to
// the internet and needs no inbound hole: the same machine that runs your
// sessions reaches out for its own messages. That is what makes this usable
// without Tailscale on the phone, which is the point of it.
//
// What crosses the wire is deliberately narrow. Telegram carries the
// conversation and the controls; it does not carry file contents or command
// output. See render.go, which owns that line.
package telegram

import "encoding/json"

// apiResponse wraps every Bot API reply.
type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
	ErrorCode   int             `json:"error_code"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters,omitempty"`
}

// Update is one event from getUpdates. Exactly one of the payloads is set.
type Update struct {
	UpdateID int64          `json:"update_id"`
	Message  *Message       `json:"message,omitempty"`
	Callback *CallbackQuery `json:"callback_query,omitempty"`
}

// Message is an incoming chat message. Only the fields the bot acts on.
type Message struct {
	MessageID int64  `json:"message_id"`
	Chat      Chat   `json:"chat"`
	From      *User  `json:"from,omitempty"`
	Text      string `json:"text,omitempty"`
}

type Chat struct {
	ID int64 `json:"id"`
}

type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	Username  string `json:"username,omitempty"`
	FirstName string `json:"first_name,omitempty"`
}

// CallbackQuery is a tap on an inline button, which is how a permission ask is
// answered.
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    *User    `json:"from,omitempty"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data,omitempty"`
}

// InputRichMessage is a rich message's body (Bot API 10.1). Exactly one of Html
// or Markdown may be set; we always use Markdown, because that is already what
// the model writes.
type InputRichMessage struct {
	Markdown string `json:"markdown,omitempty"`
	Html     string `json:"html,omitempty"`
}

// InlineKeyboard is the button grid attached to a message: rows of buttons.
type InlineKeyboard struct {
	Rows [][]InlineButton `json:"inline_keyboard"`
}

type InlineButton struct {
	Text string `json:"text"`
	Data string `json:"callback_data,omitempty"`
	URL  string `json:"url,omitempty"`
}

// sentMessage is the slice of a sent message we keep, so a streaming reply can
// edit the message it already posted.
type sentMessage struct {
	MessageID int64 `json:"message_id"`
	Chat      Chat  `json:"chat"`
}
