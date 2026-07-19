package storage_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"soulman/common"
	"soulman/memory-svc/storage"
)

func newTestStimulus(id string) *common.Stimulus {
	return &common.Stimulus{
		StimulusID:  id,
		ReceivedAt:  time.Now().UTC(),
		Channel:     "test",
		Source:      common.Source{Identity: "tester"},
		Content:     common.Content{RawText: "hello", RawPayload: json.RawMessage(`{}`)},
		Hints:       common.Hints{Priority: "normal"},
		Override:    common.Override{Params: json.RawMessage(`{}`)},
	}
}

func TestFileLog_AppendAndScanPending(t *testing.T) {
	fl, err := storage.NewFileLog(t.TempDir(), storage.DefaultMaxFileSize)
	if err != nil {
		t.Fatalf("NewFileLog: %v", err)
	}
	defer fl.Close()

	s := newTestStimulus("id-001")
	if err := fl.AppendStimulus(s); err != nil {
		t.Fatalf("AppendStimulus: %v", err)
	}

	pending, err := fl.ScanPending()
	if err != nil {
		t.Fatalf("ScanPending: %v", err)
	}
	if len(pending) != 1 || pending[0].StimulusID != "id-001" {
		t.Errorf("ScanPending = %v, want [{id-001}]", pending)
	}
}

func TestFileLog_SyncedRemovesFromPending(t *testing.T) {
	fl, err := storage.NewFileLog(t.TempDir(), storage.DefaultMaxFileSize)
	if err != nil {
		t.Fatalf("NewFileLog: %v", err)
	}
	defer fl.Close()

	fl.AppendStimulus(newTestStimulus("id-002"))
	fl.AppendSynced("id-002")

	pending, err := fl.ScanPending()
	if err != nil {
		t.Fatalf("ScanPending: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("ScanPending = %v, want empty", pending)
	}
}

func TestFileLog_PartialSync(t *testing.T) {
	fl, err := storage.NewFileLog(t.TempDir(), storage.DefaultMaxFileSize)
	if err != nil {
		t.Fatalf("NewFileLog: %v", err)
	}
	defer fl.Close()

	fl.AppendStimulus(newTestStimulus("id-A"))
	fl.AppendStimulus(newTestStimulus("id-B"))
	fl.AppendSynced("id-A")

	pending, err := fl.ScanPending()
	if err != nil {
		t.Fatalf("ScanPending: %v", err)
	}
	if len(pending) != 1 || pending[0].StimulusID != "id-B" {
		t.Errorf("ScanPending = %v, want [{id-B}]", pending)
	}
}

func TestFileLog_Rotation(t *testing.T) {
	dir := t.TempDir()
	// 512-byte threshold triggers rotation quickly
	fl, err := storage.NewFileLog(dir, 512)
	if err != nil {
		t.Fatalf("NewFileLog: %v", err)
	}
	defer fl.Close()

	// Each stimulus is ~300 bytes; 3 records exceed 512 bytes
	for i := 0; i < 10; i++ {
		s := newTestStimulus(fmt.Sprintf("rotation-%02d", i))
		if err := fl.AppendStimulus(s); err != nil {
			t.Fatalf("AppendStimulus %d: %v", i, err)
		}
	}

	rotated := filepath.Join(dir, "raw_inputs.jsonl.1")
	if _, err := os.Stat(rotated); err != nil {
		t.Errorf("rotation file not found at %s: %v", rotated, err)
	}
}

func TestFileLog_ScanPendingAcrossRotation(t *testing.T) {
	dir := t.TempDir()
	fl, err := storage.NewFileLog(dir, 512)
	if err != nil {
		t.Fatalf("NewFileLog: %v", err)
	}
	defer fl.Close()

	// Write enough to rotate (old entries go to .1), then write more
	for i := 0; i < 5; i++ {
		fl.AppendStimulus(newTestStimulus(fmt.Sprintf("pre-%02d", i)))
	}
	// Write one more to current file, not synced
	fl.AppendStimulus(newTestStimulus("post-00"))

	pending, err := fl.ScanPending()
	if err != nil {
		t.Fatalf("ScanPending: %v", err)
	}
	// All entries across both files should be pending
	if len(pending) == 0 {
		t.Error("expected pending entries across rotation, got none")
	}
}
