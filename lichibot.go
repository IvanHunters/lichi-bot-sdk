// Package lichibot is a minimal Go SDK for the Lichi Bot API (P1). A bot
// "connects and listens" by long-polling getUpdates; the Run loop hides that
// behind an onUpdate callback. Replies go through SendMessage. The transport is
// plain HTTPS to <baseURL>/bot/<token>/<method> (Telegram-method-compatible).
//
// Example (echo bot):
//
//	c := lichibot.New("https://lichi.chat", token)
//	ctx := context.Background()
//	c.Run(ctx, func(c *lichibot.Client, u lichibot.Update) {
//		if u.Message != nil && u.Message.Text != "" {
//			_ = c.SendMessage(ctx, u.Message.From, "echo: "+u.Message.Text)
//		}
//	})
package lichibot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// Client talks to one bot's API endpoint.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
	offset  int64 // highest update_id seen + 1 (long-poll cursor)
}

// New returns a client. baseURL is the server origin (e.g.
// "https://lichi.chat"); token is "<botUID>:<secret>" from @botfather.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

// Message is a Telegram-shaped incoming message (subset).
type Message struct {
	MsgID string `json:"msg_id"`
	From  string `json:"from"`
	To    string `json:"to"`
	Text  string `json:"text"`
}

// InlineQuery is an inline_query update: the user typed "@bot <query>".
type InlineQuery struct {
	ID     string `json:"id"`
	From   string `json:"from"`
	Query  string `json:"query"`
	Offset string `json:"offset"`
}

// InlineResult is one item returned from AnswerInlineQuery. Type is one of
// article|photo|gif|video|document. For media results set URL (+ ThumbURL);
// for article/text set MessageText.
type InlineResult struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	Title       string `json:"title,omitempty"`
	ThumbURL    string `json:"thumb_url,omitempty"`
	URL         string `json:"url,omitempty"`
	MimeType    string `json:"mime_type,omitempty"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	MessageText string `json:"message_text,omitempty"`
}

// InlineButton is one keyboard button: set URL (open a link) or CallbackData
// (tap back to the bot). Color is an optional client hint (our extension).
type InlineButton struct {
	Text         string `json:"text"`
	URL          string `json:"url,omitempty"`
	CallbackData string `json:"callback_data,omitempty"`
	Color        string `json:"color,omitempty"`
}

// InlineKeyboard is the reply_markup grid of buttons under a message.
type InlineKeyboard struct {
	Rows [][]InlineButton `json:"inline_keyboard"`
}

// CallbackQuery is a callback_query update: the user tapped a callback button.
type CallbackQuery struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	MessageID string `json:"message_id"`
	Data      string `json:"data"`
}

// Update is one queue entry.
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	InlineQuery   *InlineQuery   `json:"inline_query"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

// User is the getMe result (subset).
type User struct {
	ID       string `json:"id"`
	IsBot    bool   `json:"is_bot"`
	Username string `json:"username"`
}

type apiEnvelope struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
	ErrorCode   int             `json:"error_code"`
}

// call POSTs a JSON request to one method and decodes result into out.
func (c *Client) call(ctx context.Context, method string, req, out any) error {
	var body []byte
	if req != nil {
		b, err := json.Marshal(req)
		if err != nil {
			return err
		}
		body = b
	}
	url := c.baseURL + "/bot/" + c.token + "/" + method
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var env apiEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return fmt.Errorf("lichibot: decode %s: %w", method, err)
	}
	if !env.OK {
		return fmt.Errorf("lichibot: %s: %s (code %d)", method, env.Description, env.ErrorCode)
	}
	if out != nil && len(env.Result) > 0 {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

// GetMe returns the bot's own identity.
func (c *Client) GetMe(ctx context.Context) (User, error) {
	var u User
	err := c.call(ctx, "getMe", nil, &u)
	return u, err
}

// SendMessage sends text to chatID (a user UID or "@username").
func (c *Client) SendMessage(ctx context.Context, chatID, text string) error {
	return c.call(ctx, "sendMessage", map[string]any{"chat_id": chatID, "text": text}, nil)
}

// SetCommands sets the bot's command list.
func (c *Client) SetCommands(ctx context.Context, cmds []map[string]string) error {
	return c.call(ctx, "setMyCommands", map[string]any{"commands": cmds}, nil)
}

// SendMessageMarkup sends text to chatID with an inline keyboard (buttons).
func (c *Client) SendMessageMarkup(ctx context.Context, chatID, text string, kb *InlineKeyboard) error {
	return c.call(ctx, "sendMessage", map[string]any{"chat_id": chatID, "text": text, "reply_markup": kb}, nil)
}

// EditMessageText rewrites a message's text (and buttons, when kb is non-nil).
func (c *Client) EditMessageText(ctx context.Context, chatID, messageID, text string, kb *InlineKeyboard) error {
	req := map[string]any{"chat_id": chatID, "message_id": messageID, "text": text}
	if kb != nil {
		req["reply_markup"] = kb
	}
	return c.call(ctx, "editMessageText", req, nil)
}

// EditMessageReplyMarkup replaces a message's inline keyboard (kb nil clears it).
func (c *Client) EditMessageReplyMarkup(ctx context.Context, chatID, messageID string, kb *InlineKeyboard) error {
	return c.call(ctx, "editMessageReplyMarkup", map[string]any{"chat_id": chatID, "message_id": messageID, "reply_markup": kb}, nil)
}

// AnswerCallback answers a callback_query (from Update.CallbackQuery) with an
// optional toast; showAlert makes the client show a modal instead of a toast.
func (c *Client) AnswerCallback(ctx context.Context, callbackQueryID, text string, showAlert bool) error {
	return c.call(ctx, "answerCallbackQuery", map[string]any{
		"callback_query_id": callbackQueryID,
		"text":              text,
		"show_alert":        showAlert,
	}, nil)
}

// SendPhoto uploads an image to chatID with an optional caption. contentType
// must be image/* (e.g. "image/png"). filename is for the upload part only.
func (c *Client) SendPhoto(ctx context.Context, chatID, filename, contentType string, data []byte, caption string) error {
	return c.sendMedia(ctx, "sendPhoto", chatID, filename, contentType, data, caption, nil)
}

// SendDocument uploads an arbitrary file to chatID with an optional caption.
// The original filename is preserved on the file bubble.
func (c *Client) SendDocument(ctx context.Context, chatID, filename, contentType string, data []byte, caption string) error {
	return c.sendMedia(ctx, "sendDocument", chatID, filename, contentType, data, caption, nil)
}

// SendVoice uploads a voice note (audio/*) to chatID. durationMs > 0 makes the
// client render the waveform voice bubble rather than a plain file.
func (c *Client) SendVoice(ctx context.Context, chatID, filename, contentType string, data []byte, caption string, durationMs int) error {
	return c.sendMedia(ctx, "sendVoice", chatID, filename, contentType, data, caption,
		map[string]string{"duration": strconv.Itoa(durationMs)})
}

// sendMedia posts a multipart request (chat_id + caption + optional extra
// fields + one file part) to a media method.
func (c *Client) sendMedia(ctx context.Context, method, chatID, filename, contentType string, data []byte, caption string, extra map[string]string) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("chat_id", chatID); err != nil {
		return err
	}
	if caption != "" {
		if err := mw.WriteField("caption", caption); err != nil {
			return err
		}
	}
	for k, v := range extra {
		if err := mw.WriteField(k, v); err != nil {
			return err
		}
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	part, err := mw.CreatePart(h)
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}
	url := c.baseURL + "/bot/" + c.token + "/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var env apiEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return fmt.Errorf("lichibot: decode %s: %w", method, err)
	}
	if !env.OK {
		return fmt.Errorf("lichibot: %s: %s (code %d)", method, env.Description, env.ErrorCode)
	}
	return nil
}

// AnswerInlineQuery answers an inline_query (from Update.InlineQuery) with a
// list of results the client will show in its inline panel.
func (c *Client) AnswerInlineQuery(ctx context.Context, inlineQueryID string, results []InlineResult) error {
	return c.call(ctx, "answerInlineQuery", map[string]any{
		"inline_query_id": inlineQueryID,
		"results":         results,
	}, nil)
}

// GetUpdates long-polls for new updates (timeout seconds) and advances the
// internal cursor so each update is returned once.
func (c *Client) GetUpdates(ctx context.Context, timeout int) ([]Update, error) {
	var ups []Update
	err := c.call(ctx, "getUpdates", map[string]any{
		"offset":  c.offset,
		"timeout": timeout,
	}, &ups)
	if err != nil {
		return nil, err
	}
	for _, u := range ups {
		if u.UpdateID >= c.offset {
			c.offset = u.UpdateID + 1
		}
	}
	return ups, nil
}

// Run is the long-poll loop: it calls onUpdate for each update until ctx is
// cancelled. Transient errors back off and retry.
func (c *Client) Run(ctx context.Context, onUpdate func(*Client, Update)) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		ups, err := c.GetUpdates(ctx, 25)
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
			}
			continue
		}
		for _, u := range ups {
			onUpdate(c, u)
		}
	}
}
