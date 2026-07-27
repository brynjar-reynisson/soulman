package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"soulman/action-svc/config"
)

type consumerNames struct {
	ActionSvc string `json:"action_svc"`
}

type sharedFields struct {
	NATSURL                string        `json:"nats_url"`
	ThinkingRequestSubject string        `json:"thinking_request_subject"`
	MemoryWriteSubject     string        `json:"memory_write_subject"`
	ConsumerNames          consumerNames `json:"consumer_names"`
}

func writeConfigFile(t *testing.T, natsURL, thinkingRequestSubject, memoryWriteSubject, actionSvcConsumerName string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	data, err := json.Marshal(sharedFields{
		NATSURL:                natsURL,
		ThinkingRequestSubject: thinkingRequestSubject,
		MemoryWriteSubject:     memoryWriteSubject,
		ConsumerNames:          consumerNames{ActionSvc: actionSvcConsumerName},
	})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func unsetAllEnv() {
	os.Unsetenv("HTTP_PORT")
	os.Unsetenv("CONFIG_PATH")
	os.Unsetenv("SOULMAN_ROOT")
	os.Unsetenv("REPORT_SEND_TIME")
	os.Unsetenv("REPORT_NOTIFIER")
	os.Unsetenv("DISCORD_BOT_TOKEN")
	os.Unsetenv("DISCORD_CHANNEL_ID")
}

func TestLoad_Defaults(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://localhost:4222", "soulman.thinking.request", "soulman.memory.write", "action-svc")
	os.Setenv("CONFIG_PATH", configPath)
	defer os.Unsetenv("CONFIG_PATH")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.NATSURL != "nats://localhost:4222" {
		t.Errorf("NATSURL = %q, want nats://localhost:4222", cfg.NATSURL)
	}
	if cfg.HTTPPort != "9004" {
		t.Errorf("HTTPPort = %q, want 9004", cfg.HTTPPort)
	}
	if cfg.SoulmanRoot != `C:\Users\Lenovo\soulman-dev` {
		t.Errorf("SoulmanRoot = %q, want C:\\Users\\Lenovo\\soulman-dev", cfg.SoulmanRoot)
	}
	if cfg.ReportSendTime != "10:00" {
		t.Errorf("ReportSendTime = %q, want 10:00", cfg.ReportSendTime)
	}
	if cfg.ReportNotifier != "discord" {
		t.Errorf("ReportNotifier = %q, want discord", cfg.ReportNotifier)
	}
	if cfg.DiscordBotToken != "" {
		t.Errorf("DiscordBotToken = %q, want empty when unset", cfg.DiscordBotToken)
	}
	if cfg.DiscordChannelID != "" {
		t.Errorf("DiscordChannelID = %q, want empty when unset", cfg.DiscordChannelID)
	}
	if cfg.ThinkingRequestSubject != "soulman.thinking.request" {
		t.Errorf("ThinkingRequestSubject = %q, want soulman.thinking.request", cfg.ThinkingRequestSubject)
	}
	if cfg.MemoryWriteSubject != "soulman.memory.write" {
		t.Errorf("MemoryWriteSubject = %q, want soulman.memory.write", cfg.MemoryWriteSubject)
	}
	if cfg.ActionSvcConsumerName != "action-svc" {
		t.Errorf("ActionSvcConsumerName = %q, want action-svc", cfg.ActionSvcConsumerName)
	}
}

func TestLoad_SharedConfigValues(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://remote:4222", "soulman.dev.thinking.request", "soulman.dev.memory.write", "action-svc-dev")
	os.Setenv("CONFIG_PATH", configPath)
	defer os.Unsetenv("CONFIG_PATH")
	os.Setenv("SOULMAN_ROOT", `C:\Users\Lenovo\soulman-prod`)
	defer os.Unsetenv("SOULMAN_ROOT")
	os.Setenv("REPORT_SEND_TIME", "09:30")
	defer os.Unsetenv("REPORT_SEND_TIME")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.NATSURL != "nats://remote:4222" {
		t.Errorf("NATSURL = %q, want nats://remote:4222", cfg.NATSURL)
	}
	if cfg.SoulmanRoot != `C:\Users\Lenovo\soulman-prod` {
		t.Errorf("SoulmanRoot = %q, want C:\\Users\\Lenovo\\soulman-prod", cfg.SoulmanRoot)
	}
	if cfg.ReportSendTime != "09:30" {
		t.Errorf("ReportSendTime = %q, want 09:30", cfg.ReportSendTime)
	}
	if cfg.ThinkingRequestSubject != "soulman.dev.thinking.request" {
		t.Errorf("ThinkingRequestSubject = %q, want soulman.dev.thinking.request", cfg.ThinkingRequestSubject)
	}
	if cfg.MemoryWriteSubject != "soulman.dev.memory.write" {
		t.Errorf("MemoryWriteSubject = %q, want soulman.dev.memory.write", cfg.MemoryWriteSubject)
	}
	if cfg.ActionSvcConsumerName != "action-svc-dev" {
		t.Errorf("ActionSvcConsumerName = %q, want action-svc-dev", cfg.ActionSvcConsumerName)
	}
}

func TestLoad_MissingConfigFile_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	dir := t.TempDir()
	os.Setenv("CONFIG_PATH", filepath.Join(dir, "does-not-exist.json"))
	defer os.Unsetenv("CONFIG_PATH")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for missing config file, got nil")
	}
}

func TestLoad_EmptyThinkingRequestSubject_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://localhost:4222", "", "soulman.memory.write", "action-svc")
	os.Setenv("CONFIG_PATH", configPath)
	defer os.Unsetenv("CONFIG_PATH")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty thinking_request_subject, got nil")
	}
}

func TestLoad_EmptyMemoryWriteSubject_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://localhost:4222", "soulman.thinking.request", "", "action-svc")
	os.Setenv("CONFIG_PATH", configPath)
	defer os.Unsetenv("CONFIG_PATH")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty memory_write_subject, got nil")
	}
}

func TestLoad_EmptyNATSURL_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "", "soulman.thinking.request", "soulman.memory.write", "action-svc")
	os.Setenv("CONFIG_PATH", configPath)
	defer os.Unsetenv("CONFIG_PATH")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty nats_url, got nil")
	}
}

func TestLoad_EmptyActionSvcConsumerName_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://localhost:4222", "soulman.thinking.request", "soulman.memory.write", "")
	os.Setenv("CONFIG_PATH", configPath)
	defer os.Unsetenv("CONFIG_PATH")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty consumer_names.action_svc, got nil")
	}
}

func TestLoad_FeignModeTrue(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"nats_url": "nats://localhost:4222",
		"thinking_request_subject": "soulman.thinking.request",
		"memory_write_subject": "soulman.memory.write",
		"consumer_names": {"action_svc": "action-svc"},
		"feign_mode": true
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	os.Setenv("CONFIG_PATH", path)
	defer os.Unsetenv("CONFIG_PATH")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.FeignMode {
		t.Error("FeignMode = false, want true")
	}
}

func TestLoad_FeignModeAbsent_DefaultsFalse(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://localhost:4222", "soulman.thinking.request", "soulman.memory.write", "action-svc")
	os.Setenv("CONFIG_PATH", configPath)
	defer os.Unsetenv("CONFIG_PATH")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.FeignMode {
		t.Error("FeignMode = true, want false when absent from JSON")
	}
}

func TestLoad_DoNotDisturb_AllFieldsSet(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"nats_url": "nats://localhost:4222",
		"thinking_request_subject": "soulman.thinking.request",
		"memory_write_subject": "soulman.memory.write",
		"consumer_names": {"action_svc": "action-svc"},
		"do_not_disturb": {"enabled": true, "start": "00:00", "end": "10:00"}
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	os.Setenv("CONFIG_PATH", path)
	defer os.Unsetenv("CONFIG_PATH")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.DNDEnabled {
		t.Error("DNDEnabled = false, want true")
	}
	if cfg.DNDStart != "00:00" {
		t.Errorf("DNDStart = %q, want 00:00", cfg.DNDStart)
	}
	if cfg.DNDEnd != "10:00" {
		t.Errorf("DNDEnd = %q, want 10:00", cfg.DNDEnd)
	}
}

func TestLoad_DoNotDisturb_Absent_DefaultsDisabledAndEmpty(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://localhost:4222", "soulman.thinking.request", "soulman.memory.write", "action-svc")
	os.Setenv("CONFIG_PATH", configPath)
	defer os.Unsetenv("CONFIG_PATH")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DNDEnabled {
		t.Error("DNDEnabled = true, want false when do_not_disturb absent from JSON")
	}
	if cfg.DNDStart != "" {
		t.Errorf("DNDStart = %q, want empty when do_not_disturb absent", cfg.DNDStart)
	}
	if cfg.DNDEnd != "" {
		t.Errorf("DNDEnd = %q, want empty when do_not_disturb absent", cfg.DNDEnd)
	}
}

func TestLoad_DoNotDisturb_MalformedTime_PassesThroughWithoutError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"nats_url": "nats://localhost:4222",
		"thinking_request_subject": "soulman.thinking.request",
		"memory_write_subject": "soulman.memory.write",
		"consumer_names": {"action_svc": "action-svc"},
		"do_not_disturb": {"enabled": true, "start": "not-a-time", "end": "10:00"}
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	os.Setenv("CONFIG_PATH", path)
	defer os.Unsetenv("CONFIG_PATH")

	// action-svc/config.Load performs no time-format validation of its own
	// (matching how ReportSendTime is handled) — a malformed value is
	// passed through unchanged; the actual "HH:MM" parsing and fallback to
	// 10:00 happens downstream in dnd.Window.Active, covered by
	// action-svc/dnd/window_test.go.
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: want no error for a malformed do_not_disturb.start, got %v", err)
	}
	if cfg.DNDStart != "not-a-time" {
		t.Errorf("DNDStart = %q, want the malformed value passed through unchanged", cfg.DNDStart)
	}
}
