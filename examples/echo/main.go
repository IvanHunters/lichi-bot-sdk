// Command echo is a minimal Lichi bot: it replies to every text message with
// the same text. Get a token from @botfather in the Lichi app, then run:
//
//	LICHI_BOT_TOKEN=<botUID>:<secret> go run ./examples/echo
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	lichibot "github.com/IvanHunters/lichi-bot-sdk"
)

func main() {
	token := os.Getenv("LICHI_BOT_TOKEN")
	if token == "" {
		log.Fatal("set LICHI_BOT_TOKEN=<botUID>:<secret> (from @botfather)")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	c := lichibot.New("https://lichi.chat", token)

	me, err := c.GetMe(ctx)
	if err != nil {
		log.Fatalf("getMe: %v", err)
	}
	log.Printf("running as @%s", me.Username)

	// Run blocks until ctx is cancelled (Ctrl-C), long-polling for updates.
	if err := c.Run(ctx, func(c *lichibot.Client, u lichibot.Update) {
		if u.Message == nil || u.Message.Text == "" {
			return
		}
		if err := c.SendMessage(ctx, u.Message.From, "echo: "+u.Message.Text); err != nil {
			log.Printf("reply: %v", err)
		}
	}); err != nil && ctx.Err() == nil {
		log.Fatalf("run: %v", err)
	}
}
