# perception-svc Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go service that watches a configurable list of local directories for newly-created files (the Folder Watcher channel adapter) and publishes each as a canonical `Stimulus` to the NATS message bus, so `memory-svc` (already subscribed) and, later, Thinking can consume it.

**Architecture:** Single binary with three concurrent pieces — an `fsnotify`-based directory watcher backed by a periodic reconciliation scan (catches missed events and backlog), a local JSON checkpoint file that prevents re-publishing already-seen files, and a JetStream publisher writing to `soulman.stimulus.raw`. A chi HTTP server on `:9001` exposes `/health`. There is no direct database dependency — durability comes from NATS JetStream plus the checkpoint file.

**Tech Stack:** Go 1.21+, `github.com/fsnotify/fsnotify` (directory watching), `github.com/nats-io/nats.go` (JetStream v2 API), `github.com/google/uuid` (UUID v7 stimulus IDs), `github.com/go-chi/chi/v5` (HTTP router)

## Prerequisites

Before starting:
1. **Go 1.21+** installed: `go version` (this environment has 1.26.0)
2. **NATS** is optional for this plan's own test suite — every test that needs a live NATS connection skips cleanly (`t.Skipf`) if `nats://localhost:4222` (or `$NATS_URL`) isn't reachable. If you want to exercise the full publish path, start NATS with the `STIMULUS` stream present (see `Messaging Bus.md` — `nats stream add STIMULUS --subjects "input.>,soulman.stimulus.raw" --retention limits --max-age 30d --storage file`). No new stream setup is required by this service; it publishes onto the stream `memory-svc` already consumes.
3. This is a git **worktree** inside the `brynjar-obsidian` vault repo (not a separate git repo) — do **not** `git init` inside `perception-svc/`. All git commands operate on the worktree root.

## Global Constraints

- Working directory for the service: `perception-svc/` at the worktree root
- Go module: `soulman/perception-svc`
- All git commands use `git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\perception-svc` (the worktree root) — never `cd`
- All Go commands use `go -C perception-svc ...` instead of `cd`-ing into the directory
- NATS subject: `soulman.stimulus.raw` — the existing `STIMULUS` JetStream stream already covers it; no new stream/subject setup
- HTTP port: `9001`
- Checkpoint file default: `./checkpoints.json` (shape: `{ "<folder_path>": { "<filename>": { "hash", "mtime", "published_at" } } }`)
- `hints.tags` is hardcoded to `["error", "folder-watcher"]`, `hints.priority` to `"high"` — this iteration only watches error folders (see spec)
- Attachment inline threshold: files < 1 MB and valid UTF-8 are inlined as `raw_text`; everything else becomes a single `attachments[]` entry with a local file path `uri`
- In-memory fsnotify event queue bounded to 100 pending events; overflow is dropped and left to the next reconciliation scan
- Tests needing a live NATS connection read `NATS_URL` env var; default `nats://localhost:4222`; must `t.Skipf` (not fail) when unreachable
- Package names: `watcher` (not `fsnotify`), `natspublish` (not `nats`), `httpserver` (not `http`) — avoids stdlib/library name collisions
- No cross-package import cycles: `model` ← `watcher`, `natspublish` ← `main`; `httpserver` ← `main`. `watcher` never imports `natspublish` — it depends on an unexported `Publisher` interface it declares itself, satisfied structurally by `*natspublish.Publisher`

---

## File Structure

```
perception-svc/
├── main.go                    # wiring: config → checkpoint → publisher → watcher → http
├── go.mod
├── go.sum
├── config/
│   ├── config.go               # Load() → Config from env vars
│   └── config_test.go
├── model/
│   ├── stimulus.go             # Stimulus struct — independent copy of memory-svc's model package
│   └── stimulus_test.go
├── watcher/
│   ├── checkpoint.go           # Checkpoint: LoadCheckpoint, IsNew, Mark
│   ├── checkpoint_test.go
│   ├── folderwatcher.go        # Watcher: fsnotify + reconciliation + Stimulus construction
│   └── folderwatcher_test.go
├── natspublish/
│   ├── publisher.go            # Publisher: New, Publish, Status, Close
│   └── publisher_test.go
└── httpserver/
    ├── server.go                # Server: New, Handler(), Start()
    └── server_test.go
```

---

### Task 1: Scaffold + Config

**Files:**
- Create: `perception-svc/main.go` (stub only)
- Create: `perception-svc/go.mod`
- Create: `perception-svc/config/config.go`
- Create: `perception-svc/config/config_test.go`

**Interfaces:**
- Produces: `config.Config{NATSURL, HTTPPort, CheckpointPath string; WatchPaths []string; ReconcileInterval int}`, `config.Load() *Config`

- [ ] **Step 1: Create directory and init the Go module**

```powershell
New-Item -ItemType Directory -Force "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\perception-svc\perception-svc\config"
go -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\perception-svc\perception-svc mod init soulman/perception-svc
```

Expected: `perception-svc/go.mod` created containing `module soulman/perception-svc`

- [ ] **Step 2: Write `perception-svc/config/config.go`**

```go
package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	NATSURL           string
	HTTPPort          string
	WatchPaths        []string
	CheckpointPath    string
	ReconcileInterval int // seconds
}

func Load() *Config {
	return &Config{
		NATSURL:           env("NATS_URL", "nats://localhost:4222"),
		HTTPPort:          env("HTTP_PORT", "9001"),
		WatchPaths:        splitPaths(env("WATCH_PATHS", `C:\Users\Lenovo\DigitalMe\errors`)),
		CheckpointPath:    env("CHECKPOINT_PATH", "./checkpoints.json"),
		ReconcileInterval: envInt("RECONCILE_INTERVAL_SECONDS", 30),
	}
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

// splitPaths turns a comma-separated env var into a trimmed, non-empty
// slice of paths.
func splitPaths(v string) []string {
	parts := strings.Split(v, ",")
	paths := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}
```

- [ ] **Step 3: Write `perception-svc/config/config_test.go`**

```go
package config_test

import (
	"os"
	"testing"

	"soulman/perception-svc/config"
)

func TestLoad_Defaults(t *testing.T) {
	os.Unsetenv("NATS_URL")
	os.Unsetenv("HTTP_PORT")
	os.Unsetenv("WATCH_PATHS")
	os.Unsetenv("CHECKPOINT_PATH")
	os.Unsetenv("RECONCILE_INTERVAL_SECONDS")

	cfg := config.Load()

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
}

func TestLoad_EnvOverride(t *testing.T) {
	os.Setenv("NATS_URL", "nats://remote:4222")
	os.Setenv("HTTP_PORT", "9999")
	os.Setenv("WATCH_PATHS", `C:\a\errors, C:\b\errors ,C:\c\errors`)
	os.Setenv("CHECKPOINT_PATH", "./data/checkpoints.json")
	os.Setenv("RECONCILE_INTERVAL_SECONDS", "45")
	defer func() {
		os.Unsetenv("NATS_URL")
		os.Unsetenv("HTTP_PORT")
		os.Unsetenv("WATCH_PATHS")
		os.Unsetenv("CHECKPOINT_PATH")
		os.Unsetenv("RECONCILE_INTERVAL_SECONDS")
	}()

	cfg := config.Load()

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
}

func TestLoad_InvalidReconcileInterval_FallsBackToDefault(t *testing.T) {
	os.Setenv("RECONCILE_INTERVAL_SECONDS", "not-a-number")
	defer os.Unsetenv("RECONCILE_INTERVAL_SECONDS")

	cfg := config.Load()
	if cfg.ReconcileInterval != 30 {
		t.Errorf("ReconcileInterval = %d, want default 30 for invalid input", cfg.ReconcileInterval)
	}
}
```

- [ ] **Step 4: Write `perception-svc/main.go` stub**

```go
package main

func main() {}
```

- [ ] **Step 5: Run tests**

```
go -C perception-svc test ./config/...
```

Expected output: `ok  	soulman/perception-svc/config`

- [ ] **Step 6: Commit**

```
git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\perception-svc add perception-svc/go.mod perception-svc/main.go perception-svc/config/
git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\perception-svc commit -m "feat(perception-svc): scaffold module with config package"
```

---

### Task 2: Stimulus model

**Files:**
- Create: `perception-svc/model/stimulus.go`
- Create: `perception-svc/model/stimulus_test.go`

**Interfaces:**
- Produces: `model.Stimulus` (and nested types `Source`, `Content`, `Attachment`, `ChannelMeta`, `Hints`, `Override`) — shared by `watcher` and `natspublish`. Field-for-field identical JSON shape to `memory-svc`'s `model.Stimulus`, kept as an independent copy per the spec (no shared library between services).

- [ ] **Step 1: Write `perception-svc/model/stimulus.go`**

```go
package model

import (
	"encoding/json"
	"time"
)

type Stimulus struct {
	StimulusID    string      `json:"stimulus_id"`
	SchemaVersion int         `json:"schema_version"`
	ReceivedAt    time.Time   `json:"received_at"`
	OccurredAt    *time.Time  `json:"occurred_at,omitempty"`
	Channel       string      `json:"channel"`
	Source        Source      `json:"source"`
	Content       Content     `json:"content"`
	ChannelMeta   ChannelMeta `json:"channel_metadata"`
	Hints         Hints       `json:"hints"`
	Override      Override    `json:"override"`
}

type Source struct {
	Identity      string `json:"identity"`
	Authenticated bool   `json:"authenticated"`
	AuthMethod    string `json:"auth_method"`
}

type Content struct {
	RawText     string          `json:"raw_text"`
	RawPayload  json.RawMessage `json:"raw_payload"`
	ContentType string          `json:"content_type"`
	Attachments []Attachment    `json:"attachments"`
}

type Attachment struct {
	Filename  string `json:"filename"`
	MIMEType  string `json:"mime_type"`
	SizeBytes int64  `json:"size_bytes"`
	URI       string `json:"uri"`
}

type ChannelMeta struct {
	MessageID       string          `json:"message_id"`
	ThreadID        string          `json:"thread_id"`
	ReplyTo         string          `json:"reply_to"`
	ChannelSpecific json.RawMessage `json:"channel_specific"`
}

type Hints struct {
	Intent   *string  `json:"intent"`
	Priority string   `json:"priority"`
	Tags     []string `json:"tags"`
}

type Override struct {
	IsOverride bool            `json:"is_override"`
	Command    *string         `json:"command"`
	Params     json.RawMessage `json:"params"`
}
```

- [ ] **Step 2: Write `perception-svc/model/stimulus_test.go`**

```go
package model_test

import (
	"encoding/json"
	"testing"
	"time"

	"soulman/perception-svc/model"
)

func TestStimulus_JSONRoundtrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	occurred := now.Add(-time.Minute)
	s := model.Stimulus{
		StimulusID:    "018f1a2b-3c4d-7e8f-9a0b-1c2d3e4f5a6b",
		SchemaVersion: 1,
		ReceivedAt:    now,
		OccurredAt:    &occurred,
		Channel:       "folder-watcher",
		Source:        model.Source{Identity: "folder-watcher", Authenticated: true, AuthMethod: "system"},
		Content: model.Content{
			RawText:     "boom, something broke",
			ContentType: "text",
			RawPayload:  json.RawMessage(`{}`),
			Attachments: []model.Attachment{},
		},
		ChannelMeta: model.ChannelMeta{
			MessageID:       "abc123",
			ChannelSpecific: json.RawMessage(`{"watched_path":"C:\\errors"}`),
		},
		Hints:    model.Hints{Priority: "high", Tags: []string{"error", "folder-watcher"}},
		Override: model.Override{IsOverride: false, Params: json.RawMessage(`{}`)},
	}

	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got model.Stimulus
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.StimulusID != s.StimulusID {
		t.Errorf("StimulusID = %q, want %q", got.StimulusID, s.StimulusID)
	}
	if got.Channel != s.Channel {
		t.Errorf("Channel = %q, want %q", got.Channel, s.Channel)
	}
	if got.Content.RawText != s.Content.RawText {
		t.Errorf("Content.RawText = %q, want %q", got.Content.RawText, s.Content.RawText)
	}
	if got.Source.Identity != s.Source.Identity {
		t.Errorf("Source.Identity = %q, want %q", got.Source.Identity, s.Source.Identity)
	}
	if !got.ReceivedAt.Equal(s.ReceivedAt) {
		t.Errorf("ReceivedAt = %v, want %v", got.ReceivedAt, s.ReceivedAt)
	}
	if got.OccurredAt == nil || !got.OccurredAt.Equal(*s.OccurredAt) {
		t.Errorf("OccurredAt = %v, want %v", got.OccurredAt, s.OccurredAt)
	}
	if len(got.Hints.Tags) != 2 || got.Hints.Tags[0] != "error" || got.Hints.Tags[1] != "folder-watcher" {
		t.Errorf("Hints.Tags = %v, want [error folder-watcher]", got.Hints.Tags)
	}
}

func TestStimulus_NilOccurredAt_OmittedFromJSON(t *testing.T) {
	s := model.Stimulus{
		StimulusID: "id-nil-occurred",
		ReceivedAt: time.Now().UTC(),
		Channel:    "test",
	}
	b, _ := json.Marshal(s)
	if string(b) == "" {
		t.Fatal("marshal returned empty")
	}
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	if _, ok := m["occurred_at"]; ok {
		t.Error("occurred_at should be omitted when nil")
	}
}
```

- [ ] **Step 3: Run tests**

```
go -C perception-svc test ./model/...
```

Expected: `ok  	soulman/perception-svc/model`

- [ ] **Step 4: Commit**

```
git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\perception-svc add perception-svc/model/
git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\perception-svc commit -m "feat(perception-svc): add Stimulus model with JSON roundtrip"
```

---

### Task 3: Checkpoint store

**Files:**
- Create: `perception-svc/watcher/checkpoint.go`
- Create: `perception-svc/watcher/checkpoint_test.go`

**Interfaces:**
- Produces:
  - `watcher.CheckpointEntry{Hash, Mtime, PublishedAt string}`
  - `watcher.LoadCheckpoint(path string) *Checkpoint` — never errors; logs and starts empty on missing/corrupt file
  - `(*Checkpoint).IsNew(folder, filename, hash string) bool`
  - `(*Checkpoint).Mark(folder, filename string, entry CheckpointEntry) error` — persists to disk

- [ ] **Step 1: Write `perception-svc/watcher/checkpoint.go`**

```go
package watcher

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
)

// CheckpointEntry records what we know about the last time a file was
// published: its content hash (for change detection), its mtime at
// publish time, and when we published it.
type CheckpointEntry struct {
	Hash        string `json:"hash"`
	Mtime       string `json:"mtime"`
	PublishedAt string `json:"published_at"`
}

// Checkpoint tracks which files have already been published, keyed by
// folder path then filename. Persisted as JSON to a local file after every
// successful Mark.
type Checkpoint struct {
	mu   sync.Mutex
	path string
	data map[string]map[string]CheckpointEntry
}

// LoadCheckpoint reads the checkpoint file at path. If the file doesn't
// exist, or is unreadable/corrupt, it logs and starts with an empty
// checkpoint — this may re-publish everything currently present once,
// which is an accepted tradeoff per the perception-svc design spec.
func LoadCheckpoint(path string) *Checkpoint {
	c := &Checkpoint{path: path, data: map[string]map[string]CheckpointEntry{}}

	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("checkpoint: read %s failed, starting empty: %v", path, err)
		}
		return c
	}

	var data map[string]map[string]CheckpointEntry
	if err := json.Unmarshal(b, &data); err != nil {
		log.Printf("checkpoint: parse %s failed, starting empty: %v", path, err)
		return c
	}

	c.data = data
	return c
}

// IsNew reports whether filename in folder is absent from the checkpoint,
// or present but with a different content hash (file replaced under the
// same name).
func (c *Checkpoint) IsNew(folder, filename, hash string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	folderEntries, ok := c.data[folder]
	if !ok {
		return true
	}
	entry, ok := folderEntries[filename]
	if !ok {
		return true
	}
	return entry.Hash != hash
}

// Mark records filename as published and persists the checkpoint to disk.
// Call only after a successful publish — a crash between publish and Mark
// results in a harmless duplicate stimulus on restart (accepted per spec).
func (c *Checkpoint) Mark(folder, filename string, entry CheckpointEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.data[folder] == nil {
		c.data[folder] = map[string]CheckpointEntry{}
	}
	c.data[folder][filename] = entry

	return c.saveLocked()
}

func (c *Checkpoint) saveLocked() error {
	b, err := json.MarshalIndent(c.data, "", "  ")
	if err != nil {
		return fmt.Errorf("checkpoint: marshal: %w", err)
	}
	if err := os.WriteFile(c.path, b, 0o644); err != nil {
		return fmt.Errorf("checkpoint: write %s: %w", c.path, err)
	}
	return nil
}
```

- [ ] **Step 2: Write `perception-svc/watcher/checkpoint_test.go`**

```go
package watcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCheckpoint_MissingFile_StartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoints.json")
	cp := LoadCheckpoint(path)

	if !cp.IsNew("C:\\errors", "any.txt", "sha256:whatever") {
		t.Error("IsNew should be true for a checkpoint with no entries")
	}
}

func TestLoadCheckpoint_CorruptFile_StartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoints.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cp := LoadCheckpoint(path)
	if !cp.IsNew("C:\\errors", "any.txt", "sha256:whatever") {
		t.Error("IsNew should be true after falling back to empty checkpoint on corrupt file")
	}
}

func TestCheckpoint_IsNew_UnseenFilename(t *testing.T) {
	cp := LoadCheckpoint(filepath.Join(t.TempDir(), "checkpoints.json"))
	if !cp.IsNew("folder", "file.txt", "sha256:abc") {
		t.Error("IsNew should be true for an unseen filename")
	}
}

func TestCheckpoint_Mark_ThenIsNew_SameHash_False(t *testing.T) {
	cp := LoadCheckpoint(filepath.Join(t.TempDir(), "checkpoints.json"))
	entry := CheckpointEntry{Hash: "sha256:abc", Mtime: "2026-07-17T00:00:00Z", PublishedAt: "2026-07-17T00:00:01Z"}

	if err := cp.Mark("folder", "file.txt", entry); err != nil {
		t.Fatalf("Mark: %v", err)
	}

	if cp.IsNew("folder", "file.txt", "sha256:abc") {
		t.Error("IsNew should be false for a filename already marked with the same hash")
	}
}

func TestCheckpoint_IsNew_ChangedHash_True(t *testing.T) {
	cp := LoadCheckpoint(filepath.Join(t.TempDir(), "checkpoints.json"))
	cp.Mark("folder", "file.txt", CheckpointEntry{Hash: "sha256:abc", Mtime: "t1", PublishedAt: "t2"})

	if !cp.IsNew("folder", "file.txt", "sha256:different") {
		t.Error("IsNew should be true when content hash has changed")
	}
}

func TestCheckpoint_Mark_PersistsToDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoints.json")
	cp := LoadCheckpoint(path)
	cp.Mark("folder", "file.txt", CheckpointEntry{Hash: "sha256:abc", Mtime: "t1", PublishedAt: "t2"})

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var data map[string]map[string]CheckpointEntry
	if err := json.Unmarshal(b, &data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	entry, ok := data["folder"]["file.txt"]
	if !ok {
		t.Fatal("persisted checkpoint missing folder/file.txt entry")
	}
	if entry.Hash != "sha256:abc" {
		t.Errorf("Hash = %q, want sha256:abc", entry.Hash)
	}
}

func TestLoadCheckpoint_ReloadsFromDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoints.json")
	cp1 := LoadCheckpoint(path)
	cp1.Mark("folder", "file.txt", CheckpointEntry{Hash: "sha256:abc", Mtime: "t1", PublishedAt: "t2"})

	cp2 := LoadCheckpoint(path)
	if cp2.IsNew("folder", "file.txt", "sha256:abc") {
		t.Error("reloaded checkpoint should retain previously marked entry")
	}
}
```

- [ ] **Step 3: Run tests**

```
go -C perception-svc test ./watcher/... -run TestCheckpoint -v
go -C perception-svc test ./watcher/... -run TestLoadCheckpoint -v
```

Expected: all checkpoint tests pass

- [ ] **Step 4: Commit**

```
git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\perception-svc add perception-svc/watcher/checkpoint.go perception-svc/watcher/checkpoint_test.go
git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\perception-svc commit -m "feat(perception-svc): JSON checkpoint store with hash-based change detection"
```

---

### Task 4: NATS publisher

**Files:**
- Create: `perception-svc/natspublish/publisher.go`
- Create: `perception-svc/natspublish/publisher_test.go`

**Interfaces:**
- Consumes: `model.Stimulus`
- Produces:
  - `natspublish.Subject string` — `"soulman.stimulus.raw"`
  - `natspublish.New(natsURL string) (*Publisher, error)` — does not block/error on an unreachable NATS at startup (uses `RetryOnFailedConnect` + infinite reconnect, matching the spec's "NATS unavailable at startup: log warning, HTTP server still starts")
  - `(*Publisher).Publish(ctx context.Context, s *model.Stimulus) error` — synchronous JetStream publish, waits for ack
  - `(*Publisher).Status() string` — `"connected"` or `"disconnected"`
  - `(*Publisher).Close()`

- [ ] **Step 1: Add nats.go dependency**

```
go -C perception-svc get github.com/nats-io/nats.go
```

- [ ] **Step 2: Write `perception-svc/natspublish/publisher.go`**

```go
package natspublish

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"soulman/perception-svc/model"
)

// Subject is the NATS subject Perception publishes normalized Stimuli to.
// The existing STIMULUS JetStream stream already covers it — no new stream
// or subject setup is needed.
const Subject = "soulman.stimulus.raw"

type Publisher struct {
	nc *nats.Conn
	js jetstream.JetStream
}

// New connects to NATS. RetryOnFailedConnect + infinite reconnects mean New
// does not block or return an error when NATS is unreachable at startup —
// the connection retries in the background while the rest of the service
// (HTTP server, fsnotify watcher) starts normally.
func New(natsURL string) (*Publisher, error) {
	nc, err := nats.Connect(natsURL,
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
	)
	if err != nil {
		return nil, fmt.Errorf("natspublish: connect to %s: %w", natsURL, err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("natspublish: jetstream: %w", err)
	}

	return &Publisher{nc: nc, js: js}, nil
}

// Publish synchronously publishes a Stimulus to Subject, waiting for the
// JetStream ack.
func (p *Publisher) Publish(ctx context.Context, s *model.Stimulus) error {
	b, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("natspublish: marshal stimulus %s: %w", s.StimulusID, err)
	}

	if _, err := p.js.Publish(ctx, Subject, b); err != nil {
		return fmt.Errorf("natspublish: publish %s: %w", s.StimulusID, err)
	}
	return nil
}

// Status reports the current connection status for the /health endpoint.
func (p *Publisher) Status() string {
	if p.nc.IsConnected() {
		return "connected"
	}
	return "disconnected"
}

func (p *Publisher) Close() {
	p.nc.Drain()
}
```

- [ ] **Step 3: Write `perception-svc/natspublish/publisher_test.go`**

```go
package natspublish_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"soulman/perception-svc/model"
	"soulman/perception-svc/natspublish"
)

func natsURL() string {
	if u := os.Getenv("NATS_URL"); u != "" {
		return u
	}
	return "nats://localhost:4222"
}

// TestNew_UnreachableNATS_DoesNotBlock does not require a live NATS — it
// verifies the spec's "HTTP server still starts" requirement: New() must
// return quickly (not hang, not error) even against an address nothing is
// listening on.
func TestNew_UnreachableNATS_DoesNotBlock(t *testing.T) {
	start := time.Now()
	pub, err := natspublish.New("nats://127.0.0.1:1")
	if err != nil {
		t.Fatalf("New should not error on unreachable NATS (retry is async): %v", err)
	}
	defer pub.Close()

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("New blocked for %v, want a fast return", elapsed)
	}
	if pub.Status() != "disconnected" {
		t.Errorf("Status() = %q, want disconnected", pub.Status())
	}
}

func TestPublisher_PublishAndStatus(t *testing.T) {
	url := natsURL()

	probe, err := nats.Connect(url)
	if err != nil {
		t.Skipf("NATS not available (%v) — set NATS_URL to run this test", err)
	}
	probe.Close()

	pub, err := natspublish.New(url)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer pub.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && pub.Status() != "connected" {
		time.Sleep(50 * time.Millisecond)
	}
	if pub.Status() != "connected" {
		t.Fatalf("Status() = %q, want connected", pub.Status())
	}

	s := &model.Stimulus{
		StimulusID: fmt.Sprintf("pub-test-%d", time.Now().UnixNano()),
		ReceivedAt: time.Now().UTC(),
		Channel:    "folder-watcher",
		Source:     model.Source{Identity: "folder-watcher", Authenticated: true, AuthMethod: "system"},
		Content:    model.Content{ContentType: "text", RawText: "smoke", RawPayload: json.RawMessage(`{}`)},
		Hints:      model.Hints{Priority: "high", Tags: []string{"error", "folder-watcher"}},
		Override:   model.Override{Params: json.RawMessage(`{}`)},
	}

	if err := pub.Publish(context.Background(), s); err != nil {
		t.Fatalf("Publish: %v", err)
	}
}
```

- [ ] **Step 4: Run tests**

```
go -C perception-svc test ./natspublish/... -v -timeout 30s
```

Expected: `TestNew_UnreachableNATS_DoesNotBlock` always passes; `TestPublisher_PublishAndStatus` passes if NATS is running, otherwise skips with "NATS not available"

- [ ] **Step 5: Commit**

```
git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\perception-svc add perception-svc/natspublish/ perception-svc/go.mod perception-svc/go.sum
git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\perception-svc commit -m "feat(perception-svc): JetStream publisher with non-blocking connect"
```

---

### Task 5: Folder Watcher

**Files:**
- Create: `perception-svc/watcher/folderwatcher.go`
- Create: `perception-svc/watcher/folderwatcher_test.go`

**Interfaces:**
- Consumes: `*Checkpoint` (Task 3), `model.Stimulus` (Task 2), an unexported `Publisher` interface satisfied by `*natspublish.Publisher` (Task 4)
- Produces:
  - `watcher.Publisher` interface: `Publish(ctx context.Context, s *model.Stimulus) error`
  - `watcher.New(paths []string, checkpoint *Checkpoint, publisher Publisher, reconcileInterval time.Duration) (*Watcher, error)`
  - `(*Watcher).Start(ctx context.Context)` — adds fsnotify watches (skipping missing directories, logged, retried by reconciliation), starts the fsnotify event loop and reconciliation ticker, and runs one immediate reconciliation pass so files present at startup are picked up without waiting a full interval
  - `(*Watcher).Close() error`

- [ ] **Step 1: Add fsnotify and uuid dependencies**

```
go -C perception-svc get github.com/fsnotify/fsnotify
go -C perception-svc get github.com/google/uuid
```

- [ ] **Step 2: Write `perception-svc/watcher/folderwatcher.go`**

```go
package watcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"mime"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/fsnotify/fsnotify"
	"github.com/google/uuid"

	"soulman/perception-svc/model"
)

const (
	// maxInlineBytes is the attachment inline threshold from the spec: files
	// smaller than this and valid UTF-8 are inlined as raw_text; everything
	// else becomes a single attachment entry.
	maxInlineBytes = 1 << 20 // 1 MB

	// maxQueuedEvents bounds the in-memory fsnotify event queue, mirroring
	// Perception module.md's max_buffer_size default. Overflow is dropped;
	// the next reconciliation scan catches it instead.
	maxQueuedEvents = 100
)

// Publisher is satisfied by *natspublish.Publisher. Declared here (not
// imported from natspublish) to avoid an import cycle — watcher has no
// dependency on natspublish.
type Publisher interface {
	Publish(ctx context.Context, s *model.Stimulus) error
}

// Watcher watches a set of directories (top-level only, not recursive) for
// newly created files and publishes each as a Stimulus, backed by a
// checkpoint file so already-seen files aren't re-published.
type Watcher struct {
	paths             []string
	checkpoint        *Checkpoint
	publisher         Publisher
	reconcileInterval time.Duration

	fsw    *fsnotify.Watcher
	events chan fsnotify.Event
}

func New(paths []string, checkpoint *Checkpoint, publisher Publisher, reconcileInterval time.Duration) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("watcher: create fsnotify watcher: %w", err)
	}

	return &Watcher{
		paths:             paths,
		checkpoint:        checkpoint,
		publisher:         publisher,
		reconcileInterval: reconcileInterval,
		fsw:               fsw,
		events:            make(chan fsnotify.Event, maxQueuedEvents),
	}, nil
}

// Start adds an fsnotify watch for each configured directory (logging and
// skipping any that don't exist yet — retried automatically by the next
// reconciliation scan), then launches the fsnotify event loop and the
// periodic reconciliation loop in background goroutines. It also runs one
// immediate reconciliation pass before returning, so files already present
// at startup are picked up without waiting a full interval.
func (w *Watcher) Start(ctx context.Context) {
	for _, p := range w.paths {
		if err := w.fsw.Add(p); err != nil {
			log.Printf("watcher: cannot watch %s (will retry via reconciliation): %v", p, err)
		}
	}

	go w.fsEventLoop(ctx)
	go w.processLoop(ctx)
	go w.reconcileLoop(ctx)

	w.reconcileAll(ctx)
}

// fsEventLoop drains fsnotify's Events/Errors channels and enqueues Create
// events onto the bounded internal queue, dropping (non-blocking) if full.
func (w *Watcher) fsEventLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			if ev.Op&fsnotify.Create == 0 {
				continue
			}
			select {
			case w.events <- ev:
			default:
				log.Printf("watcher: event queue full (%d), dropping create event for %s — reconciliation will catch it", maxQueuedEvents, ev.Name)
			}
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			log.Printf("watcher: fsnotify error: %v", err)
		}
	}
}

// processLoop handles queued Create events one at a time.
func (w *Watcher) processLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-w.events:
			w.handleFile(ctx, filepath.Dir(ev.Name), filepath.Base(ev.Name))
		}
	}
}

// reconcileLoop runs a reconciliation scan every reconcileInterval.
func (w *Watcher) reconcileLoop(ctx context.Context) {
	ticker := time.NewTicker(w.reconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.reconcileAll(ctx)
		}
	}
}

// reconcileAll lists each watched directory and diffs its files against the
// checkpoint, catching files created while the service was down and files
// fsnotify missed (a known OS-level gap on some network drives).
func (w *Watcher) reconcileAll(ctx context.Context) {
	for _, dir := range w.paths {
		entries, err := os.ReadDir(dir)
		if err != nil {
			log.Printf("watcher: reconcile: cannot list %s (will retry next scan): %v", dir, err)
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			w.handleFile(ctx, dir, e.Name())
		}
	}
}

// handleFile reads filename in dir and, if it's new or changed since the
// last checkpoint, builds and publishes a Stimulus, then marks the
// checkpoint on success.
func (w *Watcher) handleFile(ctx context.Context, dir, filename string) {
	fullPath := filepath.Join(dir, filename)

	info, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("watcher: %s deleted before it could be read, skipping", fullPath)
			return
		}
		log.Printf("watcher: stat %s failed: %v", fullPath, err)
		return
	}
	if info.IsDir() {
		return
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		log.Printf("watcher: read %s failed: %v", fullPath, err)
		return
	}

	hash := hashBytes(data)
	if !w.checkpoint.IsNew(dir, filename, hash) {
		return
	}

	mtime := info.ModTime().UTC()
	stimulus := buildStimulus(dir, filename, data, mtime)

	if err := w.publisher.Publish(ctx, stimulus); err != nil {
		log.Printf("watcher: publish failed for %s (checkpoint left unset, will retry): %v", fullPath, err)
		return
	}

	entry := CheckpointEntry{
		Hash:        hash,
		Mtime:       mtime.Format(time.RFC3339),
		PublishedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := w.checkpoint.Mark(dir, filename, entry); err != nil {
		log.Printf("watcher: checkpoint write failed for %s (may re-publish on restart): %v", fullPath, err)
	}
}

func (w *Watcher) Close() error {
	return w.fsw.Close()
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// buildStimulus constructs a Stimulus per the perception-svc design spec's
// Stimulus Construction field mapping table.
func buildStimulus(watchedPath, filename string, data []byte, mtime time.Time) *model.Stimulus {
	isText := len(data) < maxInlineBytes && utf8.Valid(data)

	content := model.Content{RawPayload: json.RawMessage(`{}`)}
	if isText {
		content.RawText = string(data)
		content.ContentType = "text"
		content.Attachments = []model.Attachment{}
	} else {
		content.RawText = ""
		content.ContentType = "binary"
		content.Attachments = []model.Attachment{{
			Filename:  filename,
			MIMEType:  mimeType(filename),
			SizeBytes: int64(len(data)),
			URI:       filepath.Join(watchedPath, filename),
		}}
	}

	mtimeStr := mtime.Format(time.RFC3339)
	occurredAt := mtime
	id, err := uuid.NewV7()
	if err != nil {
		// Extremely unlikely (crypto/rand failure); fall back to a random v4
		// rather than crash the watcher over a single file.
		id = uuid.New()
	}

	return &model.Stimulus{
		StimulusID:    id.String(),
		SchemaVersion: 1,
		ReceivedAt:    time.Now().UTC(),
		OccurredAt:    &occurredAt,
		Channel:       "folder-watcher",
		Source: model.Source{
			Identity:      "folder-watcher",
			Authenticated: true,
			AuthMethod:    "system",
		},
		Content: content,
		ChannelMeta: model.ChannelMeta{
			MessageID:       computeMessageID(watchedPath, filename, mtimeStr),
			ChannelSpecific: json.RawMessage(fmt.Sprintf(`{"watched_path":%s}`, jsonString(watchedPath))),
		},
		Hints: model.Hints{
			Priority: "high",
			Tags:     []string{"error", "folder-watcher"},
		},
		Override: model.Override{
			IsOverride: false,
			Params:     json.RawMessage(`{}`),
		},
	}
}

// computeMessageID gives downstream consumers a stable dedup key, per spec:
// sha256(watched_path + filename + mtime).
func computeMessageID(watchedPath, filename, mtimeRFC3339 string) string {
	sum := sha256.Sum256([]byte(watchedPath + filename + mtimeRFC3339))
	return hex.EncodeToString(sum[:])
}

func mimeType(filename string) string {
	if t := mime.TypeByExtension(filepath.Ext(filename)); t != "" {
		return t
	}
	return "application/octet-stream"
}

// jsonString safely encodes a Go string as a JSON string literal, used to
// build the channel_specific object without pulling in a struct just for
// one field.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
```

- [ ] **Step 3: Write `perception-svc/watcher/folderwatcher_test.go`**

```go
package watcher

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type mockPublisher struct {
	mu        sync.Mutex
	published []*model.Stimulus
	failNext  bool
}

func (m *mockPublisher) Publish(_ context.Context, s *model.Stimulus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failNext {
		m.failNext = false
		return errors.New("mock publish failure")
	}
	m.published = append(m.published, s)
	return nil
}

func (m *mockPublisher) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.published)
}

func (m *mockPublisher) last() *model.Stimulus {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.published) == 0 {
		return nil
	}
	return m.published[len(m.published)-1]
}

func TestHashBytes_DeterministicAndDistinct(t *testing.T) {
	h1 := hashBytes([]byte("hello"))
	h2 := hashBytes([]byte("hello"))
	h3 := hashBytes([]byte("world"))

	if h1 != h2 {
		t.Errorf("hashBytes not deterministic: %q != %q", h1, h2)
	}
	if h1 == h3 {
		t.Errorf("hashBytes collided for different content")
	}
	if !strings.HasPrefix(h1, "sha256:") {
		t.Errorf("hashBytes = %q, want sha256: prefix", h1)
	}
}

func TestComputeMessageID_Deterministic(t *testing.T) {
	id1 := computeMessageID("/watch/dir", "file.txt", "2026-07-17T00:00:00Z")
	id2 := computeMessageID("/watch/dir", "file.txt", "2026-07-17T00:00:00Z")
	id3 := computeMessageID("/watch/dir", "other.txt", "2026-07-17T00:00:00Z")

	if id1 != id2 {
		t.Errorf("computeMessageID not deterministic: %q != %q", id1, id2)
	}
	if id1 == id3 {
		t.Errorf("computeMessageID collided for different filename")
	}
}

func TestBuildStimulus_TextFile(t *testing.T) {
	mtime := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	s := buildStimulus(`C:\errors`, "log.txt", []byte("boom, something broke"), mtime)

	if s.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", s.SchemaVersion)
	}
	if s.Channel != "folder-watcher" {
		t.Errorf("Channel = %q, want folder-watcher", s.Channel)
	}
	if s.Source.Identity != "folder-watcher" || !s.Source.Authenticated || s.Source.AuthMethod != "system" {
		t.Errorf("Source = %+v, want {folder-watcher true system}", s.Source)
	}
	if s.Content.ContentType != "text" {
		t.Errorf("ContentType = %q, want text", s.Content.ContentType)
	}
	if s.Content.RawText != "boom, something broke" {
		t.Errorf("RawText = %q, want file contents", s.Content.RawText)
	}
	if len(s.Content.Attachments) != 0 {
		t.Errorf("Attachments = %v, want empty for inlined text", s.Content.Attachments)
	}
	if s.OccurredAt == nil || !s.OccurredAt.Equal(mtime) {
		t.Errorf("OccurredAt = %v, want %v", s.OccurredAt, mtime)
	}
	if s.Hints.Priority != "high" {
		t.Errorf("Priority = %q, want high", s.Hints.Priority)
	}
	if len(s.Hints.Tags) != 2 || s.Hints.Tags[0] != "error" || s.Hints.Tags[1] != "folder-watcher" {
		t.Errorf("Tags = %v, want [error folder-watcher]", s.Hints.Tags)
	}
	if s.Hints.Intent != nil {
		t.Errorf("Intent = %v, want nil", s.Hints.Intent)
	}
	if s.Override.IsOverride {
		t.Errorf("IsOverride = true, want false")
	}
	if s.ChannelMeta.MessageID != computeMessageID(`C:\errors`, "log.txt", mtime.Format(time.RFC3339)) {
		t.Errorf("MessageID mismatch")
	}

	var specific map[string]string
	if err := json.Unmarshal(s.ChannelMeta.ChannelSpecific, &specific); err != nil {
		t.Fatalf("ChannelSpecific unmarshal: %v", err)
	}
	if specific["watched_path"] != `C:\errors` {
		t.Errorf("watched_path = %q, want C:\\errors", specific["watched_path"])
	}
}

func TestBuildStimulus_BinaryFile(t *testing.T) {
	mtime := time.Now().UTC()
	data := []byte{0xff, 0xfe, 0x00, 0x01, 0x02} // invalid UTF-8
	s := buildStimulus(`C:\errors`, "dump.bin", data, mtime)

	if s.Content.ContentType != "binary" {
		t.Errorf("ContentType = %q, want binary", s.Content.ContentType)
	}
	if s.Content.RawText != "" {
		t.Errorf("RawText = %q, want empty for binary", s.Content.RawText)
	}
	if len(s.Content.Attachments) != 1 {
		t.Fatalf("Attachments = %v, want 1 entry", s.Content.Attachments)
	}
	att := s.Content.Attachments[0]
	if att.Filename != "dump.bin" {
		t.Errorf("Filename = %q, want dump.bin", att.Filename)
	}
	if att.SizeBytes != int64(len(data)) {
		t.Errorf("SizeBytes = %d, want %d", att.SizeBytes, len(data))
	}
	if att.URI != filepath.Join(`C:\errors`, "dump.bin") {
		t.Errorf("URI = %q, want local file path", att.URI)
	}
}

func TestBuildStimulus_LargeTextFile_TreatedAsBinary(t *testing.T) {
	data := make([]byte, maxInlineBytes) // exactly at the threshold: not < maxInlineBytes
	for i := range data {
		data[i] = 'a'
	}
	s := buildStimulus(`C:\errors`, "huge.txt", data, time.Now().UTC())

	if s.Content.ContentType != "binary" {
		t.Errorf("ContentType = %q, want binary for a file >= 1MB", s.Content.ContentType)
	}
}

func TestWatcher_ReconciliationOnStart_PublishesExistingFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("already here"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cp := LoadCheckpoint(filepath.Join(t.TempDir(), "checkpoints.json"))
	pub := &mockPublisher{}
	w, err := New([]string{dir}, cp, pub, time.Hour) // long interval — rely on the startup pass
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	if pub.count() != 1 {
		t.Fatalf("published count = %d, want 1 after startup reconciliation", pub.count())
	}
	if pub.last().Content.RawText != "already here" {
		t.Errorf("published RawText = %q, want file contents", pub.last().Content.RawText)
	}
	if cp.IsNew(dir, "existing.txt", hashBytes([]byte("already here"))) {
		t.Errorf("checkpoint not updated after successful publish")
	}
}

func TestWatcher_Reconcile_SkipsAlreadyCheckpointedFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "seen.txt"), []byte("content"), 0o644)

	cp := LoadCheckpoint(filepath.Join(t.TempDir(), "checkpoints.json"))
	pub := &mockPublisher{}
	w, _ := New([]string{dir}, cp, pub, time.Hour)
	defer w.Close()

	ctx := context.Background()
	w.reconcileAll(ctx) // first pass: publishes
	w.reconcileAll(ctx) // second pass: should be a no-op

	if pub.count() != 1 {
		t.Errorf("published count = %d after two reconcile passes, want 1 (dedup via checkpoint)", pub.count())
	}
}

func TestWatcher_Reconcile_PublishFailureLeavesCheckpointUnset(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "fails.txt"), []byte("content"), 0o644)

	cp := LoadCheckpoint(filepath.Join(t.TempDir(), "checkpoints.json"))
	pub := &mockPublisher{failNext: true}
	w, _ := New([]string{dir}, cp, pub, time.Hour)
	defer w.Close()

	w.reconcileAll(context.Background())

	if pub.count() != 0 {
		t.Errorf("published count = %d, want 0 (publish failed)", pub.count())
	}
	if !cp.IsNew(dir, "fails.txt", hashBytes([]byte("content"))) {
		t.Errorf("checkpoint marked despite publish failure — retry would be skipped")
	}
}

func TestWatcher_Reconcile_MissingDirectory_ContinuesWithOthers(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	valid := t.TempDir()
	os.WriteFile(filepath.Join(valid, "ok.txt"), []byte("fine"), 0o644)

	cp := LoadCheckpoint(filepath.Join(t.TempDir(), "checkpoints.json"))
	pub := &mockPublisher{}
	w, _ := New([]string{missing, valid}, cp, pub, time.Hour)
	defer w.Close()

	w.reconcileAll(context.Background())

	if pub.count() != 1 {
		t.Errorf("published count = %d, want 1 (missing dir skipped, valid dir processed)", pub.count())
	}
}

func TestWatcher_HandleFile_DeletedBeforeRead_NoPanic(t *testing.T) {
	dir := t.TempDir()
	cp := LoadCheckpoint(filepath.Join(t.TempDir(), "checkpoints.json"))
	pub := &mockPublisher{}
	w, _ := New([]string{dir}, cp, pub, time.Hour)
	defer w.Close()

	w.handleFile(context.Background(), dir, "never-existed.txt")

	if pub.count() != 0 {
		t.Errorf("published count = %d, want 0 for a file that doesn't exist", pub.count())
	}
}

func TestWatcher_Start_DetectsNewFileViaFsnotify(t *testing.T) {
	dir := t.TempDir()
	cp := LoadCheckpoint(filepath.Join(t.TempDir(), "checkpoints.json"))
	pub := &mockPublisher{}
	w, err := New([]string{dir}, cp, pub, time.Hour) // rely on fsnotify, not the reconcile ticker
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	if err := os.WriteFile(filepath.Join(dir, "live.txt"), []byte("fresh error"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if pub.count() >= 1 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if pub.count() < 1 {
		t.Fatalf("fsnotify did not deliver a create event for live.txt within 5s")
	}
}
```

Note: this test file uses `model.Stimulus` via the `mockPublisher` struct but does not import the `model` package explicitly in the listing above — add `"soulman/perception-svc/model"` to the import block alongside the other imports.

- [ ] **Step 4: Run tests**

```
go -C perception-svc test ./watcher/... -v -timeout 30s
```

Expected: all tests pass, including the live fsnotify test (no live NATS or Postgres needed — fsnotify is purely local)

- [ ] **Step 5: Commit**

```
git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\perception-svc add perception-svc/watcher/folderwatcher.go perception-svc/watcher/folderwatcher_test.go perception-svc/go.mod perception-svc/go.sum
git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\perception-svc commit -m "feat(perception-svc): fsnotify folder watcher with reconciliation and Stimulus construction"
```

---

### Task 6: HTTP server

**Files:**
- Create: `perception-svc/httpserver/server.go`
- Create: `perception-svc/httpserver/server_test.go`

**Interfaces:**
- Consumes: a `func() string` NATS-status callback (satisfied by `(*natspublish.Publisher).Status`), `[]string` watched paths
- Produces:
  - `httpserver.New(port string, watchedPaths []string, natsStatus func() string) *Server`
  - `(*Server).Handler() http.Handler`
  - `(*Server).Start() error` — blocks; calls `http.ListenAndServe`

- [ ] **Step 1: Add chi dependency**

```
go -C perception-svc get github.com/go-chi/chi/v5
```

- [ ] **Step 2: Write `perception-svc/httpserver/server.go`**

```go
package httpserver

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Server struct {
	port         string
	watchedPaths []string
	natsStatus   func() string
	router       chi.Router
}

func New(port string, watchedPaths []string, natsStatus func() string) *Server {
	s := &Server{port: port, watchedPaths: watchedPaths, natsStatus: natsStatus}
	s.router = s.buildRouter()
	return s
}

func (s *Server) Handler() http.Handler { return s.router }

func (s *Server) Start() error {
	return http.ListenAndServe(":"+s.port, s.router)
}

func (s *Server) buildRouter() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Get("/health", s.health)
	return r
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	status := "disconnected"
	if s.natsStatus != nil {
		status = s.natsStatus()
	}

	paths := s.watchedPaths
	if paths == nil {
		paths = []string{}
	}

	body := map[string]interface{}{
		"status":        "ok",
		"nats":          status,
		"watched_paths": paths,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(body)
}
```

- [ ] **Step 3: Write `perception-svc/httpserver/server_test.go`**

```go
package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"soulman/perception-svc/httpserver"
)

func TestHealth_ReportsStatusAndWatchedPaths(t *testing.T) {
	srv := httpserver.New("9001", []string{`C:\errors`, `C:\other`}, func() string { return "connected" })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body struct {
		Status       string   `json:"status"`
		NATS         string   `json:"nats"`
		WatchedPaths []string `json:"watched_paths"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
	if body.NATS != "connected" {
		t.Errorf("nats = %q, want connected", body.NATS)
	}
	if len(body.WatchedPaths) != 2 {
		t.Errorf("watched_paths = %v, want 2 entries", body.WatchedPaths)
	}
}

func TestHealth_NilStatusFunc_DefaultsToDisconnected(t *testing.T) {
	srv := httpserver.New("9001", nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.Handler().ServeHTTP(rec, req)

	var body map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&body)
	if body["nats"] != "disconnected" {
		t.Errorf("nats = %v, want disconnected", body["nats"])
	}
	paths, ok := body["watched_paths"].([]interface{})
	if !ok || len(paths) != 0 {
		t.Errorf("watched_paths = %v, want empty array", body["watched_paths"])
	}
}
```

- [ ] **Step 4: Run tests**

```
go -C perception-svc test ./httpserver/... -v
```

Expected: both tests pass

- [ ] **Step 5: Commit**

```
git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\perception-svc add perception-svc/httpserver/ perception-svc/go.mod perception-svc/go.sum
git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\perception-svc commit -m "feat(perception-svc): chi HTTP server with /health"
```

---

### Task 7: Main wiring + smoke test

**Files:**
- Modify: `perception-svc/main.go` (replace stub)

**Interfaces:**
- Consumes: all packages — `config`, `watcher`, `natspublish`, `httpserver`
- Produces: a working binary; startup sequence is: config → checkpoint → NATS publisher → folder watcher → HTTP server → wait for signal

- [ ] **Step 1: Write `perception-svc/main.go`**

```go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"soulman/perception-svc/config"
	"soulman/perception-svc/httpserver"
	"soulman/perception-svc/natspublish"
	"soulman/perception-svc/watcher"
)

func main() {
	cfg := config.Load()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cp := watcher.LoadCheckpoint(cfg.CheckpointPath)

	// NATS — non-fatal at startup for unreachable hosts; RetryOnFailedConnect
	// keeps trying in the background while the watcher and HTTP server start.
	pub, err := natspublish.New(cfg.NATSURL)
	if err != nil {
		log.Fatalf("natspublish: %v", err)
	}
	defer pub.Close()

	w, err := watcher.New(cfg.WatchPaths, cp, pub, time.Duration(cfg.ReconcileInterval)*time.Second)
	if err != nil {
		log.Fatalf("watcher: %v", err)
	}
	defer w.Close()

	w.Start(ctx)

	srv := httpserver.New(cfg.HTTPPort, cfg.WatchPaths, pub.Status)
	go func() {
		log.Printf("HTTP listening on :%s", cfg.HTTPPort)
		if err := srv.Start(); err != nil {
			log.Printf("http: %v", err)
		}
	}()

	log.Printf("perception-svc started (NATS=%s, HTTP=:%s, watching=%v, checkpoint=%s)",
		cfg.NATSURL, cfg.HTTPPort, cfg.WatchPaths, cfg.CheckpointPath)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Printf("perception-svc shutting down")
}
```

- [ ] **Step 2: Run `go mod tidy` to finalize dependencies**

```
go -C perception-svc mod tidy
```

Expected: `go.sum` updated, no errors

- [ ] **Step 3: Build to verify compilation**

```
go -C perception-svc build ./...
```

Expected: no errors; `perception-svc.exe` produced in `perception-svc/`

- [ ] **Step 4: Run full test suite**

```
go -C perception-svc test ./... -timeout 60s
```

Expected: all packages pass, or the two NATS-dependent tests in `natspublish` report "NATS not available" as a skip if NATS isn't running locally — no failures either way

- [ ] **Step 5: Smoke test — start the service and verify HTTP (manual, best-effort in this environment)**

Create a scratch watched folder and run the service:

```powershell
New-Item -ItemType Directory -Force "$env:TEMP\perception-svc-smoke"
$env:WATCH_PATHS = "$env:TEMP\perception-svc-smoke"
$env:CHECKPOINT_PATH = "$env:TEMP\perception-svc-smoke-checkpoints.json"
& "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\perception-svc\perception-svc\perception-svc.exe"
```

Expected log output (NATS connected or not, HTTP always starts):
```
HTTP listening on :9001
perception-svc started (NATS=nats://localhost:4222, HTTP=:9001, watching=[...perception-svc-smoke], checkpoint=...)
```

In another terminal, verify health endpoint:
```
curl http://localhost:9001/health
```

Expected: `{"nats":"connected"|"disconnected","status":"ok","watched_paths":["...perception-svc-smoke"]}`

Drop a file into the watched folder and confirm it gets picked up (checkpoint file grows, and if NATS is running, `nats sub soulman.stimulus.raw` shows the Stimulus):
```powershell
"test error" | Out-File "$env:TEMP\perception-svc-smoke\err1.txt"
Get-Content "$env:TEMP\perception-svc-smoke-checkpoints.json"
```

Stop the service with Ctrl+C when done.

- [ ] **Step 6: Commit**

```
git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\perception-svc add perception-svc/main.go perception-svc/go.mod perception-svc/go.sum
git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\perception-svc commit -m "feat(perception-svc): main wiring — checkpoint, publisher, watcher, HTTP, graceful shutdown"
```

---

## Self-Review

**Spec coverage check:**

| Spec section | Covered in |
|---|---|
| fsnotify watch per directory, top-level only, Create events only | Task 5 (`fsw.Add`, `ev.Op&fsnotify.Create`) |
| 30s reconciliation scan (configurable) | Task 5 (`reconcileLoop`), Task 1 (`RECONCILE_INTERVAL_SECONDS`) |
| Checkpoint file shape `{folder: {filename: {hash, mtime, published_at}}}` | Task 3 |
| File is "new" if absent or hash differs | Task 3 (`IsNew`) |
| Checkpoint written only after successful publish ack | Task 5 (`handleFile` — `Mark` only on `Publish` success) |
| Files never moved/renamed/deleted | Task 5 (`handleFile` only reads, never mutates the watched dir) |
| `stimulus_id` UUID v7 | Task 5 (`uuid.NewV7()`) |
| `schema_version` = 1 | Task 5 (`buildStimulus`) |
| `received_at` = now UTC, `occurred_at` = file mtime | Task 5 (`buildStimulus`) |
| `channel` = "folder-watcher", `source.*` fields | Task 5 (`buildStimulus`) |
| `content.raw_text`/`content_type`/`attachments` per <1MB UTF-8 rule | Task 5 (`buildStimulus`, `maxInlineBytes`) |
| `channel_metadata.message_id` = sha256(watched_path+filename+mtime) | Task 5 (`computeMessageID`) |
| `channel_metadata.channel_specific` = `{"watched_path": ...}` | Task 5 (`buildStimulus`) |
| `hints.tags` = `["error","folder-watcher"]`, `priority` = "high", `intent` = null | Task 5 (`buildStimulus`) |
| `override.is_override` = false | Task 5 (`buildStimulus`) |
| Project layout (`config/`, `model/`, `watcher/{checkpoint,folderwatcher}.go`, `natspublish/`, `httpserver/`) | Tasks 1–6 |
| `NATS_URL`, `HTTP_PORT`, `WATCH_PATHS`, `CHECKPOINT_PATH`, `RECONCILE_INTERVAL_SECONDS` env vars + defaults | Task 1 |
| Publish to `soulman.stimulus.raw`, synchronous JetStream publish | Task 4 |
| Publish error → log, no checkpoint write, reconciliation retries | Task 5 (`handleFile`) |
| `GET /health` → `{"status","nats","watched_paths"}` | Task 6 |
| Watched dir missing at startup → log, skip, retried by reconciliation | Task 5 (`Start`, `reconcileAll`) |
| NATS unavailable at startup → log warning, HTTP still starts | Task 4 (`RetryOnFailedConnect`), Task 7 (`main.go` doesn't block on NATS) |
| File deleted between event and read → log, skip | Task 5 (`handleFile`, `os.IsNotExist`) |
| Partial-write file re-checked next reconciliation tick | Task 5 (hash-based `IsNew` re-evaluates every scan) |
| Corrupt/unreadable `checkpoints.json` → log, start empty | Task 3 (`LoadCheckpoint`) |
| Bounded 100-event in-memory queue, drop + rely on reconciliation on overflow | Task 5 (`maxQueuedEvents`, `fsEventLoop`) |
| Module path `soulman/perception-svc` | Task 1 |

**Type consistency:**
- `watcher.New(paths []string, checkpoint *Checkpoint, publisher Publisher, reconcileInterval time.Duration) (*Watcher, error)` — consistent across Tasks 5, 7
- `watcher.Publisher` interface (`Publish(ctx, *model.Stimulus) error`) — satisfied structurally by `*natspublish.Publisher` (Task 4), used in Task 5 and wired in Task 7 without either package importing the other
- `natspublish.New(natsURL string) (*Publisher, error)` — consistent across Tasks 4, 7
- `httpserver.New(port string, watchedPaths []string, natsStatus func() string) *Server` — consistent across Tasks 6, 7; `pub.Status` (Task 4) matches the `func() string` shape
- `watcher.LoadCheckpoint(path string) *Checkpoint` — consistent across Tasks 3, 5, 7
- `watcher.CheckpointEntry{Hash, Mtime, PublishedAt string}` — consistent across Tasks 3, 5
- `model.Stimulus` and nested types — defined once in Task 2, used unchanged in Tasks 4, 5

**No placeholders found.**

**Assumption log (design ambiguities resolved during planning, not blocking):**
- The spec's project-layout diagram shows `soulman-dev/perception-svc/...`; per the task instructions this is illustrating the relative file tree only — the actual git location is `perception-svc/` at the vault worktree root, sibling to `memory-svc/`, matching how `memory-svc` is actually placed today.
- `natspublish.New` uses `nats.RetryOnFailedConnect(true)` + `nats.MaxReconnects(-1)` to satisfy "NATS unavailable at startup: log warning, HTTP server still starts" without inventing a bespoke retry loop — this is nats.go's built-in mechanism for exactly this case (verified against the installed `nats.go@v1.52.0` source and doc comment).
- `channel_metadata.channel_specific` is built as raw JSON (`jsonString` helper) rather than a typed struct, since it's explicitly described as free-form ("anything the adapter wants to pass through") in `Perception module.md`.
- Checkpoint reconciliation and fsnotify both funnel through the same `handleFile` path so "new or changed" logic (hash comparison) lives in exactly one place, per DRY.
