package storage_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"soulman/common"
	"soulman/memory-svc/storage"
)

func testDB(t *testing.T) *storage.DB {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@localhost:54322/postgres"
	}
	ctx := context.Background()
	db, err := storage.NewDB(ctx, dbURL, "memory_dev")
	if err != nil {
		t.Skipf("postgres not available (%v) — set DATABASE_URL to run DB tests", err)
	}
	t.Cleanup(db.Close)
	return db
}

func TestDB_InsertRawInput(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	id := fmt.Sprintf("test-%d", time.Now().UnixNano())
	s := &common.Stimulus{
		StimulusID: id,
		ReceivedAt: time.Now().UTC(),
		Channel:    "test",
		Source:     common.Source{Identity: "test-runner"},
		Content:    common.Content{RawText: "integration test", RawPayload: json.RawMessage(`{}`)},
		Hints:      common.Hints{Priority: "normal"},
		Override:   common.Override{Params: json.RawMessage(`{}`)},
	}

	if err := db.InsertRawInput(ctx, s); err != nil {
		t.Fatalf("InsertRawInput: %v", err)
	}

	t.Cleanup(func() {
		db.ExecCleanup(context.Background(), "DELETE FROM memory_dev.raw_inputs WHERE stimulus_id = $1", id)
	})

	// Idempotency: second insert should not error
	if err := db.InsertRawInput(ctx, s); err != nil {
		t.Errorf("second insert (ON CONFLICT DO NOTHING) errored: %v", err)
	}
}

func TestDB_GetRecent(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	id1 := fmt.Sprintf("recent-a-%d", time.Now().UnixNano())
	id2 := fmt.Sprintf("recent-b-%d", time.Now().UnixNano())

	for _, id := range []string{id1, id2} {
		s := &common.Stimulus{
			StimulusID: id,
			ReceivedAt: time.Now().UTC(),
			Channel:    "test",
			Content:    common.Content{RawPayload: json.RawMessage(`{}`)},
			Override:   common.Override{Params: json.RawMessage(`{}`)},
		}
		if err := db.InsertRawInput(ctx, s); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}

	t.Cleanup(func() {
		for _, id := range []string{id1, id2} {
			db.ExecCleanup(context.Background(), "DELETE FROM memory_dev.raw_inputs WHERE stimulus_id = $1", id)
		}
	})

	rows, err := db.GetRecent(ctx, 5)
	if err != nil {
		t.Fatalf("GetRecent: %v", err)
	}
	if len(rows) < 2 {
		t.Errorf("GetRecent returned %d rows, want >= 2", len(rows))
	}
}
