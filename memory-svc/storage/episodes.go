package storage

import (
	"context"
	"fmt"
	"time"

	"soulman/common"
)

type Episode struct {
	ID         int64     `json:"id"`
	StreamSeq  uint64    `json:"stream_seq"`
	OccurredAt time.Time `json:"occurred_at"`
	ReceivedAt time.Time `json:"received_at"`
	Source     string    `json:"source"`
	ActionType string    `json:"action_type"`
	Status     string    `json:"status"`
	TaskID     *string   `json:"task_id,omitempty"`
	Summary    string    `json:"summary"`
	Decision   string    `json:"decision"`
	Outcome    string    `json:"outcome"`
	Tags       []string  `json:"tags"`
}

// WriteEpisode records an action-svc outcome as an episode. streamSeq is
// the MEMORY_WRITE JetStream message's stream sequence number, used (not
// rec.TaskID, which is sometimes empty) as the dedup key on redelivery.
// Safe to call on a nil *DB (returns an error instead of panicking) so the
// MEMORY_WRITE consumer can NAK-and-retry when Postgres is down, the same
// way the rest of this package treats DB unavailability as non-fatal.
func (db *DB) WriteEpisode(ctx context.Context, streamSeq uint64, rec *common.OutcomeRecord) error {
	if db == nil {
		return fmt.Errorf("postgres: db unavailable")
	}

	var taskID *string
	if rec.TaskID != "" {
		taskID = &rec.TaskID
	}
	tags := rec.Tags
	if tags == nil {
		tags = []string{}
	}

	_, err := db.pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.episodes
			(stream_seq, occurred_at, source, action_type, status, task_id,
			 summary, decision, outcome, tags)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (stream_seq) DO NOTHING
	`, db.schema),
		int64(streamSeq),
		rec.OccurredAt,
		"action-svc",
		rec.ActionType,
		rec.Status,
		taskID,
		rec.Summary,
		rec.Decision,
		rec.Status,
		tags,
	)
	if err != nil {
		return fmt.Errorf("postgres: write episode (stream_seq %d): %w", streamSeq, err)
	}
	return nil
}

func (db *DB) GetRecentEpisodes(ctx context.Context, limit int) ([]Episode, error) {
	rows, err := db.pool.Query(ctx, fmt.Sprintf(`
		SELECT id, stream_seq, occurred_at, received_at, source, action_type,
		       status, task_id, summary, decision, outcome, tags
		FROM %s.episodes
		WHERE forgotten_at IS NULL
		ORDER BY received_at DESC
		LIMIT $1
	`, db.schema), limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: query recent episodes: %w", err)
	}
	defer rows.Close()

	var results []Episode
	for rows.Next() {
		var e Episode
		var streamSeq int64
		if err := rows.Scan(
			&e.ID, &streamSeq, &e.OccurredAt, &e.ReceivedAt, &e.Source,
			&e.ActionType, &e.Status, &e.TaskID, &e.Summary, &e.Decision,
			&e.Outcome, &e.Tags,
		); err != nil {
			return nil, fmt.Errorf("postgres: scan episode: %w", err)
		}
		e.StreamSeq = uint64(streamSeq)
		results = append(results, e)
	}
	return results, rows.Err()
}
