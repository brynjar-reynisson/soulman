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

type fakeHealth struct {
	mu      sync.Mutex
	healthy map[string]bool
	detail  map[string]string
}

func (f *fakeHealth) Check(target string, timeout time.Duration) (bool, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.healthy[target], f.detail[target]
}

func (f *fakeHealth) set(target string, healthy bool, detail string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.healthy == nil {
		f.healthy = map[string]bool{}
	}
	if f.detail == nil {
		f.detail = map[string]string{}
	}
	f.healthy[target] = healthy
	f.detail[target] = detail
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

func serviceCheck(name, target string) CheckConfig {
	return CheckConfig{Type: "service_health", Name: name, Target: target}
}

func TestPoll_NoThresholdCrossed_NoStimulus(t *testing.T) {
	stats := &fakeStats{disk: map[string]float64{`C:\`: 50}}
	pub := &fakePublisher{}
	w := newWatcher(stats, nil, []CheckConfig{diskCheck(`C:\`)}, pub, time.Hour)

	w.poll(context.Background())
	w.poll(context.Background())

	if got := pub.publishedCount(); got != 0 {
		t.Errorf("published = %d, want 0 (steady ok state)", got)
	}
}

func TestPoll_CrossesIntoWarning_PublishesOnce(t *testing.T) {
	stats := &fakeStats{disk: map[string]float64{`C:\`: 50}}
	pub := &fakePublisher{}
	w := newWatcher(stats, nil, []CheckConfig{diskCheck(`C:\`)}, pub, time.Hour)

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
	w := newWatcher(stats, nil, []CheckConfig{diskCheck(`C:\`)}, pub, time.Hour)

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
	w := newWatcher(stats, nil, []CheckConfig{diskCheck(`C:\`)}, pub, time.Hour)

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
	w := newWatcher(stats, nil, checks, pub, time.Hour)

	w.poll(context.Background())

	if got := pub.publishedCount(); got != 1 {
		t.Fatalf("published = %d, want 1 (memory check should still fire despite disk error)", got)
	}
}

func TestPoll_PublishFailure_StateNotAdvanced_RetriesNextPoll(t *testing.T) {
	stats := &fakeStats{disk: map[string]float64{`C:\`: 50}}
	pub := &fakePublisher{}
	w := newWatcher(stats, nil, []CheckConfig{diskCheck(`C:\`)}, pub, time.Hour)

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
	w := newWatcher(stats, nil, checks, pub, time.Hour)

	w.poll(context.Background())

	if got := pub.publishedCount(); got != 1 {
		t.Fatalf("published = %d, want 1 (only D:\\ starts critical)", got)
	}
}

func TestPoll_CPUNoBaselineFirstCall_SkippedSilently(t *testing.T) {
	stats := &fakeStats{cpuErr: errNoCPUBaseline}
	pub := &fakePublisher{}
	checks := []CheckConfig{{Type: "cpu", WarningThresholdPercent: 90}}
	w := newWatcher(stats, nil, checks, pub, time.Hour)

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

func TestPoll_ServiceHealth_NoChange_NoStimulus(t *testing.T) {
	health := &fakeHealth{healthy: map[string]bool{"svc:1234": true}}
	pub := &fakePublisher{}
	w := newWatcher(&fakeStats{}, health, []CheckConfig{serviceCheck("svc", "svc:1234")}, pub, time.Hour)

	w.poll(context.Background())
	w.poll(context.Background())

	if got := pub.publishedCount(); got != 0 {
		t.Errorf("published = %d, want 0 (steady healthy state)", got)
	}
}

func TestPoll_ServiceHealth_GoesDownThenRecovers(t *testing.T) {
	health := &fakeHealth{healthy: map[string]bool{"svc:1234": true}}
	pub := &fakePublisher{}
	w := newWatcher(&fakeStats{}, health, []CheckConfig{serviceCheck("svc", "svc:1234")}, pub, time.Hour)

	w.poll(context.Background()) // healthy baseline, no stimulus

	health.set("svc:1234", false, "dial tcp svc:1234: connect: connection refused")
	w.poll(context.Background()) // ok -> critical

	if got := pub.publishedCount(); got != 1 {
		t.Fatalf("published = %d, want 1", got)
	}
	if pub.published[0].Hints.Priority != "critical" {
		t.Errorf("Hints.Priority = %q, want critical", pub.published[0].Hints.Priority)
	}
	wantText := "Service down: svc unreachable (dial tcp svc:1234: connect: connection refused)"
	if got := pub.published[0].Content.RawText; got != wantText {
		t.Errorf("RawText = %q, want %q", got, wantText)
	}

	health.set("svc:1234", true, "")
	w.poll(context.Background()) // critical -> ok

	if got := pub.publishedCount(); got != 2 {
		t.Fatalf("published = %d, want 2 (down + recovery)", got)
	}
	if pub.published[1].Content.RawText != "Service recovered: svc is back up" {
		t.Errorf("recovery RawText = %q, want %q", pub.published[1].Content.RawText, "Service recovered: svc is back up")
	}
	if pub.published[1].Hints.Priority != "normal" {
		t.Errorf("recovery Hints.Priority = %q, want normal", pub.published[1].Hints.Priority)
	}
}

func TestPoll_ServiceHealth_FirstPollAlreadyDown_PublishesImmediately(t *testing.T) {
	health := &fakeHealth{
		healthy: map[string]bool{"svc:1234": false},
		detail:  map[string]string{"svc:1234": "dial tcp: i/o timeout"},
	}
	pub := &fakePublisher{}
	w := newWatcher(&fakeStats{}, health, []CheckConfig{serviceCheck("svc", "svc:1234")}, pub, time.Hour)

	w.poll(context.Background())

	if got := pub.publishedCount(); got != 1 {
		t.Fatalf("published = %d, want 1 (already-down state must fire on first poll, not be treated as a baseline)", got)
	}
}

func TestPoll_ServiceHealth_PublishFailure_StateNotAdvanced_RetriesNextPoll(t *testing.T) {
	health := &fakeHealth{healthy: map[string]bool{"svc:1234": true}}
	pub := &fakePublisher{}
	w := newWatcher(&fakeStats{}, health, []CheckConfig{serviceCheck("svc", "svc:1234")}, pub, time.Hour)

	w.poll(context.Background()) // healthy baseline

	health.set("svc:1234", false, "connection refused")
	pub.publishErr = errors.New("nats down")
	w.poll(context.Background()) // ok -> critical, publish fails

	if got := pub.publishedCount(); got != 0 {
		t.Fatalf("published = %d, want 0 (publish failed)", got)
	}

	pub.publishErr = nil
	w.poll(context.Background()) // retry: still ok -> critical transition

	if got := pub.publishedCount(); got != 1 {
		t.Errorf("published = %d, want 1 (transition retried after publish recovered)", got)
	}
}

func TestPoll_ServiceHealthAndDiskCheck_TrackedIndependently(t *testing.T) {
	stats := &fakeStats{disk: map[string]float64{`C:\`: 50}}
	health := &fakeHealth{healthy: map[string]bool{"svc:1234": false}}
	pub := &fakePublisher{}
	checks := []CheckConfig{diskCheck(`C:\`), serviceCheck("svc", "svc:1234")}
	w := newWatcher(stats, health, checks, pub, time.Hour)

	w.poll(context.Background())

	if got := pub.publishedCount(); got != 1 {
		t.Fatalf("published = %d, want 1 (only the service check starts down)", got)
	}

	var meta struct {
		CheckType string `json:"check_type"`
		Name      string `json:"name"`
	}
	if err := json.Unmarshal(pub.published[0].ChannelMeta.ChannelSpecific, &meta); err != nil {
		t.Fatalf("decode channel_specific: %v", err)
	}
	if meta.CheckType != "service_health" || meta.Name != "svc" {
		t.Errorf("channel_specific = %+v, want check_type=service_health name=svc", meta)
	}
}
