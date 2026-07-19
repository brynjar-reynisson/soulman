// Package discordread reads a Discord channel's message history via
// Discord's REST API, using the same bot token action-svc already uses to
// send. Placed under perception-svc (not action-svc/notify, which stays
// send-only) since a future "Discord as a perception channel" direction
// would naturally build on this same read capability — see
// docs/superpowers/specs/2026-07-18-pipeline-debugging-tools-design.md.
// This package only reads; it never sends anything.
package discordread

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// Message is the minimal shape needed for debugging — not a full Discord
// API model.
type Message struct {
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

// FetchHistory fetches up to limit most-recent messages from channelID
// using botToken, via GET /channels/{id}/messages. Returned in the same
// order Discord returns them (newest-first); callers that want
// chronological order should reverse the slice themselves.
func FetchHistory(ctx context.Context, botToken, channelID string, limit int) ([]Message, error) {
	url := fmt.Sprintf("https://discord.com/api/v10/channels/%s/messages?limit=%s", channelID, strconv.Itoa(limit))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("discordread: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bot "+botToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("discordread: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discordread: status %d", resp.StatusCode)
	}

	var raw []discordAPIMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("discordread: decode response: %w", err)
	}

	messages := make([]Message, 0, len(raw))
	for _, m := range raw {
		ts, err := time.Parse(time.RFC3339, m.Timestamp)
		if err != nil {
			ts = time.Time{}
		}
		messages = append(messages, Message{
			ID:        m.ID,
			Author:    m.Author.Username,
			Content:   m.Content,
			Timestamp: ts,
		})
	}
	return messages, nil
}
