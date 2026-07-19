package main

// Deliberately duplicated from perception-svc/discordread rather than
// imported: cli is its own Go module with no existing cross-module
// dependency on perception-svc (unlike common, which every service already
// imports via a replace directive), and adding one just for this ~40-line
// debug helper isn't worth it. Keep this in sync by hand if
// perception-svc/discordread's shape changes.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

type discordMessage struct {
	ID        string
	Author    string
	Content   string
	Timestamp time.Time
}

type discordAPIMessage struct {
	ID        string `json:"id"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
	Author    struct {
		Username string `json:"username"`
	} `json:"author"`
}

func fetchDiscordHistory(ctx context.Context, botToken, channelID string, limit int) ([]discordMessage, error) {
	url := fmt.Sprintf("https://discord.com/api/v10/channels/%s/messages?limit=%s", channelID, strconv.Itoa(limit))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("discord-history: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bot "+botToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("discord-history: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discord-history: status %d", resp.StatusCode)
	}

	var raw []discordAPIMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("discord-history: decode response: %w", err)
	}

	messages := make([]discordMessage, 0, len(raw))
	for _, m := range raw {
		ts, err := time.Parse(time.RFC3339, m.Timestamp)
		if err != nil {
			ts = time.Time{}
		}
		messages = append(messages, discordMessage{
			ID:        m.ID,
			Author:    m.Author.Username,
			Content:   m.Content,
			Timestamp: ts,
		})
	}
	return messages, nil
}
