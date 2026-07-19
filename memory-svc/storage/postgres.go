package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"soulman/common"
)

type RawInput struct {
	StimulusID     string          `json:"stimulus_id"`
	ReceivedAt     time.Time       `json:"received_at"`
	Channel        string          `json:"channel"`
	NormalizedText *string         `json:"normalized_text,omitempty"`
	RawPayload     json.RawMessage `json:"raw_payload"`
	OverrideCmd    *string         `json:"override_cmd,omitempty"`
}

type DB struct {
	pool   *pgxpool.Pool
	schema string
}

func NewDB(ctx context.Context, connStr, schema string) (*DB, error) {
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		return nil, fmt.Errorf("postgres: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	return &DB{pool: pool, schema: schema}, nil
}

func (db *DB) InsertRawInput(ctx context.Context, s *common.Stimulus) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("postgres: marshal stimulus: %w", err)
	}

	var normalizedText *string
	if s.Content.RawText != "" {
		normalizedText = &s.Content.RawText
	}

	_, err = db.pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.raw_inputs
			(stimulus_id, received_at, occurred_at, channel, source_identity,
			 raw_payload, normalized_text, is_override, override_cmd)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (stimulus_id) DO NOTHING
	`, db.schema),
		s.StimulusID,
		s.ReceivedAt,
		s.OccurredAt,
		s.Channel,
		s.Source.Identity,
		raw,
		normalizedText,
		s.Override.IsOverride,
		s.Override.Command,
	)
	if err != nil {
		return fmt.Errorf("postgres: insert raw_input %s: %w", s.StimulusID, err)
	}
	return nil
}

func (db *DB) GetRecent(ctx context.Context, limit int) ([]RawInput, error) {
	rows, err := db.pool.Query(ctx, fmt.Sprintf(`
		SELECT stimulus_id, received_at, channel, normalized_text,
		       raw_payload, override_cmd
		FROM %s.raw_inputs
		WHERE forgotten_at IS NULL
		ORDER BY received_at DESC
		LIMIT $1
	`, db.schema), limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: query recent: %w", err)
	}
	defer rows.Close()

	var results []RawInput
	for rows.Next() {
		var r RawInput
		if err := rows.Scan(
			&r.StimulusID, &r.ReceivedAt, &r.Channel, &r.NormalizedText,
			&r.RawPayload, &r.OverrideCmd,
		); err != nil {
			return nil, fmt.Errorf("postgres: scan: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (db *DB) Close() {
	db.pool.Close()
}

// ExecCleanup runs an arbitrary SQL statement — used only by tests for cleanup.
func (db *DB) ExecCleanup(ctx context.Context, sql string, args ...interface{}) {
	db.pool.Exec(ctx, sql, args...)
}
