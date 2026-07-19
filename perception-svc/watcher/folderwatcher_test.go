package watcher

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"soulman/common"
)

type mockPublisher struct {
	mu        sync.Mutex
	published []*common.Stimulus
	failNext  bool
}

func (m *mockPublisher) Publish(_ context.Context, s *common.Stimulus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failNext {
		m.failNext = false
		return errors.New("mock publish failure")
	}
	m.published = append(m.published, s)
	return nil
}

func (m *mockPublisher) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.published)
}

func (m *mockPublisher) last() *common.Stimulus {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.published) == 0 {
		return nil
	}
	return m.published[len(m.published)-1]
}

func TestHashBytes_DeterministicAndDistinct(t *testing.T) {
	h1 := hashBytes([]byte("hello"))
	h2 := hashBytes([]byte("hello"))
	h3 := hashBytes([]byte("world"))

	if h1 != h2 {
		t.Errorf("hashBytes not deterministic: %q != %q", h1, h2)
	}
	if h1 == h3 {
		t.Errorf("hashBytes collided for different content")
	}
	if !strings.HasPrefix(h1, "sha256:") {
		t.Errorf("hashBytes = %q, want sha256: prefix", h1)
	}
}

func TestComputeMessageID_Deterministic(t *testing.T) {
	id1 := computeMessageID("/watch/dir", "file.txt", "2026-07-17T00:00:00Z")
	id2 := computeMessageID("/watch/dir", "file.txt", "2026-07-17T00:00:00Z")
	id3 := computeMessageID("/watch/dir", "other.txt", "2026-07-17T00:00:00Z")

	if id1 != id2 {
		t.Errorf("computeMessageID not deterministic: %q != %q", id1, id2)
	}
	if id1 == id3 {
		t.Errorf("computeMessageID collided for different filename")
	}
}

func TestBuildStimulus_TextFile(t *testing.T) {
	mtime := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	s := buildStimulus(`C:\errors`, "log.txt", []byte("boom, something broke"), mtime)

	if s.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", s.SchemaVersion)
	}
	if s.Channel != "folder-watcher" {
		t.Errorf("Channel = %q, want folder-watcher", s.Channel)
	}
	if s.Source.Identity != "folder-watcher" || !s.Source.Authenticated || s.Source.AuthMethod != "system" {
		t.Errorf("Source = %+v, want {folder-watcher true system}", s.Source)
	}
	if s.Content.ContentType != "text" {
		t.Errorf("ContentType = %q, want text", s.Content.ContentType)
	}
	if s.Content.RawText != "boom, something broke" {
		t.Errorf("RawText = %q, want file contents", s.Content.RawText)
	}
	if len(s.Content.Attachments) != 0 {
		t.Errorf("Attachments = %v, want empty for inlined text", s.Content.Attachments)
	}
	if s.OccurredAt == nil || !s.OccurredAt.Equal(mtime) {
		t.Errorf("OccurredAt = %v, want %v", s.OccurredAt, mtime)
	}
	if s.Hints.Priority != "high" {
		t.Errorf("Priority = %q, want high", s.Hints.Priority)
	}
	if len(s.Hints.Tags) != 2 || s.Hints.Tags[0] != "error" || s.Hints.Tags[1] != "folder-watcher" {
		t.Errorf("Tags = %v, want [error folder-watcher]", s.Hints.Tags)
	}
	if s.Hints.Intent != nil {
		t.Errorf("Intent = %v, want nil", s.Hints.Intent)
	}
	if s.Override.IsOverride {
		t.Errorf("IsOverride = true, want false")
	}
	if s.ChannelMeta.MessageID != computeMessageID(`C:\errors`, "log.txt", mtime.Format(time.RFC3339)) {
		t.Errorf("MessageID mismatch")
	}

	var specific map[string]string
	if err := json.Unmarshal(s.ChannelMeta.ChannelSpecific, &specific); err != nil {
		t.Fatalf("ChannelSpecific unmarshal: %v", err)
	}
	if specific["watched_path"] != `C:\errors` {
		t.Errorf("watched_path = %q, want C:\\errors", specific["watched_path"])
	}
}

func TestBuildStimulus_BinaryFile(t *testing.T) {
	mtime := time.Now().UTC()
	data := []byte{0xff, 0xfe, 0x00, 0x01, 0x02} // invalid UTF-8
	s := buildStimulus(`C:\errors`, "dump.bin", data, mtime)

	if s.Content.ContentType != "binary" {
		t.Errorf("ContentType = %q, want binary", s.Content.ContentType)
	}
	if s.Content.RawText != "" {
		t.Errorf("RawText = %q, want empty for binary", s.Content.RawText)
	}
	if len(s.Content.Attachments) != 1 {
		t.Fatalf("Attachments = %v, want 1 entry", s.Content.Attachments)
	}
	att := s.Content.Attachments[0]
	if att.Filename != "dump.bin" {
		t.Errorf("Filename = %q, want dump.bin", att.Filename)
	}
	if att.SizeBytes != int64(len(data)) {
		t.Errorf("SizeBytes = %d, want %d", att.SizeBytes, len(data))
	}
	if att.URI != filepath.Join(`C:\errors`, "dump.bin") {
		t.Errorf("URI = %q, want local file path", att.URI)
	}
}

func TestBuildStimulus_LargeTextFile_TreatedAsBinary(t *testing.T) {
	data := make([]byte, maxInlineBytes) // exactly at the threshold: not < maxInlineBytes
	for i := range data {
		data[i] = 'a'
	}
	s := buildStimulus(`C:\errors`, "huge.txt", data, time.Now().UTC())

	if s.Content.ContentType != "binary" {
		t.Errorf("ContentType = %q, want binary for a file >= 1MB", s.Content.ContentType)
	}
}

func TestWatcher_ReconciliationOnStart_PublishesExistingFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("already here"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cp := LoadCheckpoint(filepath.Join(t.TempDir(), "checkpoints.json"))
	pub := &mockPublisher{}
	w, err := New([]string{dir}, cp, pub, time.Hour) // long interval — rely on the startup pass
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	if pub.count() != 1 {
		t.Fatalf("published count = %d, want 1 after startup reconciliation", pub.count())
	}
	if pub.last().Content.RawText != "already here" {
		t.Errorf("published RawText = %q, want file contents", pub.last().Content.RawText)
	}
	if cp.IsNew(dir, "existing.txt", hashBytes([]byte("already here"))) {
		t.Errorf("checkpoint not updated after successful publish")
	}
}

func TestWatcher_Reconcile_SkipsAlreadyCheckpointedFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "seen.txt"), []byte("content"), 0o644)

	cp := LoadCheckpoint(filepath.Join(t.TempDir(), "checkpoints.json"))
	pub := &mockPublisher{}
	w, _ := New([]string{dir}, cp, pub, time.Hour)
	defer w.Close()

	ctx := context.Background()
	w.reconcileAll(ctx) // first pass: publishes
	w.reconcileAll(ctx) // second pass: should be a no-op

	if pub.count() != 1 {
		t.Errorf("published count = %d after two reconcile passes, want 1 (dedup via checkpoint)", pub.count())
	}
}

func TestWatcher_Reconcile_PublishFailureLeavesCheckpointUnset(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "fails.txt"), []byte("content"), 0o644)

	cp := LoadCheckpoint(filepath.Join(t.TempDir(), "checkpoints.json"))
	pub := &mockPublisher{failNext: true}
	w, _ := New([]string{dir}, cp, pub, time.Hour)
	defer w.Close()

	w.reconcileAll(context.Background())

	if pub.count() != 0 {
		t.Errorf("published count = %d, want 0 (publish failed)", pub.count())
	}
	if !cp.IsNew(dir, "fails.txt", hashBytes([]byte("content"))) {
		t.Errorf("checkpoint marked despite publish failure — retry would be skipped")
	}
}

func TestWatcher_Reconcile_MissingDirectory_ContinuesWithOthers(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	valid := t.TempDir()
	os.WriteFile(filepath.Join(valid, "ok.txt"), []byte("fine"), 0o644)

	cp := LoadCheckpoint(filepath.Join(t.TempDir(), "checkpoints.json"))
	pub := &mockPublisher{}
	w, _ := New([]string{missing, valid}, cp, pub, time.Hour)
	defer w.Close()

	w.reconcileAll(context.Background())

	if pub.count() != 1 {
		t.Errorf("published count = %d, want 1 (missing dir skipped, valid dir processed)", pub.count())
	}
}

func TestIsTempFilename(t *testing.T) {
	cases := map[string]bool{
		"report.txt.tmp.42272.f44c9650e4f9": true,
		"report.txt.tmp":                    true,
		"report.txt":                        false,
		"tmp-report.txt":                    false,
	}
	for name, want := range cases {
		if got := isTempFilename(name); got != want {
			t.Errorf("isTempFilename(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestWatcher_HandleFile_SkipsTempFilename(t *testing.T) {
	dir := t.TempDir()
	name := "report.txt.tmp.123.abc"
	os.WriteFile(filepath.Join(dir, name), []byte("partial content"), 0o644)

	cp := LoadCheckpoint(filepath.Join(t.TempDir(), "checkpoints.json"))
	pub := &mockPublisher{}
	w, _ := New([]string{dir}, cp, pub, time.Hour)
	defer w.Close()

	w.handleFile(context.Background(), dir, name)

	if pub.count() != 0 {
		t.Errorf("published count = %d, want 0 for a temp filename", pub.count())
	}
}

func TestWatcher_HandleFile_SkipsZeroByteRead(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "empty.txt"), []byte{}, 0o644)

	cp := LoadCheckpoint(filepath.Join(t.TempDir(), "checkpoints.json"))
	pub := &mockPublisher{}
	w, _ := New([]string{dir}, cp, pub, time.Hour)
	defer w.Close()

	w.handleFile(context.Background(), dir, "empty.txt")

	if pub.count() != 0 {
		t.Errorf("published count = %d, want 0 for a zero-byte read", pub.count())
	}
	if !cp.IsNew(dir, "empty.txt", hashBytes(nil)) {
		t.Errorf("checkpoint marked despite empty read — later content would be skipped as already-seen")
	}
}

func TestWatcher_HandleFile_ZeroByteThenRealContent_BothProcessed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "grows.txt")
	os.WriteFile(path, []byte{}, 0o644)

	cp := LoadCheckpoint(filepath.Join(t.TempDir(), "checkpoints.json"))
	pub := &mockPublisher{}
	w, _ := New([]string{dir}, cp, pub, time.Hour)
	defer w.Close()

	w.handleFile(context.Background(), dir, "grows.txt")
	if pub.count() != 0 {
		t.Fatalf("published count = %d after empty read, want 0", pub.count())
	}

	os.WriteFile(path, []byte("now has content"), 0o644)
	w.handleFile(context.Background(), dir, "grows.txt")
	if pub.count() != 1 {
		t.Fatalf("published count = %d after real content written, want 1", pub.count())
	}
	if pub.last().Content.RawText != "now has content" {
		t.Errorf("RawText = %q, want the real content", pub.last().Content.RawText)
	}
}

func TestWatcher_HandleFile_DeletedBeforeRead_NoPanic(t *testing.T) {
	dir := t.TempDir()
	cp := LoadCheckpoint(filepath.Join(t.TempDir(), "checkpoints.json"))
	pub := &mockPublisher{}
	w, _ := New([]string{dir}, cp, pub, time.Hour)
	defer w.Close()

	w.handleFile(context.Background(), dir, "never-existed.txt")

	if pub.count() != 0 {
		t.Errorf("published count = %d, want 0 for a file that doesn't exist", pub.count())
	}
}

func TestWatcher_Start_DetectsNewFileViaFsnotify(t *testing.T) {
	dir := t.TempDir()
	cp := LoadCheckpoint(filepath.Join(t.TempDir(), "checkpoints.json"))
	pub := &mockPublisher{}
	w, err := New([]string{dir}, cp, pub, time.Hour) // rely on fsnotify, not the reconcile ticker
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	if err := os.WriteFile(filepath.Join(dir, "live.txt"), []byte("fresh error"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if pub.count() >= 1 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if pub.count() < 1 {
		t.Fatalf("fsnotify did not deliver a create event for live.txt within 5s")
	}
}
