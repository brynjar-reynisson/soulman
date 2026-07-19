# Memory Episodes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give `memory-svc` a real, working `episodes` table and `GET /memory/episodes` endpoint, fed by a new durable consumer of `action-svc`'s existing-but-never-consumed `MEMORY_WRITE` JetStream stream.

**Architecture:** `action-svc`'s `OutcomeRecord` (currently thin, currently private to `action-svc/natsclient`) is promoted to `common.OutcomeRecord` and enriched with `OccurredAt`/`Summary`/`Decision`/`Tags`. `memory-svc` gets a second, sibling NATS consumer (`natsconsumer.MemoryWriteConsumer`, alongside the existing STIMULUS `Consumer`) that writes each outcome into a new `episodes` Postgres table (deduped by JetStream stream sequence, not `task_id`, since `task_id` is sometimes empty) and NAKs-and-relies-on-JetStream-redelivery on failure rather than duplicating the STIMULUS consumer's file-log/replay layer. `GET /memory/episodes` mirrors the existing `GET /raw-inputs/recent` handler exactly.

**Tech Stack:** Go 1.25, `github.com/nats-io/nats.go` + `nats.go/jetstream`, `github.com/jackc/pgx/v5`, `github.com/go-chi/chi/v5`. No new dependencies.

## Global Constraints

- Design spec: `docs/superpowers/specs/2026-07-18-memory-episodes-design.md` — read it first; this plan implements it exactly.
- All SQL uses fully-qualified table names (`memory_dev.episodes`, no `SET search_path`), matching `raw_inputs`'s existing convention.
- `memory-svc` never creates its own tables. The `episodes` table DDL lives at `docs/superpowers/specs/sql/2026-07-18-episodes-table.sql` (already written and committed) and must be applied by hand against dev's local Supabase (`postgres://postgres:postgres@localhost:54322/postgres`, schema `memory_dev`) before any DB-backed test in this plan will pass instead of skip. **Prerequisite for the human running this plan:** start dev's Supabase (`cd ~/soulman-dev/memory && supabase start`) and apply that SQL file (`psql "$DATABASE_URL" -v schema=memory_dev -f docs/superpowers/specs/sql/2026-07-18-episodes-table.sql`) before Task 7. Tests skip cleanly (not fail) if the DB is unreachable, matching every existing Postgres-backed test in this repo — so it's safe to proceed through earlier tasks regardless.
- JetStream identifies a durable consumer by `(stream, name)` — the new `memory-svc` episodes consumer name must be distinct from the existing `memory_svc` STIMULUS consumer name (this is exactly the collision hazard called out in the root `CLAUDE.md`).
- `NATS_URL` env var (default `nats://localhost:4222`) gates NATS-backed tests the same way `DATABASE_URL` gates DB-backed tests — skip (not fail) when unreachable.
- Every Go file this plan touches: run `gofmt -l <file>` (or just trust `go build`/`go test`, which don't care about formatting) — no strict gofmt-diff requirement beyond normal Go style already visible in the files being edited.

---

### Task 1: `common.OutcomeRecord` — the enriched Action→Memory wire type

**Files:**
- Create: `common/outcome.go`
- Create: `common/outcome_test.go`

**Interfaces:**
- Produces: `common.OutcomeRecord` struct — `Type, ActionType, Status, TaskID, Summary, Decision string`; `OccurredAt time.Time`; `Tags []string`. JSON tags: `type, action_type, status, task_id, occurred_at, summary, decision, tags`. Every later task that publishes or consumes an outcome record uses this exact type.

- [ ] **Step 1: Write the failing test**

Create `common/outcome_test.go`:

```go
package common_test

import (
	"encoding/json"
	"testing"
	"time"

	"soulman/common"
)

func TestOutcomeRecord_JSONRoundtrip(t *testing.T) {
	occurredAt := time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)
	rec := common.OutcomeRecord{
		Type:       "action_log",
		ActionType: "triage_gmail_email",
		Status:     "success",
		TaskID:     "t1",
		OccurredAt: occurredAt,
		Summary:    "Server down — important",
		Decision:   "notified via Discord",
		Tags:       []string{"gmail", "triage"},
	}

	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got common.OutcomeRecord
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ActionType != rec.ActionType || got.Summary != rec.Summary || !got.OccurredAt.Equal(rec.OccurredAt) {
		t.Errorf("got = %+v, want %+v", got, rec)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "gmail" || got.Tags[1] != "triage" {
		t.Errorf("Tags = %v, want [gmail triage]", got.Tags)
	}
}

// TestOutcomeRecord_WireFieldNames documents the exact wire contract, the
// same way TestActionRequest_WireFieldNames does for ActionRequest — so a
// future field rename doesn't silently break the action-svc <-> memory-svc
// contract without a test catching it.
func TestOutcomeRecord_WireFieldNames(t *testing.T) {
	rec := common.OutcomeRecord{ActionType: "x", Status: "success", TaskID: "t1"}
	b, _ := json.Marshal(rec)

	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	for _, field := range []string{"type", "action_type", "status", "task_id", "occurred_at", "summary", "decision", "tags"} {
		if _, ok := m[field]; !ok {
			t.Errorf("expected %q field in wire JSON", field)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestOutcomeRecord` from `common/`
Expected: FAIL — `common.OutcomeRecord` undefined (compile error)

- [ ] **Step 3: Write the implementation**

Create `common/outcome.go`:

```go
package common

import "time"

// OutcomeRecord is the payload published to soulman.memory.write — the
// record of an action-svc dispatch outcome that memory-svc's episodic
// memory consumer turns into an episodes row. This is the single canonical
// wire format for Action -> Memory, mirroring how ActionRequest is the
// canonical wire format for Thinking -> Action.
//
// Type is a forward-compat discriminator; today it's always "action_log"
// (set internally by natsclient.Publisher.PublishOutcome, not by callers).
// TaskID may be empty (e.g. the daily-report cron has no per-message
// correlation ID) — memory-svc dedups on the JetStream message's stream
// sequence number instead, not TaskID.
type OutcomeRecord struct {
	Type       string    `json:"type"`
	ActionType string    `json:"action_type"`
	Status     string    `json:"status"`
	TaskID     string    `json:"task_id"`
	OccurredAt time.Time `json:"occurred_at"`
	Summary    string    `json:"summary"`
	Decision   string    `json:"decision"`
	Tags       []string  `json:"tags"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run TestOutcomeRecord` from `common/`
Expected: PASS (both tests)

- [ ] **Step 5: Commit**

```bash
git add common/outcome.go common/outcome_test.go
git commit -m "feat(common): add OutcomeRecord wire type for Action -> Memory"
```

---

### Task 2: `MemorySvcEpisodes` consumer name in shared config

**Files:**
- Modify: `common/sharedconfig/config.go`
- Modify: `common/sharedconfig/config_test.go`
- Modify: `config/dev.json`
- Modify: `config/prod.json`

**Interfaces:**
- Consumes: nothing new.
- Produces: `sharedconfig.ConsumerNames.MemorySvcEpisodes string` (json `memory_svc_episodes`). Task 6 (`memory-svc/config`) reads this field.

- [ ] **Step 1: Write the failing test**

Add to `common/sharedconfig/config_test.go` (append at end of file):

```go
func TestLoad_MemorySvcEpisodesConsumerName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"watch_paths": [],
		"consumer_names": {
			"memory_svc": "memory-svc",
			"memory_svc_episodes": "memory-svc-episodes",
			"thinking_svc": "thinking-svc",
			"action_svc": "action-svc"
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := sharedconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ConsumerNames.MemorySvcEpisodes != "memory-svc-episodes" {
		t.Errorf("ConsumerNames.MemorySvcEpisodes = %q, want memory-svc-episodes", cfg.ConsumerNames.MemorySvcEpisodes)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sharedconfig/... -run TestLoad_MemorySvcEpisodesConsumerName -v` from `common/`
Expected: FAIL — `cfg.ConsumerNames.MemorySvcEpisodes` undefined (compile error)

- [ ] **Step 3: Write the implementation**

In `common/sharedconfig/config.go`, replace the `ConsumerNames` type and its doc comment:

```go
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
```

In `config/dev.json`, change the `consumer_names` block to:

```json
  "consumer_names": {
    "memory_svc": "memory-svc-dev",
    "memory_svc_episodes": "memory-svc-episodes-dev",
    "thinking_svc": "thinking-svc-dev",
    "action_svc": "action-svc-dev"
  },
```

In `config/prod.json`, change the `consumer_names` block to:

```json
  "consumer_names": {
    "memory_svc": "memory-svc",
    "memory_svc_episodes": "memory-svc-episodes",
    "thinking_svc": "thinking-svc",
    "action_svc": "action-svc"
  },
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./sharedconfig/... -v` from `common/`
Expected: PASS (all tests, including the pre-existing ones)

- [ ] **Step 5: Commit**

```bash
git add common/sharedconfig/config.go common/sharedconfig/config_test.go config/dev.json config/prod.json
git commit -m "feat(common): add memory_svc_episodes consumer name"
```

---

### Task 3: `action-svc`'s `Publisher.PublishOutcome` — switch to `common.OutcomeRecord`

**Files:**
- Modify: `action-svc/natsclient/publisher.go`
- Modify: `action-svc/natsclient/natsclient_test.go`

**Interfaces:**
- Consumes: `common.OutcomeRecord` (Task 1).
- Produces: `Publisher.PublishOutcome(rec common.OutcomeRecord) error` — replaces the old `PublishOutcome(actionType, status, taskID string) error`. `rec.Type` is forced to `"action_log"` inside the method; callers don't need to set it. Tasks 4 and 5 (the `dispatch` and `scheduler` packages, whose locally-defined `Publisher`/`OutcomePublisher` interfaces are structurally satisfied by `*natsclient.Publisher`) depend on this exact new signature.

- [ ] **Step 1: Write the failing test**

Replace `action-svc/natsclient/natsclient_test.go`'s `TestPublisher_PublishOutcome` and `TestNewPublisher_CreatesMemoryWriteStream` (the whole file becomes):

```go
package natsclient_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"soulman/action-svc/natsclient"
	"soulman/common"
)

func natsURL() string {
	if u := os.Getenv("NATS_URL"); u != "" {
		return u
	}
	return "nats://localhost:4222"
}

// TestConnect_RetriesInBackgroundWhenServerUnreachable proves the core
// behavioral change from Finding 1: previously nats.Connect against an
// unreachable server returned a connection-refused error immediately; now
// RetryOnFailedConnect(true) makes Connect succeed right away (with the
// *nats.Conn reconnecting in the background) instead of failing outright.
// Port 1 on localhost is reserved and nothing listens on it, so this is
// deterministic and requires no live NATS server or timing-dependent wait.
func TestConnect_RetriesInBackgroundWhenServerUnreachable(t *testing.T) {
	conn, err := natsclient.Connect("nats://localhost:1")
	if err != nil {
		t.Fatalf("Connect: want nil error (RetryOnFailedConnect should suppress the initial failure), got %v", err)
	}
	defer conn.Close()
}

func TestPublisher_PublishOutcome(t *testing.T) {
	url := natsURL()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer nc.Close()

	ch := make(chan *nats.Msg, 1)
	sub, err := nc.Subscribe("soulman.memory.write", func(m *nats.Msg) { ch <- m })
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pub, err := natsclient.NewPublisher(ctx, nc, "soulman.memory.write")
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}

	id := fmt.Sprintf("outcome-%d", time.Now().UnixNano())
	rec := common.OutcomeRecord{
		ActionType: "append_daily_report_entry",
		Status:     "success",
		TaskID:     id,
		OccurredAt: time.Now().UTC(),
		Summary:    "Daily report entry appended",
		Decision:   "append_daily_report_entry",
		Tags:       []string{"report"},
	}
	if err := pub.PublishOutcome(rec); err != nil {
		t.Fatalf("PublishOutcome: %v", err)
	}

	select {
	case msg := <-ch:
		var got common.OutcomeRecord
		if err := json.Unmarshal(msg.Data, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got.TaskID != id || got.Status != "success" || got.Type != "action_log" {
			t.Errorf("outcome = %+v, want task_id=%s status=success type=action_log", got, id)
		}
		if got.Summary != rec.Summary || got.Decision != rec.Decision {
			t.Errorf("outcome = %+v, want summary=%q decision=%q", got, rec.Summary, rec.Decision)
		}
	case <-time.After(3 * time.Second):
		t.Error("outcome record not received within 3 seconds")
	}
}

func TestNewPublisher_CreatesMemoryWriteStream(t *testing.T) {
	url := natsURL()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer nc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pub, err := natsclient.NewPublisher(ctx, nc, "soulman.memory.write")
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}

	id := fmt.Sprintf("stream-probe-%d", time.Now().UnixNano())
	rec := common.OutcomeRecord{ActionType: "probe", Status: "success", TaskID: id, OccurredAt: time.Now().UTC()}
	if err := pub.PublishOutcome(rec); err != nil {
		t.Errorf("PublishOutcome after NewPublisher: %v, want nil (MEMORY_WRITE stream should exist)", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./natsclient/... -v` from `action-svc/`
Expected: FAIL — compile error, `pub.PublishOutcome(rec)` doesn't match the old `(string, string, string)` signature

- [ ] **Step 3: Write the implementation**

Replace `action-svc/natsclient/publisher.go` entirely:

```go
package natsclient

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"soulman/common"
)

// Publisher publishes outcome records to the configured subject
// (soulman.memory.write by default) via JetStream — durable, so records
// aren't lost even before memory-svc's consumer processes them (kept
// durable via MEMORY_WRITE's 30-day retention either way).
type Publisher struct {
	js      jetstream.JetStream
	subject string
}

// NewPublisher builds a Publisher against an already-connected nc, ensuring
// the MEMORY_WRITE stream exists (idempotent create-or-update).
func NewPublisher(ctx context.Context, nc *nats.Conn, subject string) (*Publisher, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("natsclient: jetstream: %w", err)
	}

	_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     "MEMORY_WRITE",
		Subjects: []string{"soulman.memory.write", "soulman.dev.memory.write"},
		MaxAge:   30 * 24 * time.Hour,
	})
	if err != nil {
		return nil, fmt.Errorf("natsclient: ensure MEMORY_WRITE stream: %w", err)
	}

	return &Publisher{js: js, subject: subject}, nil
}

// PublishOutcome fire-and-forgets (from the caller's perspective — it's
// still a durable JetStream publish under the hood) rec to the configured
// subject. rec.Type is forced to "action_log" so callers don't need to set
// it themselves.
func (p *Publisher) PublishOutcome(rec common.OutcomeRecord) error {
	rec.Type = "action_log"
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("natsclient: marshal outcome: %w", err)
	}
	if _, err := p.js.Publish(context.Background(), p.subject, b); err != nil {
		return fmt.Errorf("natsclient: publish outcome: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./natsclient/... -v` from `action-svc/`
Expected: PASS or SKIP (SKIP if NATS unreachable) — no FAIL

- [ ] **Step 5: Commit**

```bash
git add action-svc/natsclient/publisher.go action-svc/natsclient/natsclient_test.go
git commit -m "feat(action-svc): PublishOutcome takes common.OutcomeRecord"
```

**Note:** this leaves `action-svc/main.go` non-compiling (`dispatch.New`/`scheduler.New` expect `*natsclient.Publisher` to satisfy their old-signature local interfaces) until Tasks 4 and 5 are done. `go build ./...` for the whole `action-svc` module is expected to fail here — `go test ./natsclient/...` in isolation is the right check for this task. Task 5's final step re-verifies the whole module builds.

---

### Task 4: `action-svc/dispatch` — richer outcome records at both call sites

**Files:**
- Modify: `action-svc/dispatch/dispatch.go`
- Modify: `action-svc/dispatch/gmail_triage.go`
- Modify: `action-svc/dispatch/dispatch_test.go`
- Modify: `action-svc/dispatch/gmail_triage_test.go`

**Interfaces:**
- Consumes: `common.OutcomeRecord` (Task 1), `Publisher.PublishOutcome(common.OutcomeRecord) error` (Task 3).
- Produces: `dispatch.Publisher` interface now requires `PublishOutcome(rec common.OutcomeRecord) error`. `fakePublisher` (defined once in `dispatch_test.go`, shared by `gmail_triage_test.go` — same `dispatch_test` package) now records `[]common.OutcomeRecord` and exposes `last() (common.OutcomeRecord, bool)`.

- [ ] **Step 1: Write the failing tests**

Replace `action-svc/dispatch/dispatch_test.go` entirely:

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
	d := dispatch.New(t.TempDir(), pub, nil)

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
	d := dispatch.New(t.TempDir(), pub, nil)

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
	d := dispatch.New(t.TempDir(), pub, nil)

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
	d := dispatch.New(t.TempDir(), pub, nil)
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

Replace `action-svc/dispatch/gmail_triage_test.go`'s two assertions that read `rec.status`/`rec.actionType` (in `TestDispatch_GmailTriage_Important_AddsToBatcher` and `TestDispatch_GmailTriage_ReportAppendFailsTwice_RetriesOnceThenPublishesFailedOutcome`) — the whole file becomes:

```go
package dispatch_test

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"soulman/action-svc/dispatch"
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
	d := dispatch.New(t.TempDir(), pub, batcher)

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
	d := dispatch.New(t.TempDir(), pub, batcher)

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
	d := dispatch.New(t.TempDir(), pub, batcher)

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
	d := dispatch.New(t.TempDir(), pub, &fakeBatcher{})

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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./dispatch/... -v` from `action-svc/`
Expected: FAIL — compile error, `Publisher.PublishOutcome` in `dispatch.go` still declares the old 3-string signature

- [ ] **Step 3: Write the implementation**

Replace `action-svc/dispatch/dispatch.go` entirely:

```go
package dispatch

import (
	"encoding/json"
	"log"
	"time"

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
}

func New(root string, publisher Publisher, batcher Batcher) *Dispatcher {
	return &Dispatcher{root: root, publisher: publisher, batcher: batcher}
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

Replace `action-svc/dispatch/gmail_triage.go`'s `dispatchGmailTriage` function (keep everything above it — the `GmailTriageParams` type and `AppendGmailReportEntry` var — unchanged):

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

(`gmail_triage.go` already imports `encoding/json`, `fmt`, `log`, `time`, `soulman/action-svc/notifybatch`, `soulman/action-svc/report`, `soulman/common` — no import changes needed there.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./dispatch/... -v` from `action-svc/`
Expected: PASS (all tests)

- [ ] **Step 5: Commit**

```bash
git add action-svc/dispatch/dispatch.go action-svc/dispatch/gmail_triage.go action-svc/dispatch/dispatch_test.go action-svc/dispatch/gmail_triage_test.go
git commit -m "feat(action-svc): populate Summary/Decision/Tags on dispatch outcomes"
```

---

### Task 5: `action-svc/scheduler` — richer outcome record for the daily-report cron

**Files:**
- Modify: `action-svc/scheduler/daily.go`
- Modify: `action-svc/scheduler/daily_test.go`

**Interfaces:**
- Consumes: `common.OutcomeRecord` (Task 1).
- Produces: `scheduler.OutcomePublisher` interface now requires `PublishOutcome(rec common.OutcomeRecord) error`.

- [ ] **Step 1: Write the failing tests**

Replace `action-svc/scheduler/daily_test.go`'s `fakePublisher`/`record` type and the three assertions using `rec.status`/`rec.actionType` — the whole file becomes:

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
	s := scheduler.New(root, "10:00", notifier, pub)
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
	s := scheduler.New(root, "10:00", notifier, pub)
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
	s := scheduler.New(root, "10:00", notifier, pub)
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
	s := scheduler.New(root, "10:00", notifier, &fakePublisher{})
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
	s := scheduler.New(root, "10:00", notifier, pub)
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
	s := scheduler.New(root, "10:00", notifier, pub)
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./scheduler/... -v` from `action-svc/`
Expected: FAIL — compile error, `OutcomePublisher` in `daily.go` still declares the old 3-string signature

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
	stop      chan struct{}

	// Overridable for tests: Now controls "current time" (avoids waiting for
	// a real clock), BackoffBase controls the retry delay (avoids a slow test).
	Now         func() time.Time
	BackoffBase time.Duration
}

func New(root, sendTime string, notifier notify.Notifier, publisher OutcomePublisher) *Scheduler {
	return &Scheduler{
		root:        root,
		sendTime:    sendTime,
		notifier:    notifier,
		publisher:   publisher,
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
	summary := "Daily report delivered"
	if err != nil {
		status = "failed"
		summary = fmt.Sprintf("Daily report delivery failed: %v", err)
		log.Printf("scheduler: notifier send failed after 3 attempts: %v", err)
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

- [ ] **Step 4: Run tests to verify they pass, then confirm the whole module builds**

Run: `go test ./scheduler/... -v` from `action-svc/`
Expected: PASS (all tests)

Run: `go build ./... && go test ./... -v` from `action-svc/`
Expected: everything compiles (Tasks 3-5 together restore the whole module); all tests PASS or SKIP (NATS-dependent tests skip if unreachable), no FAIL

- [ ] **Step 5: Commit**

```bash
git add action-svc/scheduler/daily.go action-svc/scheduler/daily_test.go
git commit -m "feat(action-svc): populate Summary/Decision/Tags on daily-report outcome"
```

---

### Task 6: `memory-svc/config` — load `memory_write_subject` and the episodes consumer name

**Files:**
- Modify: `memory-svc/config/config.go`
- Modify: `memory-svc/config/config_test.go`

**Interfaces:**
- Consumes: `sharedconfig.Config.MemoryWriteSubject` (already existed, unused until now), `sharedconfig.ConsumerNames.MemorySvcEpisodes` (Task 2).
- Produces: `config.Config.MemoryWriteSubject string`, `config.Config.EpisodesConsumerName string`. Task 10 (`main.go`) reads both.

- [ ] **Step 1: Write the failing test**

Replace `memory-svc/config/config_test.go` entirely:

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
	MemorySvc         string `json:"memory_svc"`
	MemorySvcEpisodes string `json:"memory_svc_episodes"`
}

type sharedFields struct {
	NATSURL            string        `json:"nats_url"`
	StimulusSubject    string        `json:"stimulus_subject"`
	MemoryWriteSubject string        `json:"memory_write_subject"`
	ConsumerNames      consumerNames `json:"consumer_names"`
}

func writeConfigFile(t *testing.T, natsURL, stimulusSubject, memoryWriteSubject, consumerName, episodesConsumerName string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	data, err := json.Marshal(sharedFields{
		NATSURL:            natsURL,
		StimulusSubject:    stimulusSubject,
		MemoryWriteSubject: memoryWriteSubject,
		ConsumerNames:      consumerNames{MemorySvc: consumerName, MemorySvcEpisodes: episodesConsumerName},
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

	configPath := writeConfigFile(t, "nats://localhost:4222", "soulman.stimulus.raw", "soulman.memory.write", "memory-svc", "memory-svc-episodes")
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
	if cfg.MemoryWriteSubject != "soulman.memory.write" {
		t.Errorf("MemoryWriteSubject = %q, want soulman.memory.write", cfg.MemoryWriteSubject)
	}
	if cfg.EpisodesConsumerName != "memory-svc-episodes" {
		t.Errorf("EpisodesConsumerName = %q, want memory-svc-episodes", cfg.EpisodesConsumerName)
	}
}

func TestLoad_SharedConfigValues(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://remote:4222", "soulman.dev.stimulus.raw", "soulman.dev.memory.write", "memory-svc-dev", "memory-svc-episodes-dev")
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
	if cfg.MemoryWriteSubject != "soulman.dev.memory.write" {
		t.Errorf("MemoryWriteSubject = %q, want soulman.dev.memory.write", cfg.MemoryWriteSubject)
	}
	if cfg.EpisodesConsumerName != "memory-svc-episodes-dev" {
		t.Errorf("EpisodesConsumerName = %q, want memory-svc-episodes-dev", cfg.EpisodesConsumerName)
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

	configPath := writeConfigFile(t, "nats://localhost:4222", "soulman.stimulus.raw", "soulman.memory.write", "", "memory-svc-episodes")
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty consumer_names.memory_svc, got nil")
	}
}

func TestLoad_EmptyNATSURL_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "", "soulman.stimulus.raw", "soulman.memory.write", "memory-svc", "memory-svc-episodes")
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty nats_url, got nil")
	}
}

func TestLoad_EmptyMemoryWriteSubject_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://localhost:4222", "soulman.stimulus.raw", "", "memory-svc", "memory-svc-episodes")
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty memory_write_subject, got nil")
	}
}

func TestLoad_EmptyEpisodesConsumerName_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://localhost:4222", "soulman.stimulus.raw", "soulman.memory.write", "memory-svc", "")
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty consumer_names.memory_svc_episodes, got nil")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./config/... -v` from `memory-svc/`
Expected: FAIL — `cfg.MemoryWriteSubject`/`cfg.EpisodesConsumerName` undefined (compile error)

- [ ] **Step 3: Write the implementation**

Replace `memory-svc/config/config.go` entirely:

```go
package config

import (
	"fmt"
	"os"

	"soulman/common/sharedconfig"
)

type Config struct {
	NATSURL              string
	DatabaseURL          string
	HTTPPort             string
	LogDir               string
	Schema               string
	StimulusSubject      string
	ConsumerName         string
	MemoryWriteSubject   string
	EpisodesConsumerName string
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
	if shared.MemoryWriteSubject == "" {
		return nil, fmt.Errorf("shared config %s has no memory_write_subject configured", configPath)
	}
	if shared.ConsumerNames.MemorySvcEpisodes == "" {
		return nil, fmt.Errorf("shared config %s has no consumer_names.memory_svc_episodes configured", configPath)
	}

	return &Config{
		NATSURL:              shared.NATSURL,
		DatabaseURL:          env("DATABASE_URL", "postgres://postgres:postgres@localhost:54322/postgres"),
		HTTPPort:              env("HTTP_PORT", "9002"),
		LogDir:                env("LOG_DIR", "./logs"),
		Schema:                env("SCHEMA", "memory_dev"),
		StimulusSubject:       shared.StimulusSubject,
		ConsumerName:          shared.ConsumerNames.MemorySvc,
		MemoryWriteSubject:    shared.MemoryWriteSubject,
		EpisodesConsumerName:  shared.ConsumerNames.MemorySvcEpisodes,
	}, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./config/... -v` from `memory-svc/`
Expected: PASS (all tests)

- [ ] **Step 5: Commit**

```bash
git add memory-svc/config/config.go memory-svc/config/config_test.go
git commit -m "feat(memory-svc): load memory_write_subject and episodes consumer name"
```

---

### Task 7: `memory-svc/storage` — the `episodes` table read/write methods

**Prerequisite:** dev's Supabase running and the `episodes` table applied (see Global Constraints). If skipped, this task's DB-backed tests SKIP cleanly rather than FAIL — you can still complete the task and come back to verify later.

**Files:**
- Create: `memory-svc/storage/episodes.go`
- Create: `memory-svc/storage/episodes_test.go`

**Interfaces:**
- Consumes: `common.OutcomeRecord` (Task 1).
- Produces: `Episode` struct; `(db *DB) WriteEpisode(ctx context.Context, streamSeq uint64, rec *common.OutcomeRecord) error` (nil-receiver-safe — returns an error, doesn't panic, if `db == nil`); `(db *DB) GetRecentEpisodes(ctx context.Context, limit int) ([]Episode, error)`. Task 8's `natsconsumer.EpisodeWriter` interface is satisfied by `*DB.WriteEpisode`; Task 9's HTTP handler calls `GetRecentEpisodes`.

- [ ] **Step 1: Write the failing test**

Create `memory-svc/storage/episodes_test.go`:

```go
package storage_test

import (
	"context"
	"testing"
	"time"

	"soulman/common"
	"soulman/memory-svc/storage"
)

func TestDB_WriteEpisode_And_GetRecentEpisodes(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	seq := uint64(time.Now().UnixNano())
	rec := &common.OutcomeRecord{
		Type:       "action_log",
		ActionType: "triage_gmail_email",
		Status:     "success",
		TaskID:     "task-1",
		OccurredAt: time.Now().UTC(),
		Summary:    "Test email — important",
		Decision:   "notified via Discord",
		Tags:       []string{"gmail", "triage"},
	}

	if err := db.WriteEpisode(ctx, seq, rec); err != nil {
		t.Fatalf("WriteEpisode: %v", err)
	}
	t.Cleanup(func() {
		db.ExecCleanup(context.Background(), "DELETE FROM memory_dev.episodes WHERE stream_seq = $1", int64(seq))
	})

	rows, err := db.GetRecentEpisodes(ctx, 20)
	if err != nil {
		t.Fatalf("GetRecentEpisodes: %v", err)
	}

	var found *storage.Episode
	for i := range rows {
		if rows[i].StreamSeq == seq {
			found = &rows[i]
		}
	}
	if found == nil {
		t.Fatal("inserted episode not found in GetRecentEpisodes")
	}
	if found.Summary != rec.Summary || found.Decision != rec.Decision || found.ActionType != rec.ActionType {
		t.Errorf("episode = %+v, want summary/decision/action_type to match %+v", found, rec)
	}
	if len(found.Tags) != 2 || found.Tags[0] != "gmail" || found.Tags[1] != "triage" {
		t.Errorf("Tags = %v, want [gmail triage]", found.Tags)
	}
	if found.TaskID == nil || *found.TaskID != "task-1" {
		t.Errorf("TaskID = %v, want task-1", found.TaskID)
	}
}

func TestDB_WriteEpisode_DedupsByStreamSeq(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	seq := uint64(time.Now().UnixNano())
	rec := &common.OutcomeRecord{ActionType: "probe", Status: "success", OccurredAt: time.Now().UTC(), Summary: "s", Decision: "d"}

	if err := db.WriteEpisode(ctx, seq, rec); err != nil {
		t.Fatalf("first WriteEpisode: %v", err)
	}
	t.Cleanup(func() {
		db.ExecCleanup(context.Background(), "DELETE FROM memory_dev.episodes WHERE stream_seq = $1", int64(seq))
	})
	if err := db.WriteEpisode(ctx, seq, rec); err != nil {
		t.Errorf("second WriteEpisode (ON CONFLICT DO NOTHING) errored: %v", err)
	}

	rows, err := db.GetRecentEpisodes(ctx, 100)
	if err != nil {
		t.Fatalf("GetRecentEpisodes: %v", err)
	}
	count := 0
	for _, e := range rows {
		if e.StreamSeq == seq {
			count++
		}
	}
	if count != 1 {
		t.Errorf("found %d episodes with stream_seq %d, want 1 (dedup should prevent a duplicate row)", count, seq)
	}
}

func TestDB_WriteEpisode_NilDB_ReturnsErrorNotPanic(t *testing.T) {
	var db *storage.DB // nil receiver — mirrors main.go's "Postgres unavailable" state
	rec := &common.OutcomeRecord{ActionType: "probe", Status: "success", OccurredAt: time.Now().UTC()}
	if err := db.WriteEpisode(context.Background(), 1, rec); err == nil {
		t.Error("WriteEpisode on a nil *DB: want an error, got nil")
	}
}

func TestDB_WriteEpisode_NilTags_StoredAsEmptyNotNull(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	seq := uint64(time.Now().UnixNano())
	rec := &common.OutcomeRecord{ActionType: "probe", Status: "success", OccurredAt: time.Now().UTC(), Summary: "s", Decision: "d"} // Tags left nil

	if err := db.WriteEpisode(ctx, seq, rec); err != nil {
		t.Fatalf("WriteEpisode with nil Tags: %v", err)
	}
	t.Cleanup(func() {
		db.ExecCleanup(context.Background(), "DELETE FROM memory_dev.episodes WHERE stream_seq = $1", int64(seq))
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./storage/... -run TestDB_WriteEpisode -v` from `memory-svc/`
Expected: FAIL — `db.WriteEpisode`/`db.GetRecentEpisodes`/`storage.Episode` undefined (compile error)

- [ ] **Step 3: Write the implementation**

Create `memory-svc/storage/episodes.go`:

```go
package storage

import (
	"context"
	"fmt"
	"time"

	"soulman/common"
)

type Episode struct {
	ID         int64     `json:"id"`
	StreamSeq  uint64    `json:"stream_seq"`
	OccurredAt time.Time `json:"occurred_at"`
	ReceivedAt time.Time `json:"received_at"`
	Source     string    `json:"source"`
	ActionType string    `json:"action_type"`
	Status     string    `json:"status"`
	TaskID     *string   `json:"task_id,omitempty"`
	Summary    string    `json:"summary"`
	Decision   string    `json:"decision"`
	Outcome    string    `json:"outcome"`
	Tags       []string  `json:"tags"`
}

// WriteEpisode records an action-svc outcome as an episode. streamSeq is
// the MEMORY_WRITE JetStream message's stream sequence number, used (not
// rec.TaskID, which is sometimes empty) as the dedup key on redelivery.
// Safe to call on a nil *DB (returns an error instead of panicking) so the
// MEMORY_WRITE consumer can NAK-and-retry when Postgres is down, the same
// way the rest of this package treats DB unavailability as non-fatal.
func (db *DB) WriteEpisode(ctx context.Context, streamSeq uint64, rec *common.OutcomeRecord) error {
	if db == nil {
		return fmt.Errorf("postgres: db unavailable")
	}

	var taskID *string
	if rec.TaskID != "" {
		taskID = &rec.TaskID
	}
	tags := rec.Tags
	if tags == nil {
		tags = []string{}
	}

	_, err := db.pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.episodes
			(stream_seq, occurred_at, source, action_type, status, task_id,
			 summary, decision, outcome, tags)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (stream_seq) DO NOTHING
	`, db.schema),
		int64(streamSeq),
		rec.OccurredAt,
		"action-svc",
		rec.ActionType,
		rec.Status,
		taskID,
		rec.Summary,
		rec.Decision,
		rec.Status,
		tags,
	)
	if err != nil {
		return fmt.Errorf("postgres: write episode (stream_seq %d): %w", streamSeq, err)
	}
	return nil
}

func (db *DB) GetRecentEpisodes(ctx context.Context, limit int) ([]Episode, error) {
	rows, err := db.pool.Query(ctx, fmt.Sprintf(`
		SELECT id, stream_seq, occurred_at, received_at, source, action_type,
		       status, task_id, summary, decision, outcome, tags
		FROM %s.episodes
		WHERE forgotten_at IS NULL
		ORDER BY received_at DESC
		LIMIT $1
	`, db.schema), limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: query recent episodes: %w", err)
	}
	defer rows.Close()

	var results []Episode
	for rows.Next() {
		var e Episode
		var streamSeq int64
		if err := rows.Scan(
			&e.ID, &streamSeq, &e.OccurredAt, &e.ReceivedAt, &e.Source,
			&e.ActionType, &e.Status, &e.TaskID, &e.Summary, &e.Decision,
			&e.Outcome, &e.Tags,
		); err != nil {
			return nil, fmt.Errorf("postgres: scan episode: %w", err)
		}
		e.StreamSeq = uint64(streamSeq)
		results = append(results, e)
	}
	return results, rows.Err()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./storage/... -v` from `memory-svc/`
Expected: PASS if dev's Supabase is running with the `episodes` table applied; SKIP (not FAIL) otherwise, matching every other DB-backed test in this package

- [ ] **Step 5: Commit**

```bash
git add memory-svc/storage/episodes.go memory-svc/storage/episodes_test.go
git commit -m "feat(memory-svc): add episodes table read/write methods"
```

---

### Task 8: `memory-svc/natsconsumer` — `MemoryWriteConsumer`

**Files:**
- Create: `memory-svc/natsconsumer/memory_write_consumer.go`
- Create: `memory-svc/natsconsumer/memory_write_consumer_test.go`
- Modify: `memory-svc/natsconsumer/consumer_test.go` (extend the shared `TestMain` to also purge `MEMORY_WRITE`)

**Interfaces:**
- Consumes: `common.OutcomeRecord` (Task 1).
- Produces: `EpisodeWriter` interface (`WriteEpisode(ctx context.Context, streamSeq uint64, rec *common.OutcomeRecord) error` — satisfied by `*storage.DB`, Task 7); `MemoryWriteConsumer` with `NewMemoryWriteConsumer(natsURL, consumerName, subject string, w EpisodeWriter) (*MemoryWriteConsumer, error)`, `Start(ctx) error`, `Close()`. Task 10 (`main.go`) constructs and wires this.

- [ ] **Step 1: Write the failing tests**

First, extend the existing `TestMain` in `memory-svc/natsconsumer/consumer_test.go` to also purge `MEMORY_WRITE` (replace just the `TestMain` function):

```go
func TestMain(m *testing.M) {
	// Purge any lingering messages from previous test runs so new consumers
	// (which start from the beginning of the stream by default) aren't flooded
	// with old NAK'd messages.
	nc, err := nats.Connect(natsURL())
	if err == nil {
		if js, err := jetstream.New(nc); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if stream, err := js.Stream(ctx, "STIMULUS"); err == nil {
				stream.Purge(ctx)
			}
			if stream, err := js.Stream(ctx, "MEMORY_WRITE"); err == nil {
				stream.Purge(ctx)
			}
			cancel()
		}
		nc.Close()
	}
	os.Exit(m.Run())
}
```

Then create `memory-svc/natsconsumer/memory_write_consumer_test.go`:

```go
package natsconsumer_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"soulman/common"
	"soulman/memory-svc/natsconsumer"
)

type mockEpisodeWriter struct {
	mu       sync.Mutex
	received []*common.OutcomeRecord
}

func (m *mockEpisodeWriter) WriteEpisode(_ context.Context, _ uint64, rec *common.OutcomeRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.received = append(m.received, rec)
	return nil
}

func (m *mockEpisodeWriter) hasTaskID(taskID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.received {
		if r.TaskID == taskID {
			return true
		}
	}
	return false
}

func TestMemoryWriteConsumer_ReceivesMessage(t *testing.T) {
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

	consName := fmt.Sprintf("test-mw-%d", time.Now().UnixNano())
	w := &mockEpisodeWriter{}
	cons, err := natsconsumer.NewMemoryWriteConsumer(url, consName, "soulman.memory.write", w)
	if err != nil {
		t.Fatalf("NewMemoryWriteConsumer: %v", err)
	}
	defer cons.Close()

	if err := cons.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	taskID := fmt.Sprintf("mw-test-%d", time.Now().UnixNano())
	rec := common.OutcomeRecord{Type: "action_log", ActionType: "probe", Status: "success", TaskID: taskID, OccurredAt: time.Now().UTC()}
	b, _ := json.Marshal(rec)
	if _, err := js.Publish(ctx, "soulman.memory.write", b); err != nil {
		t.Fatalf("publish: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if w.hasTaskID(taskID) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("outcome record %s not received by writer within 5 seconds", taskID)
}

func TestMemoryWriteConsumer_BadJSON_IsACKedAndSkipped(t *testing.T) {
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

	consName := fmt.Sprintf("test-mw-bad-%d", time.Now().UnixNano())
	w := &mockEpisodeWriter{}
	cons, err := natsconsumer.NewMemoryWriteConsumer(url, consName, "soulman.memory.write", w)
	if err != nil {
		t.Fatalf("NewMemoryWriteConsumer: %v", err)
	}
	defer cons.Close()

	if err := cons.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := js.Publish(ctx, "soulman.memory.write", []byte("not json")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	time.Sleep(2 * time.Second)

	taskID := fmt.Sprintf("mw-after-bad-%d", time.Now().UnixNano())
	rec := common.OutcomeRecord{Type: "action_log", ActionType: "probe", Status: "success", TaskID: taskID}
	b, _ := json.Marshal(rec)
	js.Publish(ctx, "soulman.memory.write", b)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if w.hasTaskID(taskID) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("consumer did not recover after bad JSON message")
}

func TestMemoryWriteConsumer_UnknownType_IsACKedAndSkipped(t *testing.T) {
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

	consName := fmt.Sprintf("test-mw-type-%d", time.Now().UnixNano())
	w := &mockEpisodeWriter{}
	cons, err := natsconsumer.NewMemoryWriteConsumer(url, consName, "soulman.memory.write", w)
	if err != nil {
		t.Fatalf("NewMemoryWriteConsumer: %v", err)
	}
	defer cons.Close()

	if err := cons.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	taskID := fmt.Sprintf("mw-unknown-type-%d", time.Now().UnixNano())
	rec := common.OutcomeRecord{Type: "future_type", ActionType: "probe", Status: "success", TaskID: taskID}
	b, _ := json.Marshal(rec)
	if _, err := js.Publish(ctx, "soulman.memory.write", b); err != nil {
		t.Fatalf("publish: %v", err)
	}

	time.Sleep(2 * time.Second)
	if w.hasTaskID(taskID) {
		t.Errorf("writer should not have received a record with an unknown type")
	}
}

type countingErrEpisodeWriter struct {
	mu    sync.Mutex
	count int
}

func (w *countingErrEpisodeWriter) WriteEpisode(_ context.Context, _ uint64, _ *common.OutcomeRecord) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.count++
	return errors.New("simulated write failure")
}

func (w *countingErrEpisodeWriter) Count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.count
}

func TestMemoryWriteConsumer_WriteError_NaksMessage(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	consName := fmt.Sprintf("test-mw-nak-%d", time.Now().UnixNano())
	w := &countingErrEpisodeWriter{}
	cons, err := natsconsumer.NewMemoryWriteConsumer(url, consName, "soulman.memory.write", w)
	if err != nil {
		t.Fatalf("NewMemoryWriteConsumer: %v", err)
	}
	defer cons.Close()

	if err := cons.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	rec := common.OutcomeRecord{Type: "action_log", ActionType: "probe", Status: "success", TaskID: fmt.Sprintf("mw-nak-%d", time.Now().UnixNano())}
	b, _ := json.Marshal(rec)
	if _, err := js.Publish(ctx, "soulman.memory.write", b); err != nil {
		t.Fatalf("publish: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if w.Count() >= 2 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Errorf("expected WriteEpisode to be called >= 2 times (NAK + redelivery), got %d", w.Count())
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./natsconsumer/... -v` from `memory-svc/`
Expected: FAIL — `natsconsumer.NewMemoryWriteConsumer` undefined (compile error)

- [ ] **Step 3: Write the implementation**

Create `memory-svc/natsconsumer/memory_write_consumer.go`:

```go
package natsconsumer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"soulman/common"
)

// EpisodeWriter is satisfied by *storage.DB. Defined here to avoid import cycles.
type EpisodeWriter interface {
	WriteEpisode(ctx context.Context, streamSeq uint64, rec *common.OutcomeRecord) error
}

// MemoryWriteConsumer durably consumes the MEMORY_WRITE stream. Unlike
// Consumer (STIMULUS), there is no local file-log/replay layer here — on a
// WriteEpisode error the message is NAK'd and JetStream's own 30-day
// MEMORY_WRITE retention is the durability backstop, since episodes aren't
// the sacred immutable audit log raw_inputs is. See
// docs/superpowers/specs/2026-07-18-memory-episodes-design.md.
type MemoryWriteConsumer struct {
	nc           *nats.Conn
	js           jetstream.JetStream
	writer       EpisodeWriter
	consumerName string
	subject      string
	cc           jetstream.ConsumeContext
	wg           sync.WaitGroup
}

// NewMemoryWriteConsumer connects to NATS. consumerName must be unique per
// environment sharing the MEMORY_WRITE stream (e.g. "memory-svc-episodes"
// for prod, "memory-svc-episodes-dev" for dev) — and distinct from the
// STIMULUS consumer's name, since JetStream identifies a durable consumer
// by (stream, name).
func NewMemoryWriteConsumer(natsURL, consumerName, subject string, w EpisodeWriter) (*MemoryWriteConsumer, error) {
	nc, err := nats.Connect(natsURL)
	if err != nil {
		return nil, fmt.Errorf("nats: connect to %s: %w", natsURL, err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats: jetstream: %w", err)
	}

	return &MemoryWriteConsumer{nc: nc, js: js, writer: w, consumerName: consumerName, subject: subject}, nil
}

// Start ensures the MEMORY_WRITE stream exists (defensively — memory-svc
// might start before action-svc has ever published, the same reasoning
// documented for action-svc's own defensive THINKING_REQUEST ensure in
// docs/superpowers/specs/2026-07-18-pipeline-debugging-tools-design.md),
// then subscribes and processes messages in the NATS library goroutine.
// Returns after the subscription is established; messages arrive
// asynchronously. Call Close to stop.
func (c *MemoryWriteConsumer) Start(ctx context.Context) error {
	_, err := c.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     "MEMORY_WRITE",
		Subjects: []string{"soulman.memory.write", "soulman.dev.memory.write"},
		MaxAge:   30 * 24 * time.Hour,
	})
	if err != nil {
		return fmt.Errorf("nats: ensure MEMORY_WRITE stream: %w", err)
	}

	stream, err := c.js.Stream(ctx, "MEMORY_WRITE")
	if err != nil {
		return fmt.Errorf("nats: get MEMORY_WRITE stream: %w", err)
	}

	cons, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Name:          c.consumerName,
		Durable:       c.consumerName,
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: c.subject,
	})
	if err != nil {
		return fmt.Errorf("nats: create consumer %s: %w", c.consumerName, err)
	}

	cc, err := cons.Consume(func(msg jetstream.Msg) {
		c.wg.Add(1)
		defer c.wg.Done()

		var rec common.OutcomeRecord
		if err := json.Unmarshal(msg.Data(), &rec); err != nil {
			log.Printf("nats: unparseable MEMORY_WRITE message (subject %s), ACKing to skip: %v", msg.Subject(), err)
			msg.Ack()
			return
		}

		if rec.Type != "action_log" {
			log.Printf("nats: MEMORY_WRITE message with unknown type %q, ACKing to skip", rec.Type)
			msg.Ack()
			return
		}

		meta, err := msg.Metadata()
		if err != nil {
			log.Printf("nats: MEMORY_WRITE message metadata unavailable, NAKing for redelivery: %v", err)
			msg.Nak()
			return
		}

		if err := c.writer.WriteEpisode(context.Background(), meta.Sequence.Stream, &rec); err != nil {
			log.Printf("nats: episode write failed (stream_seq %d), NAKing for redelivery: %v", meta.Sequence.Stream, err)
			msg.Nak()
			return
		}

		msg.Ack()
	})
	if err != nil {
		return fmt.Errorf("nats: consume: %w", err)
	}

	c.cc = cc
	log.Printf("nats: consuming MEMORY_WRITE stream as %q (subject %q)", c.consumerName, c.subject)
	return nil
}

func (c *MemoryWriteConsumer) Close() {
	if c.cc != nil {
		c.cc.Stop()
	}
	c.wg.Wait()
	c.nc.Drain()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./natsconsumer/... -v` from `memory-svc/`
Expected: PASS if `NATS_URL` is reachable; SKIP (not FAIL) otherwise, matching the existing STIMULUS consumer tests

- [ ] **Step 5: Commit**

```bash
git add memory-svc/natsconsumer/memory_write_consumer.go memory-svc/natsconsumer/memory_write_consumer_test.go memory-svc/natsconsumer/consumer_test.go
git commit -m "feat(memory-svc): add MemoryWriteConsumer for the MEMORY_WRITE stream"
```

---

### Task 9: `memory-svc/httpserver` — `GET /memory/episodes`

**Files:**
- Modify: `memory-svc/httpserver/server.go`
- Modify: `memory-svc/httpserver/server_test.go`

**Interfaces:**
- Consumes: `storage.Episode`, `(db *storage.DB) GetRecentEpisodes` (Task 7).
- Produces: `GET /memory/episodes?limit=N` — JSON array of `storage.Episode`, `503` if DB unavailable. `/memory/search`, `/memory/procedures`, `/memory/goals` remain `501` stubs.

- [ ] **Step 1: Write the failing tests**

Replace `memory-svc/httpserver/server_test.go`'s `TestMemoryStubs_Return501` (drop `/memory/episodes` from the stub list) and add two new tests — the whole file becomes:

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
	paths := []string{"/memory/search", "/memory/procedures", "/memory/goals"}

	for _, path := range paths {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s: status = %d, want 501", path, rec.Code)
		}
	}
}

func TestMemoryEpisodes_NilDB_Returns503(t *testing.T) {
	srv := httpserver.New(nil, "9002")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/memory/episodes", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
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

func TestMemoryEpisodes_DefaultLimit(t *testing.T) {
	srv := httpserver.New(nil, "9002")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/memory/episodes?limit=abc", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusBadRequest {
		t.Error("bad limit param should be silently ignored, not return 400")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./httpserver/... -v` from `memory-svc/`
Expected: FAIL — `TestMemoryEpisodes_NilDB_Returns503` and `TestMemoryEpisodes_DefaultLimit` fail (route still returns 501, not 503, since `/memory/episodes` is still wired to `stub`)

- [ ] **Step 3: Write the implementation**

Replace `memory-svc/httpserver/server.go` entirely:

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
	r.Get("/memory/episodes", s.memoryEpisodes)
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

func (s *Server) memoryEpisodes(w http.ResponseWriter, r *http.Request) {
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

	rows, err := s.db.GetRecentEpisodes(r.Context(), limit)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if rows == nil {
		rows = []storage.Episode{}
	}
	json.NewEncoder(w).Encode(rows)
}

func stub(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Not Implemented", http.StatusNotImplemented)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./httpserver/... -v` from `memory-svc/`
Expected: PASS (all tests)

- [ ] **Step 5: Commit**

```bash
git add memory-svc/httpserver/server.go memory-svc/httpserver/server_test.go
git commit -m "feat(memory-svc): implement GET /memory/episodes"
```

---

### Task 10: `memory-svc/main.go` — wire the second consumer

**Files:**
- Modify: `memory-svc/main.go`

**Interfaces:**
- Consumes: `natsconsumer.NewMemoryWriteConsumer` (Task 8), `config.Config.MemoryWriteSubject`/`EpisodesConsumerName` (Task 6), `*storage.DB` as an `EpisodeWriter` (Task 7).
- Produces: nothing further downstream — this is the final wiring task.

- [ ] **Step 1: Write the implementation**

`memory-svc/main.go` has no dedicated test file (matching the existing pattern — it's pure wiring). Replace it entirely:

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
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

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

	// STIMULUS consumer
	cons, err := natsconsumer.New(cfg.NATSURL, cfg.ConsumerName, cfg.StimulusSubject, w)
	if err != nil {
		log.Fatalf("nats: %v", err)
	}
	defer cons.Close()

	if err := cons.Start(ctx); err != nil {
		log.Fatalf("nats start: %v", err)
	}

	// MEMORY_WRITE (episodes) consumer — wired independently of the STIMULUS
	// consumer above, so a hiccup in one never silently disables the other
	// (the "keep dual consumer setup independent" lesson documented in
	// action-svc/NOTES.md). db may be nil if Postgres is down; WriteEpisode
	// handles that safely (returns an error, NATS NAKs and retries later).
	episodeCons, err := natsconsumer.NewMemoryWriteConsumer(cfg.NATSURL, cfg.EpisodesConsumerName, cfg.MemoryWriteSubject, db)
	if err != nil {
		log.Fatalf("nats (memory write): %v", err)
	}
	defer episodeCons.Close()

	if err := episodeCons.Start(ctx); err != nil {
		log.Fatalf("nats start (memory write): %v", err)
	}

	// HTTP server (non-blocking)
	srv := httpserver.New(db, cfg.HTTPPort)
	log.Printf("HTTP listening on :%s", cfg.HTTPPort)
	go func() {
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

- [ ] **Step 2: Confirm the whole module builds and tests pass**

Run: `go build ./...` from `memory-svc/`
Expected: no errors

Run: `go vet ./...` from `memory-svc/`
Expected: no errors

Run: `go test ./... -v` from `memory-svc/`
Expected: all tests PASS or SKIP (NATS/DB-dependent ones skip if unreachable), no FAIL

- [ ] **Step 3: Commit**

```bash
git add memory-svc/main.go
git commit -m "feat(memory-svc): wire MemoryWriteConsumer alongside the STIMULUS consumer"
```

---

### Task 11: Docs — `memory-svc/NOTES.md` and root `CLAUDE.md` updates

**Files:**
- Create: `memory-svc/NOTES.md`
- Modify: `CLAUDE.md` (repo root)

**Interfaces:** none — documentation only.

- [ ] **Step 1: Create `memory-svc/NOTES.md`**

```markdown
# memory-svc — Operational Notes

Incidents, gotchas, and decisions learned running this service — not captured in the design specs themselves (see `CLAUDE.md`'s Services section for spec links).

## Episodes consumer has no file-log/replay layer

Unlike the STIMULUS consumer (`natsconsumer.Consumer`), `MemoryWriteConsumer` doesn't write to a local file log before acking — on a DB write failure it NAKs and relies purely on JetStream's own 30-day `MEMORY_WRITE` retention for redelivery. This was a deliberate first-cut simplification (see `docs/superpowers/specs/2026-07-18-memory-episodes-design.md`), not an oversight: episodes aren't the sacred immutable audit log `raw_inputs` is, so skipping the extra local-durability layer was an acceptable tradeoff against duplicating the STIMULUS consumer's more complex replay machinery for a second stream.

## Episode dedup uses JetStream stream sequence, not task_id

`action-svc`'s `OutcomeRecord.TaskID` is sometimes empty (the daily-report cron has no per-message correlation ID), so it can't be a unique dedup key. `episodes.stream_seq` (the MEMORY_WRITE message's JetStream stream sequence number, from `msg.Metadata().Sequence.Stream`) is used instead — `ON CONFLICT (stream_seq) DO NOTHING` on insert.

## The episodes table isn't created by memory-svc

Same as `raw_inputs`: `memory-svc` never runs its own DDL. The `episodes` table is applied by hand once per environment via `docs/superpowers/specs/sql/2026-07-18-episodes-table.sql`. As of this writing it's applied to `memory_dev` only — `memory_prod`'s schema doesn't exist yet at all (see root `CLAUDE.md`).
```

- [ ] **Step 2: Update root `CLAUDE.md`**

In the Repository Structure table, change the `memory-svc/` row from:

```
| `memory-svc/`                   | Go service — Memory module runtime (`:9002`)                                    |
```

to:

```
| `memory-svc/`                   | Go service — Memory module runtime (`:9002`). See `memory-svc/NOTES.md`.        |
```

In the Services numbered list, replace item 1 (`memory-svc`) from:

```
1. **`memory-svc`** — consumes `soulman.stimulus.raw`, writes to `raw_inputs.jsonl` then Postgres (`memory_dev`/`memory_prod` schema).
   - Specs: `2026-06-27-memory-svc-design.md`
```

to:

```
1. **`memory-svc`** — consumes `soulman.stimulus.raw`, writes to `raw_inputs.jsonl` then Postgres (`memory_dev`/`memory_prod` schema). Also durably consumes `action-svc`'s `soulman.memory.write` outcome records into an `episodes` table, exposed read-only via `GET /memory/episodes`; `/memory/search`, `/memory/procedures`, `/memory/goals` remain unimplemented stubs.
   - Specs: `2026-06-27-memory-svc-design.md`, `2026-07-18-memory-episodes-design.md`
   - Notes: `memory-svc/NOTES.md`
```

In the Shared modules section, change the `common` bullet from:

```
- **`common`** — `Stimulus` and `ActionRequest` wire-format types (imported via `replace soulman/common => ../common` in each service's `go.mod`) plus the `sharedconfig` schema for `config/dev.json`/`config/prod.json`. Specs: `2026-07-17-common-module-design.md`, `2026-07-18-shared-config-design.md`, `2026-07-18-shared-config-nats-design.md`. Notes: `common/NOTES.md`.
```

to:

```
- **`common`** — `Stimulus`, `ActionRequest`, and `OutcomeRecord` wire-format types (imported via `replace soulman/common => ../common` in each service's `go.mod`) plus the `sharedconfig` schema for `config/dev.json`/`config/prod.json`. Specs: `2026-07-17-common-module-design.md`, `2026-07-18-shared-config-design.md`, `2026-07-18-shared-config-nats-design.md`, `2026-07-18-memory-episodes-design.md`. Notes: `common/NOTES.md`.
```

- [ ] **Step 3: Commit**

```bash
git add memory-svc/NOTES.md CLAUDE.md
git commit -m "docs: document episodes work in memory-svc/NOTES.md and CLAUDE.md"
```
