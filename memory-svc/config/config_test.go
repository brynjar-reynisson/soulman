package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"soulman/memory-svc/config"
)

type consumerNames struct {
	MemorySvc         string `json:"memory_svc"`
	MemorySvcEpisodes string `json:"memory_svc_episodes"`
}

type sharedFields struct {
	NATSURL            string        `json:"nats_url"`
	StimulusSubject    string        `json:"stimulus_subject"`
	MemoryWriteSubject string        `json:"memory_write_subject"`
	ConsumerNames      consumerNames `json:"consumer_names"`
}

func writeConfigFile(t *testing.T, natsURL, stimulusSubject, memoryWriteSubject, consumerName, episodesConsumerName string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	data, err := json.Marshal(sharedFields{
		NATSURL:            natsURL,
		StimulusSubject:    stimulusSubject,
		MemoryWriteSubject: memoryWriteSubject,
		ConsumerNames:      consumerNames{MemorySvc: consumerName, MemorySvcEpisodes: episodesConsumerName},
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
	os.Unsetenv("SCHEMA")
	os.Unsetenv("DATABASE_URL")
	os.Unsetenv("LOG_DIR")
}

func TestLoad_Defaults(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://localhost:4222", "soulman.stimulus.raw", "soulman.memory.write", "memory-svc", "memory-svc-episodes")
	os.Setenv("CONFIG_PATH", configPath)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.NATSURL != "nats://localhost:4222" {
		t.Errorf("NATSURL = %q, want nats://localhost:4222", cfg.NATSURL)
	}
	if cfg.HTTPPort != "9002" {
		t.Errorf("HTTPPort = %q, want 9002", cfg.HTTPPort)
	}
	if cfg.Schema != "memory_dev" {
		t.Errorf("Schema = %q, want memory_dev", cfg.Schema)
	}
	if cfg.StimulusSubject != "soulman.stimulus.raw" {
		t.Errorf("StimulusSubject = %q, want soulman.stimulus.raw", cfg.StimulusSubject)
	}
	if cfg.ConsumerName != "memory-svc" {
		t.Errorf("ConsumerName = %q, want memory-svc", cfg.ConsumerName)
	}
	if cfg.MemoryWriteSubject != "soulman.memory.write" {
		t.Errorf("MemoryWriteSubject = %q, want soulman.memory.write", cfg.MemoryWriteSubject)
	}
	if cfg.EpisodesConsumerName != "memory-svc-episodes" {
		t.Errorf("EpisodesConsumerName = %q, want memory-svc-episodes", cfg.EpisodesConsumerName)
	}
}

func TestLoad_SharedConfigValues(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://remote:4222", "soulman.dev.stimulus.raw", "soulman.dev.memory.write", "memory-svc-dev", "memory-svc-episodes-dev")
	os.Setenv("CONFIG_PATH", configPath)
	os.Setenv("SCHEMA", "memory_prod")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.NATSURL != "nats://remote:4222" {
		t.Errorf("NATSURL = %q, want nats://remote:4222", cfg.NATSURL)
	}
	if cfg.Schema != "memory_prod" {
		t.Errorf("Schema = %q, want memory_prod", cfg.Schema)
	}
	if cfg.StimulusSubject != "soulman.dev.stimulus.raw" {
		t.Errorf("StimulusSubject = %q, want soulman.dev.stimulus.raw", cfg.StimulusSubject)
	}
	if cfg.ConsumerName != "memory-svc-dev" {
		t.Errorf("ConsumerName = %q, want memory-svc-dev", cfg.ConsumerName)
	}
	if cfg.MemoryWriteSubject != "soulman.dev.memory.write" {
		t.Errorf("MemoryWriteSubject = %q, want soulman.dev.memory.write", cfg.MemoryWriteSubject)
	}
	if cfg.EpisodesConsumerName != "memory-svc-episodes-dev" {
		t.Errorf("EpisodesConsumerName = %q, want memory-svc-episodes-dev", cfg.EpisodesConsumerName)
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

func TestLoad_EmptyConsumerName_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://localhost:4222", "soulman.stimulus.raw", "soulman.memory.write", "", "memory-svc-episodes")
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty consumer_names.memory_svc, got nil")
	}
}

func TestLoad_EmptyNATSURL_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "", "soulman.stimulus.raw", "soulman.memory.write", "memory-svc", "memory-svc-episodes")
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty nats_url, got nil")
	}
}

func TestLoad_EmptyMemoryWriteSubject_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://localhost:4222", "soulman.stimulus.raw", "", "memory-svc", "memory-svc-episodes")
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty memory_write_subject, got nil")
	}
}

func TestLoad_EmptyEpisodesConsumerName_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://localhost:4222", "soulman.stimulus.raw", "soulman.memory.write", "memory-svc", "")
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty consumer_names.memory_svc_episodes, got nil")
	}
}
