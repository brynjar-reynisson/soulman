package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const discordMaxMessageLen = 2000

// DiscordNotifier sends messages via Discord's Bot REST API. BotToken and
// ChannelID come from DISCORD_BOT_TOKEN / DISCORD_CHANNEL_ID — never
// hardcoded, and may be empty in environments where Discord isn't
// configured yet (Send will simply fail, which the caller handles like any
// other notifier failure).
type DiscordNotifier struct {
	BotToken  string
	ChannelID string
	BaseURL   string // overridable in tests; defaults to Discord's API
	client    *http.Client
}

func NewDiscordNotifier(botToken, channelID string) *DiscordNotifier {
	return &DiscordNotifier{
		BotToken:  botToken,
		ChannelID: channelID,
		BaseURL:   "https://discord.com/api/v10",
		client:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (d *DiscordNotifier) Send(message string) error {
	for _, chunk := range splitMessage(message, discordMaxMessageLen) {
		if err := d.sendOne(chunk); err != nil {
			return err
		}
	}
	return nil
}

func (d *DiscordNotifier) sendOne(content string) error {
	url := fmt.Sprintf("%s/channels/%s/messages", d.BaseURL, d.ChannelID)
	body, err := json.Marshal(map[string]string{"content": content})
	if err != nil {
		return fmt.Errorf("notify: marshal discord payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("notify: build discord request: %w", err)
	}
	req.Header.Set("Authorization", "Bot "+d.BotToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("notify: discord request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("notify: discord returned %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// splitMessage splits message into chunks no longer than maxLen, breaking
// only at blank-line (paragraph) boundaries — never mid-entry. If a single
// paragraph itself exceeds maxLen, it is kept whole as an oversized chunk
// rather than truncated (report entries are expected to stay well under the
// limit; this is a defensive fallback, not the common case).
func splitMessage(message string, maxLen int) []string {
	if len(message) <= maxLen {
		return []string{message}
	}

	parts := strings.Split(message, "\n\n")
	var chunks []string
	var current string

	for _, part := range parts {
		candidate := part
		if current != "" {
			candidate = current + "\n\n" + part
		}
		if len(candidate) > maxLen && current != "" {
			chunks = append(chunks, current)
			current = part
		} else {
			current = candidate
		}
	}
	if current != "" {
		chunks = append(chunks, current)
	}
	return chunks
}
