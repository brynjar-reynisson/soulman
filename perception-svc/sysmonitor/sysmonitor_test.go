package sysmonitor

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"soulman/common"
)

type fakeStats struct {
	mu      sync.Mutex
	disk    map[string]float64
	diskErr error
	memory  float64
	memErr  error
	cpu     float64
	cpuErr  error
}

func (f *fakeStats) DiskUsagePercent(path string) (float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.diskErr != nil {
		return 0, f.diskErr
	}
	return f.disk[path], nil
}

func (f *fakeStats) MemoryUsagePercent() (float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.memory, f.memErr
}

func (f *fakeStats) CPUUsagePercent() (float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cpu, f.cpuErr
}

type fakePublisher struct {
	mu         sync.Mutex
	published  []*common.Stimulus
	publishErr error
}

func (f *fakePublisher) Publish(ctx context.Context, s *common.Stimulus) error {
	if f.publishErr != nil {
		return f.publishErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published = append(f.published, s)
	return nil
}

func (f *fakePublisher) publishedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.published)
}

func diskCheck(path string) CheckConfig {
	return CheckConfig{Type: "disk_space", Path: path, WarningThresholdPercent: 80, CriticalThresholdPercent: 95}
}

func TestPoll_NoThresholdCrossed_NoStimulus(t *testing.T) {
	stats := &fakeStats{disk: map[string]float64{`C:\`: 50}}
	pub := &fakePublisher{}
	w := newWatcher(stats, []CheckConfig{diskCheck(`C:\`)}, pub, time.Hour)

	w.poll(context.Background())
	w.poll(context.Background())

	if got := pub.publishedCount(); got != 0 {
		t.Errorf("published = %d, want 0 (steady ok state)", got)
	}
}

func TestPoll_CrossesIntoWarning_PublishesOnce(t *testing.T) {
	stats := &fakeStats{disk: map[string]float64{`C:\`: 50}}
	pub := &fakePublisher{}
	w := newWatcher(stats, []CheckConfig{diskCheck(`C:\`)}, pub, time.Hour)

	w.poll(context.Background()) // establishes ok baseline, no stimulus
	stats.mu.Lock()
	stats.disk[`C:\`] = 85
	stats.mu.Unlock()
	w.poll(context.Background()) // ok -> warning

	if got := pub.publishedCount(); got != 1 {
		t.Fatalf("published = %d, want 1", got)
	}
	if pub.published[0].Hints.Priority != "high" {
		t.Errorf("Hints.Priority = %q, want high", pub.published[0].Hints.Priority)
	}

	w.poll(context.Background()) // still warning, no new stimulus
	if got := pub.publishedCount(); got != 1 {
		t.Errorf("published = %d after repeated warning poll, want still 1", got)
	}
}

func TestPoll_EscalatesToCriticalThenRecovers(t *testing.T) {
	stats := &fakeStats{disk: map[string]float64{`C:\`: 50}}
	pub := &fakePublisher{}
	w := newWatcher(stats, []CheckConfig{diskCheck(`C:\`)}, pub, time.Hour)

	w.poll(context.Background()) // ok baseline

	stats.mu.Lock()
	stats.disk[`C:\`] = 85
	stats.mu.Unlock()
	w.poll(context.Background()) // ok -> warning

	stats.mu.Lock()
	stats.disk[`C:\`] = 97
	stats.mu.Unlock()
	w.poll(context.Background()) // warning -> critical

	stats.mu.Lock()
	stats.disk[`C:\`] = 50
	stats.mu.Unlock()
	w.poll(context.Background()) // critical -> ok

	if got := pub.publishedCount(); got != 3 {
		t.Fatalf("published = %d, want 3 (warning, critical, recovery)", got)
	}
	if pub.published[0].ChannelMeta.MessageID == "" {
		t.Error("MessageID must be set")
	}
	if got := severityFromStimulus(t, pub.published[1]); got != "critical" {
		t.Errorf("second stimulus severity = %q, want critical", got)
	}
	if pub.published[2].Hints.Priority != "normal" {
		t.Errorf("recovery Hints.Priority = %q, want normal", pub.published[2].Hints.Priority)
	}
}

func TestPoll_FirstPollAlreadyCritical_PublishesImmediately(t *testing.T) {
	stats := &fakeStats{disk: map[string]float64{`C:\`: 97}}
	pub := &fakePublisher{}
	w := newWatcher(stats, []CheckConfig{diskCheck(`C:\`)}, pub, time.Hour)

	w.poll(context.Background())

	if got := pub.publishedCount(); got != 1 {
		t.Fatalf("published = %d, want 1 (already-critical state must fire on first poll, not be treated as a baseline)", got)
	}
}

func TestPoll_CheckErrorSkipsThatCheckOnly(t *testing.T) {
	stats := &fakeStats{diskErr: errors.New("disk unavailable"), memory: 90}
	pub := &fakePublisher{}
	checks := []CheckConfig{
		diskCheck(`C:\`),
		{Type: "memory", WarningThresholdPercent: 85},
	}
	w := newWatcher(stats, checks, pub, time.Hour)

	w.poll(context.Background())

	if got := pub.publishedCount(); got != 1 {
		t.Fatalf("published = %d, want 1 (memory check should still fire despite disk error)", got)
	}
}

func TestPoll_PublishFailure_StateNotAdvanced_RetriesNextPoll(t *testing.T) {
	stats := &fakeStats{disk: map[string]float64{`C:\`: 50}}
	pub := &fakePublisher{}
	w := newWatcher(stats, []CheckConfig{diskCheck(`C:\`)}, pub, time.Hour)

	w.poll(context.Background()) // ok baseline

	stats.mu.Lock()
	stats.disk[`C:\`] = 85
	stats.mu.Unlock()
	pub.publishErr = errors.New("nats down")
	w.poll(context.Background()) // ok -> warning, publish fails

	if got := pub.publishedCount(); got != 0 {
		t.Fatalf("published = %d, want 0 (publish failed)", got)
	}

	pub.publishErr = nil
	w.poll(context.Background()) // retry: still ok -> warning transition

	if got := pub.publishedCount(); got != 1 {
		t.Errorf("published = %d, want 1 (transition retried after publish recovered)", got)
	}
}

func TestPoll_MultipleDiskPaths_TrackedIndependently(t *testing.T) {
	stats := &fakeStats{disk: map[string]float64{`C:\`: 50, `D:\`: 97}}
	pub := &fakePublisher{}
	checks := []CheckConfig{diskCheck(`C:\`), diskCheck(`D:\`)}
	w := newWatcher(stats, checks, pub, time.Hour)

	w.poll(context.Background())

	if got := pub.publishedCount(); got != 1 {
		t.Fatalf("published = %d, want 1 (only D:\\ starts critical)", got)
	}
}

func TestPoll_CPUNoBaselineFirstCall_SkippedSilently(t *testing.T) {
	stats := &fakeStats{cpuErr: errNoCPUBaseline}
	pub := &fakePublisher{}
	checks := []CheckConfig{{Type: "cpu", WarningThresholdPercent: 90}}
	w := newWatcher(stats, checks, pub, time.Hour)

	w.poll(context.Background())
	if got := pub.publishedCount(); got != 0 {
		t.Fatalf("published = %d, want 0 (no baseline yet)", got)
	}

	stats.mu.Lock()
	stats.cpuErr = nil
	stats.cpu = 95
	stats.mu.Unlock()
	w.poll(context.Background())
	if got := pub.publishedCount(); got != 1 {
		t.Errorf("published = %d, want 1 (first real reading is critical)", got)
	}
}

func severityFromStimulus(t *testing.T, s *common.Stimulus) string {
	t.Helper()
	var meta struct {
		Severity string `json:"severity"`
	}
	if err := json.Unmarshal(s.ChannelMeta.ChannelSpecific, &meta); err != nil {
		t.Fatalf("decode channel_specific: %v", err)
	}
	return meta.Severity
}
