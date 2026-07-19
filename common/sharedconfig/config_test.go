package sharedconfig_test

import (
	"os"
	"path/filepath"
	"testing"

	"soulman/common/sharedconfig"
)

func TestLoad_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{"watch_paths": ["C:\\a\\errors", "C:\\b\\errors"]}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := sharedconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := []string{`C:\a\errors`, `C:\b\errors`}
	if len(cfg.WatchPaths) != len(want) {
		t.Fatalf("WatchPaths = %v, want %v", cfg.WatchPaths, want)
	}
	for i, p := range want {
		if cfg.WatchPaths[i] != p {
			t.Errorf("WatchPaths[%d] = %q, want %q", i, cfg.WatchPaths[i], p)
		}
	}
}

func TestLoad_MissingFile_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.json")

	_, err := sharedconfig.Load(path)
	if err == nil {
		t.Fatal("Load: want error for missing file, got nil")
	}
}

func TestLoad_MalformedJSON_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := sharedconfig.Load(path)
	if err == nil {
		t.Fatal("Load: want error for malformed JSON, got nil")
	}
}

func TestLoad_EmptyWatchPaths_NotAnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"watch_paths": []}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := sharedconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.WatchPaths) != 0 {
		t.Errorf("WatchPaths = %v, want empty", cfg.WatchPaths)
	}
}

func TestLoad_AllFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"watch_paths": ["C:\\a\\errors"],
		"nats_url": "nats://localhost:4222",
		"stimulus_subject": "soulman.stimulus.raw",
		"thinking_request_subject": "soulman.thinking.request",
		"memory_write_subject": "soulman.memory.write",
		"consumer_names": {
			"memory_svc": "memory-svc",
			"thinking_svc": "thinking-svc"
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := sharedconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.NATSURL != "nats://localhost:4222" {
		t.Errorf("NATSURL = %q, want nats://localhost:4222", cfg.NATSURL)
	}
	if cfg.StimulusSubject != "soulman.stimulus.raw" {
		t.Errorf("StimulusSubject = %q, want soulman.stimulus.raw", cfg.StimulusSubject)
	}
	if cfg.ThinkingRequestSubject != "soulman.thinking.request" {
		t.Errorf("ThinkingRequestSubject = %q, want soulman.thinking.request", cfg.ThinkingRequestSubject)
	}
	if cfg.MemoryWriteSubject != "soulman.memory.write" {
		t.Errorf("MemoryWriteSubject = %q, want soulman.memory.write", cfg.MemoryWriteSubject)
	}
	if cfg.ConsumerNames.MemorySvc != "memory-svc" {
		t.Errorf("ConsumerNames.MemorySvc = %q, want memory-svc", cfg.ConsumerNames.MemorySvc)
	}
	if cfg.ConsumerNames.ThinkingSvc != "thinking-svc" {
		t.Errorf("ConsumerNames.ThinkingSvc = %q, want thinking-svc", cfg.ConsumerNames.ThinkingSvc)
	}
}

func TestLoad_MissingNATSFields_ZeroValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"watch_paths": []}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := sharedconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.NATSURL != "" {
		t.Errorf("NATSURL = %q, want empty when absent from JSON", cfg.NATSURL)
	}
	if cfg.ConsumerNames.MemorySvc != "" {
		t.Errorf("ConsumerNames.MemorySvc = %q, want empty when absent from JSON", cfg.ConsumerNames.MemorySvc)
	}
}

func TestLoad_GmailFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"watch_paths": ["C:\\a\\errors"],
		"gmail": {
			"query": "in:inbox is:unread -label:soulman/seen",
			"seen_label": "soulman/seen",
			"poll_interval_seconds": 60
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := sharedconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Gmail.Query != "in:inbox is:unread -label:soulman/seen" {
		t.Errorf("Gmail.Query = %q, want in:inbox is:unread -label:soulman/seen", cfg.Gmail.Query)
	}
	if cfg.Gmail.SeenLabel != "soulman/seen" {
		t.Errorf("Gmail.SeenLabel = %q, want soulman/seen", cfg.Gmail.SeenLabel)
	}
	if cfg.Gmail.PollIntervalSeconds != 60 {
		t.Errorf("Gmail.PollIntervalSeconds = %d, want 60", cfg.Gmail.PollIntervalSeconds)
	}
}

func TestLoad_MissingGmailField_ZeroValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"watch_paths": []}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := sharedconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Gmail.Query != "" {
		t.Errorf("Gmail.Query = %q, want empty when gmail block absent from JSON", cfg.Gmail.Query)
	}
	if cfg.Gmail.PollIntervalSeconds != 0 {
		t.Errorf("Gmail.PollIntervalSeconds = %d, want 0 when gmail block absent from JSON", cfg.Gmail.PollIntervalSeconds)
	}
}

func TestLoad_ActionSvcConsumerName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"watch_paths": [],
		"consumer_names": {
			"memory_svc": "memory-svc",
			"thinking_svc": "thinking-svc",
			"action_svc": "action-svc"
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := sharedconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ConsumerNames.ActionSvc != "action-svc" {
		t.Errorf("ConsumerNames.ActionSvc = %q, want action-svc", cfg.ConsumerNames.ActionSvc)
	}
}

func TestLoad_MemorySvcEpisodesConsumerName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"watch_paths": [],
		"consumer_names": {
			"memory_svc": "memory-svc",
			"memory_svc_episodes": "memory-svc-episodes",
			"thinking_svc": "thinking-svc",
			"action_svc": "action-svc"
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := sharedconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ConsumerNames.MemorySvcEpisodes != "memory-svc-episodes" {
		t.Errorf("ConsumerNames.MemorySvcEpisodes = %q, want memory-svc-episodes", cfg.ConsumerNames.MemorySvcEpisodes)
	}
}
func TestLoad_SystemMonitorFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"watch_paths": ["C:\\a\\errors"],
		"system_monitor": {
			"poll_interval_seconds": 300,
			"checks": [
				{"type": "disk_space", "path": "C:\\", "warning_threshold_percent": 80, "critical_threshold_percent": 95},
				{"type": "memory", "warning_threshold_percent": 85},
				{"type": "cpu", "warning_threshold_percent": 90}
			]
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := sharedconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.SystemMonitor.PollIntervalSeconds != 300 {
		t.Errorf("SystemMonitor.PollIntervalSeconds = %d, want 300", cfg.SystemMonitor.PollIntervalSeconds)
	}
	if len(cfg.SystemMonitor.Checks) != 3 {
		t.Fatalf("SystemMonitor.Checks = %d entries, want 3", len(cfg.SystemMonitor.Checks))
	}
	disk := cfg.SystemMonitor.Checks[0]
	if disk.Type != "disk_space" || disk.Path != `C:\` || disk.WarningThresholdPercent != 80 || disk.CriticalThresholdPercent != 95 {
		t.Errorf("Checks[0] = %+v, want disk_space C:\\ 80/95", disk)
	}
	mem := cfg.SystemMonitor.Checks[1]
	if mem.Type != "memory" || mem.WarningThresholdPercent != 85 || mem.CriticalThresholdPercent != 0 {
		t.Errorf("Checks[1] = %+v, want memory 85/0 (no critical tier)", mem)
	}
}

func TestLoad_SystemMonitorServiceHealthFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"watch_paths": ["C:\\a\\errors"],
		"system_monitor": {
			"poll_interval_seconds": 300,
			"checks": [
				{"type": "service_health", "name": "agent-suite-backend", "target": "http://localhost:8091/health"}
			]
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := sharedconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.SystemMonitor.Checks) != 1 {
		t.Fatalf("SystemMonitor.Checks = %d entries, want 1", len(cfg.SystemMonitor.Checks))
	}
	svc := cfg.SystemMonitor.Checks[0]
	if svc.Type != "service_health" || svc.Name != "agent-suite-backend" || svc.Target != "http://localhost:8091/health" {
		t.Errorf("Checks[0] = %+v, want service_health agent-suite-backend http://localhost:8091/health", svc)
	}
}

func TestLoad_MissingSystemMonitorField_ZeroValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"watch_paths": []}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := sharedconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SystemMonitor.PollIntervalSeconds != 0 {
		t.Errorf("SystemMonitor.PollIntervalSeconds = %d, want 0 when system_monitor absent from JSON", cfg.SystemMonitor.PollIntervalSeconds)
	}
	if len(cfg.SystemMonitor.Checks) != 0 {
		t.Errorf("SystemMonitor.Checks = %v, want empty when system_monitor absent from JSON", cfg.SystemMonitor.Checks)
	}
}

func TestLoad_FeignModeTrue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"watch_paths": [], "feign_mode": true}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := sharedconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.FeignMode {
		t.Error("FeignMode = false, want true")
	}
}

func TestLoad_MissingFeignMode_DefaultsFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"watch_paths": []}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := sharedconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.FeignMode {
		t.Error("FeignMode = true, want false when absent from JSON")
	}
}
