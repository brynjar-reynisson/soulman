package storage

import (
	"context"
	"fmt"
	"log"

	"soulman/common"
)

type Writer struct {
	fl *FileLog
	db *DB
}

func NewWriter(fl *FileLog, db *DB) *Writer {
	return &Writer{fl: fl, db: db}
}

// Write persists a Stimulus. The file write is blocking and must succeed before
// ACKing NATS. DB failure is non-fatal: the file entry is left as pending and
// will be replayed on next startup.
func (w *Writer) Write(ctx context.Context, s *common.Stimulus) error {
	if err := w.fl.AppendStimulus(s); err != nil {
		return fmt.Errorf("writer: file append failed: %w", err)
	}

	if w.db == nil {
		log.Printf("writer: DB unavailable, %s written to file only", s.StimulusID)
		return nil
	}

	if err := w.db.InsertRawInput(ctx, s); err != nil {
		log.Printf("writer: DB insert failed for %s (will replay on restart): %v", s.StimulusID, err)
		return nil
	}

	if err := w.fl.AppendSynced(s.StimulusID); err != nil {
		log.Printf("writer: synced marker failed for %s: %v", s.StimulusID, err)
		// Non-fatal: ON CONFLICT DO NOTHING handles the duplicate on next replay
	}

	return nil
}

// ReplayPending scans the file log for unsynced entries and inserts them into
// Postgres. Called on startup before NATS subscription begins.
func (w *Writer) ReplayPending(ctx context.Context) error {
	if w.db == nil {
		return nil
	}

	pending, err := w.fl.ScanPending()
	if err != nil {
		return fmt.Errorf("writer: scan pending: %w", err)
	}

	if len(pending) == 0 {
		return nil
	}

	log.Printf("writer: replaying %d pending file entries to DB", len(pending))

	for _, s := range pending {
		if err := w.db.InsertRawInput(ctx, s); err != nil {
			log.Printf("writer: replay failed for %s: %v", s.StimulusID, err)
			continue
		}
		if err := w.fl.AppendSynced(s.StimulusID); err != nil {
			log.Printf("writer: replay synced marker failed for %s: %v", s.StimulusID, err)
		}
	}

	return nil
}
