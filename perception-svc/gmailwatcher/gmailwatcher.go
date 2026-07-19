package gmailwatcher

import (
	"context"
	"log"
	"time"

	"soulman/common"
)

// Publisher is satisfied by *natspublish.Publisher. Declared here (not
// imported from natspublish) to avoid an import cycle, mirroring
// watcher.Publisher's same rationale.
type Publisher interface {
	Publish(ctx context.Context, s *common.Stimulus) error
}

// Config holds everything the Watcher needs to poll Gmail and publish
// matching messages. Populated from perception-svc/config.Config's Gmail*
// fields by main.go.
type Config struct {
	ClientID     string
	ClientSecret string
	RefreshToken string
	Query        string
	SeenLabel    string
	PollInterval time.Duration
}

// Watcher polls a Gmail inbox for messages matching Query, publishes each
// as a Stimulus, then labels it with SeenLabel so it drops out of future
// poll results — Gmail's own labels are the checkpoint, no local state
// file is needed (unlike folder-watcher's hash-based checkpoint).
type Watcher struct {
	client    gmailClient
	publisher Publisher
	query     string
	seenLabel string
	interval  time.Duration

	seenLabelID string
	cancel      context.CancelFunc
}

// New builds a Watcher backed by the real Gmail API, authenticated via an
// OAuth2 offline refresh token (auto-refreshing, no interactive consent
// after the initial one-time setup — see the Gmail channel design spec).
func New(ctx context.Context, cfg Config, publisher Publisher) (*Watcher, error) {
	client, err := newRealGmailClient(ctx, cfg.ClientID, cfg.ClientSecret, cfg.RefreshToken)
	if err != nil {
		return nil, err
	}
	return newWatcher(client, publisher, cfg.Query, cfg.SeenLabel, cfg.PollInterval), nil
}

// newWatcher builds a Watcher against any gmailClient — the seam
// gmailwatcher_test.go uses to inject a fake instead of a live Gmail
// account, mirroring watcher.New's Publisher-interface seam.
func newWatcher(client gmailClient, publisher Publisher, query, seenLabel string, interval time.Duration) *Watcher {
	return &Watcher{
		client:    client,
		publisher: publisher,
		query:     query,
		seenLabel: seenLabel,
		interval:  interval,
	}
}

// Start launches the poll loop in a background goroutine and returns
// immediately — it never blocks the caller, regardless of how long the
// first poll takes (a real inbox can have a large unread backlog on first
// run, and Start must not delay the rest of perception-svc's startup, e.g.
// the HTTP server, while that backlog is processed).
func (w *Watcher) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	go w.pollLoop(ctx)
}

// pollLoop runs one immediate poll (so messages already unread at startup
// aren't stuck waiting a full interval) and then the ticker-driven poll
// loop, all within this single background goroutine.
func (w *Watcher) pollLoop(ctx context.Context) {
	w.poll(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.poll(ctx)
		}
	}
}

// poll runs one list -> get -> publish -> label cycle. Every step logs and
// moves on rather than aborting the whole cycle on a single message's
// failure, so one bad message doesn't block the rest of the batch.
func (w *Watcher) poll(ctx context.Context) {
	if w.seenLabelID == "" {
		labelID, err := w.client.EnsureLabel(ctx, w.seenLabel)
		if err != nil {
			log.Printf("gmailwatcher: seen label %q still unresolved, skipping poll: %v", w.seenLabel, err)
			return
		}
		w.seenLabelID = labelID
	}

	ids, err := w.client.ListMatching(ctx, w.query)
	if err != nil {
		log.Printf("gmailwatcher: list matching messages failed, will retry next poll: %v", err)
		return
	}

	for _, id := range ids {
		w.handleMessage(ctx, id)
	}
}

func (w *Watcher) handleMessage(ctx context.Context, id string) {
	msg, err := w.client.GetMessage(ctx, id)
	if err != nil {
		log.Printf("gmailwatcher: get message %s failed, will retry next poll: %v", id, err)
		return
	}

	stimulus, err := buildStimulus(msg)
	if err != nil {
		log.Printf("gmailwatcher: build stimulus for message %s failed, skipping: %v", id, err)
		return
	}

	if err := w.publisher.Publish(ctx, stimulus); err != nil {
		log.Printf("gmailwatcher: publish failed for message %s (seen-label left unset, will retry): %v", id, err)
		return
	}

	if err := w.client.AddLabel(ctx, id, w.seenLabelID); err != nil {
		log.Printf("gmailwatcher: label message %s as seen failed (will be re-published next poll): %v", id, err)
	}
}

func (w *Watcher) Close() error {
	if w.cancel != nil {
		w.cancel()
	}
	return nil
}
