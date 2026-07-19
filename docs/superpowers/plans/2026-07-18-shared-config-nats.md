# Shared Config: NATS URL, Subjects, and Consumer Names Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move `NATS_URL`, `STIMULUS_SUBJECT`, `THINKING_REQUEST_SUBJECT`, `MEMORY_WRITE_SUBJECT`, and the two JetStream `CONSUMER_NAME` values out of per-service env vars and into the shared JSON config file (`config/dev.json`, `config/prod.json`), read via `common/sharedconfig`, with no env-var fallback.

**Architecture:** Extend `common/sharedconfig.Config` with the new fields (flat + a nested `ConsumerNames` struct). Each of the four services' `config.Load()` calls `sharedconfig.Load` (three of them gain this call for the first time), validates its own required fields are non-empty, and drops the now-removed env vars. `memory-svc`, `thinking-svc`, and `action-svc`'s `Load()` signature changes from `*Config` to `(*Config, error)` to match `perception-svc`'s existing pattern, so their `main.go` call sites get a one-line update too.

**Tech Stack:** Go (`encoding/json`, standard `testing` package), PowerShell (`run-<svc>.ps1` launcher scripts).

## Global Constraints

- File is the sole source for these fields — no env var override (per the approved design spec).
- `HTTP_PORT` stays an env var in all four services — out of scope.
- `sharedconfig.Load` itself stays validation-free; each service's own `config.Load` validates the fields it actually uses.
- No live-reload — a config file edit takes effect on the next service restart.

---

### Task 1: Extend `common/sharedconfig.Config` with NATS/subject/consumer-name fields

**Files:**
- Modify: `common/sharedconfig/config.go`
- Modify: `common/sharedconfig/config_test.go`

**Interfaces:**
- Produces: `sharedconfig.Config{WatchPaths []string, NATSURL string, StimulusSubject string, ThinkingRequestSubject string, MemoryWriteSubject string, ConsumerNames ConsumerNames}` and `sharedconfig.ConsumerNames{MemorySvc string, ThinkingSvc string}` — every later task reads these exact field names off the `*sharedconfig.Config` returned by `sharedconfig.Load`.

- [ ] **Step 1: Write the failing test**

Add to `common/sharedconfig/config_test.go` (append after the existing tests):

```go
func TestLoad_AllFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"watch_paths": ["C:\\a\\errors"],
		"nats_url": "nats://localhost:4222",
		"stimulus_subject": "soulman.stimulus.raw",
		"thinking_request_subject": "soulman.thinking.request",
		"memory_write_subject": "soulman.memory.write",
		"consumer_names": {
			"memory_svc": "memory-svc",
			"thinking_svc": "thinking-svc"
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := sharedconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.NATSURL != "nats://localhost:4222" {
		t.Errorf("NATSURL = %q, want nats://localhost:4222", cfg.NATSURL)
	}
	if cfg.StimulusSubject != "soulman.stimulus.raw" {
		t.Errorf("StimulusSubject = %q, want soulman.stimulus.raw", cfg.StimulusSubject)
	}
	if cfg.ThinkingRequestSubject != "soulman.thinking.request" {
		t.Errorf("ThinkingRequestSubject = %q, want soulman.thinking.request", cfg.ThinkingRequestSubject)
	}
	if cfg.MemoryWriteSubject != "soulman.memory.write" {
		t.Errorf("MemoryWriteSubject = %q, want soulman.memory.write", cfg.MemoryWriteSubject)
	}
	if cfg.ConsumerNames.MemorySvc != "memory-svc" {
		t.Errorf("ConsumerNames.MemorySvc = %q, want memory-svc", cfg.ConsumerNames.MemorySvc)
	}
	if cfg.ConsumerNames.ThinkingSvc != "thinking-svc" {
		t.Errorf("ConsumerNames.ThinkingSvc = %q, want thinking-svc", cfg.ConsumerNames.ThinkingSvc)
	}
}

func TestLoad_MissingNATSFields_ZeroValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"watch_paths": []}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := sharedconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.NATSURL != "" {
		t.Errorf("NATSURL = %q, want empty when absent from JSON", cfg.NATSURL)
	}
	if cfg.ConsumerNames.MemorySvc != "" {
		t.Errorf("ConsumerNames.MemorySvc = %q, want empty when absent from JSON", cfg.ConsumerNames.MemorySvc)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go -C common test ./sharedconfig/...`
Expected: FAIL — compile error, `cfg.NATSURL` etc. undefined on `sharedconfig.Config`.

- [ ] **Step 3: Implement the schema change**

Replace the `Config` type in `common/sharedconfig/config.go` (lines 14-19) with:

```go
// Config is the schema of the shared config file. New fields get added
// here as more services need non-secret settings; a service that doesn't
// use a given field simply ignores it.
type Config struct {
	WatchPaths             []string      `json:"watch_paths"`
	NATSURL                string        `json:"nats_url"`
	StimulusSubject        string        `json:"stimulus_subject"`
	ThinkingRequestSubject string        `json:"thinking_request_subject"`
	MemoryWriteSubject     string        `json:"memory_write_subject"`
	ConsumerNames          ConsumerNames `json:"consumer_names"`
}

// ConsumerNames holds the JetStream durable consumer name for each service
// that has one. Only memory-svc and thinking-svc use these today —
// perception-svc only publishes, and action-svc's subscribe is ephemeral
// core NATS with no durable name.
type ConsumerNames struct {
	MemorySvc   string `json:"memory_svc"`
	ThinkingSvc string `json:"thinking_svc"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go -C common test ./sharedconfig/...`
Expected: PASS (all tests, old and new)

- [ ] **Step 5: Commit**

```bash
git -C common add sharedconfig/config.go sharedconfig/config_test.go
git -C common commit -m "feat(common): add NATS URL, subjects, and consumer names to shared config schema"
```

(If `common` is not its own git root, run `git add`/`git commit` from the vault root instead, with the same paths prefixed `common/`.)

---

### Task 2: `perception-svc` reads `nats_url`/`stimulus_subject` from shared config

**Files:**
- Modify: `perception-svc/config/config.go`
- Modify: `perception-svc/config/config_test.go`

**Interfaces:**
- Consumes: `sharedconfig.Config.NATSURL`, `sharedconfig.Config.StimulusSubject` (from Task 1).
- Produces: `config.Load() (*config.Config, error)` — signature unchanged from today; `main.go` needs no changes for this task.

- [ ] **Step 1: Write the failing test**

Replace `perception-svc/config/config_test.go` in full with:

```go
package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"soulman/perception-svc/config"
)

type sharedFields struct {
	WatchPaths      []string `json:"watch_paths"`
	NATSURL         string   `json:"nats_url"`
	StimulusSubject string   `json:"stimulus_subject"`
}

func writeConfigFile(t *testing.T, watchPaths []string, natsURL, stimulusSubject string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	data, err := json.Marshal(sharedFields{
		WatchPaths:      watchPaths,
		NATSURL:         natsURL,
		StimulusSubject: stimulusSubject,
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
}

func TestLoad_Defaults(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, []string{`C:\Users\Lenovo\DigitalMe\errors`}, "nats://localhost:4222", "soulman.stimulus.raw")
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
}

func TestLoad_SharedConfigValues(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, []string{`C:\a\errors`, `C:\b\errors`, `C:\c\errors`}, "nats://remote:4222", "soulman.dev.stimulus.raw")
	os.Setenv("CONFIG_PATH", configPath)
	os.Setenv("HTTP_PORT", "9999")
	os.Setenv("CHECKPOINT_PATH", "./data/checkpoints.json")
	os.Setenv("RECONCILE_INTERVAL_SECONDS", "45")

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
}

func TestLoad_InvalidReconcileInterval_FallsBackToDefault(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw")
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

	configPath := writeConfigFile(t, []string{}, "nats://localhost:4222", "soulman.stimulus.raw")
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty watch_paths, got nil")
	}
}

func TestLoad_EmptyNATSURL_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "", "soulman.stimulus.raw")
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty nats_url, got nil")
	}
}

func TestLoad_EmptyStimulusSubject_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "")
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty stimulus_subject, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go -C perception-svc test ./config/...`
Expected: FAIL — `TestLoad_Defaults` and `TestLoad_SharedConfigValues` fail because `cfg.NATSURL`/`cfg.StimulusSubject` still come from env vars (which are unset), and `TestLoad_EmptyNATSURL_ReturnsError`/`TestLoad_EmptyStimulusSubject_ReturnsError` fail because `Load` doesn't validate those fields yet.

- [ ] **Step 3: Implement the config change**

Replace `perception-svc/config/config.go` in full with:

```go
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

	return &Config{
		NATSURL:           shared.NATSURL,
		HTTPPort:          env("HTTP_PORT", "9001"),
		WatchPaths:        shared.WatchPaths,
		CheckpointPath:    env("CHECKPOINT_PATH", "./checkpoints.json"),
		ReconcileInterval: envInt("RECONCILE_INTERVAL_SECONDS", 30),
		StimulusSubject:   shared.StimulusSubject,
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go -C perception-svc test ./config/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git -C perception-svc add config/config.go config/config_test.go
git -C perception-svc commit -m "feat(perception-svc): read nats_url and stimulus_subject from shared config"
```

---

### Task 3: `memory-svc` reads NATS wiring from shared config

**Files:**
- Modify: `memory-svc/config/config.go`
- Modify: `memory-svc/config/config_test.go`
- Modify: `memory-svc/main.go:17`

**Interfaces:**
- Consumes: `sharedconfig.Config.NATSURL`, `sharedconfig.Config.StimulusSubject`, `sharedconfig.Config.ConsumerNames.MemorySvc` (from Task 1).
- Produces: `config.Load() (*config.Config, error)` — signature changes from `*config.Config` to `(*config.Config, error)`; `main.go`'s call site updates accordingly.

- [ ] **Step 1: Write the failing test**

Replace `memory-svc/config/config_test.go` in full with:

```go
package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"soulman/memory-svc/config"
)

type consumerNames struct {
	MemorySvc string `json:"memory_svc"`
}

type sharedFields struct {
	NATSURL         string        `json:"nats_url"`
	StimulusSubject string        `json:"stimulus_subject"`
	ConsumerNames   consumerNames `json:"consumer_names"`
}

func writeConfigFile(t *testing.T, natsURL, stimulusSubject, consumerName string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	data, err := json.Marshal(sharedFields{
		NATSURL:         natsURL,
		StimulusSubject: stimulusSubject,
		ConsumerNames:   consumerNames{MemorySvc: consumerName},
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

	configPath := writeConfigFile(t, "nats://localhost:4222", "soulman.stimulus.raw", "memory-svc")
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
}

func TestLoad_SharedConfigValues(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://remote:4222", "soulman.dev.stimulus.raw", "memory-svc-dev")
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

	configPath := writeConfigFile(t, "nats://localhost:4222", "soulman.stimulus.raw", "")
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty consumer_names.memory_svc, got nil")
	}
}

func TestLoad_EmptyNATSURL_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "", "soulman.stimulus.raw", "memory-svc")
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty nats_url, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go -C memory-svc test ./config/...`
Expected: FAIL — compile error (`config.Load()` returns one value, test expects two) and, once that's reconciled, the new assertions fail since fields are still env-driven.

- [ ] **Step 3: Implement the config change**

Replace `memory-svc/config/config.go` in full with:

```go
package config

import (
	"fmt"
	"os"

	"soulman/common/sharedconfig"
)

type Config struct {
	NATSURL         string
	DatabaseURL     string
	HTTPPort        string
	LogDir          string
	Schema          string
	StimulusSubject string
	ConsumerName    string
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

	return &Config{
		NATSURL:         shared.NATSURL,
		DatabaseURL:     env("DATABASE_URL", "postgres://postgres:postgres@localhost:54322/postgres"),
		HTTPPort:        env("HTTP_PORT", "9002"),
		LogDir:          env("LOG_DIR", "./logs"),
		Schema:          env("SCHEMA", "memory_dev"),
		StimulusSubject: shared.StimulusSubject,
		ConsumerName:    shared.ConsumerNames.MemorySvc,
	}, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
```

- [ ] **Step 4: Update `main.go`'s call site**

In `memory-svc/main.go`, replace line 17:

```go
	cfg := config.Load()
```

with:

```go
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
```

- [ ] **Step 5: Run tests to verify they pass, and build to verify main.go compiles**

Run: `go -C memory-svc test ./config/...`
Expected: PASS

Run: `go -C memory-svc build ./...`
Expected: builds with no errors

- [ ] **Step 6: Commit**

```bash
git -C memory-svc add config/config.go config/config_test.go main.go
git -C memory-svc commit -m "feat(memory-svc): read nats_url, stimulus_subject, and consumer name from shared config"
```

---

### Task 4: `thinking-svc` reads NATS wiring from shared config

**Files:**
- Modify: `thinking-svc/config/config.go`
- Modify: `thinking-svc/config/config_test.go`
- Modify: `thinking-svc/main.go:43`

**Interfaces:**
- Consumes: `sharedconfig.Config.NATSURL`, `sharedconfig.Config.StimulusSubject`, `sharedconfig.Config.ConsumerNames.ThinkingSvc`, `sharedconfig.Config.ThinkingRequestSubject` (from Task 1).
- Produces: `config.Load() (*config.Config, error)` — signature changes from `*config.Config`.

- [ ] **Step 1: Write the failing test**

Replace `thinking-svc/config/config_test.go` in full with:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go -C thinking-svc test ./config/...`
Expected: FAIL — compile error (`config.Load()` returns one value, test expects two).

- [ ] **Step 3: Implement the config change**

Replace `thinking-svc/config/config.go` in full with:

```go
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
```

- [ ] **Step 4: Update `main.go`'s call site**

In `thinking-svc/main.go`, replace line 43:

```go
	cfg := config.Load()
```

with:

```go
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
```

- [ ] **Step 5: Run tests to verify they pass, and build to verify main.go compiles**

Run: `go -C thinking-svc test ./config/...`
Expected: PASS

Run: `go -C thinking-svc build ./...`
Expected: builds with no errors

- [ ] **Step 6: Commit**

```bash
git -C thinking-svc add config/config.go config/config_test.go main.go
git -C thinking-svc commit -m "feat(thinking-svc): read nats_url, subjects, and consumer name from shared config"
```

---

### Task 5: `action-svc` reads NATS wiring from shared config

**Files:**
- Modify: `action-svc/config/config.go`
- Modify: `action-svc/config/config_test.go`
- Modify: `action-svc/main.go:18`

**Interfaces:**
- Consumes: `sharedconfig.Config.NATSURL`, `sharedconfig.Config.ThinkingRequestSubject`, `sharedconfig.Config.MemoryWriteSubject` (from Task 1).
- Produces: `config.Load() (*config.Config, error)` — signature changes from `*config.Config`.

- [ ] **Step 1: Write the failing test**

Replace `action-svc/config/config_test.go` in full with:

```go
package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"soulman/action-svc/config"
)

type sharedFields struct {
	NATSURL                string `json:"nats_url"`
	ThinkingRequestSubject string `json:"thinking_request_subject"`
	MemoryWriteSubject     string `json:"memory_write_subject"`
}

func writeConfigFile(t *testing.T, natsURL, thinkingRequestSubject, memoryWriteSubject string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	data, err := json.Marshal(sharedFields{
		NATSURL:                natsURL,
		ThinkingRequestSubject: thinkingRequestSubject,
		MemoryWriteSubject:     memoryWriteSubject,
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

	configPath := writeConfigFile(t, "nats://localhost:4222", "soulman.thinking.request", "soulman.memory.write")
	os.Setenv("CONFIG_PATH", configPath)

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
}

func TestLoad_SharedConfigValues(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://remote:4222", "soulman.dev.thinking.request", "soulman.dev.memory.write")
	os.Setenv("CONFIG_PATH", configPath)
	os.Setenv("SOULMAN_ROOT", `C:\Users\Lenovo\soulman-prod`)
	os.Setenv("REPORT_SEND_TIME", "09:30")

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

	configPath := writeConfigFile(t, "nats://localhost:4222", "", "soulman.memory.write")
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty thinking_request_subject, got nil")
	}
}

func TestLoad_EmptyMemoryWriteSubject_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://localhost:4222", "soulman.thinking.request", "")
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty memory_write_subject, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go -C action-svc test ./config/...`
Expected: FAIL — compile error (`config.Load()` returns one value, test expects two).

- [ ] **Step 3: Implement the config change**

Replace `action-svc/config/config.go` in full with:

```go
package config

import (
	"fmt"
	"os"

	"soulman/common/sharedconfig"
)

type Config struct {
	NATSURL                string
	HTTPPort               string
	SoulmanRoot            string
	ReportSendTime         string
	ReportNotifier         string
	DiscordBotToken        string
	DiscordChannelID       string
	ThinkingRequestSubject string
	MemoryWriteSubject     string
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
	if shared.ThinkingRequestSubject == "" {
		return nil, fmt.Errorf("shared config %s has no thinking_request_subject configured", configPath)
	}
	if shared.MemoryWriteSubject == "" {
		return nil, fmt.Errorf("shared config %s has no memory_write_subject configured", configPath)
	}

	return &Config{
		NATSURL:                shared.NATSURL,
		HTTPPort:               env("HTTP_PORT", "9004"),
		SoulmanRoot:            env("SOULMAN_ROOT", `C:\Users\Lenovo\soulman-dev`),
		ReportSendTime:         env("REPORT_SEND_TIME", "10:00"),
		ReportNotifier:         env("REPORT_NOTIFIER", "discord"),
		DiscordBotToken:        env("DISCORD_BOT_TOKEN", ""),
		DiscordChannelID:       env("DISCORD_CHANNEL_ID", ""),
		ThinkingRequestSubject: shared.ThinkingRequestSubject,
		MemoryWriteSubject:     shared.MemoryWriteSubject,
	}, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
```

- [ ] **Step 4: Update `main.go`'s call site**

In `action-svc/main.go`, replace line 18:

```go
	cfg := config.Load()
```

with:

```go
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
```

- [ ] **Step 5: Run tests to verify they pass, and build to verify main.go compiles**

Run: `go -C action-svc test ./config/...`
Expected: PASS

Run: `go -C action-svc build ./...`
Expected: builds with no errors

- [ ] **Step 6: Commit**

```bash
git -C action-svc add config/config.go config/config_test.go main.go
git -C action-svc commit -m "feat(action-svc): read nats_url and subjects from shared config"
```

---

### Task 6: Add the new fields to `config/dev.json` and `config/prod.json`

**Files:**
- Modify: `config/dev.json`
- Modify: `config/prod.json`

**Interfaces:**
- Consumes: nothing (data file).
- Produces: the on-disk JSON every service's `sharedconfig.Load` call in Tasks 2-5 reads at runtime.

- [ ] **Step 1: Update `config/dev.json`**

Replace `config/dev.json` in full with:

```json
{
  "watch_paths": [
    "C:\\Users\\Lenovo\\soulman-dev\\test-errors"
  ],
  "nats_url": "nats://localhost:4222",
  "stimulus_subject": "soulman.dev.stimulus.raw",
  "thinking_request_subject": "soulman.dev.thinking.request",
  "memory_write_subject": "soulman.dev.memory.write",
  "consumer_names": {
    "memory_svc": "memory-svc-dev",
    "thinking_svc": "thinking-svc-dev"
  }
}
```

- [ ] **Step 2: Update `config/prod.json`**

Replace `config/prod.json` in full with:

```json
{
  "watch_paths": [
    "C:\\Users\\Lenovo\\DigitalMe\\errors"
  ],
  "nats_url": "nats://localhost:4222",
  "stimulus_subject": "soulman.stimulus.raw",
  "thinking_request_subject": "soulman.thinking.request",
  "memory_write_subject": "soulman.memory.write",
  "consumer_names": {
    "memory_svc": "memory-svc",
    "thinking_svc": "thinking-svc"
  }
}
```

- [ ] **Step 3: Verify both files parse as valid JSON**

Run (from vault root): `go run -C common ./... 2>NUL & powershell -Command "Get-Content config/dev.json | ConvertFrom-Json | Out-Null; Get-Content config/prod.json | ConvertFrom-Json | Out-Null; Write-Output OK"`
Expected: prints `OK` with no error (this only validates JSON syntax, not the schema — Task 8's smoke test validates the schema end-to-end).

- [ ] **Step 4: Commit**

```bash
git add config/dev.json config/prod.json
git commit -m "chore: add nats_url, subjects, and consumer names to config/dev.json and config/prod.json"
```

---

### Task 7: Remove the now-redundant env var lines from `run-<svc>.ps1` scripts

**Files:**
- Modify: `C:\Users\Lenovo\soulman-dev\run-perception-svc.ps1`
- Modify: `C:\Users\Lenovo\soulman-dev\run-memory-svc.ps1`
- Modify: `C:\Users\Lenovo\soulman-dev\run-thinking-svc.ps1`
- Modify: `C:\Users\Lenovo\soulman-dev\run-action-svc.ps1`

**Interfaces:**
- Consumes: `config/dev.json`'s new fields (Task 6) — these scripts stop setting the env vars that Tasks 2-5's code no longer reads.
- Produces: nothing further downstream — this is the last task that touches the runtime wiring.

These files are outside the git-tracked vault (`soulman-dev`'s own directory, not a git repo per the project's convention), so there's no commit step for this task — edit them directly. The 4 prod scripts need no changes: none of them currently override these vars, so they already rely on Go-level behavior that's now driven by `config/prod.json` instead.

- [ ] **Step 1: Edit `run-perception-svc.ps1`**

Remove this line (and the two comment lines directly above it, since they describe the override being removed):

```powershell
# Dev uses its own port and a soulman.dev.* subject so it can run alongside
# prod on the same shared local NATS server without colliding.
$env:HTTP_PORT = "9011"
$env:STIMULUS_SUBJECT = "soulman.dev.stimulus.raw"
```

Replace with (keep `HTTP_PORT`, drop `STIMULUS_SUBJECT`, update the comment):

```powershell
# Dev uses its own port so it can run alongside prod on the same shared
# local NATS server without colliding; the soulman.dev.* subject now comes
# from config/dev.json.
$env:HTTP_PORT = "9011"
```

- [ ] **Step 2: Edit `run-memory-svc.ps1`**

Replace:

```powershell
# Dev uses its own port, subject, and JetStream consumer name so it can run
# alongside prod on the same shared local NATS server without colliding.
# SCHEMA is left at its default (memory_dev).
$env:HTTP_PORT = "9012"
$env:STIMULUS_SUBJECT = "soulman.dev.stimulus.raw"
$env:CONSUMER_NAME = "memory-svc-dev"
```

with:

```powershell
# Dev uses its own port so it can run alongside prod on the same shared
# local NATS server without colliding; the soulman.dev.* subject and -dev
# consumer name now come from config/dev.json. SCHEMA is left at its
# default (memory_dev).
$env:HTTP_PORT = "9012"
```

- [ ] **Step 3: Edit `run-thinking-svc.ps1`**

Replace:

```powershell
# Dev uses its own port, subjects, and JetStream consumer name so it can run
# alongside prod on the same shared local NATS server without colliding.
$env:HTTP_PORT = "9013"
$env:STIMULUS_SUBJECT = "soulman.dev.stimulus.raw"
$env:CONSUMER_NAME = "thinking-svc-dev"
$env:THINKING_REQUEST_SUBJECT = "soulman.dev.thinking.request"
```

with:

```powershell
# Dev uses its own port so it can run alongside prod on the same shared
# local NATS server without colliding; the soulman.dev.* subjects and -dev
# consumer name now come from config/dev.json.
$env:HTTP_PORT = "9013"
```

- [ ] **Step 4: Edit `run-action-svc.ps1`**

Replace:

```powershell
# Dev uses its own port and subjects so it can run alongside prod on the
# same shared local NATS server without colliding.
$env:HTTP_PORT = "9014"
$env:THINKING_REQUEST_SUBJECT = "soulman.dev.thinking.request"
$env:MEMORY_WRITE_SUBJECT = "soulman.dev.memory.write"
```

with:

```powershell
# Dev uses its own port so it can run alongside prod on the same shared
# local NATS server without colliding; the soulman.dev.* subjects now come
# from config/dev.json.
$env:HTTP_PORT = "9014"
```

- [ ] **Step 5: Verify no other references to the removed env vars remain in these 4 scripts**

Run: `powershell -Command "Select-String -Path C:\Users\Lenovo\soulman-dev\run-*.ps1 -Pattern 'STIMULUS_SUBJECT|CONSUMER_NAME|THINKING_REQUEST_SUBJECT|MEMORY_WRITE_SUBJECT|NATS_URL'"`
Expected: no output (no matches).

---

### Task 8: End-to-end smoke test across all four services

**Files:**
- None modified — this task only runs and observes the built binaries.

**Interfaces:**
- Consumes: everything from Tasks 1-7.
- Produces: confirmation that the fatal-fast validation paths (Error Handling table in the design spec) actually behave as designed, since none of the four services have a `main_test.go`.

- [ ] **Step 1: Build all four services**

Run:
```bash
go -C perception-svc build -o /tmp/perception-svc.exe .
go -C memory-svc build -o /tmp/memory-svc.exe .
go -C thinking-svc build -o /tmp/thinking-svc.exe .
go -C action-svc build -o /tmp/action-svc.exe .
```
Expected: all four build with no errors.

- [ ] **Step 2: Verify perception-svc starts cleanly against `config/dev.json`**

Run (PowerShell):
```powershell
$env:CONFIG_PATH = "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.claude\worktrees\shared-config-nats\config\dev.json"
$env:CHECKPOINT_PATH = "$env:TEMP\smoke-checkpoints.json"
$proc = Start-Process -FilePath "C:\tmp\perception-svc.exe" -PassThru -RedirectStandardOutput "$env:TEMP\smoke-out.log" -RedirectStandardError "$env:TEMP\smoke-err.log"
Start-Sleep -Seconds 2
Stop-Process -Id $proc.Id -Force
Get-Content "$env:TEMP\smoke-out.log", "$env:TEMP\smoke-err.log"
```
Expected: log line `perception-svc started (NATS=nats://localhost:4222, HTTP=:9001, watching=[...test-errors], checkpoint=...)` — no fatal error about missing `nats_url` or `stimulus_subject`. (Adjust the exe path from Step 1 to match your OS's temp dir; on Windows this is typically `$env:TEMP`, not `/tmp`.)

- [ ] **Step 3: Verify fatal-fast behavior when a required field is missing**

Run (PowerShell):
```powershell
$badConfig = "$env:TEMP\smoke-bad-config.json"
'{"watch_paths": ["C:\\a\\errors"], "stimulus_subject": "soulman.stimulus.raw"}' | Set-Content -Path $badConfig -Encoding utf8
$env:CONFIG_PATH = $badConfig
& "C:\tmp\perception-svc.exe"
```
Expected: process exits immediately, logging `config: shared config ...smoke-bad-config.json has no nats_url configured` (missing `nats_url` field triggers the Task 2 validation added to `perception-svc/config/config.go`).

- [ ] **Step 4: Repeat Steps 2-3's pattern for `memory-svc`, `thinking-svc`, and `action-svc`**

For each service, point `CONFIG_PATH` at `config/dev.json`, run the built exe briefly, confirm its startup log line (`memory-svc started (...)`, `thinking-svc started (...)`, `action-svc started (...)`) shows no fatal config error, then repeat with a config file missing one required field (per the Task 3/4/5 validation you just added) and confirm the process exits with the expected `log.Fatalf` message instead of starting.

- [ ] **Step 5: Run the full test suite for all four services plus `common`, as a final check**

Run:
```bash
go -C common test ./...
go -C perception-svc test ./...
go -C memory-svc test ./...
go -C thinking-svc test ./...
go -C action-svc test ./...
```
Expected: all PASS.

No commit for this task — it's verification only, no file changes.
