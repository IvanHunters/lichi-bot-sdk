// Package lichibot is a minimal Go SDK for the Lichi Bot API (P1). A bot
// "connects and listens" by long-polling getUpdates; the Run loop hides that
// behind an onUpdate callback. Replies go through SendMessage. The transport is
// plain HTTPS to <baseURL>/bot/<token>/<method> (Telegram-method-compatible).
//
// Example (echo bot):
//
//	c := lichibot.New("https://lichi.chat", token)
//	c.Run(context.Background(), func(c *lichibot.Client, u lichibot.Update) {
//		if u.Message != nil && u.Message.Text != "" {
//			_ = c.SendMessage(u.Message.From, "echo: "+u.Message.Text)
//		}
//	})
package lichibot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	// Media carries attachments the user sent (photos, documents, video, voice,
	// round notes). Download the bytes with Client.Download(ctx, m.From, md.ID).
	Media []Media `json:"media,omitempty"`
	// Checklist is non-nil when the message is a shared checklist (a to-do list)
	// instead of plain text.
	Checklist *Checklist `json:"checklist,omitempty"`
}

// Media is one attachment on an incoming message. Use the kind helpers to branch.
type Media struct {
	ID          string `json:"id"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size,omitempty"`
	Filename    string `json:"filename,omitempty"`
	DurationMs  int    `json:"duration_ms,omitempty"`
	Circle      bool   `json:"circle,omitempty"` // a round video note
}

func (m Media) IsImage() bool { return strings.HasPrefix(m.ContentType, "image/") }
func (m Media) IsVideo() bool { return strings.HasPrefix(m.ContentType, "video/") }
func (m Media) IsVoice() bool { return strings.HasPrefix(m.ContentType, "audio/") }
func (m Media) IsFile() bool {
	return !m.IsImage() && !m.IsVideo() && !m.IsVoice()
}

// Checklist is a shared to-do list carried by a message.
type Checklist struct {
	Title string          `json:"title"`
	Items []ChecklistItem `json:"items"`
}

// ChecklistItem is one line of a checklist.
type ChecklistItem struct {
	ID      string `json:"id"`
	Text    string `json:"text"`
	Checked bool   `json:"checked"`
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
// CallEvent is a call_event update: the outcome of a call the bot placed with
// CallUser. Status is "answered", "rejected", or "ended"; for "ended", Reason is
// "completed" (the clip played out), "no_answer" (the ring timed out), or
// "hangup" (the user hung up).
type CallEvent struct {
	CallID string `json:"call_id"`
	UserID string `json:"user_id"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	InlineQuery   *InlineQuery   `json:"inline_query"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
	CallEvent     *CallEvent     `json:"call_event"`
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

// SendVideo uploads a video (video/*) to chatID with an optional caption, shown
// as an inline video bubble.
func (c *Client) SendVideo(ctx context.Context, chatID, filename, contentType string, data []byte, caption string) error {
	return c.sendMedia(ctx, "sendVideo", chatID, filename, contentType, data, caption, nil)
}

// SendVoice uploads a voice note (audio/*) to chatID. durationMs > 0 makes the
// client render the waveform voice bubble rather than a plain file.
func (c *Client) SendVoice(ctx context.Context, chatID, filename, contentType string, data []byte, caption string, durationMs int) error {
	return c.sendMedia(ctx, "sendVoice", chatID, filename, contentType, data, caption,
		map[string]string{"duration": strconv.Itoa(durationMs)})
}

// CallUser places a voice call to chatID that plays ogg (Opus in an Ogg
// container, 48 kHz — encode with `ffmpeg -i in.mp3 -c:a libopus -ac 1 -ar 48000
// out.ogg`) once the user answers, then hangs up. It returns the new call_id.
// The direction gate applies: the user must have written to the bot first, else
// this fails with a 403. The call's outcome arrives on GetUpdates as an Update
// with a CallEvent (answered / rejected / ended).
func (c *Client) CallUser(ctx context.Context, chatID string, ogg []byte) (string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("chat_id", chatID); err != nil {
		return "", err
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="voice"; filename="clip.ogg"`)
	h.Set("Content-Type", "audio/ogg")
	part, err := mw.CreatePart(h)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(ogg); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}
	url := c.baseURL + "/bot/" + c.token + "/callUser"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var env apiEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return "", fmt.Errorf("lichibot: decode callUser: %w", err)
	}
	if !env.OK {
		return "", fmt.Errorf("lichibot: callUser: %s (code %d)", env.Description, env.ErrorCode)
	}
	var out struct {
		CallID string `json:"call_id"`
	}
	if err := json.Unmarshal(env.Result, &out); err != nil {
		return "", fmt.Errorf("lichibot: callUser result: %w", err)
	}
	return out.CallID, nil
}

// Download fetches the bytes of a received attachment. owner is the media owner
// (use the incoming Message.From), id is Media.ID. Authenticated by the bot
// token — the bot may download anything a user sent it (and its own uploads).
func (c *Client) Download(ctx context.Context, owner, id string) ([]byte, error) {
	url := c.baseURL + "/bot/" + c.token + "/file/" + owner + "/" + id
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("lichibot: download %s/%s: status %d", owner, id, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// SendChecklist sends a shared to-do list (title + items) to chatID. Items are
// created unchecked; either party can tick them.
func (c *Client) SendChecklist(ctx context.Context, chatID, title string, items []string) error {
	return c.call(ctx, "sendChecklist",
		map[string]any{"chat_id": chatID, "title": title, "items": items}, nil)
}

// MediaUpload is one file for SendMediaGroup.
type MediaUpload struct {
	Filename    string
	ContentType string
	Data        []byte
}

// SendMediaGroup sends several files as one album message with an optional
// caption (max 10 files — extras are ignored by the server).
func (c *Client) SendMediaGroup(ctx context.Context, chatID, caption string, files []MediaUpload) error {
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
	for i, f := range files {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition",
			fmt.Sprintf(`form-data; name="file%d"; filename=%q`, i, f.Filename))
		if f.ContentType != "" {
			h.Set("Content-Type", f.ContentType)
		}
		part, err := mw.CreatePart(h)
		if err != nil {
			return err
		}
		if _, err := part.Write(f.Data); err != nil {
			return err
		}
	}
	if err := mw.Close(); err != nil {
		return err
	}
	url := c.baseURL + "/bot/" + c.token + "/sendMediaGroup"
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
		return fmt.Errorf("lichibot: decode sendMediaGroup: %w", err)
	}
	if !env.OK {
		return fmt.Errorf("lichibot: sendMediaGroup: %s (code %d)", env.Description, env.ErrorCode)
	}
	return nil
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
