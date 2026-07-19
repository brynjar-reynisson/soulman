package natsconsumer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"soulman/common"
)

// EpisodeWriter is satisfied by *storage.DB. Defined here to avoid import cycles.
type EpisodeWriter interface {
	WriteEpisode(ctx context.Context, streamSeq uint64, rec *common.OutcomeRecord) error
}

// nakDelay paces MEMORY_WRITE redelivery on NAK so a persistent failure
// (e.g. DB down, or the target schema missing entirely) doesn't turn into a
// hot pull-NAK-redeliver loop. 5s is the low end of the 5-30s range that's
// enough to meaningfully pace retries during a real outage, while still
// leaving comfortable margin inside TestMemoryWriteConsumer_WriteError_NaksMessage's
// existing 10s poll / 15s context timeout for a second redelivery to land.
const nakDelay = 5 * time.Second

// MemoryWriteConsumer durably consumes the MEMORY_WRITE stream. Unlike
// Consumer (STIMULUS), there is no local file-log/replay layer here — on a
// WriteEpisode error the message is NAK'd and JetStream's own 30-day
// MEMORY_WRITE retention is the durability backstop, since episodes aren't
// the sacred immutable audit log raw_inputs is. See
// docs/superpowers/specs/2026-07-18-memory-episodes-design.md.
type MemoryWriteConsumer struct {
	nc           *nats.Conn
	js           jetstream.JetStream
	writer       EpisodeWriter
	consumerName string
	subject      string
	cc           jetstream.ConsumeContext
	wg           sync.WaitGroup
}

// NewMemoryWriteConsumer connects to NATS. consumerName must be unique per
// environment sharing the MEMORY_WRITE stream (e.g. "memory-svc-episodes"
// for prod, "memory-svc-episodes-dev" for dev) — and distinct from the
// STIMULUS consumer's name, since JetStream identifies a durable consumer
// by (stream, name).
func NewMemoryWriteConsumer(natsURL, consumerName, subject string, w EpisodeWriter) (*MemoryWriteConsumer, error) {
	nc, err := nats.Connect(natsURL)
	if err != nil {
		return nil, fmt.Errorf("nats: connect to %s: %w", natsURL, err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats: jetstream: %w", err)
	}

	return &MemoryWriteConsumer{nc: nc, js: js, writer: w, consumerName: consumerName, subject: subject}, nil
}

// Start ensures the MEMORY_WRITE stream exists (defensively — memory-svc
// might start before action-svc has ever published, the same reasoning
// documented for action-svc's own defensive THINKING_REQUEST ensure in
// docs/superpowers/specs/2026-07-18-pipeline-debugging-tools-design.md),
// then subscribes and processes messages in the NATS library goroutine.
// Returns after the subscription is established; messages arrive
// asynchronously. Call Close to stop.
func (c *MemoryWriteConsumer) Start(ctx context.Context) error {
	_, err := c.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     "MEMORY_WRITE",
		Subjects: []string{"soulman.memory.write", "soulman.dev.memory.write"},
		MaxAge:   30 * 24 * time.Hour,
	})
	if err != nil {
		return fmt.Errorf("nats: ensure MEMORY_WRITE stream: %w", err)
	}

	stream, err := c.js.Stream(ctx, "MEMORY_WRITE")
	if err != nil {
		return fmt.Errorf("nats: get MEMORY_WRITE stream: %w", err)
	}

	cons, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Name:          c.consumerName,
		Durable:       c.consumerName,
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: c.subject,
	})
	if err != nil {
		return fmt.Errorf("nats: create consumer %s: %w", c.consumerName, err)
	}

	cc, err := cons.Consume(func(msg jetstream.Msg) {
		c.wg.Add(1)
		defer c.wg.Done()

		var rec common.OutcomeRecord
		if err := json.Unmarshal(msg.Data(), &rec); err != nil {
			log.Printf("nats: unparseable MEMORY_WRITE message (subject %s), ACKing to skip: %v", msg.Subject(), err)
			msg.Ack()
			return
		}

		if rec.Type != "action_log" {
			log.Printf("nats: MEMORY_WRITE message with unknown type %q, ACKing to skip", rec.Type)
			msg.Ack()
			return
		}

		meta, err := msg.Metadata()
		if err != nil {
			log.Printf("nats: MEMORY_WRITE message metadata unavailable, NAKing for redelivery in %s: %v", nakDelay, err)
			msg.NakWithDelay(nakDelay)
			return
		}

		if err := c.writer.WriteEpisode(context.Background(), meta.Sequence.Stream, &rec); err != nil {
			log.Printf("nats: episode write failed (stream_seq %d), NAKing for redelivery in %s: %v", meta.Sequence.Stream, nakDelay, err)
			msg.NakWithDelay(nakDelay)
			return
		}

		msg.Ack()
	})
	if err != nil {
		return fmt.Errorf("nats: consume: %w", err)
	}

	c.cc = cc
	log.Printf("nats: consuming MEMORY_WRITE stream as %q (subject %q)", c.consumerName, c.subject)
	return nil
}

func (c *MemoryWriteConsumer) Close() {
	if c.cc != nil {
		c.cc.Stop()
	}
	c.wg.Wait()
	c.nc.Drain()
}
