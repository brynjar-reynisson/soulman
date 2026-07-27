package dnd

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"soulman/action-svc/notify"
)

// Flusher wakes at the do-not-disturb window's end time each day, sends
// the pending file's accumulated content as one message through notifier,
// and clears the file. Mirrors scheduler.Scheduler's wake-loop shape
// (nextRun/time.Until/time.After) applied to a window's End time instead
// of a single daily send time.
type Flusher struct {
	window   Window
	path     string
	notifier notify.Notifier
	stop     chan struct{}

	// Overridable for tests: Now controls "current time" (avoids waiting for
	// a real clock), BackoffBase controls the retry delay (avoids a slow
	// test) — same pattern as scheduler.Scheduler.
	Now         func() time.Time
	BackoffBase time.Duration
}

// NewFlusher builds a Flusher. notifier is the same notifier chain the
// Batcher sends through (still feign-wrapped) — DND only changes *when*
// pending content is sent, not how feign_mode governs the send itself.
func NewFlusher(window Window, pendingFilePath string, notifier notify.Notifier) *Flusher {
	return &Flusher{
		window:      window,
		path:        pendingFilePath,
		notifier:    notifier,
		stop:        make(chan struct{}),
		Now:         time.Now,
		BackoffBase: 1 * time.Second,
	}
}

// Start launches the catch-up check (flush immediately if currently
// outside the window and there's stale pending content — see
// flushIfOutsideWindow) and the wake-at-window-end loop, both in background
// goroutines, so Start itself never blocks. The catch-up check can reach a
// real Discord send with retries (sendWithRetry: up to 3 attempts, each
// with its own HTTP timeout, plus backoff between attempts) — running it
// synchronously here would delay everything main.go sets up after calling
// Start (NATS, the durable consumer, the HTTP server) on a slow or failing
// Discord API at restart time. Mirrors scheduler.Scheduler.Start, which
// also does nothing synchronous before its `go s.loop()`.
func (f *Flusher) Start() {
	go f.flushIfOutsideWindow()
	go f.loop()
}

func (f *Flusher) Stop() {
	close(f.stop)
}

// flushIfOutsideWindow is Start's catch-up check (launched in its own
// goroutine by Start, so it never blocks startup — see Start's doc
// comment), factored out so tests can also call it directly and
// synchronously, without spawning either goroutine Start launches — in
// particular without the background wake-loop goroutine, which would
// otherwise race against a test's assertions, since its wait duration is
// computed from the real clock via time.Until while Now may be a fixed
// test value.
func (f *Flusher) flushIfOutsideWindow() {
	if !f.window.Active(f.Now()) {
		f.FlushIfPending()
	}
}

func (f *Flusher) loop() {
	for {
		wait := time.Until(f.nextWindowEnd(f.Now()))
		select {
		case <-time.After(wait):
			f.FlushIfPending()
		case <-f.stop:
			return
		}
	}
}

// nextWindowEnd computes the next occurrence of window.End the same way
// Scheduler.nextRun computes the next send time.
func (f *Flusher) nextWindowEnd(from time.Time) time.Time {
	hh, mm := parseTime(f.window.End)
	next := time.Date(from.Year(), from.Month(), from.Day(), hh, mm, 0, 0, from.Location())
	if !next.After(from) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// FlushIfPending atomically reads and clears the pending file (see
// readAndClearPending — this is the half of the concurrency fix that
// prevents a message appended between the read and the clear from being
// silently lost), then, if there was anything pending, formats and sends
// it as one message (retried up to 3 times). The file is already cleared
// by the time Send is attempted, so a slow or failing send can never leave
// stale content to be double-sent on the next flush, and — symmetrically
// with the brief's original intent — a send failure never leaves the file
// unflushed either: there is no cross-day retry chain.
func (f *Flusher) FlushIfPending() {
	content, err := readAndClearPending(f.path)
	if err != nil {
		slog.Error("dnd: flush pending file failed", "path", f.path, "error", err)
		return
	}
	if strings.TrimSpace(content) == "" {
		return
	}

	entries := strings.Split(content, entrySeparator)
	message := fmt.Sprintf("%d notification(s) from overnight:\n\n%s", len(entries), content)

	if err := f.sendWithRetry(message); err != nil {
		slog.Error("dnd: flush send failed after 3 attempts", "error", err)
		return
	}
	slog.Info("dnd: flushed pending notifications", "entries", len(entries))
}

func (f *Flusher) sendWithRetry(message string) error {
	var err error
	backoff := f.BackoffBase
	for attempt := 1; attempt <= 3; attempt++ {
		err = f.notifier.Send(message)
		if err == nil {
			return nil
		}
		slog.Warn("dnd: flush send attempt failed", "attempt", attempt, "max_attempts", 3, "error", err)
		if attempt < 3 {
			time.Sleep(backoff)
			backoff *= 2
		}
	}
	return err
}

// readAndClearPending reads the pending file and, if it had any content,
// clears it — both under pendingFileMu, held for the full read-then-clear
// sequence so the two steps are atomic with respect to dndNotifier.append
// (notifier.go). This is what actually closes the race described on
// pendingFileMu's doc comment: any append that hasn't yet acquired the
// lock when a flush begins is fully visible in the read (it happened
// entirely before, since append also holds the lock for its whole
// mkdir/stat/open/write/close sequence — no torn reads), and any append
// that arrives after a flush's read must wait for the clear to finish
// first, so it lands in the now-empty file instead of being wiped by it.
// Returns "" (no error) if the file doesn't exist yet — never written to
// during this window.
func readAndClearPending(path string) (string, error) {
	pendingFileMu.Lock()
	defer pendingFileMu.Unlock()

	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("dnd: read %s: %w", path, err)
	}

	content := string(b)
	if strings.TrimSpace(content) == "" {
		return content, nil
	}

	if err := os.WriteFile(path, nil, 0o644); err != nil {
		slog.Error("dnd: clear pending file failed", "path", path, "error", err)
	}
	return content, nil
}
