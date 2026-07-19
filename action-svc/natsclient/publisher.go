package natsclient

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"soulman/common"
)

// Publisher publishes outcome records to the configured subject
// (soulman.memory.write by default) via JetStream — durable, so records
// aren't lost even before memory-svc's consumer processes them (kept
// durable via MEMORY_WRITE's 30-day retention either way).
type Publisher struct {
	js      jetstream.JetStream
	subject string
}

// NewPublisher builds a Publisher against an already-connected nc, ensuring
// the MEMORY_WRITE stream exists (idempotent create-or-update).
func NewPublisher(ctx context.Context, nc *nats.Conn, subject string) (*Publisher, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("natsclient: jetstream: %w", err)
	}

	_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     "MEMORY_WRITE",
		Subjects: []string{"soulman.memory.write", "soulman.dev.memory.write"},
		MaxAge:   30 * 24 * time.Hour,
	})
	if err != nil {
		return nil, fmt.Errorf("natsclient: ensure MEMORY_WRITE stream: %w", err)
	}

	return &Publisher{js: js, subject: subject}, nil
}

// PublishOutcome fire-and-forgets (from the caller's perspective — it's
// still a durable JetStream publish under the hood) rec to the configured
// subject. rec.Type is forced to "action_log" so callers don't need to set
// it themselves.
func (p *Publisher) PublishOutcome(rec common.OutcomeRecord) error {
	rec.Type = "action_log"
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("natsclient: marshal outcome: %w", err)
	}
	if _, err := p.js.Publish(context.Background(), p.subject, b); err != nil {
		return fmt.Errorf("natsclient: publish outcome: %w", err)
	}
	return nil
}
