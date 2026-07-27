package dnd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeFlushNotifier struct {
	sent []string
}

func (f *fakeFlushNotifier) Send(message string) error {
	f.sent = append(f.sent, message)
	return nil
}

type failNTimesNotifier struct {
	failN int
	calls int
}

func (f *failNTimesNotifier) Send(message string) error {
	f.calls++
	if f.failN > 0 {
		f.failN--
		return errors.New("simulated send failure")
	}
	return nil
}

func TestFlushIfPending_EmptyFile_NoSend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dnd-pending.txt")
	notifier := &fakeFlushNotifier{}
	fl := NewFlusher(Window{Start: "00:00", End: "10:00"}, path, notifier)

	fl.FlushIfPending()

	if len(notifier.sent) != 0 {
		t.Errorf("Send called %d times, want 0 for a missing pending file", len(notifier.sent))
	}
}

func TestFlushIfPending_NonEmptyFile_SendsFormattedContentAndClears(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dnd-pending.txt")
	if err := os.WriteFile(path, []byte("first"+entrySeparator+"second"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	notifier := &fakeFlushNotifier{}
	fl := NewFlusher(Window{Start: "00:00", End: "10:00"}, path, notifier)

	fl.FlushIfPending()

	if len(notifier.sent) != 1 {
		t.Fatalf("Send called %d times, want 1", len(notifier.sent))
	}
	msg := notifier.sent[0]
	if !strings.Contains(msg, "2 notification(s) from overnight:") {
		t.Errorf("message = %q, want a 2-notification header", msg)
	}
	if !strings.Contains(msg, "first") || !strings.Contains(msg, "second") {
		t.Errorf("message = %q, want both entries present", msg)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after flush: %v", err)
	}
	if len(b) != 0 {
		t.Errorf("pending file not cleared, content = %q", string(b))
	}
}

func TestFlushIfPending_SendFailsAllThreeAttempts_StillClearsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dnd-pending.txt")
	if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	notifier := &failNTimesNotifier{failN: 3}
	fl := NewFlusher(Window{Start: "00:00", End: "10:00"}, path, notifier)
	fl.BackoffBase = time.Millisecond

	fl.FlushIfPending()

	if notifier.calls != 3 {
		t.Errorf("Send called %d times, want 3 (retried, then given up)", notifier.calls)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after flush: %v", err)
	}
	if len(b) != 0 {
		t.Errorf("pending file should still be cleared after exhausted retries, content = %q", string(b))
	}
}

func TestFlushIfOutsideWindow_OutsideWindowWithPendingContent_FlushesImmediately(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dnd-pending.txt")
	if err := os.WriteFile(path, []byte("stale content"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	notifier := &fakeFlushNotifier{}
	fl := NewFlusher(Window{Start: "00:00", End: "10:00"}, path, notifier)
	fl.Now = fixedOutsideWindow // 14:00, outside 00:00-10:00

	fl.flushIfOutsideWindow()

	if len(notifier.sent) != 1 {
		t.Errorf("Send called %d times, want 1 (immediate catch-up flush)", len(notifier.sent))
	}
}

func TestFlushIfOutsideWindow_InsideWindow_DoesNotFlush(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dnd-pending.txt")
	if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	notifier := &fakeFlushNotifier{}
	fl := NewFlusher(Window{Start: "00:00", End: "10:00"}, path, notifier)
	fl.Now = fixedInsideWindow // 05:00, inside 00:00-10:00

	fl.flushIfOutsideWindow()

	if len(notifier.sent) != 0 {
		t.Errorf("Send called %d times, want 0 (still inside window, wait for window-end)", len(notifier.sent))
	}
}

// TestConcurrentAppendAndFlush_NoLostMessagesNoTornReads is the
// concurrency-fix test flagged by Task 2's review: dndNotifier.append
// (notifier.go) and Flusher.FlushIfPending both operate on the same
// pending file from different goroutines with no coordination other than
// the shared pendingFileMu. This test proves the shared lock actually
// closes the race — not just that each operation works in isolation — by
// hammering the same file with concurrent appends and flushes and then
// verifying that every appended message shows up exactly once across all
// flush sends, byte-for-byte intact. A torn read would corrupt an entry's
// text; a lost-message race would drop an entry entirely; either would be
// caught by the accounting below. Run with `go test -race` for the
// strongest signal — the Go race detector must also report nothing.
func TestConcurrentAppendAndFlush_NoLostMessagesNoTornReads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dnd-pending.txt")
	window := Window{Start: "00:00", End: "10:00"}

	real := &fakeNotifier{}
	n := newNotifier(window, path, real, fixedInsideWindow)

	flushNotifier := &fakeFlushNotifier{}
	var flushMu sync.Mutex // guards flushNotifier.sent against concurrent flush goroutines
	fl := NewFlusher(window, path, &lockedNotifier{inner: flushNotifier, mu: &flushMu})

	const numAppends = 200
	const numFlushers = 8

	var appendWg sync.WaitGroup
	appendWg.Add(numAppends)
	for i := 0; i < numAppends; i++ {
		go func(i int) {
			defer appendWg.Done()
			if err := n.Send(fmt.Sprintf("msg-%d", i)); err != nil {
				t.Errorf("Send(%d): %v", i, err)
			}
		}(i)
	}

	var flushWg sync.WaitGroup
	flushWg.Add(numFlushers)
	for i := 0; i < numFlushers; i++ {
		go func() {
			defer flushWg.Done()
			fl.FlushIfPending()
		}()
	}

	appendWg.Wait()
	flushWg.Wait()

	// Collect whatever's left after the race, plus everything the
	// concurrent flushes already sent.
	fl.FlushIfPending()

	got := map[string]int{}
	for _, msg := range flushNotifier.sent {
		parts := strings.SplitN(msg, ":\n\n", 2)
		if len(parts) != 2 {
			t.Fatalf("malformed flushed message (no header separator found — possible torn read): %q", msg)
		}
		for _, entry := range strings.Split(parts[1], entrySeparator) {
			if entry == "" {
				continue
			}
			got[entry]++
		}
	}

	if len(got) != numAppends {
		t.Errorf("got %d distinct entries across all flushes, want %d (lost or duplicated messages)", len(got), numAppends)
	}
	for i := 0; i < numAppends; i++ {
		want := fmt.Sprintf("msg-%d", i)
		count, ok := got[want]
		if !ok {
			t.Errorf("entry %q missing from all flushed messages (lost in the read/clear race)", want)
			continue
		}
		if count != 1 {
			t.Errorf("entry %q appeared %d times across flushes, want exactly 1 (sent twice or torn/merged with another entry)", want, count)
		}
	}
}

// lockedNotifier serializes Send calls from concurrent Flusher goroutines
// in the race test above so appending to the test's own bookkeeping slice
// isn't itself a data race — orthogonal to (and not a stand-in for) the
// pendingFileMu fix under test, which guards the pending *file*, not this
// test's assertions.
type lockedNotifier struct {
	inner *fakeFlushNotifier
	mu    *sync.Mutex
}

func (l *lockedNotifier) Send(message string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inner.Send(message)
}
