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

// dndNotifier wraps a real notify.Notifier so Send appends to a pending
// file instead of sending while window is active, and delegates straight
// through otherwise. Mirrors feign.gatedNotifier's shape exactly.
type dndNotifier struct {
	window Window
	path   string
	real   notify.Notifier
	now    func() time.Time
	mu     sync.Mutex
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
	n.mu.Lock()
	defer n.mu.Unlock()

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
