package notifybatch

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"soulman/action-svc/notify"
)

// DefaultGrace and DefaultMaxWait are the hardcoded (not environment-
// configurable, per the design spec) debounce durations action-svc's
// main.go constructs its Batcher with.
const (
	DefaultGrace   = 30 * time.Second
	DefaultMaxWait = 2 * time.Minute
)

// Item is one important-email notification queued for the next flush.
type Item struct {
	Sender      string
	Subject     string
	Reason      string
	BodyExcerpt string
	ThreadID    string
}

// Batcher collects important-email Items and flushes them as a single
// Discord message once either the grace period (no new item has arrived
// recently) or the max-wait cap (measured from the first item in the
// pending batch) elapses — whichever comes first. See
// docs/superpowers/specs/2026-07-18-gmail-triage-action-design.md's
// "Notification batching" section for the rationale behind the two
// timers. The queue is in-memory only: a process restart with a batch
// pending loses it (an accepted v1 limitation).
type Batcher struct {
	grace    time.Duration
	maxWait  time.Duration
	notifier notify.Notifier

	mu         sync.Mutex
	items      []Item
	graceTimer *time.Timer
	maxTimer   *time.Timer
}

func New(grace, maxWait time.Duration, notifier notify.Notifier) *Batcher {
	return &Batcher{grace: grace, maxWait: maxWait, notifier: notifier}
}

// Add queues item for the next flush. The first item in a new batch starts
// both timers; later items reset only the grace timer — the max-wait
// timer keeps counting from the first item and is never reset, bounding
// worst-case delay during a steady trickle of arrivals.
func (b *Batcher) Add(item Item) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.items = append(b.items, item)

	if b.graceTimer == nil {
		b.maxTimer = time.AfterFunc(b.maxWait, b.Flush)
		b.graceTimer = time.AfterFunc(b.grace, b.Flush)
		return
	}

	b.graceTimer.Stop()
	b.graceTimer = time.AfterFunc(b.grace, b.Flush)
}

// Flush sends all currently-queued items as one message and clears the
// batch. Safe to call when the batch is already empty — a no-op — which is
// how the timer that loses the grace-vs-max-wait race resolves once it
// fires after the other timer already flushed.
func (b *Batcher) Flush() {
	b.mu.Lock()
	items := b.items
	b.items = nil
	if b.graceTimer != nil {
		b.graceTimer.Stop()
		b.graceTimer = nil
	}
	if b.maxTimer != nil {
		b.maxTimer.Stop()
		b.maxTimer = nil
	}
	b.mu.Unlock()

	if len(items) == 0 {
		return
	}
	_ = b.notifier.Send(formatBatch(items))
}

func formatBatch(items []Item) string {
	blocks := make([]string, 0, len(items)+1)
	blocks = append(blocks, fmt.Sprintf("%d important email(s):", len(items)))
	for _, it := range items {
		blocks = append(blocks, fmt.Sprintf(
			"From: %s\nSubject: %s\nWhy: %s\n\"%s\"\nhttps://mail.google.com/mail/u/0/#inbox/%s",
			it.Sender, it.Subject, it.Reason, it.BodyExcerpt, it.ThreadID))
	}
	return strings.Join(blocks, "\n\n")
}
