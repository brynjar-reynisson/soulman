package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"soulman/thinking-svc/config"
)

type consumerNames struct {
	ThinkingSvc string `json:"thinking_svc"`
}

type sharedFields struct {
	NATSURL                string        `json:"nats_url"`
	StimulusSubject        string        `json:"stimulus_subject"`
	ThinkingRequestSubject string        `json:"thinking_request_subject"`
	ConsumerNames          consumerNames `json:"consumer_names"`
}

func writeConfigFile(t *testing.T, natsURL, stimulusSubject, thinkingRequestSubject, consumerName string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	data, err := json.Marshal(sharedFields{
		NATSURL:                natsURL,
		StimulusSubject:        stimulusSubject,
		ThinkingRequestSubject: thinkingRequestSubject,
		ConsumerNames:          consumerNames{ThinkingSvc: consumerName},
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
	os.Unsetenv("DEEPSEEK_API_KEY")
	os.Unsetenv("DEEPSEEK_MODEL")
	os.Unsetenv("DEEPSEEK_BASE_URL")
	os.Unsetenv("DEEPSEEK_TIMEOUT_SECONDS")
}

func TestLoad_Defaults(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://localhost:4222", "soulman.stimulus.raw", "soulman.thinking.request", "thinking-svc")
	os.Setenv("CONFIG_PATH", configPath)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.NATSURL != "nats://localhost:4222" {
		t.Errorf("NATSURL = %q, want nats://localhost:4222", cfg.NATSURL)
	}
	if cfg.HTTPPort != "9003" {
		t.Errorf("HTTPPort = %q, want 9003", cfg.HTTPPort)
	}
	if cfg.DeepSeekAPIKey != "" {
		t.Errorf("DeepSeekAPIKey = %q, want empty (no default key)", cfg.DeepSeekAPIKey)
	}
	if cfg.DeepSeekModel != "deepseek-chat" {
		t.Errorf("DeepSeekModel = %q, want deepseek-chat", cfg.DeepSeekModel)
	}
	if cfg.DeepSeekBaseURL != "https://api.deepseek.com" {
		t.Errorf("DeepSeekBaseURL = %q, want https://api.deepseek.com", cfg.DeepSeekBaseURL)
	}
	if cfg.DeepSeekTimeoutSeconds != 15 {
		t.Errorf("DeepSeekTimeoutSeconds = %d, want 15", cfg.DeepSeekTimeoutSeconds)
	}
	if cfg.StimulusSubject != "soulman.stimulus.raw" {
		t.Errorf("StimulusSubject = %q, want soulman.stimulus.raw", cfg.StimulusSubject)
	}
	if cfg.ConsumerName != "thinking-svc" {
		t.Errorf("ConsumerName = %q, want thinking-svc", cfg.ConsumerName)
	}
	if cfg.ThinkingRequestSubject != "soulman.thinking.request" {
		t.Errorf("ThinkingRequestSubject = %q, want soulman.thinking.request", cfg.ThinkingRequestSubject)
	}
}

func TestLoad_SharedConfigValues(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://remote:4222", "soulman.dev.stimulus.raw", "soulman.dev.thinking.request", "thinking-svc-dev")
	os.Setenv("CONFIG_PATH", configPath)
	os.Setenv("HTTP_PORT", "9099")
	os.Setenv("DEEPSEEK_API_KEY", "sk-test")
	os.Setenv("DEEPSEEK_TIMEOUT_SECONDS", "30")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.NATSURL != "nats://remote:4222" {
		t.Errorf("NATSURL = %q, want nats://remote:4222", cfg.NATSURL)
	}
	if cfg.HTTPPort != "9099" {
		t.Errorf("HTTPPort = %q, want 9099", cfg.HTTPPort)
	}
	if cfg.DeepSeekAPIKey != "sk-test" {
		t.Errorf("DeepSeekAPIKey = %q, want sk-test", cfg.DeepSeekAPIKey)
	}
	if cfg.DeepSeekTimeoutSeconds != 30 {
		t.Errorf("DeepSeekTimeoutSeconds = %d, want 30", cfg.DeepSeekTimeoutSeconds)
	}
	if cfg.StimulusSubject != "soulman.dev.stimulus.raw" {
		t.Errorf("StimulusSubject = %q, want soulman.dev.stimulus.raw", cfg.StimulusSubject)
	}
	if cfg.ConsumerName != "thinking-svc-dev" {
		t.Errorf("ConsumerName = %q, want thinking-svc-dev", cfg.ConsumerName)
	}
	if cfg.ThinkingRequestSubject != "soulman.dev.thinking.request" {
		t.Errorf("ThinkingRequestSubject = %q, want soulman.dev.thinking.request", cfg.ThinkingRequestSubject)
	}
}

func TestLoad_InvalidTimeoutFallsBackToDefault(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://localhost:4222", "soulman.stimulus.raw", "soulman.thinking.request", "thinking-svc")
	os.Setenv("CONFIG_PATH", configPath)
	os.Setenv("DEEPSEEK_TIMEOUT_SECONDS", "not-a-number")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DeepSeekTimeoutSeconds != 15 {
		t.Errorf("DeepSeekTimeoutSeconds = %d, want default 15 on invalid input", cfg.DeepSeekTimeoutSeconds)
	}
}

func TestLoad_MissingConfigFile_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	dir := t.TempDir()
	os.Setenv("CONFIG_PATH", filepath.Join(dir, "does-not-exist.json"))

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for missing config file, got nil")
	}
}

func TestLoad_EmptyThinkingRequestSubject_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://localhost:4222", "soulman.stimulus.raw", "", "thinking-svc")
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty thinking_request_subject, got nil")
	}
}

func TestLoad_EmptyConsumerName_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://localhost:4222", "soulman.stimulus.raw", "soulman.thinking.request", "")
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty consumer_names.thinking_svc, got nil")
	}
}

func TestLoad_EmptyNATSURL_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "", "soulman.stimulus.raw", "soulman.thinking.request", "thinking-svc")
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty nats_url, got nil")
	}
}

func TestLoad_EmptyStimulusSubject_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://localhost:4222", "", "soulman.thinking.request", "thinking-svc")
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty stimulus_subject, got nil")
	}
}
