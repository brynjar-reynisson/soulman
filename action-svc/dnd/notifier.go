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
// pending file across goroutines, and also guards dndNotifier.Send's
// window-active decision so that decision is atomic with respect to a
// concurrent flush: dndNotifier.Send (below) re-checks window.Active and,
// if active, appends, both under this lock; Flusher.FlushIfPending
// (flusher.go) reads-and-clears the file under the same lock from its own
// background wake-loop goroutine. Two distinct hazards motivate this
// single shared, whole-decision critical section rather than a narrower
// per-file-operation lock: (1) without any shared lock, a flush could race
// an in-progress append — reading a torn/partial write, or clearing the
// file out from under a message appended between the flush's read and its
// clear step, silently losing it; (2) even with the file operations
// individually locked, if Send's window.Active check happened *before*
// acquiring the lock, a Send racing the window-end boundary could decide
// "active" a moment too early, then block on the lock behind a flush's
// critical section, and only append after the file was already cleared
// for the day — stranding the message until tomorrow's flush instead of
// either landing in the current flush or being sent immediately. A single
// package-level mutex (rather than a per-instance one on dndNotifier, or
// on Flusher) is what makes the exclusion actually mutual: both types must
// contend for the exact same lock instance around the exact same file and
// the exact same decision.
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

// Send decides whether to append to the pending file or delegate straight
// through to real, then (if appending) does so — all under a single
// pendingFileMu critical section. This is deliberate, not incidental: if
// the window-active check happened before acquiring the lock (as an
// earlier version of this code did), a Send racing the exact window-end
// boundary could read Active() == true, then block on pendingFileMu behind
// a concurrent Flusher.FlushIfPending's read-and-clear critical section,
// and only get the lock after the file has already been cleared for this
// window — appending a message that should have gone out with this flush
// (or, since the window is now over, straight to real) into a file that
// won't be read again until tomorrow. Re-deciding Active() only after
// acquiring the lock (not before) closes that gap: the decision and the
// write it leads to happen atomically with respect to a concurrent flush.
func (n *dndNotifier) Send(message string) error {
	pendingFileMu.Lock()
	active := n.window.Active(n.now())
	if !active {
		pendingFileMu.Unlock()
		return n.real.Send(message)
	}

	err := n.appendLocked(message)
	pendingFileMu.Unlock()
	if err != nil {
		slog.Warn("dnd: pending file append failed, sending immediately instead", "path", n.path, "error", err)
		return n.real.Send(message)
	}
	return nil
}

// appendLocked writes message to the pending file, separating it from any
// already-accumulated content with entrySeparator. Callers must hold
// pendingFileMu — this is not a standalone re-entrant helper.
func (n *dndNotifier) appendLocked(message string) error {
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
