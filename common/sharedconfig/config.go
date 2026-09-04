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
	LogMonitor    LogMonitorConfig    `json:"log_monitor"`
	DoNotDisturb  DNDConfig           `json:"do_not_disturb"`
	Web           WebConfig           `json:"web"`
	School        SchoolConfig        `json:"school"`
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

	// ThresholdGracePeriodMinutes/ServiceGracePeriodMinutes require a
	// severity change to be observed on consecutive polls spanning at
	// least this many minutes before it is actually published — a flip
	// back to the previously-committed severity before then is discarded
	// silently. This is hysteresis against flapping (e.g. memory
	// oscillating around its warning threshold, or a service blipping
	// down and back up), not a delay on every notification: a change that
	// stays put still fires, just not on the very first poll that sees
	// it. Zero/absent means no grace period (publish on the first poll
	// that observes the new severity), matching this channel's original
	// behavior. Threshold covers disk_space/memory/cpu; Service covers
	// service_health and internal_health (both the top-level reachability
	// check and each reported dependency).
	ThresholdGracePeriodMinutes int `json:"threshold_grace_period_minutes"`
	ServiceGracePeriodMinutes   int `json:"service_grace_period_minutes"`
}

// CheckConfig describes one system-monitor check. CriticalThresholdPercent
// is optional — a zero value means this check only ever reports ok/warning,
// never critical. Perception module.md's own example config only gives
// disk_space a critical threshold, leaving memory and cpu warning-only.
// Name and Target are service_health/internal_health-only: Target is
// polymorphic, detected by prefix ("http://"/"https://" → HTTP GET;
// otherwise → "host:port" TCP dial) for service_health, or another
// soulman service's /health URL for internal_health — see
// docs/superpowers/specs/2026-07-19-system-monitor-service-health-design.md
// and docs/superpowers/specs/2026-07-27-dependency-health-design.md.
type CheckConfig struct {
	Type                     string  `json:"type"` // "disk_space" | "memory" | "cpu" | "service_health" | "internal_health"
	Path                     string  `json:"path,omitempty"`   // disk_space only
	Name                     string  `json:"name,omitempty"`   // service_health/internal_health only
	Target                   string  `json:"target,omitempty"` // service_health/internal_health only
	WarningThresholdPercent  float64 `json:"warning_threshold_percent,omitempty"`
	CriticalThresholdPercent float64 `json:"critical_threshold_percent,omitempty"`
}

// LogMonitorConfig holds perception-svc's Log Error channel settings: how
// often the reconciliation poll safety-net runs, alongside fsnotify's
// instant-reaction detection. Unlike GmailConfig, this channel has no
// external credential dependency and no reason to ever be optional — same
// fatal-fast-if-absent-or-non-positive treatment as
// system_monitor.poll_interval_seconds. See
// docs/superpowers/specs/2026-07-27-log-error-perception-design.md.
type LogMonitorConfig struct {
	ReconciliationIntervalSeconds int `json:"reconciliation_interval_seconds"`
}

// DNDConfig holds action-svc's do-not-disturb window settings for its
// real-time Discord notification path (the notifybatch.Batcher chain) —
// see docs/superpowers/specs/2026-07-27-discord-do-not-disturb-design.md.
// Enabled lets the window be turned off without discarding the configured
// times, matching FeignMode's explicit-boolean convention rather than an
// implicit "empty times = disabled" signal. Start/End are not fatally
// validated here — a malformed "HH:MM" silently falls back to 10:00 inside
// action-svc/dnd.Window.Active, matching scheduler.parseSendTime's existing
// loose convention for ReportSendTime.
type DNDConfig struct {
	Enabled bool   `json:"enabled"`
	Start   string `json:"start"`
	End     string `json:"end"`
}

// SchoolConfig holds the school-email-events feature's settings, shared
// between thinking-svc (SenderDomains, Enabled) and action-svc (Enabled,
// NotifyTime, CalendarRecipientEmails). Enabled is false in dev.json and
// true in prod.json — dev and prod poll the same real inbox, so running
// this feature in both would create duplicate Calendar invites and
// duplicate Discord messages. See
// docs/superpowers/specs/2026-09-03-school-email-events-design.md.
type SchoolConfig struct {
	Enabled                 bool     `json:"enabled"`
	SenderDomains           []string `json:"sender_domains"`
	NotifyTime              string   `json:"notify_time"`
	CalendarRecipientEmails []string `json:"calendar_recipient_emails"`
	// RelevantGrades filters extracted events to only those relevant to
	// these grades (e.g. ["5", "8"]) — a whole-school/ungraded
	// announcement still counts, but an event specific to a different
	// grade does not. Threaded into the extraction prompt
	// (thinking-svc/llm). Empty means no filtering (every grade counts).
	RelevantGrades []string `json:"relevant_grades"`
}

// ClaudeProjectRoot is one curated project-folder root the Claude
// remote-session launcher (web-svc/claudesession) offers: a
// human-readable label (matched against a launch request's "root"
// field) and the filesystem path it corresponds to. See
// docs/superpowers/specs/2026-08-09-claude-remote-sessions-design.md.
type ClaudeProjectRoot struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

// FileBrowserRoot is one curated root the file browser
// (web-svc/filebrowser) offers for browsing, download, and upload: a
// human-readable label (matched against a request's "root" field) and
// the filesystem path it corresponds to. A distinct type from
// ClaudeProjectRoot even though the shape is identical — they represent
// different concerns (small independent duplication over a shared type,
// consistent with this repo's existing preference — see
// web-svc/NOTES.md). See
// docs/superpowers/specs/2026-08-19-file-browser-design.md.
type FileBrowserRoot struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

// WebConfig holds web-svc's settings: the single owner email allowed full
// dashboard access, the frontend origin CORS must allow, and the base URLs
// of the four services web-svc calls into. Unlike GmailConfig/
// SystemMonitorConfig, every field here is required — web-svc has no
// degraded "partially configured" mode. ClaudeProjectRoots is required to
// be non-empty, but unlike ObsidianRoot its entries' Path values are not
// required to exist on disk at startup — a missing root is reported as
// such per-request instead (see web-svc/claudesession.ListRoots).
type WebConfig struct {
	OwnerEmail         string              `json:"owner_email"`
	CORSAllowedOrigin  string              `json:"cors_allowed_origin"`
	PerceptionSvcURL   string              `json:"perception_svc_url"`
	MemorySvcURL       string              `json:"memory_svc_url"`
	ThinkingSvcURL     string              `json:"thinking_svc_url"`
	ActionSvcURL       string              `json:"action_svc_url"`
	ProjectsSvcURL     string              `json:"projects_svc_url"`
	ObsidianRoot       string              `json:"obsidian_root"`
	ClaudeProjectRoots []ClaudeProjectRoot `json:"claude_project_roots"`
	FileBrowserRoots   []FileBrowserRoot   `json:"file_browser_roots"`
	// ShareLinkTTLMinutes is how long a generated share link
	// (web-svc/sharelink) stays valid. Optional — zero or absent defaults
	// to 60 in web-svc/config.Load, the same loose-default posture as
	// DoNotDisturb's Start/End (not a fatal validation error). See
	// docs/superpowers/specs/2026-08-19-file-sharing-design.md.
	ShareLinkTTLMinutes int `json:"share_link_ttl_minutes"`
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
