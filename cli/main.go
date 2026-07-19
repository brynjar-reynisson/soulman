package main

import (
	"context"
	"fmt"
	"os"

	"soulman/cli/client"
)

const (
	prodURL = "http://localhost:9001"
	devURL  = "http://localhost:9011"
)

func main() {
	args, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	baseURL := prodURL
	if args.Dev {
		baseURL = devURL
	}

	if args.Mode == "discord-history" {
		token := os.Getenv("DISCORD_BOT_TOKEN")
		channelID := os.Getenv("DISCORD_CHANNEL_ID")
		if token == "" || channelID == "" {
			fmt.Fprintln(os.Stderr, "DISCORD_BOT_TOKEN and DISCORD_CHANNEL_ID must both be set in the environment")
			os.Exit(1)
		}

		messages, err := fetchDiscordHistory(context.Background(), token, channelID, args.DiscordHistoryLimit)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		// Discord returns newest-first; print chronologically (oldest-first),
		// easiest to read top-to-bottom in a terminal.
		for i := len(messages) - 1; i >= 0; i-- {
			m := messages[i]
			fmt.Printf("[%s] %s: %s\n", m.Timestamp.Format("2006-01-02 15:04:05"), m.Author, m.Content)
		}
		return
	}

	if args.Mode == "inject" {
		fileBytes, err := os.ReadFile(args.InjectFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "reading %s: %v\n", args.InjectFile, err)
			os.Exit(1)
		}
		id, err := client.SendRaw(baseURL, fileBytes)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("injected (stimulus_id: %s)\n", id)
		return
	}

	id, err := client.Send(baseURL, client.Request{
		Text:     args.Text,
		Mode:     args.Mode,
		Priority: args.Priority,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	verb := "sent"
	if args.Mode == "note" {
		verb = "logged"
	}
	fmt.Printf("%s (stimulus_id: %s)\n", verb, id)
}
