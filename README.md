# lichi-bot-sdk

A minimal Go SDK for the [Lichi](https://lichi.chat) Bot API. The request and
response shapes are compatible with the Telegram Bot API, so if you have written
Telegram bots this will feel familiar.

Full API reference: **https://docs.lichi.chat**

## Install

```bash
go get github.com/IvanHunters/lichi-bot-sdk
```

```go
import lichibot "github.com/IvanHunters/lichi-bot-sdk"
```

## Get a token

Message **@botfather** in the Lichi app, send `/newbot`, pick a name and a
`@username`. You get a token of the form `<botUID>:<secret>`. Keep it secret;
`/revoke` rotates it.

## Quickstart: echo bot

```go
package main

import (
	"context"
	"log"
	"os"

	lichibot "github.com/IvanHunters/lichi-bot-sdk"
)

func main() {
	c := lichibot.New("https://lichi.chat", os.Getenv("LICHI_BOT_TOKEN"))
	ctx := context.Background()

	// Run long-polls getUpdates and calls the callback for each update,
	// blocking until ctx is cancelled.
	log.Fatal(c.Run(ctx, func(c *lichibot.Client, u lichibot.Update) {
		if u.Message != nil && u.Message.Text != "" {
			_ = c.SendMessage(ctx, u.Message.From, "echo: "+u.Message.Text)
		}
	}))
}
```

Run it:

```bash
LICHI_BOT_TOKEN=<botUID>:<secret> go run ./examples/echo
```

## How a bot connects

A bot receives messages by long-polling `getUpdates`. `Run` hides that loop; if
you want control, call `GetUpdates` yourself — it advances the cursor so each
update is returned once. Only **one** consumer should poll a given bot at a time.

Every `Update` has exactly one of `Message`, `CallbackQuery` or `InlineQuery`
set. Reply with `SendMessage` (or the media/markup methods below).

## Inline buttons

```go
kb := &lichibot.InlineKeyboard{Rows: [][]lichibot.InlineButton{
	{{Text: "Open site", URL: "https://lichi.chat"}},
	{{Text: "Ping", CallbackData: "ping", Color: "#2ea6ff"}},
}}
_ = c.SendMessageMarkup(ctx, chatID, "Menu:", kb)
```

When the user taps a `CallbackData` button you get an `Update.CallbackQuery`.
Answer it (optional toast) and, for menu navigation, edit the message in place:

```go
if cb := u.CallbackQuery; cb != nil {
	_ = c.AnswerCallback(ctx, cb.ID, "Pong", false)
	_ = c.EditMessageText(ctx, cb.From, cb.MessageID, "You pressed Ping", nil)
}
```

## Media

```go
data, _ := os.ReadFile("pic.png")
_ = c.SendPhoto(ctx, chatID, "pic.png", "image/png", data, "caption")
_ = c.SendDocument(ctx, chatID, "report.pdf", "application/pdf", pdf, "")
_ = c.SendVoice(ctx, chatID, "note.ogg", "audio/ogg", ogg, "", 4200) // duration ms
```

## Voice calls

A bot can call a user who has written to it first and play a pre-recorded audio
clip into the call, then hang up. The clip may be any common format (mp3, m4a,
wav, ogg/opus) — the server transcodes it:

```go
audio, _ := os.ReadFile("hello.mp3")
callID, _ := c.CallUser(ctx, chatID, audio)
```

The call's outcome arrives on `GetUpdates` as an `Update.CallEvent`:

```go
if ce := u.CallEvent; ce != nil {
	// ce.Status: "answered" | "rejected" | "ended"
	// for "ended", ce.Reason: "completed" | "no_answer" | "hangup"
}
```

## Inline mode

When a user types `@yourbot <query>` in any chat you get an
`Update.InlineQuery`. Answer with a list of results:

```go
if iq := u.InlineQuery; iq != nil {
	_ = c.AnswerInlineQuery(ctx, iq.ID, []lichibot.InlineResult{
		{Type: "article", ID: "1", Title: "Send text", MessageText: "hello"},
	})
}
```

## Methods

| Method | Purpose |
| --- | --- |
| `New(baseURL, token)` | Create a client (`baseURL` is `https://lichi.chat`) |
| `GetMe(ctx)` | The bot's own identity |
| `SendMessage(ctx, chatID, text)` | Send text (`chatID` = UID or `@username`) |
| `SendMessageMarkup(ctx, chatID, text, kb)` | Send text with inline buttons |
| `EditMessageText(ctx, chatID, msgID, text, kb)` | Rewrite a message (and its buttons) |
| `EditMessageReplyMarkup(ctx, chatID, msgID, kb)` | Replace a message's buttons (`nil` clears) |
| `AnswerCallback(ctx, cbID, text, showAlert)` | Answer a tapped callback button |
| `AnswerInlineQuery(ctx, iqID, results)` | Answer an inline query |
| `SendPhoto` / `SendDocument` / `SendVideo` / `SendVoice` | Upload media |
| `CallUser(ctx, chatID, ogg)` | Place a voice call that plays an Opus clip |
| `Download(ctx, owner, id)` | Fetch an attachment the user sent |
| `SetCommands(ctx, cmds)` | Set the `/` command menu |
| `GetUpdates(ctx, timeout)` / `Run(ctx, onUpdate)` | Receive updates |

`chatID` is a user UID (take `update.Message.From` to reply to the sender) or a
public `@username`.

## License

MIT. See [LICENSE](LICENSE).
