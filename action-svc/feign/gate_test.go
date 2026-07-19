package feign_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"soulman/action-svc/feign"
)

func TestGate_Enabled(t *testing.T) {
	if g := feign.New(true, "unused"); !g.Enabled() {
		t.Error("Enabled() = false, want true")
	}
	if g := feign.New(false, "unused"); g.Enabled() {
		t.Error("Enabled() = true, want false")
	}
}

func TestGate_Enabled_NilReceiverSafe(t *testing.T) {
	var g *feign.Gate
	if g.Enabled() {
		t.Error("Enabled() on a nil *Gate = true, want false")
	}
}

func TestGate_Record_AppendsJSONLine(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "nested", "feigned-actions.jsonl")
	g := feign.New(true, logPath)

	if err := g.Record("notify", "hello world"); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := g.Record("notify", "second message"); err != nil {
		t.Fatalf("Record: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), string(data))
	}

	var entry struct {
		Kind   string `json:"kind"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("unmarshal line 1: %v", err)
	}
	if entry.Kind != "notify" || entry.Detail != "hello world" {
		t.Errorf("line 1 = %+v, want kind=notify detail=%q", entry, "hello world")
	}
}

type fakeNotifier struct {
	sent []string
	err  error
}

func (f *fakeNotifier) Send(message string) error {
	f.sent = append(f.sent, message)
	return f.err
}

func TestWrapNotifier_Disabled_DelegatesToReal(t *testing.T) {
	real := &fakeNotifier{}
	gate := feign.New(false, filepath.Join(t.TempDir(), "feigned-actions.jsonl"))
	n := feign.WrapNotifier(gate, real)

	if err := n.Send("real message"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(real.sent) != 1 || real.sent[0] != "real message" {
		t.Errorf("real.sent = %v, want [\"real message\"]", real.sent)
	}
}

func TestWrapNotifier_Enabled_RecordsInsteadOfSending(t *testing.T) {
	real := &fakeNotifier{}
	logPath := filepath.Join(t.TempDir(), "feigned-actions.jsonl")
	gate := feign.New(true, logPath)
	n := feign.WrapNotifier(gate, real)

	if err := n.Send("would-be message"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(real.sent) != 0 {
		t.Errorf("real.sent = %v, want empty — real Send should never be called when the gate is enabled", real.sent)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "would-be message") {
		t.Errorf("log file = %q, want it to contain the feigned message", string(data))
	}
}

func TestWrapNotifier_Disabled_RealSendErrorPropagates(t *testing.T) {
	real := &fakeNotifier{err: errors.New("boom")}
	gate := feign.New(false, filepath.Join(t.TempDir(), "feigned-actions.jsonl"))
	n := feign.WrapNotifier(gate, real)

	if err := n.Send("x"); err == nil {
		t.Error("Send: want error from real notifier to propagate, got nil")
	}
}
