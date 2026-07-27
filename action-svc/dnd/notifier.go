package dnd

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"soulman/action-svc/notify"
)

// entrySeparator delimits distinct Send-level entries accumulated in the
// pending file — deliberately not a bare "\n\n", since a single Send call's
// message (e.g. a multi-item notifybatch.formatBatch flush) can itself
// already contain internal "\n\n" separators between its own blocks; using
// a distinct separator here lets Flusher.FlushIfPending count entries
// accurately by splitting on it.
const entrySeparator = "\n\n---\n\n"

// pendingFileMu serializes all reads and writes of the do-not-disturb
// pending file across goroutines: dndNotifier.append (below) writes to it
// from whichever goroutine calls notify.Notifier.Send while the window is
// active, and Flusher.FlushIfPending (flusher.go) reads-and-clears it from
// its own background wake-loop goroutine. Without a shared lock, a flush
// could race an in-progress append — reading a torn/partial write, or
// clearing the file out from under a message that was appended between the
// flush's read and its clear step, silently losing it. A single
// package-level mutex (rather than a per-instance one on dndNotifier, or on
// Flusher) is what makes the exclusion actually mutual: both types must
// contend for the exact same lock instance around the exact same file.
var pendingFileMu sync.Mutex

// dndNotifier wraps a real notify.Notifier so Send appends to a pending
// file instead of sending while window is active, and delegates straight
// through otherwise. Mirrors feign.gatedNotifier's shape exactly.
type dndNotifier struct {
	window Window
	path   string
	real   notify.Notifier
	now    func() time.Time
}

// WrapNotifier returns a notify.Notifier that appends to pendingFilePath
// instead of sending while window is active (local time), and delegates to
// real otherwise. The wrapped Notifier is indistinguishable from a real one
// at the call site — notifybatch.Batcher needs no code changes to benefit
// from this, the same property feign.WrapNotifier already has.
func WrapNotifier(window Window, pendingFilePath string, real notify.Notifier) notify.Notifier {
	return newNotifier(window, pendingFilePath, real, time.Now)
}

// newNotifier is the test seam: WrapNotifier calls it with the real clock;
// tests call it directly with an injectable now so Send's window-active
// decision is deterministic instead of depending on when the test happens
// to run — mirrors perception-svc/sysmonitor's New/newWatcher split.
func newNotifier(window Window, path string, real notify.Notifier, now func() time.Time) *dndNotifier {
	return &dndNotifier{window: window, path: path, real: real, now: now}
}

func (n *dndNotifier) Send(message string) error {
	if !n.window.Active(n.now()) {
		return n.real.Send(message)
	}

	if err := n.append(message); err != nil {
		slog.Warn("dnd: pending file append failed, sending immediately instead", "path", n.path, "error", err)
		return n.real.Send(message)
	}
	return nil
}

// append writes message to the pending file, separating it from any
// already-accumulated content with entrySeparator.
func (n *dndNotifier) append(message string) error {
	pendingFileMu.Lock()
	defer pendingFileMu.Unlock()

	if err := os.MkdirAll(filepath.Dir(n.path), 0o755); err != nil {
		return fmt.Errorf("dnd: mkdir for %s: %w", n.path, err)
	}

	info, statErr := os.Stat(n.path)
	sep := ""
	if statErr == nil && info.Size() > 0 {
		sep = entrySeparator
	}

	f, err := os.OpenFile(n.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("dnd: open %s: %w", n.path, err)
	}
	defer f.Close()

	if _, err := f.WriteString(sep + message); err != nil {
		return fmt.Errorf("dnd: write to %s: %w", n.path, err)
	}
	return nil
}
