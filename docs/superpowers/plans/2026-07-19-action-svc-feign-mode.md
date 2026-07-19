# action-svc Feign Mode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a configurable feign mode to `action-svc` — when on, outbound side effects (starting with Discord sends) are recorded to a file and to the existing `episodes` pipeline instead of actually happening — and turn it on in both `dev` and `prod`.

**Architecture:** A new package `action-svc/feign` provides a reusable `Gate` (enabled/disabled + a `Record` method appending JSON lines to a log file) and a `WrapNotifier` helper that wraps any `notify.Notifier` so its `Send` is intercepted when the gate is enabled. `main.go` always wraps the real `DiscordNotifier` with this — both the daily-report cron and the gmail-triage batcher already share one `Notifier` instance, so wrapping it once covers both call sites with zero changes to `notifybatch`/`scheduler`'s send-calling code. `Dispatcher` and `Scheduler` additionally hold the same `*feign.Gate` so they can phrase their `OutcomeRecord`'s `Decision`/`Summary` text honestly (a feigned send and a real one both return `nil`, so the gate's `Enabled()` is the only way to tell them apart when composing that text).

**Tech Stack:** Go 1.25. No new external dependencies.

## Global Constraints

- Design spec: `docs/superpowers/specs/2026-07-19-action-svc-feign-mode-design.md` — read it first; this plan implements it exactly.
- `.env` stays reserved for secrets. `FeignMode` is a behavior toggle and belongs in the versioned per-environment JSON config (`config/dev.json` / `config/prod.json`), not an environment variable.
- `common/sharedconfig.Config.FeignMode` is a flat top-level field (`json:"feign_mode"`), not nested — it's a single flag, unlike the `gmail`/`system_monitor` clusters. Defaults to `false` when absent from JSON, matching every other optional field in that schema.
- Feign mode gates exactly one thing today: the `notify.Notifier`-level external send. `report.Append` (report-file writes) stays always-on and untouched.
- Both `config/dev.json` and `config/prod.json` get `"feign_mode": true` as part of this plan — this is a deliberate behavior change to running config, not a default flip.
- This plan makes no process-management changes (no service restarts) — that's explicitly out of scope per the design spec.

---

### Task 1: `feign_mode` in the shared config schema

**Files:**
- Modify: `common/sharedconfig/config.go`
- Modify: `common/sharedconfig/config_test.go`
- Modify: `config/dev.json`
- Modify: `config/prod.json`

**Interfaces:**
- Produces: `sharedconfig.Config.FeignMode bool` (json `feign_mode`, defaults to Go's zero value `false` when absent). Task 3 (`action-svc/config`) reads this field.

- [ ] **Step 1: Write the failing tests**

Append to `common/sharedconfig/config_test.go` (end of file):

```go
func TestLoad_FeignModeTrue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"watch_paths": [], "feign_mode": true}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := sharedconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.FeignMode {
		t.Error("FeignMode = false, want true")
	}
}

func TestLoad_MissingFeignMode_DefaultsFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"watch_paths": []}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := sharedconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.FeignMode {
		t.Error("FeignMode = true, want false when absent from JSON")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./sharedconfig/... -run TestLoad_FeignMode -v` from `common/`
Expected: FAIL — `cfg.FeignMode` undefined (compile error)

- [ ] **Step 3: Write the implementation**

In `common/sharedconfig/config.go`, replace the `Config` struct:

```go
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
}
```

In `config/dev.json`, add `"feign_mode": true,` right after `"memory_write_subject"` (before `"consumer_names"`):

```json
{
  "watch_paths": [
    "C:\\Users\\Lenovo\\soulman-dev\\test-errors"
  ],
  "nats_url": "nats://localhost:4222",
  "stimulus_subject": "soulman.dev.stimulus.raw",
  "thinking_request_subject": "soulman.dev.thinking.request",
  "memory_write_subject": "soulman.dev.memory.write",
  "feign_mode": true,
  "consumer_names": {
    "memory_svc": "memory-svc-dev",
    "memory_svc_episodes": "memory-svc-episodes-dev",
    "thinking_svc": "thinking-svc-dev",
    "action_svc": "action-svc-dev"
  },
  "gmail": {
    "query": "in:inbox is:unread -label:soulman/seen-dev after:2026/07/17",
    "seen_label": "soulman/seen-dev",
    "poll_interval_seconds": 60
  },
  "system_monitor": {
    "poll_interval_seconds": 300,
    "checks": [
      { "type": "disk_space", "path": "C:\\", "warning_threshold_percent": 80, "critical_threshold_percent": 95 },
      { "type": "memory", "warning_threshold_percent": 85 },
      { "type": "cpu", "warning_threshold_percent": 90 }
    ]
  }
}
```

In `config/prod.json`, the same addition:

```json
{
  "watch_paths": [
    "C:\\Users\\Lenovo\\DigitalMe\\errors"
  ],
  "nats_url": "nats://localhost:4222",
  "stimulus_subject": "soulman.stimulus.raw",
  "thinking_request_subject": "soulman.thinking.request",
  "memory_write_subject": "soulman.memory.write",
  "feign_mode": true,
  "consumer_names": {
    "memory_svc": "memory-svc",
    "memory_svc_episodes": "memory-svc-episodes",
    "thinking_svc": "thinking-svc",
    "action_svc": "action-svc"
  },
  "gmail": {
    "query": "in:inbox is:unread -label:soulman/seen after:2026/07/17",
    "seen_label": "soulman/seen",
    "poll_interval_seconds": 60
  },
  "system_monitor": {
    "poll_interval_seconds": 300,
    "checks": [
      { "type": "disk_space", "path": "C:\\", "warning_threshold_percent": 80, "critical_threshold_percent": 95 },
      { "type": "memory", "warning_threshold_percent": 85 },
      { "type": "cpu", "warning_threshold_percent": 90 }
    ]
  }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./sharedconfig/... -v` from `common/`
Expected: PASS (all tests, including pre-existing ones)

- [ ] **Step 5: Commit**

```bash
git add common/sharedconfig/config.go common/sharedconfig/config_test.go config/dev.json config/prod.json
git commit -m "feat(common): add feign_mode to shared config, enable in dev+prod"
```

---

### Task 2: `action-svc/feign` — the `Gate` and `WrapNotifier`

**Files:**
- Create: `action-svc/feign/gate.go`
- Create: `action-svc/feign/notifier.go`
- Create: `action-svc/feign/gate_test.go`

**Interfaces:**
- Consumes: `notify.Notifier` (`action-svc/notify`, existing — `Send(message string) error`).
- Produces: `feign.Gate` with `New(enabled bool, logPath string) *Gate`, `(*Gate) Enabled() bool` (nil-receiver-safe — returns `false` on a nil `*Gate`), `(*Gate) Record(kind, detail string) error`. `feign.WrapNotifier(gate *Gate, real notify.Notifier) notify.Notifier`. Tasks 4, 5, and 6 all depend on this exact API.

- [ ] **Step 1: Write the failing tests**

Create `action-svc/feign/gate_test.go`:

```go
package feign_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"soulman/action-svc/feign"
)

func TestGate_Enabled(t *testing.T) {
	if g := feign.New(true, "unused"); !g.Enabled() {
		t.Error("Enabled() = false, want true")
	}
	if g := feign.New(false, "unused"); g.Enabled() {
		t.Error("Enabled() = true, want false")
	}
}

func TestGate_Enabled_NilReceiverSafe(t *testing.T) {
	var g *feign.Gate
	if g.Enabled() {
		t.Error("Enabled() on a nil *Gate = true, want false")
	}
}

func TestGate_Record_AppendsJSONLine(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "nested", "feigned-actions.jsonl")
	g := feign.New(true, logPath)

	if err := g.Record("notify", "hello world"); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := g.Record("notify", "second message"); err != nil {
		t.Fatalf("Record: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), string(data))
	}

	var entry struct {
		Kind   string `json:"kind"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("unmarshal line 1: %v", err)
	}
	if entry.Kind != "notify" || entry.Detail != "hello world" {
		t.Errorf("line 1 = %+v, want kind=notify detail=%q", entry, "hello world")
	}
}

type fakeNotifier struct {
	sent []string
	err  error
}

func (f *fakeNotifier) Send(message string) error {
	f.sent = append(f.sent, message)
	return f.err
}

func TestWrapNotifier_Disabled_DelegatesToReal(t *testing.T) {
	real := &fakeNotifier{}
	gate := feign.New(false, filepath.Join(t.TempDir(), "feigned-actions.jsonl"))
	n := feign.WrapNotifier(gate, real)

	if err := n.Send("real message"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(real.sent) != 1 || real.sent[0] != "real message" {
		t.Errorf("real.sent = %v, want [\"real message\"]", real.sent)
	}
}

func TestWrapNotifier_Enabled_RecordsInsteadOfSending(t *testing.T) {
	real := &fakeNotifier{}
	logPath := filepath.Join(t.TempDir(), "feigned-actions.jsonl")
	gate := feign.New(true, logPath)
	n := feign.WrapNotifier(gate, real)

	if err := n.Send("would-be message"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(real.sent) != 0 {
		t.Errorf("real.sent = %v, want empty — real Send should never be called when the gate is enabled", real.sent)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "would-be message") {
		t.Errorf("log file = %q, want it to contain the feigned message", string(data))
	}
}

func TestWrapNotifier_Disabled_RealSendErrorPropagates(t *testing.T) {
	real := &fakeNotifier{err: errors.New("boom")}
	gate := feign.New(false, filepath.Join(t.TempDir(), "feigned-actions.jsonl"))
	n := feign.WrapNotifier(gate, real)

	if err := n.Send("x"); err == nil {
		t.Error("Send: want error from real notifier to propagate, got nil")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./feign/... -v` from `action-svc/`
Expected: FAIL — package `soulman/action-svc/feign` doesn't exist (compile error)

- [ ] **Step 3: Write the implementation**

Create `action-svc/feign/gate.go`:

```go
// Package feign implements action-svc's dry-run mechanism: a reusable Gate
// that lets any component with an outbound side effect (starting with
// notify.Notifier) record what it would have done instead of doing it. See
// docs/superpowers/specs/2026-07-19-action-svc-feign-mode-design.md.
package feign

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Gate decides whether an outbound side effect actually happens, or is
// only recorded. Any component with something to gate wraps itself around
// a shared *Gate (see WrapNotifier) — this is the one reusable mechanism
// new integrations adopt, rather than each inventing its own suppression
// flag.
type Gate struct {
	enabled bool
	logPath string
	mu      sync.Mutex
}

// New builds a Gate. logPath's parent directory is created on first
// Record call, not eagerly — a Gate that's never enabled never touches
// the filesystem.
func New(enabled bool, logPath string) *Gate {
	return &Gate{enabled: enabled, logPath: logPath}
}

// Enabled reports whether feign mode is on. Nil-receiver-safe (returns
// false) so callers that don't care about feign mode can pass a nil *Gate
// instead of constructing a disabled one. Components that need to phrase
// an outcome record differently depending on mode call this directly,
// since a feigned action and a real one both "succeed" from the caller's
// point of view and can't be told apart by return value alone.
func (g *Gate) Enabled() bool {
	return g != nil && g.enabled
}

type record struct {
	Timestamp time.Time `json:"timestamp"`
	Kind      string    `json:"kind"`
	Detail    string    `json:"detail"`
}

// Record appends one feigned-action entry to logPath as a single JSON
// line. Safe for concurrent use.
func (g *Gate) Record(kind, detail string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(g.logPath), 0o755); err != nil {
		return fmt.Errorf("feign: mkdir for %s: %w", g.logPath, err)
	}

	f, err := os.OpenFile(g.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("feign: open %s: %w", g.logPath, err)
	}
	defer f.Close()

	b, err := json.Marshal(record{Timestamp: time.Now(), Kind: kind, Detail: detail})
	if err != nil {
		return fmt.Errorf("feign: marshal record: %w", err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("feign: write to %s: %w", g.logPath, err)
	}
	return nil
}
```

Create `action-svc/feign/notifier.go`:

```go
package feign

import "soulman/action-svc/notify"

// gatedNotifier wraps a real notify.Notifier so its Send is only actually
// called when the Gate is disabled.
type gatedNotifier struct {
	gate *Gate
	real notify.Notifier
}

// WrapNotifier returns a notify.Notifier that delegates to real when gate
// is disabled, and records (instead of sending) when enabled. The wrapped
// Notifier is indistinguishable from a real one at the call site —
// notifybatch.Batcher and scheduler.Scheduler need no code changes to
// benefit from this.
func WrapNotifier(gate *Gate, real notify.Notifier) notify.Notifier {
	return &gatedNotifier{gate: gate, real: real}
}

func (n *gatedNotifier) Send(message string) error {
	if n.gate.Enabled() {
		return n.gate.Record("notify", message)
	}
	return n.real.Send(message)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./feign/... -v` from `action-svc/`
Expected: PASS (all tests)

- [ ] **Step 5: Commit**

```bash
git add action-svc/feign/gate.go action-svc/feign/notifier.go action-svc/feign/gate_test.go
git commit -m "feat(action-svc): add feign.Gate and WrapNotifier"
```

---

### Task 3: `action-svc/config` — load `FeignMode`

**Files:**
- Modify: `action-svc/config/config.go`
- Modify: `action-svc/config/config_test.go`

**Interfaces:**
- Consumes: `sharedconfig.Config.FeignMode` (Task 1).
- Produces: `config.Config.FeignMode bool`. Task 6 (`main.go`) reads this to construct the `*feign.Gate`.

- [ ] **Step 1: Write the failing tests**

Append to `action-svc/config/config_test.go` (end of file):

```go
func TestLoad_FeignModeTrue(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"nats_url": "nats://localhost:4222",
		"thinking_request_subject": "soulman.thinking.request",
		"memory_write_subject": "soulman.memory.write",
		"consumer_names": {"action_svc": "action-svc"},
		"feign_mode": true
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	os.Setenv("CONFIG_PATH", path)
	defer os.Unsetenv("CONFIG_PATH")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.FeignMode {
		t.Error("FeignMode = false, want true")
	}
}

func TestLoad_FeignModeAbsent_DefaultsFalse(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://localhost:4222", "soulman.thinking.request", "soulman.memory.write", "action-svc")
	os.Setenv("CONFIG_PATH", configPath)
	defer os.Unsetenv("CONFIG_PATH")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.FeignMode {
		t.Error("FeignMode = true, want false when absent from JSON")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./config/... -run TestLoad_FeignMode -v` from `action-svc/`
Expected: FAIL — `cfg.FeignMode` undefined (compile error)

- [ ] **Step 3: Write the implementation**

In `action-svc/config/config.go`, replace the `Config` struct and the `return &Config{...}` block in `Load()`:

```go
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
	ActionSvcConsumerName  string
	FeignMode              bool
}
```

```go
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
		ActionSvcConsumerName:  shared.ConsumerNames.ActionSvc,
		FeignMode:              shared.FeignMode,
	}, nil
```

(No new validation — `false` is a perfectly valid default for a bool, unlike the required non-empty strings already validated above it.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./config/... -v` from `action-svc/`
Expected: PASS (all tests)

- [ ] **Step 5: Commit**

```bash
git add action-svc/config/config.go action-svc/config/config_test.go
git commit -m "feat(action-svc): load feign_mode from shared config"
```

---

### Task 4: `action-svc/dispatch` — thread the gate through, phrase gmail-triage `Decision` honestly

**Files:**
- Modify: `action-svc/dispatch/dispatch.go`
- Modify: `action-svc/dispatch/gmail_triage.go`
- Modify: `action-svc/dispatch/dispatch_test.go`
- Modify: `action-svc/dispatch/gmail_triage_test.go`

**Interfaces:**
- Consumes: `*feign.Gate` (Task 2), specifically `(*feign.Gate).Enabled() bool` (nil-receiver-safe, so tests that don't care about feign mode may pass `nil`).
- Produces: `dispatch.New(root string, publisher Publisher, batcher Batcher, gate *feign.Gate) *Dispatcher` — signature gains a 4th parameter. Task 6 (`main.go`) calls this with the real gate.

- [ ] **Step 1: Write the failing tests**

Replace `action-svc/dispatch/dispatch_test.go` entirely (every `dispatch.New(...)` call gains a trailing `nil` for the new `gate` parameter — this file doesn't test feign behavior, that's covered in `gmail_triage_test.go`):

```go
package dispatch_test

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"soulman/action-svc/dispatch"
	"soulman/common"
)

type fakePublisher struct {
	mu      sync.Mutex
	records []common.OutcomeRecord
}

func (f *fakePublisher) PublishOutcome(rec common.OutcomeRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = append(f.records, rec)
	return nil
}

func (f *fakePublisher) last() (common.OutcomeRecord, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.records) == 0 {
		return common.OutcomeRecord{}, false
	}
	return f.records[len(f.records)-1], true
}

func TestHandle_UnknownActionType_DroppedWithoutPublish(t *testing.T) {
	pub := &fakePublisher{}
	d := dispatch.New(t.TempDir(), pub, nil, nil)

	req := common.ActionRequest{CorrelationID: "t1", ActionHint: "does_not_exist"}
	b, _ := json.Marshal(req)
	d.Handle(b)

	if _, ok := pub.last(); ok {
		t.Error("unknown action_hint should not publish an outcome record")
	}
}

func TestHandle_AppendSuccess_PublishesSuccessOutcome(t *testing.T) {
	orig := dispatch.AppendReportEntry
	dispatch.AppendReportEntry = func(root string, params json.RawMessage) (string, error) {
		return "fake/path.txt", nil
	}
	defer func() { dispatch.AppendReportEntry = orig }()

	pub := &fakePublisher{}
	d := dispatch.New(t.TempDir(), pub, nil, nil)

	req := common.ActionRequest{CorrelationID: "t2", ActionHint: "append_daily_report_entry", Parameters: json.RawMessage(`{}`)}
	b, _ := json.Marshal(req)
	d.Handle(b)

	rec, ok := pub.last()
	if !ok {
		t.Fatal("expected an outcome record to be published")
	}
	if rec.Status != "success" || rec.TaskID != "t2" || rec.ActionType != "append_daily_report_entry" {
		t.Errorf("outcome = %+v, want success/t2/append_daily_report_entry", rec)
	}
	if rec.Summary != "Daily report entry appended" || rec.Decision != "append_daily_report_entry" {
		t.Errorf("outcome = %+v, want summary=%q decision=%q", rec, "Daily report entry appended", "append_daily_report_entry")
	}
	if len(rec.Tags) != 1 || rec.Tags[0] != "report" {
		t.Errorf("Tags = %v, want [report]", rec.Tags)
	}
}

func TestHandle_AppendFailsTwice_RetriesOnceThenPublishesFailedOutcome(t *testing.T) {
	calls := 0
	orig := dispatch.AppendReportEntry
	dispatch.AppendReportEntry = func(root string, params json.RawMessage) (string, error) {
		calls++
		return "", errors.New("boom")
	}
	defer func() { dispatch.AppendReportEntry = orig }()

	pub := &fakePublisher{}
	d := dispatch.New(t.TempDir(), pub, nil, nil)

	req := common.ActionRequest{CorrelationID: "t3", ActionHint: "append_daily_report_entry", Parameters: json.RawMessage(`{}`)}
	b, _ := json.Marshal(req)
	d.Handle(b)

	if calls != 2 {
		t.Errorf("AppendReportEntry called %d times, want 2 (one retry)", calls)
	}
	rec, ok := pub.last()
	if !ok {
		t.Fatal("expected an outcome record to be published")
	}
	if rec.Status != "failed" {
		t.Errorf("status = %q, want failed", rec.Status)
	}
}

func TestHandle_BadJSON_DoesNotPanicOrPublish(t *testing.T) {
	pub := &fakePublisher{}
	d := dispatch.New(t.TempDir(), pub, nil, nil)
	d.Handle([]byte("not json"))
	if _, ok := pub.last(); ok {
		t.Error("bad JSON should not publish an outcome record")
	}
}

func TestAppendReportEntry_RealImplementation_WritesReportFile(t *testing.T) {
	root := t.TempDir()
	params, _ := json.Marshal(map[string]string{
		"summary":     "test error",
		"raw_content": "stack trace here",
		"source_path": `C:\errors\file.txt`,
		"occurred_at": "2026-07-17T14:32:00-06:00",
	})

	path, err := dispatch.AppendReportEntry(root, params)
	if err != nil {
		t.Fatalf("AppendReportEntry: %v", err)
	}
	if path == "" {
		t.Error("expected non-empty report path")
	}
}
```

Replace `action-svc/dispatch/gmail_triage_test.go` entirely (existing calls gain a trailing `nil`; one new test added at the end for the feign-mode `Decision` phrasing):

```go
package dispatch_test

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"soulman/action-svc/dispatch"
	"soulman/action-svc/feign"
	"soulman/action-svc/notifybatch"
	"soulman/common"
)

type fakeBatcher struct {
	mu    sync.Mutex
	items []notifybatch.Item
}

func (f *fakeBatcher) Add(item notifybatch.Item) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items = append(f.items, item)
}

func (f *fakeBatcher) added() []notifybatch.Item {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]notifybatch.Item(nil), f.items...)
}

func gmailTriageParamsJSON(t *testing.T, sender, subject, reason string, important bool) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"sender":       sender,
		"subject":      subject,
		"body_excerpt": "excerpt text",
		"reason":       reason,
		"important":    important,
		"thread_id":    "thread-1",
		"occurred_at":  "2026-07-18T09:00:00-06:00",
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return b
}

func TestDispatch_GmailTriage_Important_AddsToBatcher(t *testing.T) {
	pub := &fakePublisher{}
	batcher := &fakeBatcher{}
	d := dispatch.New(t.TempDir(), pub, batcher, nil)

	req := common.ActionRequest{
		CorrelationID: "g1",
		ActionHint:    "triage_gmail_email",
		Parameters:    gmailTriageParamsJSON(t, "boss@company.com", "Server down", "outage", true),
	}
	b, _ := json.Marshal(req)
	d.Handle(b)

	items := batcher.added()
	if len(items) != 1 {
		t.Fatalf("batcher.Add called %d times, want 1", len(items))
	}
	if items[0].Sender != "boss@company.com" || items[0].Subject != "Server down" {
		t.Errorf("batched item = %+v, want sender/subject to match", items[0])
	}

	rec, ok := pub.last()
	if !ok || rec.Status != "success" || rec.ActionType != "triage_gmail_email" {
		t.Errorf("outcome = %+v, ok=%v, want status=success actionType=triage_gmail_email", rec, ok)
	}
	if rec.Decision != "notified via Discord" {
		t.Errorf("Decision = %q, want %q", rec.Decision, "notified via Discord")
	}
	if rec.Summary != "Server down — important" {
		t.Errorf("Summary = %q, want %q", rec.Summary, "Server down — important")
	}
	if len(rec.Tags) != 2 || rec.Tags[0] != "gmail" || rec.Tags[1] != "triage" {
		t.Errorf("Tags = %v, want [gmail triage]", rec.Tags)
	}
}

func TestDispatch_GmailTriage_NotImportant_SkipsBatcher(t *testing.T) {
	pub := &fakePublisher{}
	batcher := &fakeBatcher{}
	d := dispatch.New(t.TempDir(), pub, batcher, nil)

	req := common.ActionRequest{
		CorrelationID: "g2",
		ActionHint:    "triage_gmail_email",
		Parameters:    gmailTriageParamsJSON(t, "newsletter@example.com", "Weekly digest", "routine", false),
	}
	b, _ := json.Marshal(req)
	d.Handle(b)

	if items := batcher.added(); len(items) != 0 {
		t.Errorf("batcher.Add called %d times, want 0 for a not-important email", len(items))
	}

	rec, ok := pub.last()
	if !ok || rec.Decision != "logged only" {
		t.Errorf("outcome = %+v, ok=%v, want Decision=%q", rec, ok, "logged only")
	}
}

func TestDispatch_GmailTriage_AlwaysWritesReportEntry_RegardlessOfImportance(t *testing.T) {
	orig := dispatch.AppendGmailReportEntry
	calls := 0
	dispatch.AppendGmailReportEntry = func(root string, params json.RawMessage) (string, error) {
		calls++
		return "fake/path.txt", nil
	}
	defer func() { dispatch.AppendGmailReportEntry = orig }()

	pub := &fakePublisher{}
	batcher := &fakeBatcher{}
	d := dispatch.New(t.TempDir(), pub, batcher, nil)

	req := common.ActionRequest{
		CorrelationID: "g3",
		ActionHint:    "triage_gmail_email",
		Parameters:    gmailTriageParamsJSON(t, "newsletter@example.com", "Weekly digest", "routine", false),
	}
	b, _ := json.Marshal(req)
	d.Handle(b)

	if calls != 1 {
		t.Errorf("AppendGmailReportEntry called %d times for a not-important email, want 1", calls)
	}
}

func TestDispatch_GmailTriage_ReportAppendFailsTwice_RetriesOnceThenPublishesFailedOutcome(t *testing.T) {
	orig := dispatch.AppendGmailReportEntry
	calls := 0
	dispatch.AppendGmailReportEntry = func(root string, params json.RawMessage) (string, error) {
		calls++
		return "", errors.New("boom")
	}
	defer func() { dispatch.AppendGmailReportEntry = orig }()

	pub := &fakePublisher{}
	d := dispatch.New(t.TempDir(), pub, &fakeBatcher{}, nil)

	req := common.ActionRequest{
		CorrelationID: "g4",
		ActionHint:    "triage_gmail_email",
		Parameters:    gmailTriageParamsJSON(t, "a@b.com", "s", "r", false),
	}
	b, _ := json.Marshal(req)
	d.Handle(b)

	if calls != 2 {
		t.Errorf("AppendGmailReportEntry called %d times, want 2 (one retry)", calls)
	}
	rec, ok := pub.last()
	if !ok || rec.Status != "failed" {
		t.Errorf("outcome = %+v, ok=%v, want status=failed", rec, ok)
	}
}

func TestAppendGmailReportEntry_RealImplementation_WritesReportFile(t *testing.T) {
	root := t.TempDir()
	params := gmailTriageParamsJSON(t, "a@b.com", "subject", "reason text", true)

	path, err := dispatch.AppendGmailReportEntry(root, params)
	if err != nil {
		t.Fatalf("AppendGmailReportEntry: %v", err)
	}
	if path == "" {
		t.Error("expected non-empty report path")
	}
}

func TestDispatch_GmailTriage_FeignMode_Important_DecisionSaysFeigned(t *testing.T) {
	pub := &fakePublisher{}
	batcher := &fakeBatcher{}
	gate := feign.New(true, filepath.Join(t.TempDir(), "feigned-actions.jsonl"))
	d := dispatch.New(t.TempDir(), pub, batcher, gate)

	req := common.ActionRequest{
		CorrelationID: "g5",
		ActionHint:    "triage_gmail_email",
		Parameters:    gmailTriageParamsJSON(t, "boss@company.com", "Server down", "outage", true),
	}
	b, _ := json.Marshal(req)
	d.Handle(b)

	// Deciding to notify (queuing on the batcher) is unaffected by feign
	// mode — only the eventual real Notifier.Send (inside the batcher's
	// flush, via whatever Notifier main.go wired in) is what actually gets
	// intercepted, tested separately in the feign package.
	if items := batcher.added(); len(items) != 1 {
		t.Errorf("batcher.Add called %d times, want 1 (feign mode doesn't change the decision to notify)", len(items))
	}

	rec, ok := pub.last()
	if !ok || rec.Decision != "feigned notify via Discord" {
		t.Errorf("outcome = %+v, ok=%v, want Decision=%q", rec, ok, "feigned notify via Discord")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./dispatch/... -v` from `action-svc/`
Expected: FAIL — compile error, `dispatch.New` still takes 3 args, and `dispatch.gate`/`feign` package don't exist as used

- [ ] **Step 3: Write the implementation**

Replace `action-svc/dispatch/dispatch.go` entirely:

```go
package dispatch

import (
	"encoding/json"
	"log"
	"time"

	"soulman/action-svc/feign"
	"soulman/action-svc/notifybatch"
	"soulman/common"
)

// Publisher is satisfied by *natsclient.Publisher. Defined here (not in
// natsclient) so this package doesn't need to import natsclient.
type Publisher interface {
	PublishOutcome(rec common.OutcomeRecord) error
}

// Batcher is satisfied by *notifybatch.Batcher. Defined here (not in
// notifybatch) so tests can inject a fake that records Add calls without
// waiting on real timers — flush timing itself is already covered by
// notifybatch's own tests.
type Batcher interface {
	Add(item notifybatch.Item)
}

type Dispatcher struct {
	root      string
	publisher Publisher
	batcher   Batcher
	gate      *feign.Gate
}

func New(root string, publisher Publisher, batcher Batcher, gate *feign.Gate) *Dispatcher {
	return &Dispatcher{root: root, publisher: publisher, batcher: batcher, gate: gate}
}

// Handle is the NATS message handler for soulman.thinking.request. It never
// returns an error — all failures are logged and/or published as outcome
// records, per the "a missed report entry isn't worth interrupting the
// human" decision in the error-report-action spec.
func (d *Dispatcher) Handle(msg []byte) {
	var req common.ActionRequest
	if err := json.Unmarshal(msg, &req); err != nil {
		log.Printf("dispatch: unparseable request, dropping: %v", err)
		return
	}

	switch req.ActionHint {
	case "append_daily_report_entry":
		d.dispatchAppendDailyReportEntry(req)
	case "triage_gmail_email":
		d.dispatchGmailTriage(req)
	default:
		log.Printf("dispatch: unknown action_hint %q, dropping (correlation_id=%s)", req.ActionHint, req.CorrelationID)
	}
}

func (d *Dispatcher) dispatchAppendDailyReportEntry(req common.ActionRequest) {
	_, err := AppendReportEntry(d.root, req.Parameters)
	if err != nil {
		log.Printf("dispatch: append_daily_report_entry failed for task %s, retrying once: %v", req.CorrelationID, err)
		_, err = AppendReportEntry(d.root, req.Parameters)
	}

	status := "success"
	if err != nil {
		status = "failed"
		log.Printf("dispatch: append_daily_report_entry failed for task %s after retry, giving up: %v", req.CorrelationID, err)
	}

	if d.publisher == nil {
		return
	}

	rec := common.OutcomeRecord{
		ActionType: req.ActionHint,
		Status:     status,
		TaskID:     req.CorrelationID,
		OccurredAt: time.Now(),
		Summary:    "Daily report entry appended",
		Decision:   "append_daily_report_entry",
		Tags:       []string{"report"},
	}
	if pubErr := d.publisher.PublishOutcome(rec); pubErr != nil {
		log.Printf("dispatch: outcome publish failed for task %s: %v", req.CorrelationID, pubErr)
	}
}
```

Replace `action-svc/dispatch/gmail_triage.go`'s `dispatchGmailTriage` function (keep everything above it — `GmailTriageParams` and `AppendGmailReportEntry` — unchanged):

```go
func (d *Dispatcher) dispatchGmailTriage(req common.ActionRequest) {
	var p GmailTriageParams
	if err := json.Unmarshal(req.Parameters, &p); err != nil {
		log.Printf("dispatch: triage_gmail_email unparseable params, dropping (correlation_id=%s): %v", req.CorrelationID, err)
		return
	}

	_, err := AppendGmailReportEntry(d.root, req.Parameters)
	if err != nil {
		log.Printf("dispatch: triage_gmail_email report append failed for task %s, retrying once: %v", req.CorrelationID, err)
		_, err = AppendGmailReportEntry(d.root, req.Parameters)
	}

	status := "success"
	if err != nil {
		status = "failed"
		log.Printf("dispatch: triage_gmail_email report append failed for task %s after retry, giving up: %v", req.CorrelationID, err)
	}

	if p.Important && d.batcher != nil {
		d.batcher.Add(notifybatch.Item{
			Sender:      p.Sender,
			Subject:     p.Subject,
			Reason:      p.Reason,
			BodyExcerpt: p.BodyExcerpt,
			ThreadID:    p.ThreadID,
		})
	}

	if d.publisher == nil {
		return
	}

	verdict := "not important"
	decision := "logged only"
	if p.Important {
		verdict = "important"
		decision = "notified via Discord"
		if d.gate.Enabled() {
			decision = "feigned notify via Discord"
		}
	}
	occurredAt, parseErr := time.Parse(time.RFC3339, p.OccurredAt)
	if parseErr != nil {
		occurredAt = time.Now()
	}

	rec := common.OutcomeRecord{
		ActionType: req.ActionHint,
		Status:     status,
		TaskID:     req.CorrelationID,
		OccurredAt: occurredAt,
		Summary:    fmt.Sprintf("%s — %s", p.Subject, verdict),
		Decision:   decision,
		Tags:       []string{"gmail", "triage"},
	}
	if pubErr := d.publisher.PublishOutcome(rec); pubErr != nil {
		log.Printf("dispatch: outcome publish failed for task %s: %v", req.CorrelationID, pubErr)
	}
}
```

(`gmail_triage.go` already imports `encoding/json`, `fmt`, `log`, `time`, `soulman/action-svc/notifybatch`, `soulman/action-svc/report`, `soulman/common` — no import changes needed there; `d.gate` resolves via `dispatch.go`'s import of `soulman/action-svc/feign` since both files are package `dispatch`.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./dispatch/... -v` from `action-svc/`
Expected: PASS (all tests)

- [ ] **Step 5: Commit**

```bash
git add action-svc/dispatch/dispatch.go action-svc/dispatch/gmail_triage.go action-svc/dispatch/dispatch_test.go action-svc/dispatch/gmail_triage_test.go
git commit -m "feat(action-svc): thread feign.Gate through Dispatcher"
```

---

### Task 5: `action-svc/scheduler` — thread the gate through, phrase the daily-report `Summary` honestly

**Files:**
- Modify: `action-svc/scheduler/daily.go`
- Modify: `action-svc/scheduler/daily_test.go`

**Interfaces:**
- Consumes: `*feign.Gate` (Task 2).
- Produces: `scheduler.New(root, sendTime string, notifier notify.Notifier, publisher OutcomePublisher, gate *feign.Gate) *Scheduler` — signature gains a 5th parameter. Task 6 (`main.go`) calls this with the real gate.

- [ ] **Step 1: Write the failing tests**

Replace `action-svc/scheduler/daily_test.go` entirely (every `scheduler.New(...)` call gains a trailing `nil`; one new test added for the feign-mode `Summary` phrasing):

```go
package scheduler_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"soulman/action-svc/feign"
	"soulman/action-svc/report"
	"soulman/action-svc/scheduler"
	"soulman/common"
)

type fakeNotifier struct {
	mu       sync.Mutex
	messages []string
	failN    int // number of Send calls to fail before succeeding
	calls    int // total number of Send calls
}

func (f *fakeNotifier) Send(message string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.failN > 0 {
		f.failN--
		return errors.New("simulated send failure")
	}
	f.messages = append(f.messages, message)
	return nil
}

func (f *fakeNotifier) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.messages...)
}

func (f *fakeNotifier) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type fakePublisher struct {
	mu      sync.Mutex
	records []common.OutcomeRecord
}

func (f *fakePublisher) PublishOutcome(rec common.OutcomeRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = append(f.records, rec)
	return nil
}

func (f *fakePublisher) last() (common.OutcomeRecord, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.records) == 0 {
		return common.OutcomeRecord{}, false
	}
	return f.records[len(f.records)-1], true
}

func fixedNow() time.Time { return time.Date(2026, 7, 17, 10, 0, 0, 0, time.Local) }

func TestRunOnce_MissingReport_SkipsSend(t *testing.T) {
	root := t.TempDir()
	notifier := &fakeNotifier{}
	pub := &fakePublisher{}
	s := scheduler.New(root, "10:00", notifier, pub, nil)
	s.Now = fixedNow

	s.RunOnce()

	if len(notifier.sent()) != 0 {
		t.Error("expected no send for missing report")
	}
}

func TestRunOnce_WhitespaceOnlyReport_SkipsSend(t *testing.T) {
	root := t.TempDir()
	yesterday := time.Date(2026, 7, 16, 0, 0, 0, 0, time.Local)
	path := report.PathForDate(root, yesterday)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("setup mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("   \n\n  "), 0o644); err != nil {
		t.Fatalf("setup write: %v", err)
	}

	notifier := &fakeNotifier{}
	pub := &fakePublisher{}
	s := scheduler.New(root, "10:00", notifier, pub, nil)
	s.Now = fixedNow

	s.RunOnce()

	if len(notifier.sent()) != 0 {
		t.Error("expected no send for whitespace-only report")
	}
}

func TestRunOnce_NonEmptyReport_SendsContentAndPublishesSuccess(t *testing.T) {
	root := t.TempDir()
	yesterday := time.Date(2026, 7, 16, 0, 0, 0, 0, time.Local)
	if _, err := report.Append(root, report.Entry{
		OccurredAt: yesterday,
		Summary:    "test error",
		RawContent: "trace",
		SourcePath: `C:\errors\a.txt`,
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	notifier := &fakeNotifier{}
	pub := &fakePublisher{}
	s := scheduler.New(root, "10:00", notifier, pub, nil)
	s.Now = fixedNow

	s.RunOnce()

	sent := notifier.sent()
	if len(sent) != 1 {
		t.Fatalf("expected exactly one send, got %d", len(sent))
	}
	if !strings.Contains(sent[0], "test error") {
		t.Errorf("sent message = %q, want it to contain %q", sent[0], "test error")
	}

	rec, ok := pub.last()
	if !ok || rec.Status != "success" || rec.ActionType != "daily_report_delivery" {
		t.Errorf("outcome = %+v, ok=%v, want status=success actionType=daily_report_delivery", rec, ok)
	}
	if rec.Summary != "Daily report delivered" || rec.Decision != "daily_report_delivery" {
		t.Errorf("outcome = %+v, want summary=%q decision=%q", rec, "Daily report delivered", "daily_report_delivery")
	}
	if len(rec.Tags) != 2 || rec.Tags[0] != "report" || rec.Tags[1] != "cron" {
		t.Errorf("Tags = %v, want [report cron]", rec.Tags)
	}
}

func TestRunOnce_ReportNeverModifiedOrDeleted(t *testing.T) {
	root := t.TempDir()
	yesterday := time.Date(2026, 7, 16, 0, 0, 0, 0, time.Local)
	path, err := report.Append(root, report.Entry{OccurredAt: yesterday, Summary: "s"})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	before, _ := os.ReadFile(path)

	notifier := &fakeNotifier{}
	s := scheduler.New(root, "10:00", notifier, &fakePublisher{}, nil)
	s.Now = fixedNow
	s.RunOnce()

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("report file missing after RunOnce: %v", err)
	}
	if string(before) != string(after) {
		t.Error("report file was modified by RunOnce")
	}
}

func TestRunOnce_SendFailsAllThreeAttempts_PublishesFailedOutcome(t *testing.T) {
	root := t.TempDir()
	yesterday := time.Date(2026, 7, 16, 0, 0, 0, 0, time.Local)
	if _, err := report.Append(root, report.Entry{OccurredAt: yesterday, Summary: "s"}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	notifier := &fakeNotifier{failN: 3}
	pub := &fakePublisher{}
	s := scheduler.New(root, "10:00", notifier, pub, nil)
	s.Now = fixedNow
	s.BackoffBase = time.Millisecond // keep the test fast

	s.RunOnce()

	if notifier.callCount() != 3 {
		t.Errorf("expected 3 Send calls, got %d", notifier.callCount())
	}

	rec, ok := pub.last()
	if !ok || rec.Status != "failed" {
		t.Errorf("outcome = %+v, ok=%v, want status=failed", rec, ok)
	}
}

func TestRunOnce_SendFailsTwiceThenSucceeds_PublishesSuccessOutcome(t *testing.T) {
	root := t.TempDir()
	yesterday := time.Date(2026, 7, 16, 0, 0, 0, 0, time.Local)
	if _, err := report.Append(root, report.Entry{OccurredAt: yesterday, Summary: "s"}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	notifier := &fakeNotifier{failN: 2}
	pub := &fakePublisher{}
	s := scheduler.New(root, "10:00", notifier, pub, nil)
	s.Now = fixedNow
	s.BackoffBase = time.Millisecond

	s.RunOnce()

	if len(notifier.sent()) != 1 {
		t.Errorf("expected the eventual retry to succeed and send once, got %d sends", len(notifier.sent()))
	}
	rec, ok := pub.last()
	if !ok || rec.Status != "success" {
		t.Errorf("outcome = %+v, ok=%v, want status=success", rec, ok)
	}
}

func TestRunOnce_FeignMode_SummarySaysFeigned(t *testing.T) {
	root := t.TempDir()
	yesterday := time.Date(2026, 7, 16, 0, 0, 0, 0, time.Local)
	if _, err := report.Append(root, report.Entry{OccurredAt: yesterday, Summary: "test error"}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	notifier := &fakeNotifier{}
	pub := &fakePublisher{}
	gate := feign.New(true, filepath.Join(t.TempDir(), "feigned-actions.jsonl"))
	s := scheduler.New(root, "10:00", notifier, pub, gate)
	s.Now = fixedNow

	s.RunOnce()

	// Scheduler always calls the Notifier it was given — the actual
	// interception happens one layer down, inside whatever Notifier
	// main.go wired in (tested in the feign package). Here we're only
	// verifying scheduler's own outcome-record phrasing.
	if len(notifier.sent()) != 1 {
		t.Errorf("expected exactly one Send call regardless of feign mode, got %d", len(notifier.sent()))
	}

	rec, ok := pub.last()
	if !ok || rec.Status != "success" {
		t.Errorf("outcome = %+v, ok=%v, want status=success", rec, ok)
	}
	if rec.Summary != "Daily report delivery feigned" {
		t.Errorf("Summary = %q, want %q", rec.Summary, "Daily report delivery feigned")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./scheduler/... -v` from `action-svc/`
Expected: FAIL — compile error, `scheduler.New` still takes 4 args

- [ ] **Step 3: Write the implementation**

Replace `action-svc/scheduler/daily.go` entirely:

```go
package scheduler

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"soulman/action-svc/feign"
	"soulman/action-svc/notify"
	"soulman/action-svc/report"
	"soulman/common"
)

// OutcomePublisher is satisfied by *natsclient.Publisher. Defined here (not
// in natsclient) so this package doesn't need to import natsclient.
type OutcomePublisher interface {
	PublishOutcome(rec common.OutcomeRecord) error
}

type Scheduler struct {
	root      string
	sendTime  string
	notifier  notify.Notifier
	publisher OutcomePublisher
	gate      *feign.Gate
	stop      chan struct{}

	// Overridable for tests: Now controls "current time" (avoids waiting for
	// a real clock), BackoffBase controls the retry delay (avoids a slow test).
	Now         func() time.Time
	BackoffBase time.Duration
}

func New(root, sendTime string, notifier notify.Notifier, publisher OutcomePublisher, gate *feign.Gate) *Scheduler {
	return &Scheduler{
		root:        root,
		sendTime:    sendTime,
		notifier:    notifier,
		publisher:   publisher,
		gate:        gate,
		stop:        make(chan struct{}),
		Now:         time.Now,
		BackoffBase: 1 * time.Second,
	}
}

func (s *Scheduler) Start() {
	go s.loop()
}

func (s *Scheduler) Stop() {
	close(s.stop)
}

func (s *Scheduler) loop() {
	for {
		wait := time.Until(s.nextRun(s.Now()))
		select {
		case <-time.After(wait):
			s.RunOnce()
		case <-s.stop:
			return
		}
	}
}

func (s *Scheduler) nextRun(from time.Time) time.Time {
	hh, mm := parseSendTime(s.sendTime)
	next := time.Date(from.Year(), from.Month(), from.Day(), hh, mm, 0, 0, from.Location())
	if !next.After(from) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

func parseSendTime(s string) (int, int) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 10, 0
	}
	hh, err1 := strconv.Atoi(parts[0])
	mm, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 10, 0
	}
	return hh, mm
}

// RunOnce performs a single check-and-send cycle: read yesterday's report,
// skip if missing/empty/whitespace-only, otherwise send via the Notifier
// with retry, then log the outcome. The report file is never modified or
// deleted here — only report.Read is used.
func (s *Scheduler) RunOnce() {
	yesterday := s.Now().AddDate(0, 0, -1)

	content, err := report.Read(s.root, yesterday)
	if err != nil {
		log.Printf("scheduler: read report for %s failed, will retry tomorrow: %v", yesterday.Format("2006-01-02"), err)
		return
	}
	if strings.TrimSpace(content) == "" {
		log.Printf("scheduler: report for %s empty or missing, nothing to send", yesterday.Format("2006-01-02"))
		return
	}

	err = s.sendWithRetry(content)
	status := "success"
	var summary string
	switch {
	case err != nil:
		status = "failed"
		summary = fmt.Sprintf("Daily report delivery failed: %v", err)
		log.Printf("scheduler: notifier send failed after 3 attempts: %v", err)
	case s.gate.Enabled():
		summary = "Daily report delivery feigned"
	default:
		summary = "Daily report delivered"
	}

	if s.publisher == nil {
		return
	}

	rec := common.OutcomeRecord{
		ActionType: "daily_report_delivery",
		Status:     status,
		TaskID:     "",
		OccurredAt: s.Now(),
		Summary:    summary,
		Decision:   "daily_report_delivery",
		Tags:       []string{"report", "cron"},
	}
	if pubErr := s.publisher.PublishOutcome(rec); pubErr != nil {
		log.Printf("scheduler: outcome publish failed: %v", pubErr)
	}
}

func (s *Scheduler) sendWithRetry(content string) error {
	var err error
	backoff := s.BackoffBase
	for attempt := 1; attempt <= 3; attempt++ {
		err = s.notifier.Send(content)
		if err == nil {
			return nil
		}
		log.Printf("scheduler: notifier send attempt %d/3 failed: %v", attempt, err)
		if attempt < 3 {
			time.Sleep(backoff)
			backoff *= 2
		}
	}
	return err
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./scheduler/... -v` from `action-svc/`
Expected: PASS (all tests)

- [ ] **Step 5: Commit**

```bash
git add action-svc/scheduler/daily.go action-svc/scheduler/daily_test.go
git commit -m "feat(action-svc): thread feign.Gate through Scheduler"
```

---

### Task 6: `action-svc/main.go` — wire the gate, wrap the notifier, log the mode

**Files:**
- Modify: `action-svc/main.go`

**Interfaces:**
- Consumes: `feign.New` (Task 2), `feign.WrapNotifier` (Task 2), `cfg.FeignMode` (Task 3), `dispatch.New(..., gate)` (Task 4), `scheduler.New(..., gate)` (Task 5).
- Produces: nothing further downstream — this is the final wiring task.

- [ ] **Step 1: Write the implementation**

`action-svc/main.go` has no dedicated test file (pure wiring, matching the existing pattern). Replace it entirely:

```go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"soulman/action-svc/config"
	"soulman/action-svc/dispatch"
	"soulman/action-svc/feign"
	"soulman/action-svc/httpserver"
	"soulman/action-svc/natsclient"
	"soulman/action-svc/notifybatch"
	"soulman/action-svc/notify"
	"soulman/action-svc/scheduler"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Feign gate — see docs/superpowers/specs/2026-07-19-action-svc-feign-mode-design.md.
	// When enabled, outbound side effects (starting with Discord sends) are
	// recorded to logs/feigned-actions.jsonl instead of actually happening.
	gate := feign.New(cfg.FeignMode, filepath.Join(cfg.SoulmanRoot, "logs", "feigned-actions.jsonl"))

	// Notifier — Discord is the only implementation in v1. Built regardless
	// of whether DISCORD_BOT_TOKEN/DISCORD_CHANNEL_ID are set; a missing
	// token surfaces as a Send failure, handled like any other notifier
	// failure (retried, then logged) rather than a startup crash. Always
	// wrapped with the feign gate — a transparent passthrough when disabled.
	var notifier notify.Notifier
	switch cfg.ReportNotifier {
	case "discord":
		notifier = notify.NewDiscordNotifier(cfg.DiscordBotToken, cfg.DiscordChannelID)
	default:
		log.Fatalf("unsupported REPORT_NOTIFIER %q", cfg.ReportNotifier)
	}
	notifier = feign.WrapNotifier(gate, notifier)

	// Batches important-email Discord notifications from the
	// triage_gmail_email dispatch handler (30s grace / 2min max-wait — see
	// docs/superpowers/specs/2026-07-18-gmail-triage-action-design.md).
	// Reuses the same (feign-wrapped) notifier the daily cron already sends
	// through.
	batcher := notifybatch.New(notifybatch.DefaultGrace, notifybatch.DefaultMaxWait, notifier)

	// NATS is non-fatal at startup: the dispatch side degrades until
	// reconnect, but the HTTP server and the daily cron don't depend on it.
	var publisher *natsclient.Publisher
	nc, natsErr := natsclient.Connect(cfg.NATSURL)
	if natsErr != nil {
		log.Printf("WARNING: nats unavailable (%v) — dispatch degraded until reconnect", natsErr)
	} else {
		defer nc.Close()

		var pubErr error
		publisher, pubErr = natsclient.NewPublisher(ctx, nc, cfg.MemoryWriteSubject)
		if pubErr != nil {
			log.Printf("WARNING: nats publisher setup failed (%v) — outcome records degraded", pubErr)
		}

		// dispatchPublisher stays a true nil interface (not a typed-nil
		// *natsclient.Publisher) when publisher setup failed above, so
		// Dispatcher's `d.publisher == nil` check (dispatch.go) behaves
		// correctly instead of comparing a non-nil interface wrapping a nil
		// pointer. The durable thinking.request consumer below must come up
		// independently of whether the MEMORY_WRITE publisher succeeded —
		// it's the actual fix for the incident this plan exists to close,
		// and must never be gated on an unrelated stream's provisioning.
		var dispatchPublisher dispatch.Publisher
		if publisher != nil {
			dispatchPublisher = publisher
		}
		disp := dispatch.New(cfg.SoulmanRoot, dispatchPublisher, batcher, gate)
		consumer, consErr := natsclient.NewConsumer(nc, cfg.ActionSvcConsumerName, cfg.ThinkingRequestSubject, disp.Handle)
		if consErr != nil {
			log.Printf("WARNING: nats consumer setup failed: %v", consErr)
		} else if startErr := consumer.Start(ctx); startErr != nil {
			log.Printf("WARNING: nats consumer start failed: %v", startErr)
		} else {
			defer consumer.Close()
		}
	}

	// Scheduler runs independently of NATS — a stalled cron doesn't block
	// new error entries, and a NATS outage doesn't prevent yesterday's
	// report from being sent.
	var schedPublisher scheduler.OutcomePublisher
	if publisher != nil {
		schedPublisher = publisher
	}
	sched := scheduler.New(cfg.SoulmanRoot, cfg.ReportSendTime, notifier, schedPublisher, gate)
	sched.Start()
	defer sched.Stop()

	// HTTP server (non-blocking)
	srv := httpserver.New(cfg.HTTPPort)
	go func() {
		log.Printf("HTTP listening on :%s", cfg.HTTPPort)
		if err := srv.Start(); err != nil {
			log.Printf("http: %v", err)
		}
	}()

	log.Printf("action-svc started (NATS=%s connected=%v, HTTP=:%s, root=%s, notifier=%s, feign_mode=%v)",
		cfg.NATSURL, natsErr == nil, cfg.HTTPPort, cfg.SoulmanRoot, cfg.ReportNotifier, cfg.FeignMode)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Printf("action-svc shutting down")
}
```

- [ ] **Step 2: Confirm the whole module builds and tests pass**

Run: `go build ./...` from `action-svc/`
Expected: no errors

Run: `go vet ./...` from `action-svc/`
Expected: no errors

Run: `go test ./... -v` from `action-svc/`
Expected: all tests PASS or SKIP (NATS-dependent ones skip if unreachable), no FAIL

- [ ] **Step 3: Commit**

```bash
git add action-svc/main.go
git commit -m "feat(action-svc): wire feign.Gate into main.go"
```

---

### Task 7: Docs — `action-svc/NOTES.md` and root `CLAUDE.md` updates

**Files:**
- Modify: `action-svc/NOTES.md`
- Modify: `CLAUDE.md` (repo root)

**Interfaces:** none — documentation only.

- [ ] **Step 1: Append to `action-svc/NOTES.md`**

Add this section at the end of the file (after the existing "Known deferred issue" section):

```markdown

## Feign mode

`FEIGN_MODE` (from `config/dev.json`/`config/prod.json`'s `feign_mode`, currently `true` in both environments) makes `action-svc` record outbound side effects instead of performing them — see `docs/superpowers/specs/2026-07-19-action-svc-feign-mode-design.md`. Concretely: the shared `notify.Notifier` (used by both the 10:00 AM daily-report cron and the gmail-triage batcher) is wrapped with `feign.Gate` in `main.go`; when the gate is enabled, `Send` appends a JSON line to `$SOULMAN_ROOT/logs/feigned-actions.jsonl` instead of hitting Discord's API. `episodes` rows stay honest about it too — `dispatchGmailTriage`'s `Decision` reads `"feigned notify via Discord"` instead of `"notified via Discord"`, and the daily cron's `Summary` reads `"Daily report delivery feigned"` instead of `"Daily report delivered"`, whenever the gate is on.

**If you're wondering why no Discord messages are arriving:** check `feign_mode` in the running environment's config first, before assuming something's broken. It was turned on deliberately in both dev and prod as of 2026-07-19 — turn it back off (`feign_mode: false` in `config/dev.json`/`config/prod.json`, then restart `action-svc`) when you want real sends again.
```

- [ ] **Step 2: Update root `CLAUDE.md`**

In the Services numbered list, replace item 4 (`action-svc`)'s spec line — find:

```
4. **`action-svc`** — dispatches `soulman.thinking.request` actions via a durable JetStream consumer: `append_daily_report_entry` (writes to `$SOULMAN_ROOT/reports/`) and `triage_gmail_email` (report entry + debounced batched Discord notify if important). Independently runs a 10:00 AM cron sending the previous day's report via a pluggable `Notifier` (Discord). `DISCORD_BOT_TOKEN`/`DISCORD_CHANNEL_ID` are non-fatal if blank (Send fails, retried/logged like any other notifier failure) — configured in dev and prod as of 2026-07-18 (a dedicated "Soulman Reports" bot), so the cron actively sends.
   - Specs: `2026-07-17-action-svc-design.md`, `2026-07-17-daily-report-delivery-design.md`, `2026-07-17-error-report-action-design.md`, `2026-07-18-gmail-triage-action-design.md`, `2026-07-18-pipeline-debugging-tools-design.md`
   - Notes: `action-svc/NOTES.md` — the incident that motivated durable queues, the notification-batching design, a known deferred bug (dev/prod share one Discord bot)
```

replace with:

```
4. **`action-svc`** — dispatches `soulman.thinking.request` actions via a durable JetStream consumer: `append_daily_report_entry` (writes to `$SOULMAN_ROOT/reports/`) and `triage_gmail_email` (report entry + debounced batched Discord notify if important). Independently runs a 10:00 AM cron sending the previous day's report via a pluggable `Notifier` (Discord). `DISCORD_BOT_TOKEN`/`DISCORD_CHANNEL_ID` are non-fatal if blank (Send fails, retried/logged like any other notifier failure) — configured in dev and prod as of 2026-07-18 (a dedicated "Soulman Reports" bot). As of 2026-07-19, `feign_mode` is `true` in both `config/dev.json` and `config/prod.json`, so outbound sends are currently recorded to `logs/feigned-actions.jsonl` instead of actually happening — see `action-svc/NOTES.md`.
   - Specs: `2026-07-17-action-svc-design.md`, `2026-07-17-daily-report-delivery-design.md`, `2026-07-17-error-report-action-design.md`, `2026-07-18-gmail-triage-action-design.md`, `2026-07-18-pipeline-debugging-tools-design.md`, `2026-07-19-action-svc-feign-mode-design.md`
   - Notes: `action-svc/NOTES.md` — the incident that motivated durable queues, the notification-batching design, a known deferred bug (dev/prod share one Discord bot), feign mode
```

- [ ] **Step 3: Commit**

```bash
git add action-svc/NOTES.md CLAUDE.md
git commit -m "docs: document action-svc feign mode in NOTES.md and CLAUDE.md"
```
