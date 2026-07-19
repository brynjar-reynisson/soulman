# System Monitor Perception Channel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a System Monitor pull channel to `perception-svc` (disk/memory/CPU threshold polling on Windows) paired with a mechanical `thinking-svc` rule that reuses the existing `append_daily_report_entry` action, with zero new `action-svc` code.

**Architecture:** A new `perception-svc/sysmonitor` package, shaped like the existing `watcher`/`gmailwatcher` packages (a `Watcher` with `New`/`Start`/`Close`, a local `Publisher` interface), polls disk/memory/CPU on a configurable interval and publishes a `Stimulus` only when a check's severity (`ok`/`warning`/`critical`) changes. A `statsProvider` interface separates the poll/state-machine logic (tested with a fake) from the real Windows syscalls (`golang.org/x/sys/windows`, in a `_windows.go`-suffixed file so it only builds on Windows). A new `thinking-svc/rules/system_monitor.go` rule matches `channel == "system-monitor"` and produces the same `append_daily_report_entry` Action Request shape `ErrorReportRule`/`CLINoteRule` already produce.

**Tech Stack:** Go 1.25, `golang.org/x/sys/windows` (already an indirect dependency, promoted to direct), NATS JetStream (unchanged), no new third-party dependencies.

**Spec:** `docs/superpowers/specs/2026-07-18-system-monitor-channel-design.md` — read this first for full rationale; this plan implements it task-by-task.

## Global Constraints

- Channel name is exactly `"system-monitor"` (used as the literal match value in both `perception-svc`'s Stimulus and `thinking-svc`'s rule).
- No new `action-svc` code — the rule must produce the same `{summary, raw_content, source_path, occurred_at}` shape `append_daily_report_entry` already consumes.
- `perception-svc/sysmonitor` has no cross-platform requirement — this deployment is Windows-only (per `start-everything.ps1`); the real stats implementation lives in a `_windows.go`-suffixed file (Go's implicit build-constraint filename convention — no explicit `//go:build` line strictly required, but this plan adds one anyway for clarity).
- `golang.org/x/sys` must be promoted from an indirect to a direct dependency of `perception-svc`'s `go.mod` (it's already resolved via `oauth2`/`nats.go` at v0.47.0 in `go.sum`).
- `common/sharedconfig`'s new `system_monitor` JSON block is **required**, not optional (fatal startup error if absent/misconfigured) — unlike the Gmail block, System Monitor has no external credential dependency, so it follows `watch_paths`'s fatal-fast precedent, not Gmail's skip-if-unconfigured one.
- `config/dev.json` and `config/prod.json` must both be updated in the *same task* that adds fatal validation for `system_monitor`, so no commit in this branch's history ever has a `perception-svc` that fails to start against the real config files.
- Severity edge-triggering state is in-memory only — no persisted checkpoint file (confirmed design decision; see spec).

---

### Task 1: `common/sharedconfig` — System Monitor config schema

**Files:**
- Modify: `common/sharedconfig/config.go`
- Test: `common/sharedconfig/config_test.go`

**Interfaces:**
- Produces: `sharedconfig.Config.SystemMonitor SystemMonitorConfig` (JSON tag `system_monitor`); `sharedconfig.SystemMonitorConfig{PollIntervalSeconds int, Checks []CheckConfig}`; `sharedconfig.CheckConfig{Type string, Path string, WarningThresholdPercent float64, CriticalThresholdPercent float64}`.

- [ ] **Step 1: Write the failing tests**

Add to `common/sharedconfig/config_test.go` (append at the end of the file):

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd common && go test ./sharedconfig/... -run TestLoad_SystemMonitor -v`
Expected: FAIL — `cfg.SystemMonitor` undefined (compile error), since the field doesn't exist yet.

- [ ] **Step 3: Add the config types**

In `common/sharedconfig/config.go`, add `SystemMonitor SystemMonitorConfig` to the `Config` struct (after the existing `Gmail` field):

```go
type Config struct {
	WatchPaths             []string            `json:"watch_paths"`
	NATSURL                string              `json:"nats_url"`
	StimulusSubject        string              `json:"stimulus_subject"`
	ThinkingRequestSubject string              `json:"thinking_request_subject"`
	MemoryWriteSubject     string              `json:"memory_write_subject"`
	ConsumerNames          ConsumerNames       `json:"consumer_names"`
	Gmail                  GmailConfig         `json:"gmail"`
	SystemMonitor          SystemMonitorConfig `json:"system_monitor"`
}
```

Then add these two new types after `GmailConfig`'s definition:

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd common && go test ./sharedconfig/... -v`
Expected: PASS — all tests including the two new ones and every pre-existing test in the package.

- [ ] **Step 5: Commit**

```bash
git -C common add sharedconfig/config.go sharedconfig/config_test.go
git -C common commit -m "feat: add system_monitor config schema to sharedconfig"
```

---

### Task 2: `perception-svc/config` — fatal-fast validation + real config files

**Files:**
- Modify: `perception-svc/config/config.go`
- Modify: `perception-svc/config/config_test.go`
- Modify: `config/dev.json`
- Modify: `config/prod.json`

**Interfaces:**
- Consumes: `sharedconfig.Config.SystemMonitor` (Task 1)
- Produces: `config.Config.SystemMonitorPollIntervalSeconds int`, `config.Config.SystemMonitorChecks []sharedconfig.CheckConfig`

- [ ] **Step 1: Write the failing tests**

Rewrite `perception-svc/config/config_test.go` in full (every existing test's `writeConfigFile` call gains one more argument — a valid `systemMonitorFields` fixture — so the whole file changes together):

```go
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
	WarningThresholdPercent  float64 `json:"warning_threshold_percent"`
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd perception-svc && go test ./config/... -v`
Expected: FAIL — compile error, `cfg.SystemMonitorPollIntervalSeconds`/`cfg.SystemMonitorChecks` undefined.

- [ ] **Step 3: Implement the config fields and validation**

In `perception-svc/config/config.go`, add two fields to `Config` (after `GmailPollIntervalSeconds`):

```go
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
```

Add validation in `Load()`, right after the existing `Gmail.PollIntervalSeconds` check:

```go
	if shared.Gmail.PollIntervalSeconds <= 0 {
		return nil, fmt.Errorf("shared config %s has no positive gmail.poll_interval_seconds configured", configPath)
	}
	if shared.SystemMonitor.PollIntervalSeconds <= 0 {
		return nil, fmt.Errorf("shared config %s has no positive system_monitor.poll_interval_seconds configured", configPath)
	}
	if len(shared.SystemMonitor.Checks) == 0 {
		return nil, fmt.Errorf("shared config %s has no system_monitor.checks configured", configPath)
	}
```

Add the two fields to the returned `&Config{...}` literal (after `GmailPollIntervalSeconds`):

```go
		GmailPollIntervalSeconds: shared.Gmail.PollIntervalSeconds,

		SystemMonitorPollIntervalSeconds: shared.SystemMonitor.PollIntervalSeconds,
		SystemMonitorChecks:              shared.SystemMonitor.Checks,
	}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd perception-svc && go test ./config/... -v`
Expected: PASS — all tests, including the two new fatal-validation tests.

- [ ] **Step 5: Update the real config files**

Add a `system_monitor` block to `config/dev.json` (insert after the `gmail` block, before the closing `}`):

```json
  "system_monitor": {
    "poll_interval_seconds": 300,
    "checks": [
      { "type": "disk_space", "path": "C:\\", "warning_threshold_percent": 80, "critical_threshold_percent": 95 },
      { "type": "memory", "warning_threshold_percent": 85 },
      { "type": "cpu", "warning_threshold_percent": 90 }
    ]
  }
```

And the same block into `config/prod.json` (same insertion point). Both files use identical `system_monitor` values — both environments monitor the same physical machine.

- [ ] **Step 6: Verify the real config files parse correctly**

Run: `cd perception-svc && CONFIG_PATH=../config/dev.json go run . &` then check the startup log line, then stop it (e.g. `kill %1` in bash, or Ctrl+C if run in foreground) — confirm no `config:` fatal error appears. Repeat with `CONFIG_PATH=../config/prod.json`. (This step only proves config parsing/validation succeeds; the process will still be functional as folder-watcher/Gmail-only until Task 5 wires sysmonitor in.)

- [ ] **Step 7: Commit**

```bash
git -C . add perception-svc/config/config.go perception-svc/config/config_test.go config/dev.json config/prod.json
git -C . commit -m "feat: add fatal-fast system_monitor validation to perception-svc config"
```

---

### Task 3: `perception-svc/sysmonitor` — core poll/state-machine logic (fake stats)

**Files:**
- Create: `perception-svc/sysmonitor/sysmonitor.go`
- Create: `perception-svc/sysmonitor/sysmonitor_test.go`

**Interfaces:**
- Consumes: nothing from other tasks yet (fully self-contained; the real Windows implementation is Task 4).
- Produces: `sysmonitor.Publisher` (interface), `sysmonitor.CheckConfig{Type, Path string; WarningThresholdPercent, CriticalThresholdPercent float64}`, `sysmonitor.Watcher` with unexported constructor `newWatcher(stats statsProvider, checks []CheckConfig, publisher Publisher, interval time.Duration) *Watcher` and methods `Start(ctx)`/`Close() error`. The unexported `statsProvider` interface (`DiskUsagePercent(path string) (float64, error)`, `MemoryUsagePercent() (float64, error)`, `CPUUsagePercent() (float64, error)`) and the unexported sentinel `errNoCPUBaseline` are what Task 4's `stats_windows.go` implements/returns. Task 4 also adds the exported `New(checks, publisher, interval) *Watcher` (calling `newWatcher(&winStats{}, ...)`) — deliberately not defined in this task, since the real stats type doesn't exist yet.

- [ ] **Step 1: Write the failing tests**

Create `perception-svc/sysmonitor/sysmonitor_test.go`:

```go
package sysmonitor

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"soulman/common"
)

type fakeStats struct {
	mu      sync.Mutex
	disk    map[string]float64
	diskErr error
	memory  float64
	memErr  error
	cpu     float64
	cpuErr  error
}

func (f *fakeStats) DiskUsagePercent(path string) (float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.diskErr != nil {
		return 0, f.diskErr
	}
	return f.disk[path], nil
}

func (f *fakeStats) MemoryUsagePercent() (float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.memory, f.memErr
}

func (f *fakeStats) CPUUsagePercent() (float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cpu, f.cpuErr
}

type fakePublisher struct {
	mu         sync.Mutex
	published  []*common.Stimulus
	publishErr error
}

func (f *fakePublisher) Publish(ctx context.Context, s *common.Stimulus) error {
	if f.publishErr != nil {
		return f.publishErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published = append(f.published, s)
	return nil
}

func (f *fakePublisher) publishedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.published)
}

func diskCheck(path string) CheckConfig {
	return CheckConfig{Type: "disk_space", Path: path, WarningThresholdPercent: 80, CriticalThresholdPercent: 95}
}

func TestPoll_NoThresholdCrossed_NoStimulus(t *testing.T) {
	stats := &fakeStats{disk: map[string]float64{`C:\`: 50}}
	pub := &fakePublisher{}
	w := newWatcher(stats, []CheckConfig{diskCheck(`C:\`)}, pub, time.Hour)

	w.poll(context.Background())
	w.poll(context.Background())

	if got := pub.publishedCount(); got != 0 {
		t.Errorf("published = %d, want 0 (steady ok state)", got)
	}
}

func TestPoll_CrossesIntoWarning_PublishesOnce(t *testing.T) {
	stats := &fakeStats{disk: map[string]float64{`C:\`: 50}}
	pub := &fakePublisher{}
	w := newWatcher(stats, []CheckConfig{diskCheck(`C:\`)}, pub, time.Hour)

	w.poll(context.Background()) // establishes ok baseline, no stimulus
	stats.mu.Lock()
	stats.disk[`C:\`] = 85
	stats.mu.Unlock()
	w.poll(context.Background()) // ok -> warning

	if got := pub.publishedCount(); got != 1 {
		t.Fatalf("published = %d, want 1", got)
	}
	if pub.published[0].Hints.Priority != "high" {
		t.Errorf("Hints.Priority = %q, want high", pub.published[0].Hints.Priority)
	}

	w.poll(context.Background()) // still warning, no new stimulus
	if got := pub.publishedCount(); got != 1 {
		t.Errorf("published = %d after repeated warning poll, want still 1", got)
	}
}

func TestPoll_EscalatesToCriticalThenRecovers(t *testing.T) {
	stats := &fakeStats{disk: map[string]float64{`C:\`: 50}}
	pub := &fakePublisher{}
	w := newWatcher(stats, []CheckConfig{diskCheck(`C:\`)}, pub, time.Hour)

	w.poll(context.Background()) // ok baseline

	stats.mu.Lock()
	stats.disk[`C:\`] = 85
	stats.mu.Unlock()
	w.poll(context.Background()) // ok -> warning

	stats.mu.Lock()
	stats.disk[`C:\`] = 97
	stats.mu.Unlock()
	w.poll(context.Background()) // warning -> critical

	stats.mu.Lock()
	stats.disk[`C:\`] = 50
	stats.mu.Unlock()
	w.poll(context.Background()) // critical -> ok

	if got := pub.publishedCount(); got != 3 {
		t.Fatalf("published = %d, want 3 (warning, critical, recovery)", got)
	}
	if pub.published[0].ChannelMeta.MessageID == "" {
		t.Error("MessageID must be set")
	}
	if got := severityFromStimulus(t, pub.published[1]); got != "critical" {
		t.Errorf("second stimulus severity = %q, want critical", got)
	}
	if pub.published[2].Hints.Priority != "normal" {
		t.Errorf("recovery Hints.Priority = %q, want normal", pub.published[2].Hints.Priority)
	}
}

func TestPoll_FirstPollAlreadyCritical_PublishesImmediately(t *testing.T) {
	stats := &fakeStats{disk: map[string]float64{`C:\`: 97}}
	pub := &fakePublisher{}
	w := newWatcher(stats, []CheckConfig{diskCheck(`C:\`)}, pub, time.Hour)

	w.poll(context.Background())

	if got := pub.publishedCount(); got != 1 {
		t.Fatalf("published = %d, want 1 (already-critical state must fire on first poll, not be treated as a baseline)", got)
	}
}

func TestPoll_CheckErrorSkipsThatCheckOnly(t *testing.T) {
	stats := &fakeStats{diskErr: errors.New("disk unavailable"), memory: 90}
	pub := &fakePublisher{}
	checks := []CheckConfig{
		diskCheck(`C:\`),
		{Type: "memory", WarningThresholdPercent: 85},
	}
	w := newWatcher(stats, checks, pub, time.Hour)

	w.poll(context.Background())

	if got := pub.publishedCount(); got != 1 {
		t.Fatalf("published = %d, want 1 (memory check should still fire despite disk error)", got)
	}
}

func TestPoll_PublishFailure_StateNotAdvanced_RetriesNextPoll(t *testing.T) {
	stats := &fakeStats{disk: map[string]float64{`C:\`: 50}}
	pub := &fakePublisher{}
	w := newWatcher(stats, []CheckConfig{diskCheck(`C:\`)}, pub, time.Hour)

	w.poll(context.Background()) // ok baseline

	stats.mu.Lock()
	stats.disk[`C:\`] = 85
	stats.mu.Unlock()
	pub.publishErr = errors.New("nats down")
	w.poll(context.Background()) // ok -> warning, publish fails

	if got := pub.publishedCount(); got != 0 {
		t.Fatalf("published = %d, want 0 (publish failed)", got)
	}

	pub.publishErr = nil
	w.poll(context.Background()) // retry: still ok -> warning transition

	if got := pub.publishedCount(); got != 1 {
		t.Errorf("published = %d, want 1 (transition retried after publish recovered)", got)
	}
}

func TestPoll_MultipleDiskPaths_TrackedIndependently(t *testing.T) {
	stats := &fakeStats{disk: map[string]float64{`C:\`: 50, `D:\`: 97}}
	pub := &fakePublisher{}
	checks := []CheckConfig{diskCheck(`C:\`), diskCheck(`D:\`)}
	w := newWatcher(stats, checks, pub, time.Hour)

	w.poll(context.Background())

	if got := pub.publishedCount(); got != 1 {
		t.Fatalf("published = %d, want 1 (only D:\\ starts critical)", got)
	}
}

func TestPoll_CPUNoBaselineFirstCall_SkippedSilently(t *testing.T) {
	stats := &fakeStats{cpuErr: errNoCPUBaseline}
	pub := &fakePublisher{}
	checks := []CheckConfig{{Type: "cpu", WarningThresholdPercent: 90}}
	w := newWatcher(stats, checks, pub, time.Hour)

	w.poll(context.Background())
	if got := pub.publishedCount(); got != 0 {
		t.Fatalf("published = %d, want 0 (no baseline yet)", got)
	}

	stats.mu.Lock()
	stats.cpuErr = nil
	stats.cpu = 95
	stats.mu.Unlock()
	w.poll(context.Background())
	if got := pub.publishedCount(); got != 1 {
		t.Errorf("published = %d, want 1 (first real reading is critical)", got)
	}
}

func severityFromStimulus(t *testing.T, s *common.Stimulus) string {
	t.Helper()
	var meta struct {
		Severity string `json:"severity"`
	}
	if err := json.Unmarshal(s.ChannelMeta.ChannelSpecific, &meta); err != nil {
		t.Fatalf("decode channel_specific: %v", err)
	}
	return meta.Severity
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd perception-svc && go test ./sysmonitor/... -v`
Expected: FAIL with compile errors — package `sysmonitor` doesn't exist yet.

- [ ] **Step 3: Implement `sysmonitor.go`**

Create `perception-svc/sysmonitor/sysmonitor.go`:

```go
// Package sysmonitor implements perception-svc's System Monitor pull
// channel: polls disk space, memory, and CPU usage on a fixed interval and
// publishes a Stimulus only when a check's severity (ok/warning/critical)
// changes — see docs/superpowers/specs/2026-07-18-system-monitor-channel-design.md.
package sysmonitor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"soulman/common"
)

// Publisher is satisfied by *natspublish.Publisher. Declared here (not
// imported from natspublish) to avoid an import cycle — mirrors
// watcher.Publisher and gmailwatcher.Publisher's same rationale.
type Publisher interface {
	Publish(ctx context.Context, s *common.Stimulus) error
}

// statsProvider is the seam between the poll loop and the actual OS calls.
// Tests inject a fake; stats_windows.go's winStats is the real
// golang.org/x/sys/windows-backed implementation.
type statsProvider interface {
	DiskUsagePercent(path string) (float64, error)
	MemoryUsagePercent() (float64, error)
	CPUUsagePercent() (float64, error)
}

// errNoCPUBaseline signals that this is the first CPU reading since process
// startup (or since a prior reading), so there is no previous cumulative
// snapshot to diff against — treated as a silent skip, not a logged error.
var errNoCPUBaseline = errors.New("sysmonitor: no cpu baseline yet")

// CheckConfig describes one system-monitor check, mirroring
// sharedconfig.CheckConfig (perception-svc/main.go converts one into the
// other so this package doesn't import sharedconfig directly).
type CheckConfig struct {
	Type                     string
	Path                     string
	WarningThresholdPercent  float64
	CriticalThresholdPercent float64
}

type severity string

const (
	severityOK       severity = "ok"
	severityWarning  severity = "warning"
	severityCritical severity = "critical"
)

// Watcher polls the configured checks and publishes a Stimulus on each
// severity transition. State is in-memory only (see design spec) — a
// restart resets every check to "ok", so a still-bad condition re-fires
// once more, an accepted tradeoff simpler than a persisted checkpoint.
type Watcher struct {
	checks    []CheckConfig
	stats     statsProvider
	publisher Publisher
	interval  time.Duration

	state map[string]severity

	cancel context.CancelFunc
}

func newWatcher(stats statsProvider, checks []CheckConfig, publisher Publisher, interval time.Duration) *Watcher {
	return &Watcher{
		checks:    checks,
		stats:     stats,
		publisher: publisher,
		interval:  interval,
		state:     map[string]severity{},
	}
}

// Start launches the poll loop in a background goroutine and returns
// immediately, running one immediate poll first (so a bad state is caught
// right away, not after a full interval) — same pattern as
// gmailwatcher.Start.
func (w *Watcher) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	go w.pollLoop(ctx)
}

func (w *Watcher) pollLoop(ctx context.Context) {
	w.poll(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.poll(ctx)
		}
	}
}

// poll runs every configured check once. Each check is independent — one
// check's failure is logged and skipped, the rest still run, mirroring
// watcher's per-file isolation.
func (w *Watcher) poll(ctx context.Context) {
	for _, c := range w.checks {
		w.runCheck(ctx, c)
	}
}

func checkKey(c CheckConfig) string {
	if c.Type == "disk_space" {
		return c.Type + ":" + c.Path
	}
	return c.Type
}

func (w *Watcher) measure(c CheckConfig) (float64, error) {
	switch c.Type {
	case "disk_space":
		return w.stats.DiskUsagePercent(c.Path)
	case "memory":
		return w.stats.MemoryUsagePercent()
	case "cpu":
		return w.stats.CPUUsagePercent()
	default:
		return 0, fmt.Errorf("sysmonitor: unknown check type %q", c.Type)
	}
}

func deriveSeverity(value, warning, critical float64) severity {
	if critical > 0 && value >= critical {
		return severityCritical
	}
	if value >= warning {
		return severityWarning
	}
	return severityOK
}

func (w *Watcher) runCheck(ctx context.Context, c CheckConfig) {
	value, err := w.measure(c)
	if err != nil {
		if errors.Is(err, errNoCPUBaseline) {
			return
		}
		log.Printf("sysmonitor: check %s failed, skipping this poll: %v", checkKey(c), err)
		return
	}

	sev := deriveSeverity(value, c.WarningThresholdPercent, c.CriticalThresholdPercent)
	key := checkKey(c)
	prev, seen := w.state[key]

	if seen && prev == sev {
		return
	}
	if !seen && sev == severityOK {
		// First-ever poll for this check and it's already fine: nothing to
		// report. Still record the baseline so a future flip is detected.
		w.state[key] = sev
		return
	}

	stimulus := buildStimulus(c, value, sev)
	if err := w.publisher.Publish(ctx, stimulus); err != nil {
		log.Printf("sysmonitor: publish failed for %s (state unchanged, will retry next poll): %v", key, err)
		return
	}
	w.state[key] = sev
}

func checkLabel(c CheckConfig) string {
	switch c.Type {
	case "disk_space":
		return fmt.Sprintf("Disk space %s", c.Path)
	case "memory":
		return "Memory usage"
	case "cpu":
		return "CPU usage"
	default:
		return c.Type
	}
}

func formatMessage(c CheckConfig, value float64, sev severity, threshold float64) string {
	label := checkLabel(c)
	switch sev {
	case severityOK:
		return fmt.Sprintf("%s recovered to normal: %.0f%% used", label, value)
	case severityWarning:
		return fmt.Sprintf("%s warning: %.0f%% used (threshold %.0f%%)", label, value, threshold)
	default:
		return fmt.Sprintf("%s critical: %.0f%% used (threshold %.0f%%)", label, value, threshold)
	}
}

func priorityFor(sev severity) string {
	switch sev {
	case severityCritical:
		return "critical"
	case severityWarning:
		return "high"
	default:
		return "normal"
	}
}

func computeMessageID(checkType, path string, sev severity, at time.Time) string {
	sum := sha256.Sum256([]byte(checkType + path + string(sev) + at.Format(time.RFC3339)))
	return hex.EncodeToString(sum[:])
}

func buildStimulus(c CheckConfig, value float64, sev severity) *common.Stimulus {
	now := time.Now().UTC()
	id, err := uuid.NewV7()
	if err != nil {
		// Extremely unlikely (crypto/rand failure); fall back to a random v4
		// rather than crash the watcher over one reading.
		id = uuid.New()
	}

	threshold := c.WarningThresholdPercent
	if sev == severityCritical {
		threshold = c.CriticalThresholdPercent
	}

	specific, _ := json.Marshal(struct {
		CheckType        string  `json:"check_type"`
		Path             string  `json:"path,omitempty"`
		Severity         string  `json:"severity"`
		ValuePercent     float64 `json:"value_percent"`
		ThresholdPercent float64 `json:"threshold_percent"`
	}{
		CheckType:        c.Type,
		Path:             c.Path,
		Severity:         string(sev),
		ValuePercent:     value,
		ThresholdPercent: threshold,
	})

	return &common.Stimulus{
		StimulusID:    id.String(),
		SchemaVersion: 1,
		ReceivedAt:    now,
		OccurredAt:    &now,
		Channel:       "system-monitor",
		Source: common.Source{
			Identity:      "system-monitor",
			Authenticated: true,
			AuthMethod:    "system",
		},
		Content: common.Content{
			RawText:     formatMessage(c, value, sev, threshold),
			RawPayload:  json.RawMessage(`{}`),
			ContentType: "text",
			Attachments: []common.Attachment{},
		},
		ChannelMeta: common.ChannelMeta{
			MessageID:       computeMessageID(c.Type, c.Path, sev, now),
			ChannelSpecific: specific,
		},
		Hints: common.Hints{
			Priority: priorityFor(sev),
			Tags:     []string{"system", "system-monitor", c.Type},
		},
		Override: common.Override{
			Params: json.RawMessage(`{}`),
		},
	}
}

func (w *Watcher) Close() error {
	if w.cancel != nil {
		w.cancel()
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd perception-svc && go test ./sysmonitor/... -v`
Expected: PASS — all tests in Step 1.

- [ ] **Step 5: Commit**

```bash
git -C . add perception-svc/sysmonitor/sysmonitor.go perception-svc/sysmonitor/sysmonitor_test.go
git -C . commit -m "feat: add sysmonitor poll/state-machine core with fake stats provider"
```

---

### Task 4: `perception-svc/sysmonitor` — real Windows stats implementation

**Files:**
- Create: `perception-svc/sysmonitor/stats_windows.go`
- Create: `perception-svc/sysmonitor/stats_windows_test.go`
- Modify: `perception-svc/go.mod`, `perception-svc/go.sum`

**Interfaces:**
- Consumes: `statsProvider`, `errNoCPUBaseline`, `Watcher`, `CheckConfig`, `newWatcher` (Task 3)
- Produces: `sysmonitor.New(checks []CheckConfig, publisher Publisher, interval time.Duration) *Watcher` — the constructor `perception-svc/main.go` calls in Task 5.

- [ ] **Step 1: Promote `golang.org/x/sys` to a direct dependency**

Run:
```bash
cd perception-svc
go get golang.org/x/sys/windows@v0.47.0
go mod tidy
```
Expected: `go.mod`'s `golang.org/x/sys v0.47.0` line moves out of the `// indirect` block into the main `require (...)` block; `go.sum` unchanged (already resolved).

- [ ] **Step 2: Write the failing tests**

Create `perception-svc/sysmonitor/stats_windows_test.go`:

```go
//go:build windows

package sysmonitor

import (
	"errors"
	"testing"
	"time"
)

func TestWinStats_DiskUsagePercent_ReturnsPlausibleValue(t *testing.T) {
	s := &winStats{}
	pct, err := s.DiskUsagePercent(`C:\`)
	if err != nil {
		t.Fatalf("DiskUsagePercent: %v", err)
	}
	if pct < 0 || pct > 100 {
		t.Errorf("DiskUsagePercent = %v, want value in [0,100]", pct)
	}
}

func TestWinStats_DiskUsagePercent_InvalidPath_ReturnsError(t *testing.T) {
	s := &winStats{}
	if _, err := s.DiskUsagePercent(`Z:\does-not-exist-drive`); err == nil {
		t.Fatal("DiskUsagePercent: want error for a nonexistent drive, got nil")
	}
}

func TestWinStats_MemoryUsagePercent_ReturnsPlausibleValue(t *testing.T) {
	s := &winStats{}
	pct, err := s.MemoryUsagePercent()
	if err != nil {
		t.Fatalf("MemoryUsagePercent: %v", err)
	}
	if pct < 0 || pct > 100 {
		t.Errorf("MemoryUsagePercent = %v, want value in [0,100]", pct)
	}
}

func TestWinStats_CPUUsagePercent_FirstCallReturnsNoBaselineError(t *testing.T) {
	s := &winStats{}
	if _, err := s.CPUUsagePercent(); !errors.Is(err, errNoCPUBaseline) {
		t.Fatalf("CPUUsagePercent first call error = %v, want errNoCPUBaseline", err)
	}
}

func TestWinStats_CPUUsagePercent_SecondCallReturnsPlausibleValue(t *testing.T) {
	s := &winStats{}
	if _, err := s.CPUUsagePercent(); !errors.Is(err, errNoCPUBaseline) {
		t.Fatalf("first call error = %v, want errNoCPUBaseline", err)
	}
	time.Sleep(50 * time.Millisecond)
	pct, err := s.CPUUsagePercent()
	if err != nil {
		t.Fatalf("CPUUsagePercent second call: %v", err)
	}
	if pct < 0 || pct > 100 {
		t.Errorf("CPUUsagePercent = %v, want value in [0,100]", pct)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd perception-svc && go test ./sysmonitor/... -run TestWinStats -v`
Expected: FAIL with compile errors — `winStats` doesn't exist yet.

- [ ] **Step 4: Implement `stats_windows.go`**

Create `perception-svc/sysmonitor/stats_windows.go`:

```go
//go:build windows

package sysmonitor

import (
	"fmt"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// New builds a Watcher backed by real Windows system calls
// (golang.org/x/sys/windows — already an indirect dependency of this
// module via oauth2/nats.go, promoted to direct for this package) for
// disk, memory, and CPU statistics.
func New(checks []CheckConfig, publisher Publisher, interval time.Duration) *Watcher {
	return newWatcher(&winStats{}, checks, publisher, interval)
}

// winStats implements statsProvider. CPU usage needs the previous poll's
// cumulative idle/total time to compute a delta, so it carries that state
// internally (mutex-guarded since Watcher's poll loop and any future
// concurrent caller could both invoke it).
type winStats struct {
	mu          sync.Mutex
	haveCPUPrev bool
	prevIdle    uint64
	prevTotal   uint64
}

func (s *winStats) DiskUsagePercent(path string) (float64, error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, fmt.Errorf("sysmonitor: invalid disk path %q: %w", path, err)
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(ptr, &free, &total, &totalFree); err != nil {
		return 0, fmt.Errorf("sysmonitor: GetDiskFreeSpaceEx(%q): %w", path, err)
	}
	if total == 0 {
		return 0, fmt.Errorf("sysmonitor: GetDiskFreeSpaceEx(%q) reported zero total bytes", path)
	}
	return 100 * (1 - float64(free)/float64(total)), nil
}

func (s *winStats) MemoryUsagePercent() (float64, error) {
	mem := windows.MemoryStatusEx{Length: uint32(unsafe.Sizeof(windows.MemoryStatusEx{}))}
	if err := windows.GlobalMemoryStatusEx(&mem); err != nil {
		return 0, fmt.Errorf("sysmonitor: GlobalMemoryStatusEx: %w", err)
	}
	return float64(mem.MemoryLoad), nil
}

func (s *winStats) CPUUsagePercent() (float64, error) {
	var idle, kernel, user windows.Filetime
	if err := windows.GetSystemTimes(&idle, &kernel, &user); err != nil {
		return 0, fmt.Errorf("sysmonitor: GetSystemTimes: %w", err)
	}

	idleNow := filetimeToUint64(idle)
	// kernel time from GetSystemTimes already includes idle time, so total
	// elapsed time is (kernel + user); non-idle work is (total - idle).
	totalNow := filetimeToUint64(kernel) + filetimeToUint64(user)

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.haveCPUPrev {
		s.prevIdle = idleNow
		s.prevTotal = totalNow
		s.haveCPUPrev = true
		return 0, errNoCPUBaseline
	}

	idleDelta := idleNow - s.prevIdle
	totalDelta := totalNow - s.prevTotal
	s.prevIdle = idleNow
	s.prevTotal = totalNow

	if totalDelta == 0 {
		return 0, errNoCPUBaseline
	}

	return 100 * (1 - float64(idleDelta)/float64(totalDelta)), nil
}

func filetimeToUint64(ft windows.Filetime) uint64 {
	return uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime)
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd perception-svc && go test ./sysmonitor/... -v`
Expected: PASS — every test in both `sysmonitor_test.go` (Task 3) and `stats_windows_test.go` (this task).

- [ ] **Step 6: Commit**

```bash
git -C . add perception-svc/sysmonitor/stats_windows.go perception-svc/sysmonitor/stats_windows_test.go perception-svc/go.mod perception-svc/go.sum
git -C . commit -m "feat: implement real Windows disk/memory/CPU stats for sysmonitor"
```

---

### Task 5: Wire `sysmonitor` into `perception-svc/main.go`

**Files:**
- Modify: `perception-svc/main.go`

**Interfaces:**
- Consumes: `config.Config.SystemMonitorPollIntervalSeconds`/`SystemMonitorChecks` (Task 2), `sysmonitor.New`/`CheckConfig`/`Watcher` (Tasks 3-4)

- [ ] **Step 1: Add the sysmonitor import and wiring**

In `perception-svc/main.go`, add `"soulman/perception-svc/sysmonitor"` to the import block:

```go
import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"soulman/perception-svc/config"
	"soulman/perception-svc/gmailwatcher"
	"soulman/perception-svc/httpserver"
	"soulman/perception-svc/natspublish"
	"soulman/perception-svc/sysmonitor"
	"soulman/perception-svc/watcher"
)
```

Insert this block after the Gmail channel's `if/else` block, before the `srv := httpserver.New(...)` line:

```go
	smChecks := make([]sysmonitor.CheckConfig, len(cfg.SystemMonitorChecks))
	for i, c := range cfg.SystemMonitorChecks {
		smChecks[i] = sysmonitor.CheckConfig{
			Type:                     c.Type,
			Path:                     c.Path,
			WarningThresholdPercent:  c.WarningThresholdPercent,
			CriticalThresholdPercent: c.CriticalThresholdPercent,
		}
	}
	sm := sysmonitor.New(smChecks, pub, time.Duration(cfg.SystemMonitorPollIntervalSeconds)*time.Second)
	defer sm.Close()
	sm.Start(ctx)
	log.Printf("sysmonitor: started (checks=%d, poll_interval=%ds)", len(smChecks), cfg.SystemMonitorPollIntervalSeconds)
```

- [ ] **Step 2: Build to verify it compiles**

Run: `cd perception-svc && go build ./...`
Expected: no errors.

- [ ] **Step 3: Run the full perception-svc test suite**

Run: `cd perception-svc && go test ./... -v`
Expected: PASS across every package (`config`, `watcher`, `gmailwatcher`, `sysmonitor`, `natspublish`, `httpserver`).

- [ ] **Step 4: Manually verify against the real dev config**

Run (from `perception-svc/`, with `soulman-dev`'s NATS reachable): `CONFIG_PATH=../config/dev.json go run .`
Expected: the startup log includes a `sysmonitor: started (checks=3, poll_interval=300s)` line alongside the existing folder-watcher/Gmail/HTTP log lines, and no fatal error. Stop it with Ctrl+C once confirmed.

- [ ] **Step 5: Commit**

```bash
git -C . add perception-svc/main.go
git -C . commit -m "feat: wire sysmonitor channel into perception-svc main"
```

---

### Task 6: `thinking-svc` — System Monitor rule

**Files:**
- Create: `thinking-svc/rules/system_monitor.go`
- Create: `thinking-svc/rules/system_monitor_test.go`
- Modify: `thinking-svc/rules/rule.go`

**Interfaces:**
- Consumes: `common.Stimulus`, `common.ActionRequest`, `rules.Rule`, `rules.errorReportParams` (unexported, same package, defined in `error_report.go`), `llm.Client` (unused parameter, matching `ErrorReportRule`/`CLINoteRule`'s signature)
- Produces: `rules.SystemMonitorRule`, registered in `rules.Registry`

- [ ] **Step 1: Write the failing tests**

Create `thinking-svc/rules/system_monitor_test.go`:

```go
package rules_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"soulman/common"
	"soulman/thinking-svc/rules"
)

func newSystemMonitorStimulus(rawText, checkType, path string, occurredAt time.Time) *common.Stimulus {
	specific, _ := json.Marshal(struct {
		CheckType string `json:"check_type"`
		Path      string `json:"path,omitempty"`
	}{CheckType: checkType, Path: path})

	return &common.Stimulus{
		StimulusID: "stim-sysmon-001",
		Channel:    "system-monitor",
		ReceivedAt: time.Now().UTC(),
		OccurredAt: &occurredAt,
		Content: common.Content{
			RawText:     rawText,
			ContentType: "text",
			RawPayload:  json.RawMessage(`{}`),
		},
		ChannelMeta: common.ChannelMeta{
			ChannelSpecific: specific,
		},
		Hints:    common.Hints{Priority: "critical", Tags: []string{"system", "system-monitor", checkType}},
		Override: common.Override{Params: json.RawMessage(`{}`)},
	}
}

func TestSystemMonitorRule_Match_SystemMonitorChannel(t *testing.T) {
	s := newSystemMonitorStimulus(`Disk space C:\ critical: 97% used (threshold 95%)`, "disk_space", `C:\`, time.Now())
	if !rules.SystemMonitorRule.Match(s) {
		t.Error("expected match for system-monitor channel")
	}
}

func TestSystemMonitorRule_Match_OtherChannel_NoMatch(t *testing.T) {
	s := newSystemMonitorStimulus("x", "disk_space", `C:\`, time.Now())
	s.Channel = "folder-watcher"
	if rules.SystemMonitorRule.Match(s) {
		t.Error("expected no match for folder-watcher channel")
	}
}

func TestSystemMonitorRule_Handle_BuildsActionRequest_WithPath(t *testing.T) {
	occurred := time.Date(2026, 7, 18, 15, 42, 0, 0, time.UTC)
	rawText := `Disk space C:\ critical: 97% used (threshold 95%)`
	s := newSystemMonitorStimulus(rawText, "disk_space", `C:\`, occurred)

	req, err := rules.SystemMonitorRule.Handle(context.Background(), s, &fakeSummarizer{})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if req.ActionHint != "append_daily_report_entry" {
		t.Errorf("ActionHint = %q, want append_daily_report_entry", req.ActionHint)
	}
	if req.RiskLevel != "low" {
		t.Errorf("RiskLevel = %q, want low", req.RiskLevel)
	}
	if req.CorrelationID == "" {
		t.Error("CorrelationID must be generated")
	}

	var params struct {
		Summary    string     `json:"summary"`
		RawContent string     `json:"raw_content"`
		SourcePath string     `json:"source_path"`
		OccurredAt *time.Time `json:"occurred_at"`
	}
	if err := json.Unmarshal(req.Parameters, &params); err != nil {
		t.Fatalf("decode Parameters: %v", err)
	}
	if params.Summary != rawText {
		t.Errorf("Summary = %q, want %q", params.Summary, rawText)
	}
	if params.RawContent != rawText {
		t.Errorf("RawContent = %q, want %q", params.RawContent, rawText)
	}
	if params.SourcePath != `system-monitor/disk_space/C:\` {
		t.Errorf(`SourcePath = %q, want "system-monitor/disk_space/C:\"`, params.SourcePath)
	}
}

func TestSystemMonitorRule_Handle_BuildsActionRequest_NoPath(t *testing.T) {
	occurred := time.Date(2026, 7, 18, 15, 42, 0, 0, time.UTC)
	s := newSystemMonitorStimulus("Memory usage warning: 87% used (threshold 85%)", "memory", "", occurred)

	req, err := rules.SystemMonitorRule.Handle(context.Background(), s, &fakeSummarizer{})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var params struct {
		SourcePath string `json:"source_path"`
	}
	if err := json.Unmarshal(req.Parameters, &params); err != nil {
		t.Fatalf("decode Parameters: %v", err)
	}
	if params.SourcePath != "system-monitor/memory" {
		t.Errorf(`SourcePath = %q, want "system-monitor/memory"`, params.SourcePath)
	}
}

func TestMatch_FindsSystemMonitorRule(t *testing.T) {
	s := newSystemMonitorStimulus("x", "cpu", "", time.Now())
	r := rules.Match(s)
	if r == nil {
		t.Fatal("Match = nil, want SystemMonitorRule for system-monitor stimulus")
	}
	if r.Name != "system-monitor" {
		t.Errorf("Name = %q, want system-monitor", r.Name)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd thinking-svc && go test ./rules/... -run "SystemMonitor|FindsSystemMonitorRule" -v`
Expected: FAIL with compile errors — `rules.SystemMonitorRule` doesn't exist yet.

- [ ] **Step 3: Implement the rule**

Create `thinking-svc/rules/system_monitor.go`:

```go
package rules

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"soulman/common"
	"soulman/thinking-svc/llm"
)

// SystemMonitorRule implements the System Monitor design's mechanical rule
// (docs/superpowers/specs/2026-07-18-system-monitor-channel-design.md): any
// stimulus with channel == "system-monitor" becomes an
// append_daily_report_entry Action Request, the same shape
// ErrorReportRule/CLINoteRule already produce — no LLM call, since
// perception-svc's sysmonitor package already builds a complete,
// human-readable message in raw_text.
var SystemMonitorRule = Rule{
	Name: "system-monitor",
	Match: func(s *common.Stimulus) bool {
		return s.Channel == "system-monitor"
	},
	Handle: handleSystemMonitor,
}

func handleSystemMonitor(_ context.Context, s *common.Stimulus, _ llm.Client) (*common.ActionRequest, error) {
	params, err := json.Marshal(errorReportParams{
		Summary:    s.Content.RawText,
		RawContent: s.Content.RawText,
		SourcePath: systemMonitorSourcePath(s),
		OccurredAt: s.OccurredAt,
	})
	if err != nil {
		return nil, fmt.Errorf("rules: marshal system monitor parameters: %w", err)
	}

	req := &common.ActionRequest{
		CorrelationID:   uuid.NewString(),
		Intent:          "Log this system monitor alert to today's daily report",
		ActionHint:      "append_daily_report_entry",
		Parameters:      params,
		RiskLevel:       "low",
		Urgency:         "normal",
		ExpectedOutcome: "one entry appended to today's report file",
		Fallback:        "if fs-agent fails, retry once; if it fails again, log to episodic memory with error:execution tag and give up silently — a missed report entry is not worth interrupting the human",
	}
	return req, nil
}

// systemMonitorSourcePath builds "system-monitor/<check_type>" or
// "system-monitor/<check_type>/<path>" from channel_metadata.channel_specific
// — the only keys perception-svc's sysmonitor package guarantees. Parallels
// error_report.go's watchedPath() extraction helper.
func systemMonitorSourcePath(s *common.Stimulus) string {
	var meta struct {
		CheckType string `json:"check_type"`
		Path      string `json:"path"`
	}
	if len(s.ChannelMeta.ChannelSpecific) > 0 {
		_ = json.Unmarshal(s.ChannelMeta.ChannelSpecific, &meta)
	}
	if meta.Path == "" {
		return "system-monitor/" + meta.CheckType
	}
	return "system-monitor/" + meta.CheckType + "/" + meta.Path
}
```

Modify `thinking-svc/rules/rule.go`'s `Registry` (append after `CLINoteRule`):

```go
var Registry = []Rule{
	ErrorReportRule,
	GmailTriageRule,
	CLINoteRule,
	SystemMonitorRule,
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd thinking-svc && go test ./rules/... -v`
Expected: PASS — every test in the package, including the four new ones and `TestMatch_NoRuleForUnknownChannel` etc. still passing unchanged.

- [ ] **Step 5: Run the full thinking-svc test suite**

Run: `cd thinking-svc && go test ./... -v`
Expected: PASS across every package.

- [ ] **Step 6: Commit**

```bash
git -C . add thinking-svc/rules/system_monitor.go thinking-svc/rules/system_monitor_test.go thinking-svc/rules/rule.go
git -C . commit -m "feat: add system-monitor thinking rule reusing append_daily_report_entry"
```

---

### Task 7: Documentation updates

**Files:**
- Modify: `CLAUDE.md`
- Modify: `perception-svc/NOTES.md`

**Interfaces:** none (docs only).

- [ ] **Step 1: Update `CLAUDE.md`'s `perception-svc` bullet**

Find this text in `CLAUDE.md` (under `## Services`, item 2):

```
2. **`perception-svc`** — normalizes external input into `Stimulus` events on `soulman.stimulus.raw`. Two input channels: **folder-watcher** (`fsnotify` on paths from the shared config file's `watch_paths`) and **Gmail** (`gmailwatcher` package — polls the inbox via OAuth2 offline refresh token, dedups via a per-environment Gmail label). Also serves `POST /api/perceive/cli` (CLI push channel) and `POST /api/perceive/raw` (generic Stimulus injection for debugging).
   - Specs: `2026-07-17-perception-svc-design.md`, `2026-07-18-gmail-channel-design.md`, `2026-07-18-soulman-cli-design.md`, `2026-07-18-pipeline-debugging-tools-design.md`
   - Notes: `perception-svc/NOTES.md` — real incidents (padded Gmail base64 bodies, a blocking-startup-poll bug, the unbounded-backlog incident that motivated the debugging tools)
```

Replace with:

```
2. **`perception-svc`** — normalizes external input into `Stimulus` events on `soulman.stimulus.raw`. Three input channels: **folder-watcher** (`fsnotify` on paths from the shared config file's `watch_paths`), **Gmail** (`gmailwatcher` package — polls the inbox via OAuth2 offline refresh token, dedups via a per-environment Gmail label), and **System Monitor** (`sysmonitor` package — polls disk/memory/CPU via `golang.org/x/sys/windows`, publishes only on a severity transition). Also serves `POST /api/perceive/cli` (CLI push channel) and `POST /api/perceive/raw` (generic Stimulus injection for debugging).
   - Specs: `2026-07-17-perception-svc-design.md`, `2026-07-18-gmail-channel-design.md`, `2026-07-18-soulman-cli-design.md`, `2026-07-18-pipeline-debugging-tools-design.md`, `2026-07-18-system-monitor-channel-design.md`
   - Notes: `perception-svc/NOTES.md` — real incidents (padded Gmail base64 bodies, a blocking-startup-poll bug, the unbounded-backlog incident that motivated the debugging tools)
```

- [ ] **Step 2: Update `CLAUDE.md`'s `thinking-svc` bullet**

Find this text (item 3):

```
3. **`thinking-svc`** — matches stimuli against a rule table, publishes an Action Request to `soulman.thinking.request` (durable JetStream stream). Rules today: `folder-watcher` and `cli-note` → mechanical report-entry (no LLM); `gmail` → DeepSeek-judged importance triage, always logs, notifies Discord (batched) only if judged important. `DEEPSEEK_API_KEY` is non-fatal if blank (logs a warning; DeepSeek calls then fail and summarization falls back to deterministic text) but the Gmail triage classifier needs it to actually classify anything.
   - Specs: `2026-07-17-thinking-svc-design.md`, `2026-07-18-gmail-triage-action-design.md`
   - Notes: `thinking-svc/NOTES.md` — the classifier prompt was rewritten with explicit criteria after real false positives (newsletters flagged important)
```

Replace with:

```
3. **`thinking-svc`** — matches stimuli against a rule table, publishes an Action Request to `soulman.thinking.request` (durable JetStream stream). Rules today: `folder-watcher`, `cli-note`, and `system-monitor` → mechanical report-entry (no LLM); `gmail` → DeepSeek-judged importance triage, always logs, notifies Discord (batched) only if judged important. `DEEPSEEK_API_KEY` is non-fatal if blank (logs a warning; DeepSeek calls then fail and summarization falls back to deterministic text) but the Gmail triage classifier needs it to actually classify anything.
   - Specs: `2026-07-17-thinking-svc-design.md`, `2026-07-18-gmail-triage-action-design.md`, `2026-07-18-system-monitor-channel-design.md`
   - Notes: `thinking-svc/NOTES.md` — the classifier prompt was rewritten with explicit criteria after real false positives (newsletters flagged important)
```

- [ ] **Step 3: Add a System Monitor section to `perception-svc/NOTES.md`**

Insert a new section right after the existing `## Gmail channel (\`gmailwatcher\` package)` section's content and before `## Pipeline debugging tools`:

```markdown
## System Monitor channel (`sysmonitor` package)

Uses `golang.org/x/sys/windows` syscalls directly (`GetDiskFreeSpaceEx`, `GlobalMemoryStatusEx`, `GetSystemTimes`) rather than shelling out to PowerShell or pulling in a cross-platform library like gopsutil — the syscalls are only a few lines each and the dependency was already indirect via `oauth2`/`nats.go`. CPU usage is computed by diffing cumulative idle/total time against the *previous poll's* snapshot rather than sampling twice per poll — natural since the poll interval (300s) is already long enough to average over.

Severity state (`ok`/`warning`/`critical` per check) is **in-memory only**, not persisted like `watcher`'s checkpoint file — a restart resets every check to `ok`, so a still-bad condition re-fires one redundant alert on the next poll. Accepted tradeoff: restarts are rare, and a spurious duplicate alert is far cheaper than the persistence code a checkpoint file would need.

Dev and prod both poll the same physical machine's disk/memory/CPU and will each independently detect and alert on the same real condition — the same accepted duplication the Gmail channel already has for the shared inbox.
```

- [ ] **Step 4: Commit**

```bash
git -C . add CLAUDE.md perception-svc/NOTES.md
git -C . commit -m "docs: document the system-monitor perception channel"
```
