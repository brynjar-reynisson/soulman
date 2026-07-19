package natspublish

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"soulman/common"
)

type Publisher struct {
	nc      *nats.Conn
	js      jetstream.JetStream
	subject string
}

// New connects to NATS and publishes to subject (the STIMULUS JetStream
// stream must already cover it — see the STIMULUS stream's subject list).
// RetryOnFailedConnect + infinite reconnects mean New does not block or
// return an error when NATS is unreachable at startup — the connection
// retries in the background while the rest of the service (HTTP server,
// fsnotify watcher) starts normally.
func New(natsURL, subject string) (*Publisher, error) {
	nc, err := nats.Connect(natsURL,
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
	)
	if err != nil {
		return nil, fmt.Errorf("natspublish: connect to %s: %w", natsURL, err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("natspublish: jetstream: %w", err)
	}

	return &Publisher{nc: nc, js: js, subject: subject}, nil
}

// Publish synchronously publishes a Stimulus to the configured subject,
// waiting for the JetStream ack.
func (p *Publisher) Publish(ctx context.Context, s *common.Stimulus) error {
	b, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("natspublish: marshal stimulus %s: %w", s.StimulusID, err)
	}

	if _, err := p.js.Publish(ctx, p.subject, b); err != nil {
		return fmt.Errorf("natspublish: publish %s: %w", s.StimulusID, err)
	}
	return nil
}

// Status reports the current connection status for the /health endpoint.
func (p *Publisher) Status() string {
	if p.nc.IsConnected() {
		return "connected"
	}
	return "disconnected"
}

func (p *Publisher) Close() {
	p.nc.Drain()
}
