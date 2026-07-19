package natsclient_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"soulman/action-svc/natsclient"
)

type recordingHandler struct {
	mu       sync.Mutex
	received [][]byte
}

func (h *recordingHandler) handle(data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.received = append(h.received, data)
}

func (h *recordingHandler) contains(want string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.received {
		if string(r) == want {
			return true
		}
	}
	return false
}

// countOf returns how many times the exact payload want was received. Used
// instead of a global call counter because soulman.thinking.request is a
// shared, 30-day-retained subject — other test runs (and other tests in
// this file) publish to it too, so a global counter would be inflated by
// unrelated messages regardless of whether exactly-once ACKing held for the
// message this test cares about.
func (h *recordingHandler) countOf(want string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	count := 0
	for _, r := range h.received {
		if string(r) == want {
			count++
		}
	}
	return count
}

func TestConsumer_ReceivesMessage(t *testing.T) {
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

	consName := fmt.Sprintf("test-action-%d", time.Now().UnixNano())
	h := &recordingHandler{}
	cons, err := natsclient.NewConsumer(nc, consName, "soulman.thinking.request", h.handle)
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	defer cons.Close()

	if err := cons.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	payload := fmt.Sprintf(`{"task_id":"action-cons-test-%d"}`, time.Now().UnixNano())
	if _, err := js.Publish(ctx, "soulman.thinking.request", []byte(payload)); err != nil {
		t.Fatalf("publish: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if h.contains(payload) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("payload not received by handler within 5 seconds")
}

func TestConsumer_HandlerPanicsNever_StillACKsExactlyOnce(t *testing.T) {
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

	consName := fmt.Sprintf("test-action-ack-%d", time.Now().UnixNano())
	h := &recordingHandler{}
	cons, err := natsclient.NewConsumer(nc, consName, "soulman.thinking.request", h.handle)
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	defer cons.Close()

	if err := cons.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	payload := fmt.Sprintf(`{"task_id":"action-ack-test-%d"}`, time.Now().UnixNano())
	if _, err := js.Publish(ctx, "soulman.thinking.request", []byte(payload)); err != nil {
		t.Fatalf("publish: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if h.contains(payload) {
			// Give a moment for any (incorrect) redelivery to arrive too,
			// then verify THIS payload was seen exactly once — counting
			// only occurrences of this specific payload, not a global
			// counter, since the shared, 30-day-retained subject may carry
			// other messages from other test runs.
			time.Sleep(1500 * time.Millisecond)
			count := h.countOf(payload)
			if count != 1 {
				t.Errorf("payload seen %d times, want exactly 1 (must ACK exactly once)", count)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("payload not received within 5 seconds")
}

func TestConsumer_SurvivesRestartAfterDowntime(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	consName := fmt.Sprintf("test-action-restart-%d", time.Now().UnixNano())

	// First "run": create the durable consumer so it exists, then
	// immediately close it — simulating action-svc starting once
	// (establishing the durable consumer) and then going down before this
	// message is published.
	firstHandler := &recordingHandler{}
	firstCons, err := natsclient.NewConsumer(nc, consName, "soulman.thinking.request", firstHandler.handle)
	if err != nil {
		t.Fatalf("NewConsumer (first run): %v", err)
	}
	if err := firstCons.Start(ctx); err != nil {
		t.Fatalf("Start (first run): %v", err)
	}
	firstCons.Close()

	// "Downtime": publish while nothing is consuming.
	payload := fmt.Sprintf(`{"task_id":"action-restart-test-%d"}`, time.Now().UnixNano())
	if _, err := js.Publish(ctx, "soulman.thinking.request", []byte(payload)); err != nil {
		t.Fatalf("publish during downtime: %v", err)
	}

	// "Restart": a brand new Consumer instance, same durable name, picks up
	// where the durable consumer's tracked position left off.
	secondHandler := &recordingHandler{}
	secondCons, err := natsclient.NewConsumer(nc, consName, "soulman.thinking.request", secondHandler.handle)
	if err != nil {
		t.Fatalf("NewConsumer (restart): %v", err)
	}
	defer secondCons.Close()
	if err := secondCons.Start(ctx); err != nil {
		t.Fatalf("Start (restart): %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if secondHandler.contains(payload) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("message published during downtime was not delivered after restart — durable consumer lost its position")
}
