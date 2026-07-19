package natsconsumer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"soulman/common"
)

// Writer is satisfied by *storage.Writer. Defined here to avoid import cycles.
type Writer interface {
	Write(ctx context.Context, s *common.Stimulus) error
}

type Consumer struct {
	nc           *nats.Conn
	js           jetstream.JetStream
	writer       Writer
	consumerName string
	subject      string
	cc           jetstream.ConsumeContext
	wg           sync.WaitGroup
}

// New connects to NATS. consumerName must be unique per environment sharing
// the STIMULUS stream (e.g. "memory-svc" for prod, "memory-svc-dev" for
// dev) — JetStream identifies a durable consumer by (stream, name), so two
// environments reusing the same name would silently overwrite each other's
// consumer state. subject scopes which messages this consumer receives via
// FilterSubject, so dev and prod publishing on different subjects on the
// same shared stream don't see each other's stimuli.
func New(natsURL, consumerName, subject string, w Writer) (*Consumer, error) {
	nc, err := nats.Connect(natsURL)
	if err != nil {
		return nil, fmt.Errorf("nats: connect to %s: %w", natsURL, err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats: jetstream: %w", err)
	}

	return &Consumer{nc: nc, js: js, writer: w, consumerName: consumerName, subject: subject}, nil
}

// Start subscribes to the STIMULUS stream and processes messages in the NATS
// library goroutine. Returns after the subscription is established; messages
// arrive asynchronously. Call Close to stop.
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
		c.wg.Add(1)
		defer c.wg.Done()

		var s common.Stimulus
		if err := json.Unmarshal(msg.Data(), &s); err != nil {
			log.Printf("nats: unparseable message (subject %s), ACKing to skip: %v", msg.Subject(), err)
			msg.Ack()
			return
		}

		if err := c.writer.Write(context.Background(), &s); err != nil {
			log.Printf("nats: write failed for %s, NAKing for redelivery: %v", s.StimulusID, err)
			msg.Nak()
			return
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
	c.wg.Wait()
	c.nc.Drain()
}
