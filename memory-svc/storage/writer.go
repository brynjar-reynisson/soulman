package storage

import (
	"context"
	"fmt"
	"log/slog"

	"soulman/common"
)

type Writer struct {
	fl *FileLog
	db *DBHolder
}

func NewWriter(fl *FileLog, db *DBHolder) *Writer {
	return &Writer{fl: fl, db: db}
}

// Write persists a Stimulus. The file write is blocking and must succeed before
// ACKing NATS. DB failure is non-fatal: the file entry is left as pending and
// will be replayed on next startup.
func (w *Writer) Write(ctx context.Context, s *common.Stimulus) error {
	if err := w.fl.AppendStimulus(s); err != nil {
		return fmt.Errorf("writer: file append failed: %w", err)
	}

	if w.db == nil || w.db.Get() == nil {
		slog.Warn("writer: DB unavailable, written to file only", "stimulus_id", s.StimulusID)
		return nil
	}

	if err := w.db.InsertRawInput(ctx, s); err != nil {
		slog.Error("writer: DB insert failed, will replay on restart", "stimulus_id", s.StimulusID, "error", err)
		return nil
	}

	if err := w.fl.AppendSynced(s.StimulusID); err != nil {
		// Non-fatal: ON CONFLICT DO NOTHING handles the duplicate on next replay
		slog.Warn("writer: synced marker write failed", "stimulus_id", s.StimulusID, "error", err)
	}

	return nil
}

// ReplayPending scans the file log for unsynced entries and inserts them into
// Postgres. Called on startup before NATS subscription begins.
func (w *Writer) ReplayPending(ctx context.Context) error {
	if w.db == nil || w.db.Get() == nil {
		return nil
	}

	pending, err := w.fl.ScanPending()
	if err != nil {
		return fmt.Errorf("writer: scan pending: %w", err)
	}

	if len(pending) == 0 {
		return nil
	}

	slog.Info("writer: replaying pending file entries to DB", "count", len(pending))

	for _, s := range pending {
		if err := w.db.InsertRawInput(ctx, s); err != nil {
			slog.Error("writer: replay insert failed", "stimulus_id", s.StimulusID, "error", err)
			continue
		}
		if err := w.fl.AppendSynced(s.StimulusID); err != nil {
			slog.Warn("writer: replay synced marker write failed", "stimulus_id", s.StimulusID, "error", err)
		}
	}

	return nil
}
