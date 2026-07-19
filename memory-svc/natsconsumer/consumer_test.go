package natsconsumer_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"soulman/common"
	"soulman/memory-svc/natsconsumer"
)

func TestMain(m *testing.M) {
	// Purge any lingering messages from previous test runs so new consumers
	// (which start from the beginning of the stream by default) aren't flooded
	// with old NAK'd messages.
	nc, err := nats.Connect(natsURL())
	if err == nil {
		if js, err := jetstream.New(nc); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if stream, err := js.Stream(ctx, "STIMULUS"); err == nil {
				stream.Purge(ctx)
			}
			if stream, err := js.Stream(ctx, "MEMORY_WRITE"); err == nil {
				stream.Purge(ctx)
			}
			cancel()
		}
		nc.Close()
	}
	os.Exit(m.Run())
}

type mockWriter struct {
	mu       sync.Mutex
	received []*common.Stimulus
}

func (m *mockWriter) Write(_ context.Context, s *common.Stimulus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.received = append(m.received, s)
	return nil
}

func natsURL() string {
	if u := os.Getenv("NATS_URL"); u != "" {
		return u
	}
	return "nats://localhost:4222"
}

func TestConsumer_ReceivesMessage(t *testing.T) {
	url := natsURL()

	// Connect publisher
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

	// Unique consumer name per test run to avoid position conflicts
	consName := fmt.Sprintf("test-%d", time.Now().UnixNano())

	w := &mockWriter{}
	cons, err := natsconsumer.New(url, consName, "soulman.stimulus.raw", w)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cons.Close()

	if err := cons.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Publish after subscription is established
	id := fmt.Sprintf("cons-test-%d", time.Now().UnixNano())
	s := &common.Stimulus{
		StimulusID: id,
		ReceivedAt: time.Now().UTC(),
		Channel:    "test",
		Source:     common.Source{Identity: "test"},
		Content:    common.Content{RawText: "hi", RawPayload: json.RawMessage(`{}`)},
		Hints:      common.Hints{Priority: "normal"},
		Override:   common.Override{Params: json.RawMessage(`{}`)},
	}
	b, _ := json.Marshal(s)
	if _, err := js.Publish(ctx, "soulman.stimulus.raw", b); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// Wait up to 5s for the writer to receive it
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w.mu.Lock()
		found := false
		for _, r := range w.received {
			if r.StimulusID == id {
				found = true
			}
		}
		w.mu.Unlock()
		if found {
			return // success
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Errorf("stimulus %s not received by writer within 5 seconds", id)
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
	w := &mockWriter{}
	cons, err := natsconsumer.New(url, consName, "soulman.stimulus.raw", w)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cons.Close()

	if err := cons.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Publish invalid JSON — consumer must ACK (not block) and not crash
	if _, err := js.Publish(ctx, "soulman.stimulus.raw", []byte("not json")); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// Give consumer 2s to process the bad message; verify it didn't crash
	time.Sleep(2 * time.Second)

	// Consumer should still be alive (we can publish a valid message and get it)
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
		w.mu.Lock()
		for _, r := range w.received {
			if r.StimulusID == id {
				w.mu.Unlock()
				return
			}
		}
		w.mu.Unlock()
		time.Sleep(100 * time.Millisecond)
	}

	t.Errorf("consumer did not recover after bad JSON message")
}

type countingErrWriter struct {
	mu    sync.Mutex
	count int
}

func (w *countingErrWriter) Write(_ context.Context, _ *common.Stimulus) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.count++
	return errors.New("simulated write failure")
}

func (w *countingErrWriter) Count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.count
}

func TestConsumer_WriteError_NaksMessage(t *testing.T) {
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

	consName := fmt.Sprintf("test-nak-%d", time.Now().UnixNano())
	w := &countingErrWriter{}
	cons, err := natsconsumer.New(url, consName, "soulman.stimulus.raw", w)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cons.Close()

	if err := cons.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	id := fmt.Sprintf("nak-test-%d", time.Now().UnixNano())
	s := &common.Stimulus{
		StimulusID: id,
		ReceivedAt: time.Now().UTC(),
		Channel:    "test",
		Content:    common.Content{RawPayload: json.RawMessage(`{}`)},
		Override:   common.Override{Params: json.RawMessage(`{}`)},
	}
	b, _ := json.Marshal(s)
	if _, err := js.Publish(ctx, "soulman.stimulus.raw", b); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// Wait up to 10s for Write to be called at least twice (initial + one redelivery)
	// This proves the NAK triggered redelivery
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if w.Count() >= 2 {
			return // success: message was NAK'd and redelivered
		}
		time.Sleep(200 * time.Millisecond)
	}

	t.Errorf("expected Write to be called >= 2 times (NAK + redelivery), got %d", w.Count())
}
