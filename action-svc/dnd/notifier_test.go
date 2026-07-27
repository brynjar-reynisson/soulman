package dnd

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeNotifier struct {
	sent []string
}

func (f *fakeNotifier) Send(message string) error {
	f.sent = append(f.sent, message)
	return nil
}

func fixedInsideWindow() time.Time {
	return time.Date(2026, 7, 27, 5, 0, 0, 0, time.Local) // 05:00, inside 00:00-10:00
}

func fixedOutsideWindow() time.Time {
	return time.Date(2026, 7, 27, 14, 0, 0, 0, time.Local) // 14:00, outside 00:00-10:00
}

func TestNotifier_InsideWindow_AppendsToFile_DoesNotCallReal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dnd-pending.txt")
	real := &fakeNotifier{}
	w := Window{Start: "00:00", End: "10:00"}
	n := newNotifier(w, path, real, fixedInsideWindow)

	if err := n.Send("hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if len(real.sent) != 0 {
		t.Errorf("real.Send called %d times, want 0 (inside window)", len(real.sent))
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(b) != "hello" {
		t.Errorf("pending file content = %q, want %q", string(b), "hello")
	}
}
