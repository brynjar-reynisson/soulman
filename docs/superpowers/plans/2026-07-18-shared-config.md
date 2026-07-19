# Shared JSON Config Files Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move `perception-svc`'s watched-folder list out of the `WATCH_PATHS` env var and into a new git-tracked, per-environment JSON config file (`config/dev.json`, `config/prod.json`), loaded via a new shared `common/sharedconfig` package, with each `run-<svc>.ps1` script responsible for copying the right file into its environment at launch.

**Architecture:** `common/sharedconfig` (new package in the existing `common` Go module) defines the config schema (`watch_paths []string` today) and a `Load(path) (*Config, error)` function using stdlib `encoding/json`. `perception-svc/config` calls it via a new `CONFIG_PATH` env var (default `./config.json`) instead of parsing `WATCH_PATHS`, and fails fast (`log.Fatalf`) if the file is missing, malformed, or has an empty `watch_paths` list. All 8 `run-<svc>.ps1` scripts (4 services × dev/prod) copy `config/<env>.json` from the vault to `<env-root>\config.json` on every launch, mirroring the existing "always fresh `go build`" pattern — but only `perception-svc` actually reads it this iteration.

**Tech Stack:** Go 1.25 (stdlib `encoding/json`, `os` only — no new dependency), PowerShell 5.1

## Global Constraints

- New package: `common/sharedconfig`, exposing `type Config struct { WatchPaths []string \`json:"watch_paths"\` }` and `func Load(path string) (*Config, error)`. No new `go.mod` entries anywhere — `encoding/json`/`os` are stdlib, and `perception-svc/go.mod` already depends on `soulman/common` via a local `replace` directive.
- `perception-svc/config.Load()` signature changes from `func Load() *Config` to `func Load() (*Config, error)`.
- New env var `CONFIG_PATH` (default `./config.json`) tells `perception-svc` where its copy of the shared config file is.
- `WATCH_PATHS` env var and the `splitPaths` helper are removed entirely from `perception-svc/config/config.go` — the file is the only source, no override.
- Missing/unreadable `config.json`, malformed JSON, or an empty `watch_paths` list are all fatal startup errors in `perception-svc` (`log.Fatalf` in `main.go`) — not a soft-skip like the Discord/DeepSeek credentials.
- JSON field name: `watch_paths` (snake_case, matching this codebase's existing wire-format JSON tags in `common/stimulus.go` and `common/action.go`).
- `config/dev.json` and `config/prod.json` live at the vault root, git-tracked. No secrets ever go in these files — that boundary stays with `.env`.
- `<env-root>\config.json` (the copied runtime file) is **not** git-tracked — same status as `.env`, lives outside the vault repo entirely (`soulman-dev`/`soulman-prod` are plain directories, not git repos).
- Only `perception-svc` reads the config file this iteration. The other three services' `run-<svc>.ps1` scripts still copy the file and set `CONFIG_PATH` (future-proofing), but no Go code changes in `thinking-svc`, `action-svc`, or `memory-svc`.
- All git commands for the vault-repo tasks (Tasks 1-3, 5) run with `git -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.claude\worktrees\shared-config-design"` — this session's active worktree, branch `worktree-shared-config-design`. Never `cd`.
- All Go commands use `go -C <module-dir> ...` (e.g. `go -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.claude\worktrees\shared-config-design\common" test ./sharedconfig/...`) instead of `cd`-ing into the module directory.
- Task 4 (the 8 PowerShell scripts) edits files under `C:\Users\Lenovo\soulman-dev\` and `C:\Users\Lenovo\soulman-prod\` — outside the git repo entirely, not tracked by any git worktree. No `git add`/commit for that task; its own verification is running the scripts for real.

---

## File Structure

```
common/
└── sharedconfig/
    ├── config.go          # Config struct + Load(path) (*Config, error)
    └── config_test.go

perception-svc/
├── config/
│   ├── config.go          # Modified: Load() now (*Config, error), reads CONFIG_PATH via sharedconfig
│   └── config_test.go     # Modified: CONFIG_PATH-based fixtures instead of WATCH_PATHS
└── main.go                 # Modified: handle config.Load()'s new error return

config/                     # New directory, vault root
├── dev.json
└── prod.json

CLAUDE.md                   # Modified: document the shared config file convention

C:\Users\Lenovo\soulman-dev\run-perception-svc.ps1    # Modified (outside repo)
C:\Users\Lenovo\soulman-dev\run-action-svc.ps1        # Modified (outside repo)
C:\Users\Lenovo\soulman-dev\run-thinking-svc.ps1      # Modified (outside repo)
C:\Users\Lenovo\soulman-dev\run-memory-svc.ps1        # Modified (outside repo)
C:\Users\Lenovo\soulman-prod\run-perception-svc.ps1   # Modified (outside repo)
C:\Users\Lenovo\soulman-prod\run-action-svc.ps1       # Modified (outside repo)
C:\Users\Lenovo\soulman-prod\run-thinking-svc.ps1     # Modified (outside repo)
C:\Users\Lenovo\soulman-prod\run-memory-svc.ps1       # Modified (outside repo)
```

---

### Task 1: `common/sharedconfig` package

**Files:**
- Create: `common/sharedconfig/config.go`
- Create: `common/sharedconfig/config_test.go`

**Interfaces:**
- Produces: `sharedconfig.Config{WatchPaths []string}`, `sharedconfig.Load(path string) (*sharedconfig.Config, error)` — consumed by Task 2.

- [ ] **Step 1: Write the failing tests**

Create `common/sharedconfig/config_test.go`:

```go
package sharedconfig_test

import (
	"os"
	"path/filepath"
	"testing"

	"soulman/common/sharedconfig"
)

func TestLoad_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{"watch_paths": ["C:\\a\\errors", "C:\\b\\errors"]}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := sharedconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := []string{`C:\a\errors`, `C:\b\errors`}
	if len(cfg.WatchPaths) != len(want) {
		t.Fatalf("WatchPaths = %v, want %v", cfg.WatchPaths, want)
	}
	for i, p := range want {
		if cfg.WatchPaths[i] != p {
			t.Errorf("WatchPaths[%d] = %q, want %q", i, cfg.WatchPaths[i], p)
		}
	}
}

func TestLoad_MissingFile_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.json")

	_, err := sharedconfig.Load(path)
	if err == nil {
		t.Fatal("Load: want error for missing file, got nil")
	}
}

func TestLoad_MalformedJSON_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := sharedconfig.Load(path)
	if err == nil {
		t.Fatal("Load: want error for malformed JSON, got nil")
	}
}

func TestLoad_EmptyWatchPaths_NotAnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"watch_paths": []}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := sharedconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.WatchPaths) != 0 {
		t.Errorf("WatchPaths = %v, want empty", cfg.WatchPaths)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```powershell
go -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.claude\worktrees\shared-config-design\common" test ./sharedconfig/... -v
```
Expected: a build/compile error — the `sharedconfig` package doesn't exist yet (the directory has only a `_test.go` file, no non-test `.go` file declaring `package sharedconfig`).

- [ ] **Step 3: Write the implementation**

Create `common/sharedconfig/config.go`:

```go
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
	WatchPaths []string `json:"watch_paths"`
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
```

- [ ] **Step 4: Run tests to verify they pass**

```powershell
go -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.claude\worktrees\shared-config-design\common" test ./sharedconfig/... -v
```
Expected: `PASS` for all 4 tests (`TestLoad_ValidFile`, `TestLoad_MissingFile_ReturnsError`, `TestLoad_MalformedJSON_ReturnsError`, `TestLoad_EmptyWatchPaths_NotAnError`).

- [ ] **Step 5: Commit**

```powershell
git -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.claude\worktrees\shared-config-design" add common/sharedconfig/config.go common/sharedconfig/config_test.go
git -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.claude\worktrees\shared-config-design" commit -m "feat(common): add sharedconfig package for shared JSON config loading"
```

---

### Task 2: Wire `perception-svc` to read `watch_paths` from the shared config

**Files:**
- Modify: `perception-svc/config/config.go` (full replacement)
- Modify: `perception-svc/config/config_test.go` (full replacement)
- Modify: `perception-svc/main.go:17-20`

**Interfaces:**
- Consumes: `sharedconfig.Load(path string) (*sharedconfig.Config, error)` from Task 1.
- Produces: `config.Load() (*config.Config, error)` — the signature change every caller of `perception-svc/config.Load` must handle. Only `main.go` calls it.

- [ ] **Step 1: Write the failing tests**

Replace the full contents of `perception-svc/config/config_test.go`:

```go
package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"soulman/perception-svc/config"
)

func writeConfigFile(t *testing.T, watchPaths []string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	data, err := json.Marshal(map[string][]string{"watch_paths": watchPaths})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func unsetAllEnv() {
	os.Unsetenv("NATS_URL")
	os.Unsetenv("HTTP_PORT")
	os.Unsetenv("CONFIG_PATH")
	os.Unsetenv("CHECKPOINT_PATH")
	os.Unsetenv("RECONCILE_INTERVAL_SECONDS")
	os.Unsetenv("STIMULUS_SUBJECT")
}

func TestLoad_Defaults(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, []string{`C:\Users\Lenovo\DigitalMe\errors`})
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

func TestLoad_EnvOverride(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, []string{`C:\a\errors`, `C:\b\errors`, `C:\c\errors`})

	os.Setenv("NATS_URL", "nats://remote:4222")
	os.Setenv("HTTP_PORT", "9999")
	os.Setenv("CONFIG_PATH", configPath)
	os.Setenv("CHECKPOINT_PATH", "./data/checkpoints.json")
	os.Setenv("RECONCILE_INTERVAL_SECONDS", "45")
	os.Setenv("STIMULUS_SUBJECT", "soulman.dev.stimulus.raw")

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

	configPath := writeConfigFile(t, []string{`C:\a\errors`})
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

	configPath := writeConfigFile(t, []string{})
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty watch_paths, got nil")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```powershell
go -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.claude\worktrees\shared-config-design\perception-svc" test ./config/... -v
```
Expected: compile failure — `cfg, err := config.Load()` doesn't match the current `func Load() *Config` signature (too many return values expected).

- [ ] **Step 3: Write the implementation**

Replace the full contents of `perception-svc/config/config.go`:

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

	return &Config{
		NATSURL:           env("NATS_URL", "nats://localhost:4222"),
		HTTPPort:          env("HTTP_PORT", "9001"),
		WatchPaths:        shared.WatchPaths,
		CheckpointPath:    env("CHECKPOINT_PATH", "./checkpoints.json"),
		ReconcileInterval: envInt("RECONCILE_INTERVAL_SECONDS", 30),
		StimulusSubject:   env("STIMULUS_SUBJECT", "soulman.stimulus.raw"),
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

Modify `perception-svc/main.go` — replace lines 17-20:

Before:
```go
func main() {
	cfg := config.Load()

	ctx, cancel := context.WithCancel(context.Background())
```

After:
```go
func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
```

(`log` is already imported in `main.go` — no import changes needed there. The later `pub, err := natspublish.New(...)` on line 27 still works unchanged: `err` is already declared by this point, and `pub` is new, so `:=` is still valid.)

- [ ] **Step 4: Run tests to verify they pass**

```powershell
go -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.claude\worktrees\shared-config-design\perception-svc" test ./config/... -v
```
Expected: `PASS` for all 6 tests (`TestLoad_Defaults`, `TestLoad_EnvOverride`, `TestLoad_InvalidReconcileInterval_FallsBackToDefault`, `TestLoad_MissingConfigFile_ReturnsError`, `TestLoad_EmptyWatchPaths_ReturnsError`, plus the existing suite).

- [ ] **Step 5: Verify the whole service still builds**

```powershell
go -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.claude\worktrees\shared-config-design\perception-svc" build -o "$env:TEMP\perception-svc-buildcheck.exe" .
```
Expected: exits 0, no compile errors. (This is a throwaway build to confirm `main.go` compiles against the new `config.Load()` signature — not the binary that gets deployed; that happens per-environment via `run-perception-svc.ps1` in Task 4.)

- [ ] **Step 6: Commit**

```powershell
git -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.claude\worktrees\shared-config-design" add perception-svc/config/config.go perception-svc/config/config_test.go perception-svc/main.go
git -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.claude\worktrees\shared-config-design" commit -m "feat(perception-svc): load watch_paths from shared config file instead of WATCH_PATHS"
```

---

### Task 3: Create `config/dev.json` and `config/prod.json`

**Files:**
- Create: `config/dev.json`
- Create: `config/prod.json`

**Interfaces:**
- Produces: the two files Task 4's PowerShell scripts copy into each environment.

- [ ] **Step 1: Create `config/dev.json`**

```json
{
  "watch_paths": [
    "C:\\Users\\Lenovo\\soulman-dev\\test-errors"
  ]
}
```

- [ ] **Step 2: Create `config/prod.json`**

```json
{
  "watch_paths": [
    "C:\\Users\\Lenovo\\DigitalMe\\errors"
  ]
}
```

- [ ] **Step 3: Validate both are well-formed JSON**

```powershell
Get-Content "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.claude\worktrees\shared-config-design\config\dev.json" | ConvertFrom-Json
Get-Content "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.claude\worktrees\shared-config-design\config\prod.json" | ConvertFrom-Json
```
Expected: both print a `watch_paths` property with one string element each — no parse errors.

- [ ] **Step 4: Commit**

```powershell
git -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.claude\worktrees\shared-config-design" add config/dev.json config/prod.json
git -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.claude\worktrees\shared-config-design" commit -m "chore: add config/dev.json and config/prod.json"
```

---

### Task 4: Wire config-file copying into all 8 `run-<svc>.ps1` scripts and verify live

**Files (all outside the git repo — no commit for this task, see Global Constraints):**
- Modify: `C:\Users\Lenovo\soulman-dev\run-perception-svc.ps1`
- Modify: `C:\Users\Lenovo\soulman-prod\run-perception-svc.ps1`
- Modify: `C:\Users\Lenovo\soulman-dev\run-action-svc.ps1`
- Modify: `C:\Users\Lenovo\soulman-prod\run-action-svc.ps1`
- Modify: `C:\Users\Lenovo\soulman-dev\run-thinking-svc.ps1`
- Modify: `C:\Users\Lenovo\soulman-prod\run-thinking-svc.ps1`
- Modify: `C:\Users\Lenovo\soulman-dev\run-memory-svc.ps1`
- Modify: `C:\Users\Lenovo\soulman-prod\run-memory-svc.ps1`

**Interfaces:**
- Consumes: `config/dev.json`, `config/prod.json` from Task 3; the `perception-svc.exe` built from Task 2's code.
- Produces: `<env-root>\config.json` (runtime copy) and `CONFIG_PATH` env var, for every environment × service combination.

- [ ] **Step 1: Replace `C:\Users\Lenovo\soulman-dev\run-perception-svc.ps1`**

Full new contents:

```powershell
# Builds perception-svc from the vault source and runs it in this (dev) environment.

$ErrorActionPreference = "Stop"

& "$PSScriptRoot\load-env.ps1"

$repoSrc = "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\perception-svc"
$binDir  = Join-Path $PSScriptRoot "bin"
$exe     = Join-Path $binDir "perception-svc.exe"

New-Item -ItemType Directory -Force $binDir | Out-Null

Push-Location $repoSrc
try {
    go build -o $exe .
} finally {
    Pop-Location
}

# Pin an explicit path so the checkpoint file's location doesn't depend on
# whatever directory this script happened to be launched from.
$stateDir = Join-Path $PSScriptRoot "state"
New-Item -ItemType Directory -Force $stateDir | Out-Null
$env:CHECKPOINT_PATH = Join-Path $stateDir "perception-svc-checkpoints.json"

# Watched folders now come from config/dev.json in the vault (copied fresh
# on every launch) instead of a WATCH_PATHS override — dev watches its own
# test-errors folder so manual/test drops don't mix with actual
# DigitalMe-generated error files.
$configSrc = "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\config\dev.json"
$configDst = Join-Path $PSScriptRoot "config.json"
Copy-Item $configSrc $configDst -Force
$env:CONFIG_PATH = $configDst
New-Item -ItemType Directory -Force (Join-Path $PSScriptRoot "test-errors") | Out-Null

# Dev uses its own port and a soulman.dev.* subject so it can run alongside
# prod on the same shared local NATS server without colliding.
$env:HTTP_PORT = "9011"
$env:STIMULUS_SUBJECT = "soulman.dev.stimulus.raw"

& $exe
```

- [ ] **Step 2: Replace `C:\Users\Lenovo\soulman-prod\run-perception-svc.ps1`**

Full new contents:

```powershell
# Builds perception-svc from the vault source and runs it in this (prod) environment.
# Watches folders from config/prod.json (copied fresh on every launch).

$ErrorActionPreference = "Stop"

& "$PSScriptRoot\load-env.ps1"

$repoSrc = "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\perception-svc"
$binDir  = Join-Path $PSScriptRoot "bin"
$exe     = Join-Path $binDir "perception-svc.exe"

New-Item -ItemType Directory -Force $binDir | Out-Null

Push-Location $repoSrc
try {
    go build -o $exe .
} finally {
    Pop-Location
}

# Pin an explicit path so the checkpoint file's location doesn't depend on
# whatever directory this script happened to be launched from.
$stateDir = Join-Path $PSScriptRoot "state"
New-Item -ItemType Directory -Force $stateDir | Out-Null
$env:CHECKPOINT_PATH = Join-Path $stateDir "perception-svc-checkpoints.json"

$configSrc = "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\config\prod.json"
$configDst = Join-Path $PSScriptRoot "config.json"
Copy-Item $configSrc $configDst -Force
$env:CONFIG_PATH = $configDst

& $exe
```

- [ ] **Step 3: Replace `C:\Users\Lenovo\soulman-dev\run-action-svc.ps1`**

Full new contents:

```powershell
# Builds action-svc from the vault source and runs it in this (dev) environment.
# Discord secrets (if any) come from .env in this directory via load-env.ps1.

$ErrorActionPreference = "Stop"

& "$PSScriptRoot\load-env.ps1"

$repoSrc = "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\action-svc"
$binDir  = Join-Path $PSScriptRoot "bin"
$exe     = Join-Path $binDir "action-svc.exe"

New-Item -ItemType Directory -Force $binDir | Out-Null

Push-Location $repoSrc
try {
    go build -o $exe .
} finally {
    Pop-Location
}

$configSrc = "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\config\dev.json"
$configDst = Join-Path $PSScriptRoot "config.json"
Copy-Item $configSrc $configDst -Force
$env:CONFIG_PATH = $configDst

if (-not $env:DISCORD_BOT_TOKEN -or -not $env:DISCORD_CHANNEL_ID) {
    Write-Warning "DISCORD_BOT_TOKEN / DISCORD_CHANNEL_ID not set - the 10am report cron will skip sending until both are configured. Report entries still get written to disk."
}

# Dev uses its own port and subjects so it can run alongside prod on the
# same shared local NATS server without colliding.
$env:HTTP_PORT = "9014"
$env:THINKING_REQUEST_SUBJECT = "soulman.dev.thinking.request"
$env:MEMORY_WRITE_SUBJECT = "soulman.dev.memory.write"

& $exe
```

- [ ] **Step 4: Replace `C:\Users\Lenovo\soulman-prod\run-action-svc.ps1`**

Full new contents:

```powershell
# Builds action-svc from the vault source and runs it in this (prod) environment.
# Discord secrets (if any) come from .env in this directory via load-env.ps1.

$ErrorActionPreference = "Stop"

& "$PSScriptRoot\load-env.ps1"

$repoSrc = "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\action-svc"
$binDir  = Join-Path $PSScriptRoot "bin"
$exe     = Join-Path $binDir "action-svc.exe"

New-Item -ItemType Directory -Force $binDir | Out-Null

Push-Location $repoSrc
try {
    go build -o $exe .
} finally {
    Pop-Location
}

# config.go's SOULMAN_ROOT default points at soulman-dev - must override
# explicitly here or prod would write reports into the dev tree.
$env:SOULMAN_ROOT = $PSScriptRoot

$configSrc = "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\config\prod.json"
$configDst = Join-Path $PSScriptRoot "config.json"
Copy-Item $configSrc $configDst -Force
$env:CONFIG_PATH = $configDst

if (-not $env:DISCORD_BOT_TOKEN -or -not $env:DISCORD_CHANNEL_ID) {
    Write-Warning "DISCORD_BOT_TOKEN / DISCORD_CHANNEL_ID not set - the 10am report cron will skip sending until both are configured. Report entries still get written to disk."
}

& $exe
```

- [ ] **Step 5: Replace `C:\Users\Lenovo\soulman-dev\run-thinking-svc.ps1`**

Full new contents:

```powershell
# Builds thinking-svc from the vault source and runs it in this (dev) environment.
# Secrets come from .env in this directory via load-env.ps1 - never hardcode them here.

$ErrorActionPreference = "Stop"

& "$PSScriptRoot\load-env.ps1"

$repoSrc = "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\thinking-svc"
$binDir  = Join-Path $PSScriptRoot "bin"
$exe     = Join-Path $binDir "thinking-svc.exe"

New-Item -ItemType Directory -Force $binDir | Out-Null

Push-Location $repoSrc
try {
    go build -o $exe .
} finally {
    Pop-Location
}

$configSrc = "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\config\dev.json"
$configDst = Join-Path $PSScriptRoot "config.json"
Copy-Item $configSrc $configDst -Force
$env:CONFIG_PATH = $configDst

if (-not $env:DEEPSEEK_API_KEY) {
    Write-Warning "DEEPSEEK_API_KEY is not set - unused by v1's error-report rule anyway (reserved for future rules)."
}

# Dev uses its own port, subjects, and JetStream consumer name so it can run
# alongside prod on the same shared local NATS server without colliding.
$env:HTTP_PORT = "9013"
$env:STIMULUS_SUBJECT = "soulman.dev.stimulus.raw"
$env:CONSUMER_NAME = "thinking-svc-dev"
$env:THINKING_REQUEST_SUBJECT = "soulman.dev.thinking.request"

& $exe
```

- [ ] **Step 6: Replace `C:\Users\Lenovo\soulman-prod\run-thinking-svc.ps1`**

Full new contents:

```powershell
# Builds thinking-svc from the vault source and runs it in this (prod) environment.
# Secrets come from .env in this directory via load-env.ps1 - never hardcode them here.

$ErrorActionPreference = "Stop"

& "$PSScriptRoot\load-env.ps1"

$repoSrc = "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\thinking-svc"
$binDir  = Join-Path $PSScriptRoot "bin"
$exe     = Join-Path $binDir "thinking-svc.exe"

New-Item -ItemType Directory -Force $binDir | Out-Null

Push-Location $repoSrc
try {
    go build -o $exe .
} finally {
    Pop-Location
}

$configSrc = "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\config\prod.json"
$configDst = Join-Path $PSScriptRoot "config.json"
Copy-Item $configSrc $configDst -Force
$env:CONFIG_PATH = $configDst

if (-not $env:DEEPSEEK_API_KEY) {
    Write-Warning "DEEPSEEK_API_KEY is not set - unused by v1's error-report rule anyway (reserved for future rules)."
}

& $exe
```

- [ ] **Step 7: Replace `C:\Users\Lenovo\soulman-dev\run-memory-svc.ps1`**

Full new contents:

```powershell
# Builds memory-svc from the vault source and runs it in this (dev) environment.

$ErrorActionPreference = "Stop"

& "$PSScriptRoot\load-env.ps1"

$repoSrc = "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\memory-svc"
$binDir  = Join-Path $PSScriptRoot "bin"
$exe     = Join-Path $binDir "memory-svc.exe"

New-Item -ItemType Directory -Force $binDir | Out-Null

Push-Location $repoSrc
try {
    go build -o $exe .
} finally {
    Pop-Location
}

$configSrc = "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\config\dev.json"
$configDst = Join-Path $PSScriptRoot "config.json"
Copy-Item $configSrc $configDst -Force
$env:CONFIG_PATH = $configDst

$env:LOG_DIR = Join-Path $PSScriptRoot "logs"
New-Item -ItemType Directory -Force $env:LOG_DIR | Out-Null

# Dev uses its own port, subject, and JetStream consumer name so it can run
# alongside prod on the same shared local NATS server without colliding.
# SCHEMA is left at its default (memory_dev).
$env:HTTP_PORT = "9012"
$env:STIMULUS_SUBJECT = "soulman.dev.stimulus.raw"
$env:CONSUMER_NAME = "memory-svc-dev"

& $exe
```

- [ ] **Step 8: Replace `C:\Users\Lenovo\soulman-prod\run-memory-svc.ps1`**

Full new contents:

```powershell
# Builds memory-svc from the vault source and runs it in this (prod) environment.

$ErrorActionPreference = "Stop"

& "$PSScriptRoot\load-env.ps1"

$repoSrc = "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\memory-svc"
$binDir  = Join-Path $PSScriptRoot "bin"
$exe     = Join-Path $binDir "memory-svc.exe"

New-Item -ItemType Directory -Force $binDir | Out-Null

Push-Location $repoSrc
try {
    go build -o $exe .
} finally {
    Pop-Location
}

$configSrc = "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\config\prod.json"
$configDst = Join-Path $PSScriptRoot "config.json"
Copy-Item $configSrc $configDst -Force
$env:CONFIG_PATH = $configDst

$env:SCHEMA = "memory_prod"
$env:LOG_DIR = Join-Path $PSScriptRoot "logs"
New-Item -ItemType Directory -Force $env:LOG_DIR | Out-Null

Write-Warning "memory_prod schema does not exist yet in the local Postgres instance - inserts will fail and fall back to file-only logging until it's created."

& $exe
```

- [ ] **Step 9: Stop all 8 currently-running service processes**

```powershell
Get-CimInstance Win32_Process -Filter "Name='perception-svc.exe' OR Name='thinking-svc.exe' OR Name='memory-svc.exe' OR Name='action-svc.exe'" |
    ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }
Start-Sleep -Milliseconds 500
Get-CimInstance Win32_Process -Filter "Name='perception-svc.exe' OR Name='thinking-svc.exe' OR Name='memory-svc.exe' OR Name='action-svc.exe'"
```
Expected: the final command prints nothing (all 8 stopped, if all 8 happened to be running).

- [ ] **Step 10: Relaunch dev `perception-svc` in the foreground and verify it reads the new config**

```powershell
& "C:\Users\Lenovo\soulman-dev\run-perception-svc.ps1"
```
Run this with a background/timeout mechanism (it blocks — the service runs until killed), then check its output. Expected log line:
```
perception-svc started (NATS=nats://localhost:4222, HTTP=:9011, watching=[C:\Users\Lenovo\soulman-dev\test-errors], checkpoint=...)
```
Also verify the copy happened:
```powershell
Get-Content "C:\Users\Lenovo\soulman-dev\config.json"
```
Expected: matches `config/dev.json`'s contents exactly.

- [ ] **Step 11: Relaunch prod `perception-svc` and verify**

Same as Step 10, but run `C:\Users\Lenovo\soulman-prod\run-perception-svc.ps1`. Expected watching path: `C:\Users\Lenovo\DigitalMe\errors`. Verify `C:\Users\Lenovo\soulman-prod\config.json` matches `config/prod.json`.

- [ ] **Step 12: Verify the fail-fast path**

With dev `perception-svc` stopped, temporarily move its config file aside and confirm the service refuses to start:
```powershell
Rename-Item "C:\Users\Lenovo\soulman-dev\config.json" "config.json.bak"
& "C:\Users\Lenovo\soulman-dev\run-perception-svc.ps1"
```
Expected: process logs a fatal error mentioning `config` and exits immediately (does not stay running, does not print "perception-svc started").

Then restore normal operation by re-running the script (it re-copies `config/dev.json` fresh, so no manual cleanup of the `.bak` file is needed — but remove it anyway for tidiness):
```powershell
Remove-Item "C:\Users\Lenovo\soulman-dev\config.json.bak" -ErrorAction SilentlyContinue
& "C:\Users\Lenovo\soulman-dev\run-perception-svc.ps1"
```
Expected: starts normally again, same log line as Step 10.

- [ ] **Step 13: Relaunch the remaining 6 services (dev + prod × action-svc, thinking-svc, memory-svc) and confirm each still starts cleanly**

Run each of `run-action-svc.ps1`, `run-thinking-svc.ps1`, `run-memory-svc.ps1` in both `soulman-dev` and `soulman-prod`. Expected: each logs its normal startup line (e.g. `action-svc started (... notifier=discord)`, `thinking-svc started (...)`, `memory-svc started (...)`), unaffected by the added config-copy step — confirms the new `Copy-Item`/`$env:CONFIG_PATH` lines don't break scripts whose Go binaries don't read `CONFIG_PATH` at all yet.

- [ ] **Step 14: Leave all 8 services running normally**

After verification, ensure every service ends this task in its normal steady-state (not mid-test, not with a renamed/missing config file). All 8 should be running with their real `config/dev.json` / `config/prod.json` copied in.

(No commit for this task — see Global Constraints. Report DONE once all 14 steps are verified.)

---

### Task 5: Update `CLAUDE.md` to document the shared config file

**Files:**
- Modify: `CLAUDE.md`

**Interfaces:** None — documentation only.

- [ ] **Step 1: Add a `config/` row to the Repository Structure table**

In the `## Repository Structure` table, add a row (placed after the `docs/superpowers/plans/` row):

```
| `config/`                       | Per-environment JSON config files (`dev.json`, `prod.json`) — non-secret settings shared across services, copied to `<env-root>\config.json` by each `run-<svc>.ps1` |
```

- [ ] **Step 2: Update the `perception-svc` bullet under `## Services`**

Find this text (bullet 2 under `## Services`):

```
2. **`perception-svc`** — watches folders (default: `C:\Users\Lenovo\DigitalMe\errors`, overridable via `WATCH_PATHS`) via `fsnotify`, publishes new files as Stimulus events to `soulman.stimulus.raw`. `~/soulman-dev/run-perception-svc.ps1` overrides `WATCH_PATHS` to `~/soulman-dev/test-errors/` so manual/test file drops don't mix with real DigitalMe-generated error files — `soulman-prod` has no such override and watches the real folder.
```

Replace it with:

```
2. **`perception-svc`** — watches folders listed in `watch_paths` in its copy of the shared config file (`config.json`, copied from `config/dev.json` or `config/prod.json` in this vault by `run-perception-svc.ps1`, path given via `CONFIG_PATH`) via `fsnotify`, publishes new files as Stimulus events to `soulman.stimulus.raw`. Dev's `config/dev.json` points at `soulman-dev/test-errors/` so manual/test file drops don't mix with real DigitalMe-generated error files; prod's `config/prod.json` points at the real `C:\Users\Lenovo\DigitalMe\errors`. A missing config file, malformed JSON, or an empty `watch_paths` list is a fatal startup error.
```

- [ ] **Step 3: Add a "Shared config file" subsection**

Find the `### The \`common\` module` section (its paragraph ends with "...the compiler will catch every call site across all services that needs updating."). Immediately after that paragraph (and before the next `## System Architecture (Four Modules)` heading), insert:

```markdown
### Shared config file

Non-secret settings common to the services live in `config/dev.json` and `config/prod.json` at the vault root — git-tracked, unlike `.env`. Each `run-<svc>.ps1` script copies its environment's file to `<env-root>\config.json` fresh on every launch (the same "always current with the vault" pattern already used for the `go build` step) and points `CONFIG_PATH` at it. The schema is defined once in `common/sharedconfig`; today it holds only `watch_paths` (a list, even though each environment currently watches just one folder), consumed by `perception-svc`. The other three services' run scripts copy the file too, ready for whenever they have a non-secret setting worth moving out of an env var.
```

- [ ] **Step 4: Verify the edits render correctly**

```powershell
Get-Content "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.claude\worktrees\shared-config-design\CLAUDE.md" | Select-String "Shared config file|config/dev.json|watch_paths"
```
Expected: at least 3 matching lines, confirming all three edits landed.

- [ ] **Step 5: Commit**

```powershell
git -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.claude\worktrees\shared-config-design" add CLAUDE.md
git -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.claude\worktrees\shared-config-design" commit -m "docs: document the shared config file convention"
```

---

## Out of Scope (matches the spec)

- Any config field beyond `watch_paths`.
- Live-reload without a restart.
- Validating watched paths exist on disk at config-load time.
- Moving any other env-var-driven setting into the file.
