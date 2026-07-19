package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"soulman/perception-svc/config"
)

type gmailFields struct {
	Query               string `json:"query"`
	SeenLabel           string `json:"seen_label"`
	PollIntervalSeconds int    `json:"poll_interval_seconds"`
}

type checkFields struct {
	Type                     string  `json:"type"`
	Path                     string  `json:"path,omitempty"`
	Name                     string  `json:"name,omitempty"`
	Target                   string  `json:"target,omitempty"`
	WarningThresholdPercent  float64 `json:"warning_threshold_percent,omitempty"`
	CriticalThresholdPercent float64 `json:"critical_threshold_percent,omitempty"`
}

type systemMonitorFields struct {
	PollIntervalSeconds int           `json:"poll_interval_seconds"`
	Checks              []checkFields `json:"checks"`
}

type sharedFields struct {
	WatchPaths      []string            `json:"watch_paths"`
	NATSURL         string              `json:"nats_url"`
	StimulusSubject string              `json:"stimulus_subject"`
	Gmail           gmailFields         `json:"gmail"`
	SystemMonitor   systemMonitorFields `json:"system_monitor"`
}

// validGmail is a ready-to-use gmailFields value for tests that aren't
// specifically exercising Gmail validation — every test needs a valid one
// since Load validates the gmail block fatally regardless of whether the
// GMAIL_CLIENT_ID/SECRET/REFRESH_TOKEN secrets are set.
var validGmail = gmailFields{
	Query:               "in:inbox is:unread -label:soulman/seen",
	SeenLabel:           "soulman/seen",
	PollIntervalSeconds: 60,
}

// validSystemMonitor is the same kind of ready-to-use fixture for
// system_monitor, which is fatally validated regardless of any credential
// (it has none).
var validSystemMonitor = systemMonitorFields{
	PollIntervalSeconds: 300,
	Checks: []checkFields{
		{Type: "disk_space", Path: `C:\`, WarningThresholdPercent: 80, CriticalThresholdPercent: 95},
	},
}

func writeConfigFile(t *testing.T, watchPaths []string, natsURL, stimulusSubject string, gmail gmailFields, sysMonitor systemMonitorFields) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	data, err := json.Marshal(sharedFields{
		WatchPaths:      watchPaths,
		NATSURL:         natsURL,
		StimulusSubject: stimulusSubject,
		Gmail:           gmail,
		SystemMonitor:   sysMonitor,
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
	os.Unsetenv("CHECKPOINT_PATH")
	os.Unsetenv("RECONCILE_INTERVAL_SECONDS")
	os.Unsetenv("GMAIL_CLIENT_ID")
	os.Unsetenv("GMAIL_CLIENT_SECRET")
	os.Unsetenv("GMAIL_REFRESH_TOKEN")
}

func TestLoad_Defaults(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, []string{`C:\Users\Lenovo\DigitalMe\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, validSystemMonitor)
	os.Setenv("CONFIG_PATH", configPath)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.NATSURL != "nats://localhost:4222" {
		t.Errorf("NATSURL = %q, want nats://localhost:4222", cfg.NATSURL)
	}
	if cfg.HTTPPort != "9001" {
		t.Errorf("HTTPPort = %q, want 9001", cfg.HTTPPort)
	}
	if len(cfg.WatchPaths) != 1 || cfg.WatchPaths[0] != `C:\Users\Lenovo\DigitalMe\errors` {
		t.Errorf("WatchPaths = %v, want [C:\\Users\\Lenovo\\DigitalMe\\errors]", cfg.WatchPaths)
	}
	if cfg.CheckpointPath != "./checkpoints.json" {
		t.Errorf("CheckpointPath = %q, want ./checkpoints.json", cfg.CheckpointPath)
	}
	if cfg.ReconcileInterval != 30 {
		t.Errorf("ReconcileInterval = %d, want 30", cfg.ReconcileInterval)
	}
	if cfg.StimulusSubject != "soulman.stimulus.raw" {
		t.Errorf("StimulusSubject = %q, want soulman.stimulus.raw", cfg.StimulusSubject)
	}
	if cfg.GmailQuery != "in:inbox is:unread -label:soulman/seen" {
		t.Errorf("GmailQuery = %q, want in:inbox is:unread -label:soulman/seen", cfg.GmailQuery)
	}
	if cfg.GmailSeenLabel != "soulman/seen" {
		t.Errorf("GmailSeenLabel = %q, want soulman/seen", cfg.GmailSeenLabel)
	}
	if cfg.GmailPollIntervalSeconds != 60 {
		t.Errorf("GmailPollIntervalSeconds = %d, want 60", cfg.GmailPollIntervalSeconds)
	}
	if cfg.GmailClientID != "" {
		t.Errorf("GmailClientID = %q, want empty when GMAIL_CLIENT_ID unset", cfg.GmailClientID)
	}
	if cfg.GmailClientSecret != "" {
		t.Errorf("GmailClientSecret = %q, want empty when GMAIL_CLIENT_SECRET unset", cfg.GmailClientSecret)
	}
	if cfg.GmailRefreshToken != "" {
		t.Errorf("GmailRefreshToken = %q, want empty when GMAIL_REFRESH_TOKEN unset", cfg.GmailRefreshToken)
	}
	if cfg.SystemMonitorPollIntervalSeconds != 300 {
		t.Errorf("SystemMonitorPollIntervalSeconds = %d, want 300", cfg.SystemMonitorPollIntervalSeconds)
	}
	if len(cfg.SystemMonitorChecks) != 1 || cfg.SystemMonitorChecks[0].Type != "disk_space" {
		t.Errorf("SystemMonitorChecks = %+v, want one disk_space check", cfg.SystemMonitorChecks)
	}
}

func TestLoad_SharedConfigValues(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	gmail := gmailFields{
		Query:               "in:inbox is:unread -label:soulman/seen-dev",
		SeenLabel:           "soulman/seen-dev",
		PollIntervalSeconds: 60,
	}
	configPath := writeConfigFile(t, []string{`C:\a\errors`, `C:\b\errors`, `C:\c\errors`}, "nats://remote:4222", "soulman.dev.stimulus.raw", gmail, validSystemMonitor)
	os.Setenv("CONFIG_PATH", configPath)
	os.Setenv("HTTP_PORT", "9999")
	os.Setenv("CHECKPOINT_PATH", "./data/checkpoints.json")
	os.Setenv("RECONCILE_INTERVAL_SECONDS", "45")
	os.Setenv("GMAIL_CLIENT_ID", "client-123")
	os.Setenv("GMAIL_CLIENT_SECRET", "secret-456")
	os.Setenv("GMAIL_REFRESH_TOKEN", "refresh-789")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.NATSURL != "nats://remote:4222" {
		t.Errorf("NATSURL = %q, want nats://remote:4222", cfg.NATSURL)
	}
	if cfg.HTTPPort != "9999" {
		t.Errorf("HTTPPort = %q, want 9999", cfg.HTTPPort)
	}
	want := []string{`C:\a\errors`, `C:\b\errors`, `C:\c\errors`}
	if len(cfg.WatchPaths) != len(want) {
		t.Fatalf("WatchPaths = %v, want %v", cfg.WatchPaths, want)
	}
	for i, p := range want {
		if cfg.WatchPaths[i] != p {
			t.Errorf("WatchPaths[%d] = %q, want %q", i, cfg.WatchPaths[i], p)
		}
	}
	if cfg.CheckpointPath != "./data/checkpoints.json" {
		t.Errorf("CheckpointPath = %q, want ./data/checkpoints.json", cfg.CheckpointPath)
	}
	if cfg.ReconcileInterval != 45 {
		t.Errorf("ReconcileInterval = %d, want 45", cfg.ReconcileInterval)
	}
	if cfg.StimulusSubject != "soulman.dev.stimulus.raw" {
		t.Errorf("StimulusSubject = %q, want soulman.dev.stimulus.raw", cfg.StimulusSubject)
	}
	if cfg.GmailQuery != "in:inbox is:unread -label:soulman/seen-dev" {
		t.Errorf("GmailQuery = %q, want in:inbox is:unread -label:soulman/seen-dev", cfg.GmailQuery)
	}
	if cfg.GmailSeenLabel != "soulman/seen-dev" {
		t.Errorf("GmailSeenLabel = %q, want soulman/seen-dev", cfg.GmailSeenLabel)
	}
	if cfg.GmailPollIntervalSeconds != 60 {
		t.Errorf("GmailPollIntervalSeconds = %d, want 60", cfg.GmailPollIntervalSeconds)
	}
	if cfg.GmailClientID != "client-123" {
		t.Errorf("GmailClientID = %q, want client-123", cfg.GmailClientID)
	}
	if cfg.GmailClientSecret != "secret-456" {
		t.Errorf("GmailClientSecret = %q, want secret-456", cfg.GmailClientSecret)
	}
	if cfg.GmailRefreshToken != "refresh-789" {
		t.Errorf("GmailRefreshToken = %q, want refresh-789", cfg.GmailRefreshToken)
	}
}

func TestLoad_InvalidReconcileInterval_FallsBackToDefault(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, validSystemMonitor)
	os.Setenv("CONFIG_PATH", configPath)
	os.Setenv("RECONCILE_INTERVAL_SECONDS", "not-a-number")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ReconcileInterval != 30 {
		t.Errorf("ReconcileInterval = %d, want default 30 for invalid input", cfg.ReconcileInterval)
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

func TestLoad_EmptyWatchPaths_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, []string{}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, validSystemMonitor)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty watch_paths, got nil")
	}
}

func TestLoad_EmptyNATSURL_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "", "soulman.stimulus.raw", validGmail, validSystemMonitor)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty nats_url, got nil")
	}
}

func TestLoad_EmptyStimulusSubject_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "", validGmail, validSystemMonitor)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty stimulus_subject, got nil")
	}
}

func TestLoad_EmptyGmailQuery_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	gmail := validGmail
	gmail.Query = ""
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", gmail, validSystemMonitor)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty gmail.query, got nil")
	}
}

func TestLoad_EmptyGmailSeenLabel_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	gmail := validGmail
	gmail.SeenLabel = ""
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", gmail, validSystemMonitor)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty gmail.seen_label, got nil")
	}
}

func TestLoad_ZeroGmailPollInterval_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	gmail := validGmail
	gmail.PollIntervalSeconds = 0
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", gmail, validSystemMonitor)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for zero gmail.poll_interval_seconds, got nil")
	}
}

func TestLoad_EmptySystemMonitorChecks_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	sysMon := validSystemMonitor
	sysMon.Checks = nil
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, sysMon)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty system_monitor.checks, got nil")
	}
}

func TestLoad_ZeroSystemMonitorPollInterval_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	sysMon := validSystemMonitor
	sysMon.PollIntervalSeconds = 0
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, sysMon)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for zero system_monitor.poll_interval_seconds, got nil")
	}
}

func TestLoad_UnknownSystemMonitorCheckType_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	sysMon := validSystemMonitor
	sysMon.Checks = []checkFields{{Type: "network", WarningThresholdPercent: 80}}
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, sysMon)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for unknown system_monitor check type, got nil")
	}
}

func TestLoad_DiskSpaceCheckMissingPath_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	sysMon := validSystemMonitor
	sysMon.Checks = []checkFields{{Type: "disk_space", WarningThresholdPercent: 80}}
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, sysMon)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for disk_space check with no path, got nil")
	}
}

func TestLoad_ZeroWarningThreshold_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	sysMon := validSystemMonitor
	sysMon.Checks = []checkFields{{Type: "cpu", WarningThresholdPercent: 0}}
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, sysMon)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for zero warning_threshold_percent, got nil")
	}
}

func TestLoad_CriticalThresholdBelowWarning_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	sysMon := validSystemMonitor
	sysMon.Checks = []checkFields{{Type: "disk_space", Path: `C:\`, WarningThresholdPercent: 90, CriticalThresholdPercent: 80}}
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, sysMon)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for critical_threshold_percent below warning_threshold_percent, got nil")
	}
}

func TestLoad_ValidMemoryAndCPUChecks_NoPathRequired(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	sysMon := validSystemMonitor
	sysMon.Checks = []checkFields{
		{Type: "memory", WarningThresholdPercent: 85},
		{Type: "cpu", WarningThresholdPercent: 90},
	}
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, sysMon)
	os.Setenv("CONFIG_PATH", configPath)

	if _, err := config.Load(); err != nil {
		t.Fatalf("Load: want no error for valid memory/cpu checks without path, got %v", err)
	}
}

func TestLoad_ServiceHealthCheckMissingName_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	sysMon := validSystemMonitor
	sysMon.Checks = []checkFields{{Type: "service_health", Target: "localhost:5176"}}
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, sysMon)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for service_health check with no name, got nil")
	}
}

func TestLoad_ServiceHealthCheckMissingTarget_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	sysMon := validSystemMonitor
	sysMon.Checks = []checkFields{{Type: "service_health", Name: "agent-suite-backend"}}
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, sysMon)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for service_health check with no target, got nil")
	}
}

func TestLoad_ValidServiceHealthCheck_NoThresholdRequired(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	sysMon := validSystemMonitor
	sysMon.Checks = []checkFields{
		{Type: "service_health", Name: "agent-suite-backend", Target: "http://localhost:8091/health"},
		{Type: "service_health", Name: "digital-me-frontend", Target: "localhost:5173"},
	}
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, sysMon)
	os.Setenv("CONFIG_PATH", configPath)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: want no error for valid service_health checks without thresholds, got %v", err)
	}
	if len(cfg.SystemMonitorChecks) != 2 {
		t.Fatalf("SystemMonitorChecks = %d entries, want 2", len(cfg.SystemMonitorChecks))
	}
	if cfg.SystemMonitorChecks[0].Name != "agent-suite-backend" || cfg.SystemMonitorChecks[0].Target != "http://localhost:8091/health" {
		t.Errorf("SystemMonitorChecks[0] = %+v, want agent-suite-backend/http://localhost:8091/health", cfg.SystemMonitorChecks[0])
	}
}
