package natspublish_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"soulman/common"
	"soulman/perception-svc/natspublish"
)

func natsURL() string {
	if u := os.Getenv("NATS_URL"); u != "" {
		return u
	}
	return "nats://localhost:4222"
}

// TestNew_UnreachableNATS_DoesNotBlock does not require a live NATS — it
// verifies the spec's "HTTP server still starts" requirement: New() must
// return quickly (not hang, not error) even against an address nothing is
// listening on.
func TestNew_UnreachableNATS_DoesNotBlock(t *testing.T) {
	start := time.Now()
	pub, err := natspublish.New("nats://127.0.0.1:1", "soulman.stimulus.raw")
	if err != nil {
		t.Fatalf("New should not error on unreachable NATS (retry is async): %v", err)
	}
	defer pub.Close()

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("New blocked for %v, want a fast return", elapsed)
	}
	if pub.Status() != "disconnected" {
		t.Errorf("Status() = %q, want disconnected", pub.Status())
	}
}

func TestPublisher_PublishAndStatus(t *testing.T) {
	url := natsURL()

	probe, err := nats.Connect(url)
	if err != nil {
		t.Skipf("NATS not available (%v) — set NATS_URL to run this test", err)
	}
	probe.Close()

	pub, err := natspublish.New(url, "soulman.stimulus.raw")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer pub.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && pub.Status() != "connected" {
		time.Sleep(50 * time.Millisecond)
	}
	if pub.Status() != "connected" {
		t.Fatalf("Status() = %q, want connected", pub.Status())
	}

	s := &common.Stimulus{
		StimulusID: fmt.Sprintf("pub-test-%d", time.Now().UnixNano()),
		ReceivedAt: time.Now().UTC(),
		Channel:    "folder-watcher",
		Source:     common.Source{Identity: "folder-watcher", Authenticated: true, AuthMethod: "system"},
		Content:    common.Content{ContentType: "text", RawText: "smoke", RawPayload: json.RawMessage(`{}`)},
		Hints:      common.Hints{Priority: "high", Tags: []string{"error", "folder-watcher"}},
		Override:   common.Override{Params: json.RawMessage(`{}`)},
	}

	if err := pub.Publish(context.Background(), s); err != nil {
		t.Fatalf("Publish: %v", err)
	}
}
