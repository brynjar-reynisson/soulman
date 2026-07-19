package natsclient

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Publisher publishes Action Requests to the configured subject
// (soulman.thinking.request by default) via JetStream — durable, so a
// message survives even if action-svc isn't running to consume it yet.
// This replaces the original core-NATS fire-and-forget publish, which
// caused roughly half of a real incident's triage decisions to be
// silently dropped (see docs/superpowers/specs/2026-07-18-pipeline-debugging-tools-design.md).
type Publisher struct {
	nc      *nats.Conn
	js      jetstream.JetStream
	subject string
}

// NewPublisher connects to natsURL, ensures the THINKING_REQUEST stream
// exists (creating or updating it idempotently — safe even if action-svc
// also ensures the same stream independently), and returns a Publisher
// bound to subject.
func NewPublisher(ctx context.Context, natsURL, subject string) (*Publisher, error) {
	nc, err := nats.Connect(natsURL)
	if err != nil {
		return nil, fmt.Errorf("nats: connect to %s: %w", natsURL, err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats: jetstream: %w", err)
	}

	_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     "THINKING_REQUEST",
		Subjects: []string{"soulman.thinking.request", "soulman.dev.thinking.request"},
		MaxAge:   30 * 24 * time.Hour,
	})
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats: ensure THINKING_REQUEST stream: %w", err)
	}

	return &Publisher{nc: nc, js: js, subject: subject}, nil
}

// Publish marshals v to JSON and publishes it to the configured subject.
// v is typically a *common.ActionRequest; this package accepts any to avoid
// depending on the rules package.
func (p *Publisher) Publish(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("nats: marshal action request: %w", err)
	}
	if _, err := p.js.Publish(ctx, p.subject, b); err != nil {
		return fmt.Errorf("nats: publish to %s: %w", p.subject, err)
	}
	return nil
}

func (p *Publisher) Close() {
	p.nc.Drain()
}
