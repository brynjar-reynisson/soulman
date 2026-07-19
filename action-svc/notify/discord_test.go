package notify_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"soulman/action-svc/notify"
)

func TestDiscordNotifier_Send_PostsToMessagesEndpoint(t *testing.T) {
	var mu sync.Mutex
	var gotPath, gotAuth, gotContent string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		gotContent = body["content"]
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := notify.NewDiscordNotifier("test-token", "12345")
	n.BaseURL = srv.URL

	if err := n.Send("hello world"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotPath != "/channels/12345/messages" {
		t.Errorf("path = %q, want /channels/12345/messages", gotPath)
	}
	if gotAuth != "Bot test-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bot test-token")
	}
	if gotContent != "hello world" {
		t.Errorf("content = %q, want %q", gotContent, "hello world")
	}
}

func TestDiscordNotifier_Send_NonOKStatus_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"message":"rate limited"}`))
	}))
	defer srv.Close()

	n := notify.NewDiscordNotifier("test-token", "12345")
	n.BaseURL = srv.URL

	if err := n.Send("hi"); err == nil {
		t.Error("expected error on non-2xx response")
	}
}

func TestDiscordNotifier_Send_LongMessage_SplitsAtBlankLines(t *testing.T) {
	var mu sync.Mutex
	var received []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		received = append(received, body["content"])
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := notify.NewDiscordNotifier("test-token", "12345")
	n.BaseURL = srv.URL

	var entries []string
	for i := 0; i < 40; i++ {
		entries = append(entries, strings.Repeat("x", 60))
	}
	message := strings.Join(entries, "\n\n")

	if err := n.Send(message); err != nil {
		t.Fatalf("Send: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) < 2 {
		t.Fatalf("expected message to be split into multiple sends, got %d", len(received))
	}
	for _, chunk := range received {
		if len(chunk) > 2000 {
			t.Errorf("chunk length %d exceeds 2000-char limit", len(chunk))
		}
		for _, part := range strings.Split(chunk, "\n\n") {
			if part != strings.Repeat("x", 60) {
				t.Errorf("chunk contains a mangled entry: %q", part)
			}
		}
	}
}

// Real Discord API integration test — DISCORD_BOT_TOKEN and
// DISCORD_CHANNEL_ID are not present in this environment (provided later by
// the repo owner), so this must skip cleanly rather than fail.
func TestDiscordNotifier_RealAPI_RequiresCredentials(t *testing.T) {
	token := os.Getenv("DISCORD_BOT_TOKEN")
	channel := os.Getenv("DISCORD_CHANNEL_ID")
	if token == "" || channel == "" {
		t.Skip("DISCORD_BOT_TOKEN / DISCORD_CHANNEL_ID not set — skipping live Discord integration test")
	}

	n := notify.NewDiscordNotifier(token, channel)
	if err := n.Send("action-svc integration test — please ignore"); err != nil {
		t.Fatalf("Send to real Discord API: %v", err)
	}
}
