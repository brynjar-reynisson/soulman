package natsclient_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"soulman/action-svc/natsclient"
	"soulman/common"
)

func natsURL() string {
	if u := os.Getenv("NATS_URL"); u != "" {
		return u
	}
	return "nats://localhost:4222"
}

// TestConnect_RetriesInBackgroundWhenServerUnreachable proves the core
// behavioral change from Finding 1: previously nats.Connect against an
// unreachable server returned a connection-refused error immediately; now
// RetryOnFailedConnect(true) makes Connect succeed right away (with the
// *nats.Conn reconnecting in the background) instead of failing outright.
// Port 1 on localhost is reserved and nothing listens on it, so this is
// deterministic and requires no live NATS server or timing-dependent wait.
func TestConnect_RetriesInBackgroundWhenServerUnreachable(t *testing.T) {
	conn, err := natsclient.Connect("nats://localhost:1")
	if err != nil {
		t.Fatalf("Connect: want nil error (RetryOnFailedConnect should suppress the initial failure), got %v", err)
	}
	defer conn.Close()
}

func TestPublisher_PublishOutcome(t *testing.T) {
	url := natsURL()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer nc.Close()

	ch := make(chan *nats.Msg, 1)
	sub, err := nc.Subscribe("soulman.memory.write", func(m *nats.Msg) { ch <- m })
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pub, err := natsclient.NewPublisher(ctx, nc, "soulman.memory.write")
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}

	id := fmt.Sprintf("outcome-%d", time.Now().UnixNano())
	rec := common.OutcomeRecord{
		ActionType: "append_daily_report_entry",
		Status:     "success",
		TaskID:     id,
		OccurredAt: time.Now().UTC(),
		Summary:    "Daily report entry appended",
		Decision:   "append_daily_report_entry",
		Tags:       []string{"report"},
	}
	if err := pub.PublishOutcome(rec); err != nil {
		t.Fatalf("PublishOutcome: %v", err)
	}

	select {
	case msg := <-ch:
		var got common.OutcomeRecord
		if err := json.Unmarshal(msg.Data, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got.TaskID != id || got.Status != "success" || got.Type != "action_log" {
			t.Errorf("outcome = %+v, want task_id=%s status=success type=action_log", got, id)
		}
		if got.Summary != rec.Summary || got.Decision != rec.Decision {
			t.Errorf("outcome = %+v, want summary=%q decision=%q", got, rec.Summary, rec.Decision)
		}
	case <-time.After(3 * time.Second):
		t.Error("outcome record not received within 3 seconds")
	}
}

func TestNewPublisher_CreatesMemoryWriteStream(t *testing.T) {
	url := natsURL()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer nc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pub, err := natsclient.NewPublisher(ctx, nc, "soulman.memory.write")
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}

	id := fmt.Sprintf("stream-probe-%d", time.Now().UnixNano())
	rec := common.OutcomeRecord{ActionType: "probe", Status: "success", TaskID: id, OccurredAt: time.Now().UTC()}
	if err := pub.PublishOutcome(rec); err != nil {
		t.Errorf("PublishOutcome after NewPublisher: %v, want nil (MEMORY_WRITE stream should exist)", err)
	}
}
