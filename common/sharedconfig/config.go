// Package sharedconfig loads the non-secret settings shared across
// Soulman's services from a per-environment JSON file (config/dev.json,
// config/prod.json in the vault; copied to <env-root>\config.json at
// launch by each run-<svc>.ps1 script). Secrets never belong here — they
// stay in .env, which is deliberately kept outside the git-tracked vault.
package sharedconfig

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config is the schema of the shared config file. New fields get added
// here as more services need non-secret settings; a service that doesn't
// use a given field simply ignores it.
type Config struct {
	WatchPaths             []string            `json:"watch_paths"`
	NATSURL                string              `json:"nats_url"`
	StimulusSubject        string              `json:"stimulus_subject"`
	ThinkingRequestSubject string              `json:"thinking_request_subject"`
	MemoryWriteSubject     string              `json:"memory_write_subject"`
	// FeignMode, when true, tells action-svc to record outbound side
	// effects (e.g. Discord notifications) instead of actually performing
	// them. See docs/superpowers/specs/2026-07-19-action-svc-feign-mode-design.md.
	// Only action-svc reads this field today.
	FeignMode     bool                `json:"feign_mode"`
	ConsumerNames ConsumerNames       `json:"consumer_names"`
	Gmail         GmailConfig         `json:"gmail"`
	SystemMonitor SystemMonitorConfig `json:"system_monitor"`
	Web           WebConfig           `json:"web"`
}

// ConsumerNames holds the JetStream durable consumer name for each service
// that has one: memory-svc (consuming both the STIMULUS stream via
// MemorySvc and the MEMORY_WRITE stream via MemorySvcEpisodes — two
// distinct names because JetStream identifies a durable consumer by
// (stream, name), so memory-svc's second consumer can't reuse MemorySvc's
// name even though it's the same service), thinking-svc (consuming the
// STIMULUS stream), and action-svc (consuming the THINKING_REQUEST
// stream). perception-svc only publishes, so it has no consumer name here.
type ConsumerNames struct {
	MemorySvc         string `json:"memory_svc"`
	MemorySvcEpisodes string `json:"memory_svc_episodes"`
	ThinkingSvc       string `json:"thinking_svc"`
	ActionSvc         string `json:"action_svc"`
}

// GmailConfig holds perception-svc's Gmail channel settings: the search
// query used to find matching messages, the label applied to mark them
// processed (Gmail's own labels are the dedup checkpoint — no local state
// file), and how often to poll. Both dev and prod populate this — only the
// query/seen_label values differ, since both watch the same real inbox and
// each marks what it processes with its own label so neither re-processes
// the other's work.
type GmailConfig struct {
	Query               string `json:"query"`
	SeenLabel           string `json:"seen_label"`
	PollIntervalSeconds int    `json:"poll_interval_seconds"`
}

// SystemMonitorConfig holds perception-svc's System Monitor channel
// settings: how often to poll, and the list of checks to run (disk space,
// memory, CPU). Unlike GmailConfig, this channel has no external
// credential dependency — perception-svc's config loader treats it as
// required (fatal startup error if absent), the same way it treats
// watch_paths, not the way it treats Gmail (optional, skipped if
// unconfigured).
type SystemMonitorConfig struct {
	PollIntervalSeconds int           `json:"poll_interval_seconds"`
	Checks              []CheckConfig `json:"checks"`
}

// CheckConfig describes one system-monitor check. CriticalThresholdPercent
// is optional — a zero value means this check only ever reports ok/warning,
// never critical. Perception module.md's own example config only gives
// disk_space a critical threshold, leaving memory and cpu warning-only.
type CheckConfig struct {
	Type                     string  `json:"type"` // "disk_space" | "memory" | "cpu"
	Path                     string  `json:"path,omitempty"` // disk_space only
	WarningThresholdPercent  float64 `json:"warning_threshold_percent"`
	CriticalThresholdPercent float64 `json:"critical_threshold_percent,omitempty"`
}

// WebConfig holds web-svc's settings: the single owner email allowed full
// dashboard access, the frontend origin CORS must allow, and the base URLs
// of the four services web-svc calls into. Unlike GmailConfig/
// SystemMonitorConfig, every field here is required — web-svc has no
// degraded "partially configured" mode.
type WebConfig struct {
	OwnerEmail        string `json:"owner_email"`
	CORSAllowedOrigin string `json:"cors_allowed_origin"`
	PerceptionSvcURL  string `json:"perception_svc_url"`
	MemorySvcURL      string `json:"memory_svc_url"`
	ThinkingSvcURL    string `json:"thinking_svc_url"`
	ActionSvcURL      string `json:"action_svc_url"`
}

// Load reads and parses the JSON config file at path. An empty or missing
// watch_paths list is not an error here — Load only reports file-read and
// parse failures; callers that require a non-empty value validate that
// themselves.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file %s: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file %s: %w", path, err)
	}

	return &cfg, nil
}
