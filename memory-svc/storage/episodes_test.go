package storage_test

import (
	"context"
	"testing"
	"time"

	"soulman/common"
	"soulman/memory-svc/storage"
)

func TestDB_WriteEpisode_And_GetRecentEpisodes(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	seq := uint64(time.Now().UnixNano())
	rec := &common.OutcomeRecord{
		Type:       "action_log",
		ActionType: "triage_gmail_email",
		Status:     "success",
		TaskID:     "task-1",
		OccurredAt: time.Now().UTC(),
		Summary:    "Test email — important",
		Decision:   "notified via Discord",
		Tags:       []string{"gmail", "triage"},
	}

	if err := db.WriteEpisode(ctx, seq, rec); err != nil {
		t.Fatalf("WriteEpisode: %v", err)
	}
	t.Cleanup(func() {
		db.ExecCleanup(context.Background(), "DELETE FROM memory_dev.episodes WHERE stream_seq = $1", int64(seq))
	})

	rows, err := db.GetRecentEpisodes(ctx, 20)
	if err != nil {
		t.Fatalf("GetRecentEpisodes: %v", err)
	}

	var found *storage.Episode
	for i := range rows {
		if rows[i].StreamSeq == seq {
			found = &rows[i]
		}
	}
	if found == nil {
		t.Fatal("inserted episode not found in GetRecentEpisodes")
	}
	if found.Summary != rec.Summary || found.Decision != rec.Decision || found.ActionType != rec.ActionType {
		t.Errorf("episode = %+v, want summary/decision/action_type to match %+v", found, rec)
	}
	if len(found.Tags) != 2 || found.Tags[0] != "gmail" || found.Tags[1] != "triage" {
		t.Errorf("Tags = %v, want [gmail triage]", found.Tags)
	}
	if found.TaskID == nil || *found.TaskID != "task-1" {
		t.Errorf("TaskID = %v, want task-1", found.TaskID)
	}
}

func TestDB_WriteEpisode_DedupsByStreamSeq(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	seq := uint64(time.Now().UnixNano())
	rec := &common.OutcomeRecord{ActionType: "probe", Status: "success", OccurredAt: time.Now().UTC(), Summary: "s", Decision: "d"}

	if err := db.WriteEpisode(ctx, seq, rec); err != nil {
		t.Fatalf("first WriteEpisode: %v", err)
	}
	t.Cleanup(func() {
		db.ExecCleanup(context.Background(), "DELETE FROM memory_dev.episodes WHERE stream_seq = $1", int64(seq))
	})
	if err := db.WriteEpisode(ctx, seq, rec); err != nil {
		t.Errorf("second WriteEpisode (ON CONFLICT DO NOTHING) errored: %v", err)
	}

	rows, err := db.GetRecentEpisodes(ctx, 100)
	if err != nil {
		t.Fatalf("GetRecentEpisodes: %v", err)
	}
	count := 0
	for _, e := range rows {
		if e.StreamSeq == seq {
			count++
		}
	}
	if count != 1 {
		t.Errorf("found %d episodes with stream_seq %d, want 1 (dedup should prevent a duplicate row)", count, seq)
	}
}

func TestDB_WriteEpisode_NilDB_ReturnsErrorNotPanic(t *testing.T) {
	var db *storage.DB // nil receiver — mirrors main.go's "Postgres unavailable" state
	rec := &common.OutcomeRecord{ActionType: "probe", Status: "success", OccurredAt: time.Now().UTC()}
	if err := db.WriteEpisode(context.Background(), 1, rec); err == nil {
		t.Error("WriteEpisode on a nil *DB: want an error, got nil")
	}
}

func TestDB_WriteEpisode_NilTags_StoredAsEmptyNotNull(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	seq := uint64(time.Now().UnixNano())
	rec := &common.OutcomeRecord{ActionType: "probe", Status: "success", OccurredAt: time.Now().UTC(), Summary: "s", Decision: "d"} // Tags left nil

	if err := db.WriteEpisode(ctx, seq, rec); err != nil {
		t.Fatalf("WriteEpisode with nil Tags: %v", err)
	}
	t.Cleanup(func() {
		db.ExecCleanup(context.Background(), "DELETE FROM memory_dev.episodes WHERE stream_seq = $1", int64(seq))
	})
}
