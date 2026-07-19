package natsconsumer_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"soulman/common"
	"soulman/memory-svc/natsconsumer"
)

type mockEpisodeWriter struct {
	mu       sync.Mutex
	received []*common.OutcomeRecord
}

func (m *mockEpisodeWriter) WriteEpisode(_ context.Context, _ uint64, rec *common.OutcomeRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.received = append(m.received, rec)
	return nil
}

func (m *mockEpisodeWriter) hasTaskID(taskID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.received {
		if r.TaskID == taskID {
			return true
		}
	}
	return false
}

func TestMemoryWriteConsumer_ReceivesMessage(t *testing.T) {
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

	consName := fmt.Sprintf("test-mw-%d", time.Now().UnixNano())
	w := &mockEpisodeWriter{}
	cons, err := natsconsumer.NewMemoryWriteConsumer(url, consName, "soulman.memory.write", w)
	if err != nil {
		t.Fatalf("NewMemoryWriteConsumer: %v", err)
	}
	defer cons.Close()

	if err := cons.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	taskID := fmt.Sprintf("mw-test-%d", time.Now().UnixNano())
	rec := common.OutcomeRecord{Type: "action_log", ActionType: "probe", Status: "success", TaskID: taskID, OccurredAt: time.Now().UTC()}
	b, _ := json.Marshal(rec)
	if _, err := js.Publish(ctx, "soulman.memory.write", b); err != nil {
		t.Fatalf("publish: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if w.hasTaskID(taskID) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("outcome record %s not received by writer within 5 seconds", taskID)
}

func TestMemoryWriteConsumer_BadJSON_IsACKedAndSkipped(t *testing.T) {
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

	consName := fmt.Sprintf("test-mw-bad-%d", time.Now().UnixNano())
	w := &mockEpisodeWriter{}
	cons, err := natsconsumer.NewMemoryWriteConsumer(url, consName, "soulman.memory.write", w)
	if err != nil {
		t.Fatalf("NewMemoryWriteConsumer: %v", err)
	}
	defer cons.Close()

	if err := cons.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := js.Publish(ctx, "soulman.memory.write", []byte("not json")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	time.Sleep(2 * time.Second)

	taskID := fmt.Sprintf("mw-after-bad-%d", time.Now().UnixNano())
	rec := common.OutcomeRecord{Type: "action_log", ActionType: "probe", Status: "success", TaskID: taskID}
	b, _ := json.Marshal(rec)
	js.Publish(ctx, "soulman.memory.write", b)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if w.hasTaskID(taskID) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("consumer did not recover after bad JSON message")
}

func TestMemoryWriteConsumer_UnknownType_IsACKedAndSkipped(t *testing.T) {
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

	consName := fmt.Sprintf("test-mw-type-%d", time.Now().UnixNano())
	w := &mockEpisodeWriter{}
	cons, err := natsconsumer.NewMemoryWriteConsumer(url, consName, "soulman.memory.write", w)
	if err != nil {
		t.Fatalf("NewMemoryWriteConsumer: %v", err)
	}
	defer cons.Close()

	if err := cons.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	taskID := fmt.Sprintf("mw-unknown-type-%d", time.Now().UnixNano())
	rec := common.OutcomeRecord{Type: "future_type", ActionType: "probe", Status: "success", TaskID: taskID}
	b, _ := json.Marshal(rec)
	if _, err := js.Publish(ctx, "soulman.memory.write", b); err != nil {
		t.Fatalf("publish: %v", err)
	}

	time.Sleep(2 * time.Second)
	if w.hasTaskID(taskID) {
		t.Errorf("writer should not have received a record with an unknown type")
	}
}

type countingErrEpisodeWriter struct {
	mu    sync.Mutex
	count int
}

func (w *countingErrEpisodeWriter) WriteEpisode(_ context.Context, _ uint64, _ *common.OutcomeRecord) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.count++
	return errors.New("simulated write failure")
}

func (w *countingErrEpisodeWriter) Count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.count
}

func TestMemoryWriteConsumer_WriteError_NaksMessage(t *testing.T) {
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

	consName := fmt.Sprintf("test-mw-nak-%d", time.Now().UnixNano())
	w := &countingErrEpisodeWriter{}
	cons, err := natsconsumer.NewMemoryWriteConsumer(url, consName, "soulman.memory.write", w)
	if err != nil {
		t.Fatalf("NewMemoryWriteConsumer: %v", err)
	}
	defer cons.Close()

	if err := cons.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	rec := common.OutcomeRecord{Type: "action_log", ActionType: "probe", Status: "success", TaskID: fmt.Sprintf("mw-nak-%d", time.Now().UnixNano())}
	b, _ := json.Marshal(rec)
	if _, err := js.Publish(ctx, "soulman.memory.write", b); err != nil {
		t.Fatalf("publish: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if w.Count() >= 2 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Errorf("expected WriteEpisode to be called >= 2 times (NAK + redelivery), got %d", w.Count())
}
