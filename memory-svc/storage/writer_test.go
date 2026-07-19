package storage_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"soulman/common"
	"soulman/memory-svc/storage"
)

func TestWriter_Write_FileOnly_WhenDBNil(t *testing.T) {
	fl, err := storage.NewFileLog(t.TempDir(), storage.DefaultMaxFileSize)
	if err != nil {
		t.Fatalf("NewFileLog: %v", err)
	}
	defer fl.Close()

	w := storage.NewWriter(fl, nil)
	s := newTestStimulus("write-no-db-01")

	if err := w.Write(context.Background(), s); err != nil {
		t.Fatalf("Write with nil DB should not error: %v", err)
	}

	pending, _ := fl.ScanPending()
	if len(pending) != 1 || pending[0].StimulusID != "write-no-db-01" {
		t.Errorf("pending = %v, expected [write-no-db-01]", pending)
	}
}

func TestWriter_Write_MarkedSynced_WhenDBSucceeds(t *testing.T) {
	db := testDB(t) // skips if Postgres unavailable
	fl, err := storage.NewFileLog(t.TempDir(), storage.DefaultMaxFileSize)
	if err != nil {
		t.Fatalf("NewFileLog: %v", err)
	}
	defer fl.Close()

	id := fmt.Sprintf("write-synced-%d", time.Now().UnixNano())
	w := storage.NewWriter(fl, db)
	s := &common.Stimulus{
		StimulusID: id,
		ReceivedAt: time.Now().UTC(),
		Channel:    "test",
		Content:    common.Content{RawPayload: json.RawMessage(`{}`)},
		Override:   common.Override{Params: json.RawMessage(`{}`)},
	}

	t.Cleanup(func() {
		db.ExecCleanup(context.Background(), "DELETE FROM memory_dev.raw_inputs WHERE stimulus_id = $1", id)
	})

	if err := w.Write(context.Background(), s); err != nil {
		t.Fatalf("Write: %v", err)
	}

	pending, _ := fl.ScanPending()
	for _, p := range pending {
		if p.StimulusID == id {
			t.Errorf("stimulus %s still in pending after successful DB write", id)
		}
	}
}

func TestWriter_ReplayPending(t *testing.T) {
	db := testDB(t)
	fl, err := storage.NewFileLog(t.TempDir(), storage.DefaultMaxFileSize)
	if err != nil {
		t.Fatalf("NewFileLog: %v", err)
	}
	defer fl.Close()

	// Simulate a stimulus that was written to file but not synced (e.g. DB was down)
	id := fmt.Sprintf("replay-%d", time.Now().UnixNano())
	s := &common.Stimulus{
		StimulusID: id,
		ReceivedAt: time.Now().UTC(),
		Channel:    "test",
		Content:    common.Content{RawPayload: json.RawMessage(`{}`)},
		Override:   common.Override{Params: json.RawMessage(`{}`)},
	}
	if err := fl.AppendStimulus(s); err != nil {
		t.Fatalf("AppendStimulus: %v", err)
	}

	t.Cleanup(func() {
		db.ExecCleanup(context.Background(), "DELETE FROM memory_dev.raw_inputs WHERE stimulus_id = $1", id)
	})

	w := storage.NewWriter(fl, db)
	if err := w.ReplayPending(context.Background()); err != nil {
		t.Fatalf("ReplayPending: %v", err)
	}

	// After replay, no more pending entries
	pending, _ := fl.ScanPending()
	for _, p := range pending {
		if p.StimulusID == id {
			t.Errorf("stimulus %s still pending after replay", id)
		}
	}
}
