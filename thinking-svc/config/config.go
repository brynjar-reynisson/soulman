package config

import (
	"fmt"
	"os"
	"strconv"

	"soulman/common/sharedconfig"
)

type Config struct {
	NATSURL                string
	HTTPPort               string
	DeepSeekAPIKey         string
	DeepSeekModel          string
	DeepSeekBaseURL        string
	DeepSeekTimeoutSeconds int
	StimulusSubject        string
	ConsumerName           string
	ThinkingRequestSubject string
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
	if shared.ConsumerNames.ThinkingSvc == "" {
		return nil, fmt.Errorf("shared config %s has no consumer_names.thinking_svc configured", configPath)
	}
	if shared.ThinkingRequestSubject == "" {
		return nil, fmt.Errorf("shared config %s has no thinking_request_subject configured", configPath)
	}

	return &Config{
		NATSURL:                shared.NATSURL,
		HTTPPort:               env("HTTP_PORT", "9003"),
		DeepSeekAPIKey:         env("DEEPSEEK_API_KEY", ""),
		DeepSeekModel:          env("DEEPSEEK_MODEL", "deepseek-chat"),
		DeepSeekBaseURL:        env("DEEPSEEK_BASE_URL", "https://api.deepseek.com"),
		DeepSeekTimeoutSeconds: envInt("DEEPSEEK_TIMEOUT_SECONDS", 15),
		StimulusSubject:        shared.StimulusSubject,
		ConsumerName:           shared.ConsumerNames.ThinkingSvc,
		ThinkingRequestSubject: shared.ThinkingRequestSubject,
	}, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
