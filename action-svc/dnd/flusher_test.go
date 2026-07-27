package dnd

import (
	"path/filepath"
	"testing"
)

type fakeFlushNotifier struct {
	sent []string
}

func (f *fakeFlushNotifier) Send(message string) error {
	f.sent = append(f.sent, message)
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
