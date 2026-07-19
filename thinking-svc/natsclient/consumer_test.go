package natsclient_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"soulman/common"
	"soulman/thinking-svc/natsclient"
)

type mockHandler struct {
	mu       sync.Mutex
	received []*common.Stimulus
	err      error
}

func (m *mockHandler) Handle(_ context.Context, s *common.Stimulus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.received = append(m.received, s)
	return m.err
}

func (m *mockHandler) countOf(id string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, r := range m.received {
		if r.StimulusID == id {
			count++
		}
	}
	return count
}

func natsURL() string {
	if u := os.Getenv("NATS_URL"); u != "" {
		return u
	}
	return "nats://localhost:4222"
}

func TestConsumer_ReceivesAndHandlesMessage(t *testing.T) {
	url := natsURL()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer nc.Close()

	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	consName := fmt.Sprintf("test-%d", time.Now().UnixNano())
	h := &mockHandler{}
	cons, err := natsclient.NewConsumer(url, consName, "soulman.stimulus.raw", h)
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	defer cons.Close()

	if err := cons.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	id := fmt.Sprintf("cons-test-%d", time.Now().UnixNano())
	s := &common.Stimulus{
		StimulusID: id,
		ReceivedAt: time.Now().UTC(),
		Channel:    "folder-watcher",
		Content:    common.Content{RawText: "hi", RawPayload: json.RawMessage(`{}`)},
		Override:   common.Override{Params: json.RawMessage(`{}`)},
	}
	b, _ := json.Marshal(s)
	if _, err := js.Publish(ctx, "soulman.stimulus.raw", b); err != nil {
		t.Fatalf("publish: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if h.countOf(id) > 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("stimulus %s not received by handler within 5 seconds", id)
}

func TestConsumer_BadJSON_IsACKedAndSkipped(t *testing.T) {
	url := natsURL()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer nc.Close()

	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	consName := fmt.Sprintf("test-bad-%d", time.Now().UnixNano())
	h := &mockHandler{}
	cons, err := natsclient.NewConsumer(url, consName, "soulman.stimulus.raw", h)
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	defer cons.Close()

	if err := cons.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := js.Publish(ctx, "soulman.stimulus.raw", []byte("not json")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	time.Sleep(2 * time.Second)

	id := fmt.Sprintf("after-bad-%d", time.Now().UnixNano())
	s := &common.Stimulus{
		StimulusID: id,
		ReceivedAt: time.Now().UTC(),
		Channel:    "test",
		Content:    common.Content{RawPayload: json.RawMessage(`{}`)},
		Override:   common.Override{Params: json.RawMessage(`{}`)},
	}
	b, _ := json.Marshal(s)
	js.Publish(ctx, "soulman.stimulus.raw", b)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if h.countOf(id) > 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("consumer did not recover after bad JSON message")
}

func TestConsumer_HandlerError_StillACKsExactlyOnce(t *testing.T) {
	url := natsURL()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer nc.Close()

	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	consName := fmt.Sprintf("test-err-%d", time.Now().UnixNano())
	h := &mockHandler{err: fmt.Errorf("boom")}
	cons, err := natsclient.NewConsumer(url, consName, "soulman.stimulus.raw", h)
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	defer cons.Close()

	if err := cons.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	id := fmt.Sprintf("handler-err-%d", time.Now().UnixNano())
	s := &common.Stimulus{
		StimulusID: id,
		ReceivedAt: time.Now().UTC(),
		Channel:    "folder-watcher",
		Content:    common.Content{RawPayload: json.RawMessage(`{}`)},
		Override:   common.Override{Params: json.RawMessage(`{}`)},
	}
	b, _ := json.Marshal(s)
	js.Publish(ctx, "soulman.stimulus.raw", b)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if h.countOf(id) > 0 {
			// Message arrived; give any (incorrect) redelivery a moment to
			// arrive too, then verify the count stayed at exactly 1 — proof
			// the consumer ACKed despite the handler error.
			time.Sleep(1500 * time.Millisecond)
			if count := h.countOf(id); count != 1 {
				t.Errorf("handler invoked %d times for %s, want exactly 1 (must ACK despite handler error)", count, id)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("stimulus %s not received by handler within 3 seconds", id)
}
