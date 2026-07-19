package natsclient

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Handler processes each raw message. The consumer ACKs every message
// regardless of what the handler does — dispatch.Dispatcher.Handle never
// returns an error and has its own retry-once-then-give-up logic; NATS-level
// redelivery here would only risk double-processing, not add any recovery
// this handler doesn't already do itself.
type Handler func(data []byte)

// Consumer durably consumes soulman.thinking.request via JetStream. This
// replaces the original core-NATS ephemeral Subscribe, which silently
// dropped any message published while action-svc wasn't running — the
// root cause of a real incident (see
// docs/superpowers/specs/2026-07-18-pipeline-debugging-tools-design.md).
type Consumer struct {
	js           jetstream.JetStream
	handler      Handler
	consumerName string
	subject      string
	cc           jetstream.ConsumeContext
}

// NewConsumer builds a Consumer against an already-connected nc (shared
// with action-svc's other NATS usage, per main.go's existing single-nc
// pattern). consumerName must be unique per environment sharing the
// THINKING_REQUEST stream (e.g. "action-svc" prod, "action-svc-dev" dev) —
// JetStream identifies a durable consumer by (stream, name).
func NewConsumer(nc *nats.Conn, consumerName, subject string, h Handler) (*Consumer, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("natsclient: jetstream: %w", err)
	}
	return &Consumer{js: js, handler: h, consumerName: consumerName, subject: subject}, nil
}

// Start ensures the THINKING_REQUEST stream exists (idempotent — safe even
// if thinking-svc's publisher already created it), then starts consuming
// subject in the NATS library's own goroutine. Returns once the
// subscription is established; messages arrive asynchronously. Call Close
// to stop.
func (c *Consumer) Start(ctx context.Context) error {
	stream, err := c.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     "THINKING_REQUEST",
		Subjects: []string{"soulman.thinking.request", "soulman.dev.thinking.request"},
		MaxAge:   30 * 24 * time.Hour,
	})
	if err != nil {
		return fmt.Errorf("natsclient: ensure THINKING_REQUEST stream: %w", err)
	}

	cons, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Name:          c.consumerName,
		Durable:       c.consumerName,
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: c.subject,
	})
	if err != nil {
		return fmt.Errorf("natsclient: create consumer %s: %w", c.consumerName, err)
	}

	cc, err := cons.Consume(func(msg jetstream.Msg) {
		c.handler(msg.Data())
		msg.Ack()
	})
	if err != nil {
		return fmt.Errorf("natsclient: consume: %w", err)
	}

	c.cc = cc
	log.Printf("nats: consuming THINKING_REQUEST stream as %q (subject %q)", c.consumerName, c.subject)
	return nil
}

func (c *Consumer) Close() {
	if c.cc != nil {
		c.cc.Stop()
	}
}
