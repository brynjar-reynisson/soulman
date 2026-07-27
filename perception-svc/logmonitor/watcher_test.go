package logmonitor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("OpenFile %s: %v", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
}

func TestReconcileAll_NewErrorLine_PublishesOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory-svc-startup-err.log")
	writeFile(t, path, "")

	pub := &fakePublisher{}
	w := newTestWatcher(t, dir, pub)
	w.reconcileAll(context.Background()) // establishes EOF baseline (empty file)

	appendLine(t, path, `2026/07/27 10:05:00 ERROR writer: DB insert failed, will replay on restart stimulus_id=abc error="connection refused"`)
	w.reconcileAll(context.Background())

	if got := pub.publishedCount(); got != 1 {
		t.Fatalf("published = %d, want 1", got)
	}
	s := pub.published[0]
	if s.Channel != "log-error" {
		t.Errorf("Channel = %q, want log-error", s.Channel)
	}
	if s.Hints.Priority != "critical" {
		t.Errorf("Hints.Priority = %q, want critical", s.Hints.Priority)
	}
	wantTags := []string{"system", "log-error", "memory-svc"}
	if len(s.Hints.Tags) != 3 || s.Hints.Tags[0] != wantTags[0] || s.Hints.Tags[1] != wantTags[1] || s.Hints.Tags[2] != wantTags[2] {
		t.Errorf("Hints.Tags = %v, want %v", s.Hints.Tags, wantTags)
	}
	if s.Content.RawText != `2026/07/27 10:05:00 ERROR writer: DB insert failed, will replay on restart stimulus_id=abc error="connection refused"` {
		t.Errorf("Content.RawText = %q, want the full matched line verbatim", s.Content.RawText)
	}
}

func TestReconcileAll_RepeatedIdenticalPair_PublishesOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory-svc-startup-err.log")
	writeFile(t, path, "")

	pub := &fakePublisher{}
	w := newTestWatcher(t, dir, pub)
	w.reconcileAll(context.Background())

	appendLine(t, path, `2026/07/27 10:05:00 ERROR writer: DB insert failed, will replay on restart error="connection refused"`)
	w.reconcileAll(context.Background())

	appendLine(t, path, `2026/07/27 10:05:05 ERROR writer: DB insert failed, will replay on restart error="connection refused"`)
	w.reconcileAll(context.Background())

	if got := pub.publishedCount(); got != 1 {
		t.Fatalf("published = %d, want 1 (repeated identical service+msg absorbed)", got)
	}
}

func TestReconcileAll_TwoDistinctMessagesFromSameService_PublishesTwice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory-svc-startup-err.log")
	writeFile(t, path, "")

	pub := &fakePublisher{}
	w := newTestWatcher(t, dir, pub)
	w.reconcileAll(context.Background())

	appendLine(t, path, `2026/07/27 10:05:00 ERROR writer: DB insert failed, will replay on restart error="connection refused"`)
	w.reconcileAll(context.Background())
	appendLine(t, path, `2026/07/27 10:06:00 ERROR nats consumer start failed error="dial tcp: timeout"`)
	w.reconcileAll(context.Background())

	if got := pub.publishedCount(); got != 2 {
		t.Fatalf("published = %d, want 2 (two distinct messages)", got)
	}
}

func TestReconcileAll_WarnInfoAndMalformedLines_Skipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory-svc-startup-err.log")
	writeFile(t, path, "")

	pub := &fakePublisher{}
	w := newTestWatcher(t, dir, pub)
	w.reconcileAll(context.Background())

	appendLine(t, path, "2026/07/27 10:05:00 WARN checkpoint: parse failed, starting empty path=x error=y")
	appendLine(t, path, "2026/07/27 10:05:01 INFO memory-svc started nats_url=nats://localhost:4222")
	appendLine(t, path, "goroutine 1 [running]:")
	appendLine(t, path, "\tmain.main() /home/user/main.go:42 +0x1a")
	w.reconcileAll(context.Background())

	if got := pub.publishedCount(); got != 0 {
		t.Fatalf("published = %d, want 0 (WARN/INFO/malformed lines must all be skipped)", got)
	}
}

func TestReconcileAll_PublishFailure_DedupKeyNotMarked_RetriesNextRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory-svc-startup-err.log")
	writeFile(t, path, "")

	pub := &fakePublisher{}
	w := newTestWatcher(t, dir, pub)
	w.reconcileAll(context.Background())

	pub.publishErr = errors.New("nats down")
	appendLine(t, path, `2026/07/27 10:05:00 ERROR writer: DB insert failed, will replay on restart error="connection refused"`)
	w.reconcileAll(context.Background())

	if got := pub.publishedCount(); got != 0 {
		t.Fatalf("published = %d, want 0 (publish failed)", got)
	}

	pub.publishErr = nil
	appendLine(t, path, `2026/07/27 10:06:00 ERROR writer: DB insert failed, will replay on restart error="connection refused"`)
	w.reconcileAll(context.Background())

	if got := pub.publishedCount(); got != 1 {
		t.Errorf("published = %d, want 1 (same line retried on next matching line after publish recovered)", got)
	}
}

func TestReconcileAll_Truncation_OffsetResetsAndTailingContinues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory-svc-startup-err.log")
	writeFile(t, path, strings.Repeat("padding to establish a real starting offset\n", 50))

	pub := &fakePublisher{}
	w := newTestWatcher(t, dir, pub)
	w.reconcileAll(context.Background()) // baseline: checkpoint offset = current EOF (well past 0)

	// Simulate a manual truncation (e.g. what happened to
	// memory-svc-startup-err.log on 2026-07-27) followed by a fresh error.
	writeFile(t, path, `2026/07/27 11:00:00 ERROR writer: DB insert failed, will replay on restart error="connection refused"`+"\n")
	w.reconcileAll(context.Background())

	if got := pub.publishedCount(); got != 1 {
		t.Fatalf("published = %d, want 1 (truncation must reset offset to 0 and pick up the new line)", got)
	}
}

func TestReconcileAll_TrailingPartialLine_LeftForNextRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory-svc-startup-err.log")
	writeFile(t, path, "")

	pub := &fakePublisher{}
	w := newTestWatcher(t, dir, pub)
	w.reconcileAll(context.Background())

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	partial := `2026/07/27 10:05:00 ERROR writer: DB insert failed, will replay on restart error="connection refused"`
	if _, err := f.WriteString(partial); err != nil { // no trailing newline yet
		t.Fatalf("WriteString: %v", err)
	}
	f.Close()

	w.reconcileAll(context.Background())
	if got := pub.publishedCount(); got != 0 {
		t.Fatalf("published = %d, want 0 (a trailing partial line must not be processed yet)", got)
	}

	f, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if _, err := f.WriteString("\n"); err != nil { // now complete
		t.Fatalf("WriteString: %v", err)
	}
	f.Close()

	w.reconcileAll(context.Background())
	if got := pub.publishedCount(); got != 1 {
		t.Errorf("published = %d, want 1 (the now-completed line must be processed)", got)
	}
}
