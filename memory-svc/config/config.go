package config

import (
	"fmt"
	"os"

	"soulman/common/sharedconfig"
)

type Config struct {
	NATSURL              string
	DatabaseURL          string
	HTTPPort             string
	LogDir               string
	Schema               string
	StimulusSubject      string
	ConsumerName         string
	MemoryWriteSubject   string
	EpisodesConsumerName string
}

func Load() (*Config, error) {
	configPath := env("CONFIG_PATH", "./config.json")

	shared, err := sharedconfig.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("loading shared config: %w", err)
	}
	if shared.NATSURL == "" {
		return nil, fmt.Errorf("shared config %s has no nats_url configured", configPath)
	}
	if shared.StimulusSubject == "" {
		return nil, fmt.Errorf("shared config %s has no stimulus_subject configured", configPath)
	}
	if shared.ConsumerNames.MemorySvc == "" {
		return nil, fmt.Errorf("shared config %s has no consumer_names.memory_svc configured", configPath)
	}
	if shared.MemoryWriteSubject == "" {
		return nil, fmt.Errorf("shared config %s has no memory_write_subject configured", configPath)
	}
	if shared.ConsumerNames.MemorySvcEpisodes == "" {
		return nil, fmt.Errorf("shared config %s has no consumer_names.memory_svc_episodes configured", configPath)
	}

	return &Config{
		NATSURL:              shared.NATSURL,
		DatabaseURL:          env("DATABASE_URL", "postgres://postgres:postgres@localhost:54322/postgres"),
		HTTPPort:             env("HTTP_PORT", "9002"),
		LogDir:               env("LOG_DIR", "./logs"),
		Schema:               env("SCHEMA", "memory_dev"),
		StimulusSubject:      shared.StimulusSubject,
		ConsumerName:         shared.ConsumerNames.MemorySvc,
		MemoryWriteSubject:   shared.MemoryWriteSubject,
		EpisodesConsumerName: shared.ConsumerNames.MemorySvcEpisodes,
	}, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
