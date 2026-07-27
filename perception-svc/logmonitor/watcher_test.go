package logmonitor

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"soulman/common"
)

type fakePublisher struct {
	mu         sync.Mutex
	published  []*common.Stimulus
	publishErr error
}

func (f *fakePublisher) Publish(_ context.Context, s *common.Stimulus) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.publishErr != nil {
		return f.publishErr
	}
	f.published = append(f.published, s)
	return nil
}

func (f *fakePublisher) publishedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.published)
}

func newTestWatcher(t *testing.T, logDir string, pub Publisher) *Watcher {
	t.Helper()
	cpPath := filepath.Join(t.TempDir(), "logmonitor-checkpoint.json")
	w, err := New(logDir, pub, cpPath, time.Hour)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return w
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func TestReconcileAll_FirstRun_IgnoresPreExistingContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory-svc-startup-err.log")
	writeFile(t, path, "2026/07/20 09:00:00 ERROR old error before perception-svc ever ran\n")

	pub := &fakePublisher{}
	w := newTestWatcher(t, dir, pub)

	w.reconcileAll(context.Background())

	if got := pub.publishedCount(); got != 0 {
		t.Fatalf("published = %d, want 0 (first run must start at EOF, ignoring pre-existing content)", got)
	}
}
