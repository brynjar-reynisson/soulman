package config

import (
	"fmt"
	"os"
	"strconv"

	"soulman/common/sharedconfig"
)

type Config struct {
	NATSURL           string
	HTTPPort          string
	WatchPaths        []string
	CheckpointPath    string
	ReconcileInterval int // seconds
	StimulusSubject   string

	GmailClientID            string
	GmailClientSecret        string
	GmailRefreshToken        string
	GmailQuery               string
	GmailSeenLabel           string
	GmailPollIntervalSeconds int

	SystemMonitorPollIntervalSeconds int
	SystemMonitorChecks              []sharedconfig.CheckConfig
}

func Load() (*Config, error) {
	configPath := env("CONFIG_PATH", "./config.json")

	shared, err := sharedconfig.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("loading shared config: %w", err)
	}
	if len(shared.WatchPaths) == 0 {
		return nil, fmt.Errorf("shared config %s has no watch_paths configured", configPath)
	}
	if shared.NATSURL == "" {
		return nil, fmt.Errorf("shared config %s has no nats_url configured", configPath)
	}
	if shared.StimulusSubject == "" {
		return nil, fmt.Errorf("shared config %s has no stimulus_subject configured", configPath)
	}
	if shared.Gmail.Query == "" {
		return nil, fmt.Errorf("shared config %s has no gmail.query configured", configPath)
	}
	if shared.Gmail.SeenLabel == "" {
		return nil, fmt.Errorf("shared config %s has no gmail.seen_label configured", configPath)
	}
	if shared.Gmail.PollIntervalSeconds <= 0 {
		return nil, fmt.Errorf("shared config %s has no positive gmail.poll_interval_seconds configured", configPath)
	}
	if shared.SystemMonitor.PollIntervalSeconds <= 0 {
		return nil, fmt.Errorf("shared config %s has no positive system_monitor.poll_interval_seconds configured", configPath)
	}
	if len(shared.SystemMonitor.Checks) == 0 {
		return nil, fmt.Errorf("shared config %s has no system_monitor.checks configured", configPath)
	}
	for i, c := range shared.SystemMonitor.Checks {
		switch c.Type {
		case "disk_space":
			if c.Path == "" {
				return nil, fmt.Errorf("shared config %s: system_monitor.checks[%d] (disk_space) has no path configured", configPath, i)
			}
		case "memory", "cpu":
		default:
			return nil, fmt.Errorf("shared config %s: system_monitor.checks[%d] has unknown type %q", configPath, i, c.Type)
		}
		if c.WarningThresholdPercent <= 0 {
			return nil, fmt.Errorf("shared config %s: system_monitor.checks[%d] (%s) has no positive warning_threshold_percent configured", configPath, i, c.Type)
		}
		if c.CriticalThresholdPercent > 0 && c.CriticalThresholdPercent < c.WarningThresholdPercent {
			return nil, fmt.Errorf("shared config %s: system_monitor.checks[%d] (%s) has critical_threshold_percent below warning_threshold_percent", configPath, i, c.Type)
		}
	}

	return &Config{
		NATSURL:           shared.NATSURL,
		HTTPPort:          env("HTTP_PORT", "9001"),
		WatchPaths:        shared.WatchPaths,
		CheckpointPath:    env("CHECKPOINT_PATH", "./checkpoints.json"),
		ReconcileInterval: envInt("RECONCILE_INTERVAL_SECONDS", 30),
		StimulusSubject:   shared.StimulusSubject,

		GmailClientID:            env("GMAIL_CLIENT_ID", ""),
		GmailClientSecret:        env("GMAIL_CLIENT_SECRET", ""),
		GmailRefreshToken:        env("GMAIL_REFRESH_TOKEN", ""),
		GmailQuery:               shared.Gmail.Query,
		GmailSeenLabel:           shared.Gmail.SeenLabel,
		GmailPollIntervalSeconds: shared.Gmail.PollIntervalSeconds,

		SystemMonitorPollIntervalSeconds: shared.SystemMonitor.PollIntervalSeconds,
		SystemMonitorChecks:              shared.SystemMonitor.Checks,
	}, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
