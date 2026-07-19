package natsclient_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"soulman/thinking-svc/natsclient"
)

func TestPublisher_Publish_DeliversToSubject(t *testing.T) {
	url := natsURL()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer nc.Close()

	sub, err := nc.SubscribeSync("soulman.thinking.request")
	if err != nil {
		t.Fatalf("SubscribeSync: %v", err)
	}
	defer sub.Unsubscribe()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pub, err := natsclient.NewPublisher(ctx, url, "soulman.thinking.request")
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	defer pub.Close()

	type testRequest struct {
		CorrelationID string `json:"correlation_id"`
		ActionHint    string `json:"action_hint"`
	}
	req := testRequest{CorrelationID: "corr-001", ActionHint: "append_daily_report_entry"}

	if err := pub.Publish(ctx, req); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	msg, err := sub.NextMsg(5 * time.Second)
	if err != nil {
		t.Fatalf("NextMsg: %v", err)
	}

	var got testRequest
	if err := json.Unmarshal(msg.Data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.CorrelationID != "corr-001" {
		t.Errorf("CorrelationID = %q, want corr-001", got.CorrelationID)
	}
	if got.ActionHint != "append_daily_report_entry" {
		t.Errorf("ActionHint = %q, want append_daily_report_entry", got.ActionHint)
	}
}

func TestNewPublisher_CreatesThinkingRequestStream(t *testing.T) {
	url := natsURL()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer nc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pub, err := natsclient.NewPublisher(ctx, url, "soulman.thinking.request")
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	defer pub.Close()

	// If the stream wasn't created, this publish (which the constructor
	// already exercised once, but we verify explicitly here too) would
	// fail with a "no stream matches subject" style JetStream error.
	if err := pub.Publish(ctx, map[string]string{"probe": "ok"}); err != nil {
		t.Errorf("Publish after NewPublisher: %v, want nil (THINKING_REQUEST stream should exist)", err)
	}
}
