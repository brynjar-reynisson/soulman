package natsclient

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"soulman/common"
)

// Handler processes each parsed Stimulus. The consumer ACKs every message
// regardless of the returned error — soulman.thinking.request has no
// redelivery mechanism in v1 (see thinking-svc's design spec's Error
// Handling table), so NAKing here would only cause duplicate downstream
// actions without recovering anything.
type Handler interface {
	Handle(ctx context.Context, s *common.Stimulus) error
}

type Consumer struct {
	nc           *nats.Conn
	js           jetstream.JetStream
	handler      Handler
	consumerName string
	subject      string
	cc           jetstream.ConsumeContext
}

// NewConsumer connects to NATS. consumerName must be unique per environment
// sharing the STIMULUS stream (e.g. "thinking-svc" for prod, "thinking-svc-dev"
// for dev) — JetStream identifies a durable consumer by (stream, name), so
// two environments reusing the same name would silently overwrite each
// other's consumer state. subject scopes which messages this consumer
// receives via FilterSubject.
func NewConsumer(natsURL, consumerName, subject string, h Handler) (*Consumer, error) {
	nc, err := nats.Connect(natsURL)
	if err != nil {
		return nil, fmt.Errorf("nats: connect to %s: %w", natsURL, err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats: jetstream: %w", err)
	}

	return &Consumer{nc: nc, js: js, handler: h, consumerName: consumerName, subject: subject}, nil
}

// Start subscribes to the STIMULUS stream and processes messages in the NATS
// library goroutine. Returns after the subscription is established;
// messages arrive asynchronously. Call Close to stop.
func (c *Consumer) Start(ctx context.Context) error {
	stream, err := c.js.Stream(ctx, "STIMULUS")
	if err != nil {
		return fmt.Errorf("nats: get STIMULUS stream: %w", err)
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
		var s common.Stimulus
		if err := json.Unmarshal(msg.Data(), &s); err != nil {
			log.Printf("nats: unparseable stimulus (subject %s), ACKing to skip: %v", msg.Subject(), err)
			msg.Ack()
			return
		}

		if err := c.handler.Handle(ctx, &s); err != nil {
			log.Printf("nats: handling failed for %s, ACKing anyway (no redelivery for thinking.request): %v", s.StimulusID, err)
		}
		msg.Ack()
	})
	if err != nil {
		return fmt.Errorf("nats: consume: %w", err)
	}

	c.cc = cc
	log.Printf("nats: consuming STIMULUS stream as %q (subject %q)", c.consumerName, c.subject)
	return nil
}

func (c *Consumer) Close() {
	if c.cc != nil {
		c.cc.Stop()
	}
	c.nc.Drain()
}
