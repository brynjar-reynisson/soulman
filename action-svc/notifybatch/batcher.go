package notifybatch

import (
	"fmt"
	"log/slog"
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

// Item is one important notification queued for the next flush — either a
// Gmail triage verdict or a generic append_daily_report_entry entry judged
// important (system-monitor, log-error, folder-watcher, or any future
// mechanical rule that sets Important: true). Kind discriminates which
// fields formatBatch reads: "gmail" uses Sender/Subject/ThreadID/Reason/
// BodyExcerpt (unchanged from before this generalization, added
// 2026-07-27 per docs/superpowers/specs/2026-07-27-log-error-perception-design.md);
// "report" uses Summary/SourcePath/BodyExcerpt, mirroring report.Entry's
// own field names.
type Item struct {
	Kind        string // "gmail" | "report"
	Sender      string // gmail only
	Subject     string // gmail only
	ThreadID    string // gmail only
	Summary     string // report only — mirrors report.Entry.Summary
	SourcePath  string // report only — mirrors report.Entry.SourcePath
	Reason      string // shared: gmail's triage reason, or empty for report
	BodyExcerpt string // shared: gmail body excerpt, or report's raw_content
}

// Batcher collects important Items and flushes them as a single Discord
// message once either the grace period (no new item has arrived recently)
// or the max-wait cap (measured from the first item in the pending batch)
// elapses — whichever comes first. See
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
	if err := b.notifier.Send(formatBatch(items)); err != nil {
		slog.Error("notifybatch: flush send failed, batch lost", "items", len(items), "error", err)
		return
	}
	slog.Info("notifybatch: flushed", "items", len(items))
}

// formatBatch branches per item on Kind: "report" items render as a plain
// "[<source_path>] <summary>\n<body_excerpt>" block; everything else
// (including "gmail" and, for backward compatibility, an unset Kind)
// preserves the original Gmail-shaped block exactly. The count header was
// "N important email(s):" before this generalization — renamed to "N
// important item(s):" since a batch can now legitimately contain zero
// emails (an all-report-items flush).
func formatBatch(items []Item) string {
	blocks := make([]string, 0, len(items)+1)
	blocks = append(blocks, fmt.Sprintf("%d important item(s):", len(items)))
	for _, it := range items {
		switch it.Kind {
		case "report":
			blocks = append(blocks, fmt.Sprintf("[%s] %s\n%s", it.SourcePath, it.Summary, it.BodyExcerpt))
		default:
			blocks = append(blocks, fmt.Sprintf(
				"From: %s\nSubject: %s\nWhy: %s\n\"%s\"\nhttps://mail.google.com/mail/u/0/#inbox/%s",
				it.Sender, it.Subject, it.Reason, it.BodyExcerpt, it.ThreadID))
		}
	}
	return strings.Join(blocks, "\n\n")
}
