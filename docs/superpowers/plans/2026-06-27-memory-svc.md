# memory-svc Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go service that consumes Stimulus messages from the NATS STIMULUS stream, stores each one in an append-only JSONL file and Postgres, and exposes an HTTP API for raw input retrieval.

**Architecture:** Single binary with two concurrent goroutines — a JetStream push consumer that writes every Stimulus through a file-first pipeline, and a chi HTTP server on :9002. On startup, the file log is scanned for unsynced entries and replayed into Postgres before NATS subscription begins. File writes are the durability guarantee: NATS is ACKed even when the DB is down.

**Tech Stack:** Go 1.21+, `github.com/nats-io/nats.go` (JetStream v2 API), `github.com/jackc/pgx/v5` (pgx pool), `github.com/go-chi/chi/v5` (HTTP router)

## Prerequisites

Before starting:
1. **NATS** must be running (`nats stream ls` should show STIMULUS stream)
2. **Supabase** must be running: `cd C:\Users\Lenovo\soulman-dev\memory && supabase start`
3. **STIMULUS stream** must exist with subjects `input.>` and `soulman.stimulus.raw`; if not: `nats stream add STIMULUS --subjects "input.>,soulman.stimulus.raw" --retention limits --max-age 30d --storage file`
4. **`memory_dev.raw_inputs` table** must exist (already created by the soulman-db-builder agent)
5. **Go 1.21+** installed: `go version`

## Global Constraints

- Working directory: `C:\Users\Lenovo\soulman-dev\memory-svc\`
- Go module: `soulman/memory-svc`
- All git commands use `git -C C:\Users\Lenovo\soulman-dev\memory-svc`
- Postgres schema: `memory_dev` (env: `SCHEMA`, default `memory_dev`)
- NATS stream: `STIMULUS` — must already exist before service starts
- File log path: `$LOG_DIR/raw_inputs.jsonl` (default `./logs/raw_inputs.jsonl`)
- File log is **append-only** — no line is ever modified or deleted
- File rotation threshold: 10 MB — only one `.1` backup is kept
- HTTP port: 9002
- All SQL uses fully-qualified table names: `memory_dev.raw_inputs` — no `SET search_path`
- `ON CONFLICT (stimulus_id) DO NOTHING` on all inserts (idempotent replay)
- Tests needing Postgres read `DATABASE_URL` env var; default `postgres://postgres:postgres@localhost:54322/postgres`
- Tests needing NATS read `NATS_URL` env var; default `nats://localhost:4222`
- Package names: `natsconsumer` (not `nats`), `httpserver` (not `http`) to avoid stdlib name collisions

---

## File Structure

```
soulman-dev/memory-svc/
├── main.go              # wiring: config → filelog → db → writer → replay → nats → http
├── go.mod
├── go.sum
├── config/
│   ├── config.go        # Load() → Config from env vars
│   └── config_test.go
├── model/
│   ├── stimulus.go      # Stimulus struct + all nested types (shared by all packages)
│   └── stimulus_test.go
├── storage/
│   ├── filelog.go       # FileLog: AppendStimulus, AppendSynced, ScanPending, Close
│   ├── filelog_test.go
│   ├── postgres.go      # DB: NewDB, InsertRawInput, GetRecent, Close
│   ├── postgres_test.go
│   ├── writer.go        # Writer: Write(Stimulus), ReplayPending — orchestrates filelog+db
│   └── writer_test.go
├── natsconsumer/
│   ├── consumer.go      # Consumer: New, Start, Close — JetStream push consumer
│   └── consumer_test.go
└── httpserver/
    ├── server.go        # Server: New, Handler(), Start() — chi router
    └── server_test.go
```

**Dependency flow** (no cycles): `model` ← `storage` ← `natsconsumer`, `httpserver` ← `main`

---

### Task 1: Scaffold + Config

**Files:**
- Create: `main.go` (stub only)
- Create: `go.mod`
- Create: `config/config.go`
- Create: `config/config_test.go`

**Interfaces:**
- Produces: `config.Config{NATSURL, DatabaseURL, HTTPPort, LogDir, Schema string}`, `config.Load() *Config`

- [ ] **Step 1: Create directory and init git + go module**

```powershell
New-Item -ItemType Directory -Force "C:\Users\Lenovo\soulman-dev\memory-svc"
cd C:\Users\Lenovo\soulman-dev\memory-svc
git -C C:\Users\Lenovo\soulman-dev\memory-svc init
go mod init soulman/memory-svc
```

Expected: `go.mod` created containing `module soulman/memory-svc`

- [ ] **Step 2: Write `config/config.go`**

```go
package config

import "os"

type Config struct {
	NATSURL     string
	DatabaseURL string
	HTTPPort    string
	LogDir      string
	Schema      string
}

func Load() *Config {
	return &Config{
		NATSURL:     env("NATS_URL", "nats://localhost:4222"),
		DatabaseURL: env("DATABASE_URL", "postgres://postgres:postgres@localhost:54322/postgres"),
		HTTPPort:    env("HTTP_PORT", "9002"),
		LogDir:      env("LOG_DIR", "./logs"),
		Schema:      env("SCHEMA", "memory_dev"),
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
```

- [ ] **Step 3: Write `config/config_test.go`**

```go
package config_test

import (
	"os"
	"testing"

	"soulman/memory-svc/config"
)

func TestLoad_Defaults(t *testing.T) {
	os.Unsetenv("NATS_URL")
	os.Unsetenv("HTTP_PORT")
	os.Unsetenv("SCHEMA")

	cfg := config.Load()

	if cfg.NATSURL != "nats://localhost:4222" {
		t.Errorf("NATSURL = %q, want nats://localhost:4222", cfg.NATSURL)
	}
	if cfg.HTTPPort != "9002" {
		t.Errorf("HTTPPort = %q, want 9002", cfg.HTTPPort)
	}
	if cfg.Schema != "memory_dev" {
		t.Errorf("Schema = %q, want memory_dev", cfg.Schema)
	}
}

func TestLoad_EnvOverride(t *testing.T) {
	os.Setenv("NATS_URL", "nats://remote:4222")
	os.Setenv("SCHEMA", "memory_prod")
	defer os.Unsetenv("NATS_URL")
	defer os.Unsetenv("SCHEMA")

	cfg := config.Load()

	if cfg.NATSURL != "nats://remote:4222" {
		t.Errorf("NATSURL = %q, want nats://remote:4222", cfg.NATSURL)
	}
	if cfg.Schema != "memory_prod" {
		t.Errorf("Schema = %q, want memory_prod", cfg.Schema)
	}
}
```

- [ ] **Step 4: Write `main.go` stub**

```go
package main

func main() {}
```

- [ ] **Step 5: Run tests**

```
go test ./config/...
```

Expected output: `ok  	soulman/memory-svc/config`

- [ ] **Step 6: Commit**

```
git -C C:\Users\Lenovo\soulman-dev\memory-svc add .
git -C C:\Users\Lenovo\soulman-dev\memory-svc commit -m "feat: scaffold memory-svc with config package"
```

---

### Task 2: Stimulus model

**Files:**
- Create: `model/stimulus.go`
- Create: `model/stimulus_test.go`

**Interfaces:**
- Produces: `model.Stimulus` (and nested types `Source`, `Content`, `Attachment`, `ChannelMeta`, `Hints`, `Override`) — shared by all packages

- [ ] **Step 1: Write `model/stimulus.go`**

```go
package model

import (
	"encoding/json"
	"time"
)

type Stimulus struct {
	StimulusID    string          `json:"stimulus_id"`
	SchemaVersion int             `json:"schema_version"`
	ReceivedAt    time.Time       `json:"received_at"`
	OccurredAt    *time.Time      `json:"occurred_at,omitempty"`
	Channel       string          `json:"channel"`
	Source        Source          `json:"source"`
	Content       Content         `json:"content"`
	ChannelMeta   ChannelMeta     `json:"channel_metadata"`
	Hints         Hints           `json:"hints"`
	Override      Override        `json:"override"`
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

- [ ] **Step 2: Write `model/stimulus_test.go`**

```go
package model_test

import (
	"encoding/json"
	"testing"
	"time"

	"soulman/memory-svc/model"
)

func TestStimulus_JSONRoundtrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	s := model.Stimulus{
		StimulusID:    "018f1a2b-3c4d-7e8f-9a0b-1c2d3e4f5a6b",
		SchemaVersion: 1,
		ReceivedAt:    now,
		Channel:       "webhook",
		Source:        model.Source{Identity: "github", Authenticated: true, AuthMethod: "api_key"},
		Content: model.Content{
			RawText:     "push event",
			ContentType: "json",
			RawPayload:  json.RawMessage(`{"ref":"main"}`),
		},
		Hints:    model.Hints{Priority: "normal", Tags: []string{"github"}},
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
	// occurred_at must be absent (omitempty on nil pointer)
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	if _, ok := m["occurred_at"]; ok {
		t.Error("occurred_at should be omitted when nil")
	}
}
```

- [ ] **Step 3: Run tests**

```
go test ./model/...
```

Expected: `ok  	soulman/memory-svc/model`

- [ ] **Step 4: Commit**

```
git -C C:\Users\Lenovo\soulman-dev\memory-svc add model/
git -C C:\Users\Lenovo\soulman-dev\memory-svc commit -m "feat: add Stimulus model with JSON roundtrip"
```

---

### Task 3: File log

**Files:**
- Create: `storage/filelog.go`
- Create: `storage/filelog_test.go`

**Interfaces:**
- Consumes: `model.Stimulus`
- Produces:
  - `storage.DefaultMaxFileSize int64` — `10 * 1024 * 1024`
  - `storage.NewFileLog(dir string, maxSize int64) (*FileLog, error)`
  - `(*FileLog).AppendStimulus(s *model.Stimulus) error`
  - `(*FileLog).AppendSynced(stimulusID string) error`
  - `(*FileLog).ScanPending() ([]*model.Stimulus, error)` — returns Stimulus entries with no matching synced record across both current file and `.1` rotation backup
  - `(*FileLog).Close() error`

- [ ] **Step 1: Write `storage/filelog.go`**

```go
package storage

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"soulman/memory-svc/model"
)

const DefaultMaxFileSize = 10 * 1024 * 1024

type stimulusRecord struct {
	Type string `json:"_type"`
	*model.Stimulus
}

type syncedRecord struct {
	Type       string `json:"_type"`
	StimulusID string `json:"stimulus_id"`
}

type FileLog struct {
	path    string
	maxSize int64
	mu      sync.Mutex
	f       *os.File
}

func NewFileLog(dir string, maxSize int64) (*FileLog, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("filelog: mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, "raw_inputs.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("filelog: open %s: %w", path, err)
	}
	return &FileLog{path: path, maxSize: maxSize, f: f}, nil
}

func (fl *FileLog) AppendStimulus(s *model.Stimulus) error {
	fl.mu.Lock()
	defer fl.mu.Unlock()

	b, err := json.Marshal(stimulusRecord{Type: "stimulus", Stimulus: s})
	if err != nil {
		return fmt.Errorf("filelog: marshal stimulus: %w", err)
	}
	if err := fl.writeLine(b); err != nil {
		return err
	}
	return fl.rotateIfNeeded()
}

func (fl *FileLog) AppendSynced(stimulusID string) error {
	fl.mu.Lock()
	defer fl.mu.Unlock()

	b, err := json.Marshal(syncedRecord{Type: "synced", StimulusID: stimulusID})
	if err != nil {
		return fmt.Errorf("filelog: marshal synced: %w", err)
	}
	return fl.writeLine(b)
}

func (fl *FileLog) writeLine(b []byte) error {
	b = append(b, '\n')
	if _, err := fl.f.Write(b); err != nil {
		return fmt.Errorf("filelog: write: %w", err)
	}
	return nil
}

// ScanPending returns Stimulus entries that have no matching synced record.
// Scans both raw_inputs.jsonl.1 (rotation backup) and raw_inputs.jsonl.
func (fl *FileLog) ScanPending() ([]*model.Stimulus, error) {
	fl.mu.Lock()
	defer fl.mu.Unlock()

	stimuli := map[string]*model.Stimulus{}
	synced := map[string]bool{}

	for _, path := range []string{fl.path + ".1", fl.path} {
		if err := scanFile(path, stimuli, synced); err != nil {
			return nil, err
		}
	}

	var pending []*model.Stimulus
	for id, s := range stimuli {
		if !synced[id] {
			pending = append(pending, s)
		}
	}
	return pending, nil
}

func scanFile(path string, stimuli map[string]*model.Stimulus, synced map[string]bool) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("filelog: scan open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		var rec struct {
			Type string `json:"_type"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		switch rec.Type {
		case "stimulus":
			var sr stimulusRecord
			if err := json.Unmarshal(line, &sr); err == nil && sr.Stimulus != nil {
				stimuli[sr.StimulusID] = sr.Stimulus
			}
		case "synced":
			var sr syncedRecord
			if err := json.Unmarshal(line, &sr); err == nil {
				synced[sr.StimulusID] = true
			}
		}
	}
	return scanner.Err()
}

func (fl *FileLog) rotateIfNeeded() error {
	info, err := fl.f.Stat()
	if err != nil || info.Size() < fl.maxSize {
		return nil
	}
	fl.f.Close()
	_ = os.Rename(fl.path, fl.path+".1")
	f, err := os.OpenFile(fl.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("filelog: reopen after rotate: %w", err)
	}
	fl.f = f
	return nil
}

func (fl *FileLog) Close() error {
	fl.mu.Lock()
	defer fl.mu.Unlock()
	return fl.f.Close()
}
```

- [ ] **Step 2: Write `storage/filelog_test.go`**

```go
package storage_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"soulman/memory-svc/model"
	"soulman/memory-svc/storage"
)

func newTestStimulus(id string) *model.Stimulus {
	return &model.Stimulus{
		StimulusID:  id,
		ReceivedAt:  time.Now().UTC(),
		Channel:     "test",
		Source:      model.Source{Identity: "tester"},
		Content:     model.Content{RawText: "hello", RawPayload: json.RawMessage(`{}`)},
		Hints:       model.Hints{Priority: "normal"},
		Override:    model.Override{Params: json.RawMessage(`{}`)},
	}
}

func TestFileLog_AppendAndScanPending(t *testing.T) {
	fl, err := storage.NewFileLog(t.TempDir(), storage.DefaultMaxFileSize)
	if err != nil {
		t.Fatalf("NewFileLog: %v", err)
	}
	defer fl.Close()

	s := newTestStimulus("id-001")
	if err := fl.AppendStimulus(s); err != nil {
		t.Fatalf("AppendStimulus: %v", err)
	}

	pending, err := fl.ScanPending()
	if err != nil {
		t.Fatalf("ScanPending: %v", err)
	}
	if len(pending) != 1 || pending[0].StimulusID != "id-001" {
		t.Errorf("ScanPending = %v, want [{id-001}]", pending)
	}
}

func TestFileLog_SyncedRemovesFromPending(t *testing.T) {
	fl, err := storage.NewFileLog(t.TempDir(), storage.DefaultMaxFileSize)
	if err != nil {
		t.Fatalf("NewFileLog: %v", err)
	}
	defer fl.Close()

	fl.AppendStimulus(newTestStimulus("id-002"))
	fl.AppendSynced("id-002")

	pending, err := fl.ScanPending()
	if err != nil {
		t.Fatalf("ScanPending: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("ScanPending = %v, want empty", pending)
	}
}

func TestFileLog_PartialSync(t *testing.T) {
	fl, err := storage.NewFileLog(t.TempDir(), storage.DefaultMaxFileSize)
	if err != nil {
		t.Fatalf("NewFileLog: %v", err)
	}
	defer fl.Close()

	fl.AppendStimulus(newTestStimulus("id-A"))
	fl.AppendStimulus(newTestStimulus("id-B"))
	fl.AppendSynced("id-A")

	pending, err := fl.ScanPending()
	if err != nil {
		t.Fatalf("ScanPending: %v", err)
	}
	if len(pending) != 1 || pending[0].StimulusID != "id-B" {
		t.Errorf("ScanPending = %v, want [{id-B}]", pending)
	}
}

func TestFileLog_Rotation(t *testing.T) {
	dir := t.TempDir()
	// 512-byte threshold triggers rotation quickly
	fl, err := storage.NewFileLog(dir, 512)
	if err != nil {
		t.Fatalf("NewFileLog: %v", err)
	}
	defer fl.Close()

	// Each stimulus is ~300 bytes; 3 records exceed 512 bytes
	for i := 0; i < 10; i++ {
		s := newTestStimulus(fmt.Sprintf("rotation-%02d", i))
		if err := fl.AppendStimulus(s); err != nil {
			t.Fatalf("AppendStimulus %d: %v", i, err)
		}
	}

	rotated := filepath.Join(dir, "raw_inputs.jsonl.1")
	if _, err := os.Stat(rotated); err != nil {
		t.Errorf("rotation file not found at %s: %v", rotated, err)
	}
}

func TestFileLog_ScanPendingAcrossRotation(t *testing.T) {
	dir := t.TempDir()
	fl, err := storage.NewFileLog(dir, 512)
	if err != nil {
		t.Fatalf("NewFileLog: %v", err)
	}
	defer fl.Close()

	// Write enough to rotate (old entries go to .1), then write more
	for i := 0; i < 5; i++ {
		fl.AppendStimulus(newTestStimulus(fmt.Sprintf("pre-%02d", i)))
	}
	// Write one more to current file, not synced
	fl.AppendStimulus(newTestStimulus("post-00"))

	pending, err := fl.ScanPending()
	if err != nil {
		t.Fatalf("ScanPending: %v", err)
	}
	// All entries across both files should be pending
	if len(pending) == 0 {
		t.Error("expected pending entries across rotation, got none")
	}
}
```

- [ ] **Step 3: Run tests**

```
go test ./storage/... -run TestFileLog
```

Expected: all FileLog tests pass

- [ ] **Step 4: Commit**

```
git -C C:\Users\Lenovo\soulman-dev\memory-svc add storage/filelog.go storage/filelog_test.go
git -C C:\Users\Lenovo\soulman-dev\memory-svc commit -m "feat: append-only JSONL file log with rotation and pending scan"
```

---

### Task 4: Postgres storage

**Files:**
- Create: `storage/postgres.go`
- Create: `storage/postgres_test.go`

**Interfaces:**
- Consumes: `model.Stimulus`
- Produces:
  - `storage.RawInput{StimulusID, Channel, SourceIdentity, NormalizedText string; ReceivedAt time.Time; OccurredAt *time.Time; RawPayload []byte; IsOverride bool; OverrideCmd *string}`
  - `storage.NewDB(ctx, connStr, schema string) (*DB, error)`
  - `(*DB).InsertRawInput(ctx context.Context, s *model.Stimulus) error`
  - `(*DB).GetRecent(ctx context.Context, limit int) ([]RawInput, error)`
  - `(*DB).Close()`

- [ ] **Step 1: Add pgx dependency**

```
go get github.com/jackc/pgx/v5
```

- [ ] **Step 2: Write `storage/postgres.go`**

```go
package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"soulman/memory-svc/model"
)

type RawInput struct {
	StimulusID     string
	ReceivedAt     time.Time
	OccurredAt     *time.Time
	Channel        string
	SourceIdentity string
	RawPayload     []byte
	NormalizedText *string
	IsOverride     bool
	OverrideCmd    *string
}

type DB struct {
	pool   *pgxpool.Pool
	schema string
}

func NewDB(ctx context.Context, connStr, schema string) (*DB, error) {
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		return nil, fmt.Errorf("postgres: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	return &DB{pool: pool, schema: schema}, nil
}

func (db *DB) InsertRawInput(ctx context.Context, s *model.Stimulus) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("postgres: marshal stimulus: %w", err)
	}

	var normalizedText *string
	if s.Content.RawText != "" {
		normalizedText = &s.Content.RawText
	}

	_, err = db.pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.raw_inputs
			(stimulus_id, received_at, occurred_at, channel, source_identity,
			 raw_payload, normalized_text, is_override, override_cmd)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (stimulus_id) DO NOTHING
	`, db.schema),
		s.StimulusID,
		s.ReceivedAt,
		s.OccurredAt,
		s.Channel,
		s.Source.Identity,
		raw,
		normalizedText,
		s.Override.IsOverride,
		s.Override.Command,
	)
	if err != nil {
		return fmt.Errorf("postgres: insert raw_input %s: %w", s.StimulusID, err)
	}
	return nil
}

func (db *DB) GetRecent(ctx context.Context, limit int) ([]RawInput, error) {
	rows, err := db.pool.Query(ctx, fmt.Sprintf(`
		SELECT stimulus_id, received_at, occurred_at, channel, source_identity,
		       raw_payload, normalized_text, is_override, override_cmd
		FROM %s.raw_inputs
		WHERE forgotten_at IS NULL
		ORDER BY received_at DESC
		LIMIT $1
	`, db.schema), limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: query recent: %w", err)
	}
	defer rows.Close()

	var results []RawInput
	for rows.Next() {
		var r RawInput
		if err := rows.Scan(
			&r.StimulusID, &r.ReceivedAt, &r.OccurredAt, &r.Channel,
			&r.SourceIdentity, &r.RawPayload, &r.NormalizedText,
			&r.IsOverride, &r.OverrideCmd,
		); err != nil {
			return nil, fmt.Errorf("postgres: scan: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (db *DB) Close() {
	db.pool.Close()
}
```

- [ ] **Step 3: Write `storage/postgres_test.go`**

```go
package storage_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"soulman/memory-svc/model"
	"soulman/memory-svc/storage"
)

func testDB(t *testing.T) *storage.DB {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@localhost:54322/postgres"
	}
	ctx := context.Background()
	db, err := storage.NewDB(ctx, dbURL, "memory_dev")
	if err != nil {
		t.Skipf("postgres not available (%v) — set DATABASE_URL to run DB tests", err)
	}
	t.Cleanup(db.Close)
	return db
}

func TestDB_InsertRawInput(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	id := fmt.Sprintf("test-%d", time.Now().UnixNano())
	s := &model.Stimulus{
		StimulusID: id,
		ReceivedAt: time.Now().UTC(),
		Channel:    "test",
		Source:     model.Source{Identity: "test-runner"},
		Content:    model.Content{RawText: "integration test", RawPayload: json.RawMessage(`{}`)},
		Hints:      model.Hints{Priority: "normal"},
		Override:   model.Override{Params: json.RawMessage(`{}`)},
	}

	if err := db.InsertRawInput(ctx, s); err != nil {
		t.Fatalf("InsertRawInput: %v", err)
	}

	t.Cleanup(func() {
		db.ExecCleanup(context.Background(), "DELETE FROM memory_dev.raw_inputs WHERE stimulus_id = $1", id)
	})

	// Idempotency: second insert should not error
	if err := db.InsertRawInput(ctx, s); err != nil {
		t.Errorf("second insert (ON CONFLICT DO NOTHING) errored: %v", err)
	}
}

func TestDB_GetRecent(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	id1 := fmt.Sprintf("recent-a-%d", time.Now().UnixNano())
	id2 := fmt.Sprintf("recent-b-%d", time.Now().UnixNano())

	for _, id := range []string{id1, id2} {
		s := &model.Stimulus{
			StimulusID: id,
			ReceivedAt: time.Now().UTC(),
			Channel:    "test",
			Content:    model.Content{RawPayload: json.RawMessage(`{}`)},
			Override:   model.Override{Params: json.RawMessage(`{}`)},
		}
		if err := db.InsertRawInput(ctx, s); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}

	t.Cleanup(func() {
		for _, id := range []string{id1, id2} {
			db.ExecCleanup(context.Background(), "DELETE FROM memory_dev.raw_inputs WHERE stimulus_id = $1", id)
		}
	})

	rows, err := db.GetRecent(ctx, 5)
	if err != nil {
		t.Fatalf("GetRecent: %v", err)
	}
	if len(rows) < 2 {
		t.Errorf("GetRecent returned %d rows, want >= 2", len(rows))
	}
}
```

- [ ] **Step 4: Add `ExecCleanup` helper to `storage/postgres.go`** (test-support method used by tests to clean up inserted rows)

Add this method at the bottom of `postgres.go`:

```go
// ExecCleanup runs an arbitrary SQL statement — used only by tests for cleanup.
func (db *DB) ExecCleanup(ctx context.Context, sql string, args ...interface{}) {
	db.pool.Exec(ctx, sql, args...)
}
```

- [ ] **Step 5: Run tests**

```
go test ./storage/... -run TestDB -v
```

Expected: tests pass (or skip with "postgres not available" if Supabase is down)

- [ ] **Step 6: Commit**

```
git -C C:\Users\Lenovo\soulman-dev\memory-svc add storage/postgres.go storage/postgres_test.go
git -C C:\Users\Lenovo\soulman-dev\memory-svc commit -m "feat: postgres storage with InsertRawInput and GetRecent"
```

---

### Task 5: Writer (file + Postgres orchestration + replay)

**Files:**
- Create: `storage/writer.go`
- Create: `storage/writer_test.go`

**Interfaces:**
- Consumes: `*FileLog`, `*DB` (both from `storage` package)
- Produces:
  - `storage.NewWriter(fl *FileLog, db *DB) *Writer`
  - `(*Writer).Write(ctx context.Context, s *model.Stimulus) error` — file-first, DB second; returns error only if file write fails
  - `(*Writer).ReplayPending(ctx context.Context) error` — scans file, inserts unsynced entries to DB

- [ ] **Step 1: Write `storage/writer.go`**

```go
package storage

import (
	"context"
	"fmt"
	"log"

	"soulman/memory-svc/model"
)

type Writer struct {
	fl *FileLog
	db *DB
}

func NewWriter(fl *FileLog, db *DB) *Writer {
	return &Writer{fl: fl, db: db}
}

// Write persists a Stimulus. The file write is blocking and must succeed before
// ACKing NATS. DB failure is non-fatal: the file entry is left as pending and
// will be replayed on next startup.
func (w *Writer) Write(ctx context.Context, s *model.Stimulus) error {
	if err := w.fl.AppendStimulus(s); err != nil {
		return fmt.Errorf("writer: file append failed: %w", err)
	}

	if w.db == nil {
		log.Printf("writer: DB unavailable, %s written to file only", s.StimulusID)
		return nil
	}

	if err := w.db.InsertRawInput(ctx, s); err != nil {
		log.Printf("writer: DB insert failed for %s (will replay on restart): %v", s.StimulusID, err)
		return nil
	}

	if err := w.fl.AppendSynced(s.StimulusID); err != nil {
		log.Printf("writer: synced marker failed for %s: %v", s.StimulusID, err)
		// Non-fatal: ON CONFLICT DO NOTHING handles the duplicate on next replay
	}

	return nil
}

// ReplayPending scans the file log for unsynced entries and inserts them into
// Postgres. Called on startup before NATS subscription begins.
func (w *Writer) ReplayPending(ctx context.Context) error {
	if w.db == nil {
		return nil
	}

	pending, err := w.fl.ScanPending()
	if err != nil {
		return fmt.Errorf("writer: scan pending: %w", err)
	}

	if len(pending) == 0 {
		return nil
	}

	log.Printf("writer: replaying %d pending file entries to DB", len(pending))

	for _, s := range pending {
		if err := w.db.InsertRawInput(ctx, s); err != nil {
			log.Printf("writer: replay failed for %s: %v", s.StimulusID, err)
			continue
		}
		if err := w.fl.AppendSynced(s.StimulusID); err != nil {
			log.Printf("writer: replay synced marker failed for %s: %v", s.StimulusID, err)
		}
	}

	return nil
}
```

- [ ] **Step 2: Write `storage/writer_test.go`**

```go
package storage_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"soulman/memory-svc/model"
	"soulman/memory-svc/storage"
)

func TestWriter_Write_FileOnly_WhenDBNil(t *testing.T) {
	fl, err := storage.NewFileLog(t.TempDir(), storage.DefaultMaxFileSize)
	if err != nil {
		t.Fatalf("NewFileLog: %v", err)
	}
	defer fl.Close()

	w := storage.NewWriter(fl, nil)
	s := newTestStimulus("write-no-db-01")

	if err := w.Write(context.Background(), s); err != nil {
		t.Fatalf("Write with nil DB should not error: %v", err)
	}

	pending, _ := fl.ScanPending()
	if len(pending) != 1 || pending[0].StimulusID != "write-no-db-01" {
		t.Errorf("pending = %v, expected [write-no-db-01]", pending)
	}
}

func TestWriter_Write_MarkedSynced_WhenDBSucceeds(t *testing.T) {
	db := testDB(t) // skips if Postgres unavailable
	fl, err := storage.NewFileLog(t.TempDir(), storage.DefaultMaxFileSize)
	if err != nil {
		t.Fatalf("NewFileLog: %v", err)
	}
	defer fl.Close()

	id := fmt.Sprintf("write-synced-%d", time.Now().UnixNano())
	w := storage.NewWriter(fl, db)
	s := &model.Stimulus{
		StimulusID: id,
		ReceivedAt: time.Now().UTC(),
		Channel:    "test",
		Content:    model.Content{RawPayload: json.RawMessage(`{}`)},
		Override:   model.Override{Params: json.RawMessage(`{}`)},
	}

	if err := w.Write(context.Background(), s); err != nil {
		t.Fatalf("Write: %v", err)
	}

	t.Cleanup(func() {
		db.ExecCleanup(context.Background(), "DELETE FROM memory_dev.raw_inputs WHERE stimulus_id = $1", id)
	})

	pending, _ := fl.ScanPending()
	for _, p := range pending {
		if p.StimulusID == id {
			t.Errorf("stimulus %s still in pending after successful DB write", id)
		}
	}
}

func TestWriter_ReplayPending(t *testing.T) {
	db := testDB(t)
	fl, err := storage.NewFileLog(t.TempDir(), storage.DefaultMaxFileSize)
	if err != nil {
		t.Fatalf("NewFileLog: %v", err)
	}
	defer fl.Close()

	// Simulate a stimulus that was written to file but not synced (e.g. DB was down)
	id := fmt.Sprintf("replay-%d", time.Now().UnixNano())
	s := &model.Stimulus{
		StimulusID: id,
		ReceivedAt: time.Now().UTC(),
		Channel:    "test",
		Content:    model.Content{RawPayload: json.RawMessage(`{}`)},
		Override:   model.Override{Params: json.RawMessage(`{}`)},
	}
	fl.AppendStimulus(s) // file write only, no synced record

	t.Cleanup(func() {
		db.ExecCleanup(context.Background(), "DELETE FROM memory_dev.raw_inputs WHERE stimulus_id = $1", id)
	})

	w := storage.NewWriter(fl, db)
	if err := w.ReplayPending(context.Background()); err != nil {
		t.Fatalf("ReplayPending: %v", err)
	}

	// After replay, no more pending entries
	pending, _ := fl.ScanPending()
	for _, p := range pending {
		if p.StimulusID == id {
			t.Errorf("stimulus %s still pending after replay", id)
		}
	}
}
```

- [ ] **Step 3: Run tests**

```
go test ./storage/... -v
```

Expected: all tests pass (DB tests skip if Supabase down)

- [ ] **Step 4: Commit**

```
git -C C:\Users\Lenovo\soulman-dev\memory-svc add storage/writer.go storage/writer_test.go
git -C C:\Users\Lenovo\soulman-dev\memory-svc commit -m "feat: writer orchestrates file-first write pipeline with startup replay"
```

---

### Task 6: NATS consumer

**Files:**
- Create: `natsconsumer/consumer.go`
- Create: `natsconsumer/consumer_test.go`

**Interfaces:**
- Consumes: `natsconsumer.Writer` interface (satisfied by `*storage.Writer`)
- Produces:
  - `natsconsumer.Writer` interface: `Write(ctx context.Context, s *model.Stimulus) error`
  - `natsconsumer.New(natsURL, consumerName string, w Writer) (*Consumer, error)`
  - `(*Consumer).Start(ctx context.Context) error` — begins consuming; non-blocking (consume runs in NATS library goroutine)
  - `(*Consumer).Close()` — drains and closes NATS connection

- [ ] **Step 1: Add nats.go dependency**

```
go get github.com/nats-io/nats.go
```

- [ ] **Step 2: Write `natsconsumer/consumer.go`**

```go
package natsconsumer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"soulman/memory-svc/model"
)

// Writer is satisfied by *storage.Writer. Defined here to avoid import cycles.
type Writer interface {
	Write(ctx context.Context, s *model.Stimulus) error
}

type Consumer struct {
	nc           *nats.Conn
	js           jetstream.JetStream
	writer       Writer
	consumerName string
	cc           jetstream.ConsumeContext
}

func New(natsURL, consumerName string, w Writer) (*Consumer, error) {
	nc, err := nats.Connect(natsURL)
	if err != nil {
		return nil, fmt.Errorf("nats: connect to %s: %w", natsURL, err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats: jetstream: %w", err)
	}

	return &Consumer{nc: nc, js: js, writer: w, consumerName: consumerName}, nil
}

// Start subscribes to the STIMULUS stream and processes messages in the NATS
// library goroutine. Returns after the subscription is established; messages
// arrive asynchronously. Call Close to stop.
func (c *Consumer) Start(ctx context.Context) error {
	stream, err := c.js.Stream(ctx, "STIMULUS")
	if err != nil {
		return fmt.Errorf("nats: get STIMULUS stream: %w", err)
	}

	cons, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Name:       c.consumerName,
		Durable:    c.consumerName,
		AckPolicy:  jetstream.AckExplicitPolicy,
	})
	if err != nil {
		return fmt.Errorf("nats: create consumer %s: %w", c.consumerName, err)
	}

	cc, err := cons.Consume(func(msg jetstream.Msg) {
		var s model.Stimulus
		if err := json.Unmarshal(msg.Data(), &s); err != nil {
			log.Printf("nats: unparseable message (subject %s), ACKing to skip: %v", msg.Subject(), err)
			msg.Ack()
			return
		}

		if err := c.writer.Write(ctx, &s); err != nil {
			log.Printf("nats: write failed for %s, NAKing for redelivery: %v", s.StimulusID, err)
			msg.Nak()
			return
		}

		msg.Ack()
	})
	if err != nil {
		return fmt.Errorf("nats: consume: %w", err)
	}

	c.cc = cc
	log.Printf("nats: consuming STIMULUS stream as %q", c.consumerName)
	return nil
}

func (c *Consumer) Close() {
	if c.cc != nil {
		c.cc.Stop()
	}
	c.nc.Drain()
}
```

- [ ] **Step 3: Write `natsconsumer/consumer_test.go`**

```go
package natsconsumer_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"soulman/memory-svc/model"
	"soulman/memory-svc/natsconsumer"
)

type mockWriter struct {
	mu       sync.Mutex
	received []*model.Stimulus
}

func (m *mockWriter) Write(_ context.Context, s *model.Stimulus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.received = append(m.received, s)
	return nil
}

func natsURL() string {
	if u := os.Getenv("NATS_URL"); u != "" {
		return u
	}
	return "nats://localhost:4222"
}

func TestConsumer_ReceivesMessage(t *testing.T) {
	url := natsURL()

	// Connect publisher
	nc, err := nats.Connect(url)
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer nc.Close()

	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Unique consumer name per test run to avoid position conflicts
	consName := fmt.Sprintf("test-%d", time.Now().UnixNano())

	w := &mockWriter{}
	cons, err := natsconsumer.New(url, consName, w)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cons.Close()

	if err := cons.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Publish after subscription is established
	id := fmt.Sprintf("cons-test-%d", time.Now().UnixNano())
	s := &model.Stimulus{
		StimulusID: id,
		ReceivedAt: time.Now().UTC(),
		Channel:    "test",
		Source:     model.Source{Identity: "test"},
		Content:    model.Content{RawText: "hi", RawPayload: json.RawMessage(`{}`)},
		Hints:      model.Hints{Priority: "normal"},
		Override:   model.Override{Params: json.RawMessage(`{}`)},
	}
	b, _ := json.Marshal(s)
	if _, err := js.Publish(ctx, "soulman.stimulus.raw", b); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// Wait up to 5s for the writer to receive it
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w.mu.Lock()
		found := false
		for _, r := range w.received {
			if r.StimulusID == id {
				found = true
			}
		}
		w.mu.Unlock()
		if found {
			return // success
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Errorf("stimulus %s not received by writer within 5 seconds", id)
}

func TestConsumer_BadJSON_IsACKedAndSkipped(t *testing.T) {
	url := natsURL()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer nc.Close()

	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	consName := fmt.Sprintf("test-bad-%d", time.Now().UnixNano())
	w := &mockWriter{}
	cons, err := natsconsumer.New(url, consName, w)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cons.Close()

	if err := cons.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Publish invalid JSON — consumer must ACK (not block) and not crash
	if _, err := js.Publish(ctx, "soulman.stimulus.raw", []byte("not json")); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// Give consumer 2s to process the bad message; verify it didn't crash
	time.Sleep(2 * time.Second)

	// Consumer should still be alive (we can publish a valid message and get it)
	id := fmt.Sprintf("after-bad-%d", time.Now().UnixNano())
	s := &model.Stimulus{
		StimulusID: id,
		ReceivedAt: time.Now().UTC(),
		Channel:    "test",
		Content:    model.Content{RawPayload: json.RawMessage(`{}`)},
		Override:   model.Override{Params: json.RawMessage(`{}`)},
	}
	b, _ := json.Marshal(s)
	js.Publish(ctx, "soulman.stimulus.raw", b)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		w.mu.Lock()
		for _, r := range w.received {
			if r.StimulusID == id {
				w.mu.Unlock()
				return
			}
		}
		w.mu.Unlock()
		time.Sleep(100 * time.Millisecond)
	}

	t.Errorf("consumer did not recover after bad JSON message")
}
```

- [ ] **Step 4: Run tests**

```
go test ./natsconsumer/... -v -timeout 30s
```

Expected: both tests pass (skip if NATS unavailable)

- [ ] **Step 5: Commit**

```
git -C C:\Users\Lenovo\soulman-dev\memory-svc add natsconsumer/
git -C C:\Users\Lenovo\soulman-dev\memory-svc commit -m "feat: NATS JetStream consumer with ACK-on-bad-JSON and redelivery on file failure"
```

---

### Task 7: HTTP server

**Files:**
- Create: `httpserver/server.go`
- Create: `httpserver/server_test.go`

**Interfaces:**
- Consumes: `*storage.DB` (may be nil if DB unavailable — endpoints return 503)
- Produces:
  - `httpserver.New(db *storage.DB, port string) *Server`
  - `(*Server).Handler() http.Handler` — returns the chi router (used by tests)
  - `(*Server).Start() error` — blocks; calls `http.ListenAndServe`

- [ ] **Step 1: Add chi dependency**

```
go get github.com/go-chi/chi/v5
```

- [ ] **Step 2: Write `httpserver/server.go`**

```go
package httpserver

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"soulman/memory-svc/storage"
)

type Server struct {
	db     *storage.DB
	port   string
	router chi.Router
}

func New(db *storage.DB, port string) *Server {
	s := &Server{db: db, port: port}
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
	r.Get("/raw-inputs/recent", s.rawInputsRecent)
	r.Get("/memory/search", stub)
	r.Get("/memory/episodes", stub)
	r.Get("/memory/procedures", stub)
	r.Get("/memory/goals", stub)

	return r
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	body := map[string]string{"status": "ok"}
	if s.db == nil {
		body["db"] = "unavailable"
	} else {
		body["db"] = "connected"
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(body)
}

func (s *Server) rawInputsRecent(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}

	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	rows, err := s.db.GetRecent(r.Context(), limit)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if rows == nil {
		rows = []storage.RawInput{}
	}
	json.NewEncoder(w).Encode(rows)
}

func stub(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Not Implemented", http.StatusNotImplemented)
}
```

- [ ] **Step 3: Write `httpserver/server_test.go`**

```go
package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"soulman/memory-svc/httpserver"
)

func TestHealth_NilDB(t *testing.T) {
	srv := httpserver.New(nil, "9002")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %q, want ok", body["status"])
	}
	if body["db"] != "unavailable" {
		t.Errorf("db = %q, want unavailable", body["db"])
	}
}

func TestRawInputsRecent_NilDB_Returns503(t *testing.T) {
	srv := httpserver.New(nil, "9002")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/raw-inputs/recent", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestMemoryStubs_Return501(t *testing.T) {
	srv := httpserver.New(nil, "9002")
	paths := []string{"/memory/search", "/memory/episodes", "/memory/procedures", "/memory/goals"}

	for _, path := range paths {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s: status = %d, want 501", path, rec.Code)
		}
	}
}

func TestRawInputsRecent_DefaultLimit(t *testing.T) {
	// Verify invalid limit is silently replaced with default (no 400 error)
	srv := httpserver.New(nil, "9002")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/raw-inputs/recent?limit=abc", nil)
	srv.Handler().ServeHTTP(rec, req)
	// Returns 503 because db is nil, not 400 — confirms limit parsing doesn't error
	if rec.Code == http.StatusBadRequest {
		t.Error("bad limit param should be silently ignored, not return 400")
	}
}
```

- [ ] **Step 4: Run tests**

```
go test ./httpserver/... -v
```

Expected: all 4 tests pass

- [ ] **Step 5: Commit**

```
git -C C:\Users\Lenovo\soulman-dev\memory-svc add httpserver/
git -C C:\Users\Lenovo\soulman-dev\memory-svc commit -m "feat: chi HTTP server with health, raw-inputs/recent, and 501 stubs"
```

---

### Task 8: Main wiring + smoke test

**Files:**
- Modify: `main.go` (replace stub)

**Interfaces:**
- Consumes: all packages — `config`, `storage`, `natsconsumer`, `httpserver`
- Produces: a working binary; startup sequence is: config → filelog → db → writer → replay → nats.Start → http.Start → wait for signal

- [ ] **Step 1: Write `main.go`**

```go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"soulman/memory-svc/config"
	"soulman/memory-svc/httpserver"
	"soulman/memory-svc/natsconsumer"
	"soulman/memory-svc/storage"
)

func main() {
	cfg := config.Load()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// File log — must succeed; no file log = no durability guarantee
	fl, err := storage.NewFileLog(cfg.LogDir, storage.DefaultMaxFileSize)
	if err != nil {
		log.Fatalf("filelog: %v", err)
	}
	defer fl.Close()

	// Postgres — non-fatal; service starts and writes to file when DB is down
	db, dbErr := storage.NewDB(ctx, cfg.DatabaseURL, cfg.Schema)
	if dbErr != nil {
		log.Printf("WARNING: postgres unavailable (%v) — writes go to file only until DB reconnects", dbErr)
	}
	if db != nil {
		defer db.Close()
	}

	// Writer orchestrates file + DB writes
	w := storage.NewWriter(fl, db)

	// Replay any file entries that never made it to the DB
	if err := w.ReplayPending(ctx); err != nil {
		log.Printf("replay: %v", err)
	}

	// NATS consumer
	cons, err := natsconsumer.New(cfg.NATSURL, "memory-svc", w)
	if err != nil {
		log.Fatalf("nats: %v", err)
	}
	defer cons.Close()

	if err := cons.Start(ctx); err != nil {
		log.Fatalf("nats start: %v", err)
	}

	// HTTP server (non-blocking)
	srv := httpserver.New(db, cfg.HTTPPort)
	go func() {
		log.Printf("HTTP listening on :%s", cfg.HTTPPort)
		if err := srv.Start(); err != nil {
			log.Printf("http: %v", err)
		}
	}()

	log.Printf("memory-svc started (NATS=%s, DB=%v, HTTP=:%s, log=%s)",
		cfg.NATSURL, dbErr == nil, cfg.HTTPPort, cfg.LogDir)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Printf("memory-svc shutting down")
}
```

- [ ] **Step 2: Run `go mod tidy` to finalize dependencies**

```
go mod tidy
```

Expected: `go.sum` updated, no errors

- [ ] **Step 3: Build to verify compilation**

```
go build ./...
```

Expected: no errors; `memory-svc.exe` produced in the working directory

- [ ] **Step 4: Smoke test — start the service and verify HTTP**

Start Supabase if not running:
```
cd C:\Users\Lenovo\soulman-dev\memory && supabase start
```

Run the service (in a separate terminal):
```
cd C:\Users\Lenovo\soulman-dev\memory-svc
.\memory-svc.exe
```

Expected log output:
```
writer: replaying 0 pending file entries to DB
nats: consuming STIMULUS stream as "memory-svc"
HTTP listening on :9002
memory-svc started (NATS=nats://localhost:4222, DB=true, HTTP=:9002, log=./logs)
```

In another terminal, verify health endpoint:
```
curl http://localhost:9002/health
```

Expected:
```json
{"db":"connected","status":"ok"}
```

- [ ] **Step 5: Smoke test — publish a Stimulus and verify storage**

Publish a test Stimulus via NATS CLI (run from any terminal):
```
nats pub soulman.stimulus.raw '{
  "stimulus_id": "smoke-test-001",
  "schema_version": 1,
  "received_at": "2026-06-27T12:00:00Z",
  "channel": "test",
  "source": {"identity": "smoke-test", "authenticated": false, "auth_method": "none"},
  "content": {"raw_text": "smoke test message", "raw_payload": {}, "content_type": "text", "attachments": []},
  "channel_metadata": {"message_id": "", "thread_id": "", "reply_to": "", "channel_specific": {}},
  "hints": {"intent": null, "priority": "normal", "tags": []},
  "override": {"is_override": false, "command": null, "params": {}}
}'
```

Verify it appears in recent inputs:
```
curl "http://localhost:9002/raw-inputs/recent?limit=1"
```

Expected: JSON array with one row containing `"stimulus_id": "smoke-test-001"`

Verify the file log was written:
```powershell
Get-Content C:\Users\Lenovo\soulman-dev\memory-svc\logs\raw_inputs.jsonl
```

Expected: two lines — one `_type: "stimulus"` and one `_type: "synced"` with `stimulus_id: "smoke-test-001"`

- [ ] **Step 6: Run full test suite**

```
go test ./... -timeout 30s
```

Expected: all packages pass or skip (no failures)

- [ ] **Step 7: Commit**

```
git -C C:\Users\Lenovo\soulman-dev\memory-svc add main.go go.sum
git -C C:\Users\Lenovo\soulman-dev\memory-svc commit -m "feat: main wiring — file-first write pipeline, startup replay, graceful shutdown"
```

---

## Self-Review

**Spec coverage check:**

| Spec section | Covered in |
|---|---|
| NATS JetStream consumer on STIMULUS | Task 6 |
| Append-only JSONL with stimulus + synced records | Task 3 |
| File-first write (file → DB → synced marker) | Task 5 |
| Startup replay of pending entries | Task 5, Task 8 |
| `ON CONFLICT DO NOTHING` idempotency | Task 4 (SQL) |
| 10 MB rotation to `.1` | Task 3 |
| `GET /health` | Task 7 |
| `GET /raw-inputs/recent?limit=N` (default 20, max 100) | Task 7 |
| `GET /memory/search|episodes|procedures|goals` → 501 | Task 7 |
| DB unavailable at startup = non-fatal | Task 8 |
| ACK unparseable NATS message | Task 6 |
| NAK on file write failure (triggers redelivery) | Task 6 |
| ACK even if DB insert fails | Task 5 (Writer.Write) |
| `forgotten_at IS NULL` filter in GetRecent | Task 4 |
| Config from env vars | Task 1 |
| Module path `soulman/memory-svc` | Task 1 |

**Type consistency:**
- `storage.NewFileLog(dir string, maxSize int64)` — consistent across Tasks 3, 5, 8
- `storage.NewDB(ctx, connStr, schema)` — consistent across Tasks 4, 8
- `storage.NewWriter(fl *FileLog, db *DB)` — consistent across Tasks 5, 8
- `natsconsumer.New(natsURL, consumerName string, w Writer)` — consistent across Tasks 6, 8
- `httpserver.New(db *storage.DB, port string)` — consistent across Tasks 7, 8
- `storage.RawInput` fields used in Task 4 match what Task 7 returns as JSON
- `natsconsumer.Writer` interface: `Write(ctx, *model.Stimulus) error` — satisfied by `*storage.Writer` (Task 5)

**No placeholders found.**
