// Package audit records one JSON line per Discord send attempt — the
// answer to "what got sent, when, and why" for a given message. Not a
// gate (nothing is suppressed or altered here, unlike feign.Gate or
// dnd.dndNotifier) — purely observational, mirroring feign.Gate's
// JSONL-append pattern.
package audit

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"soulman/action-svc/notify"
)

// Log appends Entry records to a single JSONL file. Shared (one *Log, one
// mutex) across every Wrap'd notifier so concurrent sends from different
// subsystems (the daily cron, the notification batcher, the school-events
// scheduler) never interleave partial writes.
type Log struct {
	path string
	mu   sync.Mutex
}

func New(path string) *Log {
	return &Log{path: path}
}

// Entry is one audit record — Summary is deliberately a single line (see
// summarize) so each JSONL line reads as a one-liner of what was sent.
type Entry struct {
	Timestamp time.Time `json:"timestamp"`
	Reason    string    `json:"reason"`
	Summary   string    `json:"summary"`
	Status    string    `json:"status"` // "sent" | "failed"
	Error     string    `json:"error,omitempty"`
}

const maxSummaryRunes = 150

// summarize reduces message to a single line, capped at maxSummaryRunes.
// The whole message collapses into it (whitespace/newlines joined with a
// single space), not just its first line — several real message shapes
// (notifybatch's "N important item(s):" header, the school scheduler's
// "**Soulman Reminder**" line) put the actual content on a later line, so
// a first-line-only summary would record nothing useful about what was
// actually sent.
func summarize(message string) string {
	collapsed := strings.Join(strings.Fields(message), " ")
	r := []rune(collapsed)
	if len(r) > maxSummaryRunes {
		return string(r[:maxSummaryRunes]) + "…"
	}
	return collapsed
}

// record appends one Entry as a single JSON line. A failure here is logged
// and otherwise swallowed — auditing a send must never be the reason the
// send itself is reported as failed to its caller.
func (l *Log) record(reason, message string, sendErr error) {
	entry := Entry{Timestamp: time.Now().UTC(), Reason: reason, Summary: summarize(message), Status: "sent"}
	if sendErr != nil {
		entry.Status = "failed"
		entry.Error = sendErr.Error()
	}

	b, err := json.Marshal(entry)
	if err != nil {
		slog.Error("audit: marshal failed, entry dropped", "reason", reason, "error", err)
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		slog.Error("audit: mkdir failed, entry dropped", "path", l.path, "error", err)
		return
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		slog.Error("audit: open failed, entry dropped", "path", l.path, "error", err)
		return
	}
	defer f.Close()

	if _, err := f.Write(append(b, '\n')); err != nil {
		slog.Error("audit: write failed, entry dropped", "path", l.path, "error", err)
	}
}

type notifier struct {
	log    *Log
	inner  notify.Notifier
	reason string
}

// Wrap returns a notify.Notifier that records one audit entry to log for
// every Send call (tagged reason), then delegates to inner and returns
// inner's own result unchanged. The wrapped Notifier is indistinguishable
// from a real one at the call site — mirrors feign.WrapNotifier's and
// dnd.WrapNotifier's same transparency.
func Wrap(log *Log, inner notify.Notifier, reason string) notify.Notifier {
	return &notifier{log: log, inner: inner, reason: reason}
}

func (n *notifier) Send(message string) error {
	err := n.inner.Send(message)
	n.log.record(n.reason, message, err)
	return err
}
