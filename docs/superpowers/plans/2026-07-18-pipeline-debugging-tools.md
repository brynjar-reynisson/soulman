# Pipeline Debugging Tools Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `soulman.thinking.request` and `soulman.memory.write` durable JetStream streams (fixing the silent-drop bug the Gmail incident exposed), add a generic Stimulus-injection endpoint + CLI subcommand for testing without touching real external systems, and add a Discord history-reading tool so sent messages can be verified directly.

**Architecture:** Two new JetStream streams (`THINKING_REQUEST`, `MEMORY_WRITE`), created idempotently by the Go code that publishes/consumes them (`jetstream.CreateOrUpdateStream`) rather than manual `nats` CLI setup. `thinking-svc`'s publisher and `action-svc`'s consumer/outcome-publisher move from core NATS to JetStream, mirroring the exact pattern `perception-svc/natspublish` and `thinking-svc/natsclient/consumer.go` already established for the `STIMULUS` stream. A new `perception-svc` endpoint and `discordread` package are thin, focused additions alongside the existing `/api/perceive/cli` and `action-svc/notify` code. The `soulman` CLI gains two new subcommands.

**Tech Stack:** Go, `github.com/nats-io/nats.go/jetstream` (already a dependency via `STIMULUS` stream usage).

## Global Constraints

- `THINKING_REQUEST` stream subjects: `soulman.thinking.request`, `soulman.dev.thinking.request`. `MEMORY_WRITE` stream subjects: `soulman.memory.write`, `soulman.dev.memory.write`. Both `MaxAge: 30 * 24 * time.Hour` (30 days), matching `STIMULUS`'s existing retention.
- Streams are created idempotently (`CreateOrUpdateStream`) by the services that use them — no manual `nats` CLI setup required, and safe for both `thinking-svc` and `action-svc` to independently ensure `THINKING_REQUEST` exists (whichever starts first "wins" harmlessly, per NATS Global Constraints elsewhere in this repo about idempotent config).
- `action-svc`'s new durable consumer names: `action-svc` (prod), `action-svc-dev` (dev) — added to `common/sharedconfig.ConsumerNames` as `ActionSvc`, following the exact `MemorySvc`/`ThinkingSvc` precedent.
- `MEMORY_WRITE` gets a durable stream but no consumer — nothing reads this subject today either; durability alone (inspectable via `nats stream view`) is the explicit scope.
- Ack policy for the new `action-svc` consumer: ACK unconditionally after calling the handler regardless of its outcome — matching `thinking-svc`'s existing consumer exactly (no NATS-level redelivery; `dispatch.go`'s own retry-once-then-give-up logic is the only retry layer).
- `/api/perceive/raw` and the `discordread` package have no new authentication — same trust model as every other endpoint in this single-user local project.
- No automated test for `discordread`'s HTTP-calling code — verified manually against the real bot/channel, per the same precedent `gmailwatcher/client.go` established for `client.go`'s real Gmail API calls.

---

### Task 1: `common/sharedconfig` gains an `ActionSvc` consumer name

**Files:**
- Modify: `common/sharedconfig/config.go`
- Modify: `common/sharedconfig/config_test.go`

**Interfaces:**
- Produces: `sharedconfig.ConsumerNames.ActionSvc string` (json `action_svc`) — Task 6 reads this exact field.

- [ ] **Step 1: Write the failing test**

Add to `common/sharedconfig/config_test.go`:

```go
func TestLoad_ActionSvcConsumerName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"watch_paths": [],
		"consumer_names": {
			"memory_svc": "memory-svc",
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
	if cfg.ConsumerNames.ActionSvc != "action-svc" {
		t.Errorf("ConsumerNames.ActionSvc = %q, want action-svc", cfg.ConsumerNames.ActionSvc)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go -C common test ./sharedconfig/...`
Expected: FAIL — compile error, `cfg.ConsumerNames.ActionSvc` undefined.

- [ ] **Step 3: Implement the schema change**

In `common/sharedconfig/config.go`, update the `ConsumerNames` struct:

```go
// ConsumerNames holds the JetStream durable consumer name for each service
// that has one: memory-svc and thinking-svc (consuming the STIMULUS
// stream), and action-svc (consuming the THINKING_REQUEST stream).
// perception-svc only publishes, so it has no consumer name here.
type ConsumerNames struct {
	MemorySvc   string `json:"memory_svc"`
	ThinkingSvc string `json:"thinking_svc"`
	ActionSvc   string `json:"action_svc"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go -C common test ./sharedconfig/...`
Expected: PASS (all tests, old and new)

- [ ] **Step 5: Commit**

```bash
git add common/sharedconfig/config.go common/sharedconfig/config_test.go
git commit -m "feat(common): add action_svc consumer name to shared config schema"
```

---

### Task 2: Add `action_svc` consumer name to `config/dev.json` and `config/prod.json`

**Files:**
- Modify: `config/dev.json`
- Modify: `config/prod.json`

- [ ] **Step 1: Update `config/dev.json`**

Add `"action_svc": "action-svc-dev"` to the `consumer_names` object.

- [ ] **Step 2: Update `config/prod.json`**

Add `"action_svc": "action-svc"` to the `consumer_names` object.

- [ ] **Step 3: Verify both files parse as valid JSON**

Run: `powershell -Command "Get-Content config/dev.json | ConvertFrom-Json | Out-Null; Get-Content config/prod.json | ConvertFrom-Json | Out-Null; Write-Output OK"`
Expected: prints `OK`.

- [ ] **Step 4: Commit**

```bash
git add config/dev.json config/prod.json
git commit -m "chore: add action_svc consumer name to config/dev.json and config/prod.json"
```

---

### Task 3: `thinking-svc`'s publisher becomes JetStream-backed

**Files:**
- Modify: `thinking-svc/natsclient/publisher.go`
- Modify: `thinking-svc/natsclient/publisher_test.go`
- Modify: `thinking-svc/main.go`

**Interfaces:**
- Produces: `natsclient.NewPublisher(ctx context.Context, natsURL, subject string) (*Publisher, error)` (signature gains a leading `ctx`) — Task 6 is a different service and unaffected; only `thinking-svc/main.go`'s own call site changes.

- [ ] **Step 1: Write the failing test**

Replace `thinking-svc/natsclient/publisher_test.go` in full with:

```go
package natsclient_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"soulman/thinking-svc/natsclient"
)

func TestPublisher_Publish_DeliversToSubject(t *testing.T) {
	url := natsURL()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer nc.Close()

	sub, err := nc.SubscribeSync("soulman.thinking.request")
	if err != nil {
		t.Fatalf("SubscribeSync: %v", err)
	}
	defer sub.Unsubscribe()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pub, err := natsclient.NewPublisher(ctx, url, "soulman.thinking.request")
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	defer pub.Close()

	type testRequest struct {
		CorrelationID string `json:"correlation_id"`
		ActionHint    string `json:"action_hint"`
	}
	req := testRequest{CorrelationID: "corr-001", ActionHint: "append_daily_report_entry"}

	if err := pub.Publish(ctx, req); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	msg, err := sub.NextMsg(5 * time.Second)
	if err != nil {
		t.Fatalf("NextMsg: %v", err)
	}

	var got testRequest
	if err := json.Unmarshal(msg.Data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.CorrelationID != "corr-001" {
		t.Errorf("CorrelationID = %q, want corr-001", got.CorrelationID)
	}
	if got.ActionHint != "append_daily_report_entry" {
		t.Errorf("ActionHint = %q, want append_daily_report_entry", got.ActionHint)
	}
}

func TestNewPublisher_CreatesThinkingRequestStream(t *testing.T) {
	url := natsURL()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer nc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pub, err := natsclient.NewPublisher(ctx, url, "soulman.thinking.request")
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	defer pub.Close()

	// If the stream wasn't created, this publish (which the constructor
	// already exercised once, but we verify explicitly here too) would
	// fail with a "no stream matches subject" style JetStream error.
	if err := pub.Publish(ctx, map[string]string{"probe": "ok"}); err != nil {
		t.Errorf("Publish after NewPublisher: %v, want nil (THINKING_REQUEST stream should exist)", err)
	}
}
```

Note: this file already has a `natsURL()` helper defined in the same test package (in `consumer_test.go`) — do not redefine it here.

- [ ] **Step 2: Run test to verify it fails**

Run: `go -C thinking-svc test ./natsclient/...`
Expected: FAIL — compile error, `NewPublisher` called with 3 args but the current signature takes 2 (no `ctx`).

- [ ] **Step 3: Implement the JetStream publisher**

Replace `thinking-svc/natsclient/publisher.go` in full with:

```go
package natsclient

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Publisher publishes Action Requests to the configured subject
// (soulman.thinking.request by default) via JetStream — durable, so a
// message survives even if action-svc isn't running to consume it yet.
// This replaces the original core-NATS fire-and-forget publish, which
// caused roughly half of a real incident's triage decisions to be
// silently dropped (see docs/superpowers/specs/2026-07-18-pipeline-debugging-tools-design.md).
type Publisher struct {
	nc      *nats.Conn
	js      jetstream.JetStream
	subject string
}

// NewPublisher connects to natsURL, ensures the THINKING_REQUEST stream
// exists (creating or updating it idempotently — safe even if action-svc
// also ensures the same stream independently), and returns a Publisher
// bound to subject.
func NewPublisher(ctx context.Context, natsURL, subject string) (*Publisher, error) {
	nc, err := nats.Connect(natsURL)
	if err != nil {
		return nil, fmt.Errorf("nats: connect to %s: %w", natsURL, err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats: jetstream: %w", err)
	}

	_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     "THINKING_REQUEST",
		Subjects: []string{"soulman.thinking.request", "soulman.dev.thinking.request"},
		MaxAge:   30 * 24 * time.Hour,
	})
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats: ensure THINKING_REQUEST stream: %w", err)
	}

	return &Publisher{nc: nc, js: js, subject: subject}, nil
}

// Publish marshals v to JSON and publishes it to the configured subject.
// v is typically a *common.ActionRequest; this package accepts any to avoid
// depending on the rules package.
func (p *Publisher) Publish(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("nats: marshal action request: %w", err)
	}
	if _, err := p.js.Publish(ctx, p.subject, b); err != nil {
		return fmt.Errorf("nats: publish to %s: %w", p.subject, err)
	}
	return nil
}

func (p *Publisher) Close() {
	p.nc.Drain()
}
```

- [ ] **Step 4: Update `thinking-svc/main.go`'s call site**

Change:
```go
	publisher, err := natsclient.NewPublisher(cfg.NATSURL, cfg.ThinkingRequestSubject)
```
to:
```go
	publisher, err := natsclient.NewPublisher(ctx, cfg.NATSURL, cfg.ThinkingRequestSubject)
```

(`ctx` is already in scope from `ctx, cancel := context.WithCancel(context.Background())` a few lines above this call site.)

- [ ] **Step 5: Run tests and build to verify**

Run: `go -C thinking-svc test ./natsclient/...`
Expected: PASS

Run: `go -C thinking-svc build ./...`
Expected: builds with no errors

- [ ] **Step 6: Commit**

```bash
git add thinking-svc/natsclient/publisher.go thinking-svc/natsclient/publisher_test.go thinking-svc/main.go
git commit -m "feat(thinking-svc): publish thinking.request via durable JetStream stream"
```

---

### Task 4: `action-svc` consumes `thinking.request` via a durable JetStream consumer

**Files:**
- Create: `action-svc/natsclient/consumer.go`
- Create: `action-svc/natsclient/consumer_test.go`
- Modify: `action-svc/natsclient/subscriber.go` (remove the now-unused `Subscribe`/`Subscriber`)
- Modify: `action-svc/natsclient/natsclient_test.go` (remove `TestSubscribe_ReceivesMessage`, which tested the removed function)

**Interfaces:**
- Produces: `natsclient.Handler` (unchanged: `func(data []byte)`), `natsclient.NewConsumer(nc *nats.Conn, consumerName, subject string, h Handler) (*Consumer, error)`, `(*Consumer).Start(ctx context.Context) error`, `(*Consumer).Close()` — Task 6 wires these into `main.go`.

- [ ] **Step 1: Write the failing test**

Create `action-svc/natsclient/consumer_test.go`:

```go
package natsclient_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"soulman/action-svc/natsclient"
)

type recordingHandler struct {
	mu       sync.Mutex
	received [][]byte
}

func (h *recordingHandler) handle(data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.received = append(h.received, data)
}

func (h *recordingHandler) contains(want string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.received {
		if string(r) == want {
			return true
		}
	}
	return false
}

func TestConsumer_ReceivesMessage(t *testing.T) {
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

	consName := fmt.Sprintf("test-action-%d", time.Now().UnixNano())
	h := &recordingHandler{}
	cons, err := natsclient.NewConsumer(nc, consName, "soulman.thinking.request", h.handle)
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	defer cons.Close()

	if err := cons.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	payload := fmt.Sprintf(`{"task_id":"action-cons-test-%d"}`, time.Now().UnixNano())
	if _, err := js.Publish(ctx, "soulman.thinking.request", []byte(payload)); err != nil {
		t.Fatalf("publish: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if h.contains(payload) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("payload not received by handler within 5 seconds")
}

func TestConsumer_HandlerPanicsNever_StillACKsExactlyOnce(t *testing.T) {
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

	consName := fmt.Sprintf("test-action-ack-%d", time.Now().UnixNano())
	var mu sync.Mutex
	callCount := 0
	var seenPayload string
	handler := func(data []byte) {
		mu.Lock()
		defer mu.Unlock()
		callCount++
		seenPayload = string(data)
	}
	cons, err := natsclient.NewConsumer(nc, consName, "soulman.thinking.request", handler)
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	defer cons.Close()

	if err := cons.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	payload := fmt.Sprintf(`{"task_id":"action-ack-test-%d"}`, time.Now().UnixNano())
	if _, err := js.Publish(ctx, "soulman.thinking.request", []byte(payload)); err != nil {
		t.Fatalf("publish: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := seenPayload == payload
		mu.Unlock()
		if got {
			// Give a moment for any (incorrect) redelivery to arrive too.
			time.Sleep(1500 * time.Millisecond)
			mu.Lock()
			count := callCount
			mu.Unlock()
			if count != 1 {
				t.Errorf("handler invoked %d times, want exactly 1 (must ACK exactly once)", count)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("payload not received within 3 seconds")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go -C action-svc test ./natsclient/...`
Expected: FAIL — compile error, `natsclient.NewConsumer` undefined.

- [ ] **Step 3: Implement the consumer**

Create `action-svc/natsclient/consumer.go`:

```go
package natsclient

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Handler processes each raw message. The consumer ACKs every message
// regardless of what the handler does — dispatch.Dispatcher.Handle never
// returns an error and has its own retry-once-then-give-up logic; NATS-level
// redelivery here would only risk double-processing, not add any recovery
// this handler doesn't already do itself.
type Handler func(data []byte)

// Consumer durably consumes soulman.thinking.request via JetStream. This
// replaces the original core-NATS ephemeral Subscribe, which silently
// dropped any message published while action-svc wasn't running — the
// root cause of a real incident (see
// docs/superpowers/specs/2026-07-18-pipeline-debugging-tools-design.md).
type Consumer struct {
	js           jetstream.JetStream
	handler      Handler
	consumerName string
	subject      string
	cc           jetstream.ConsumeContext
}

// NewConsumer builds a Consumer against an already-connected nc (shared
// with action-svc's other NATS usage, per main.go's existing single-nc
// pattern). consumerName must be unique per environment sharing the
// THINKING_REQUEST stream (e.g. "action-svc" prod, "action-svc-dev" dev) —
// JetStream identifies a durable consumer by (stream, name).
func NewConsumer(nc *nats.Conn, consumerName, subject string, h Handler) (*Consumer, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("natsclient: jetstream: %w", err)
	}
	return &Consumer{js: js, handler: h, consumerName: consumerName, subject: subject}, nil
}

// Start ensures the THINKING_REQUEST stream exists (idempotent — safe even
// if thinking-svc's publisher already created it), then starts consuming
// subject in the NATS library's own goroutine. Returns once the
// subscription is established; messages arrive asynchronously. Call Close
// to stop.
func (c *Consumer) Start(ctx context.Context) error {
	stream, err := c.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     "THINKING_REQUEST",
		Subjects: []string{"soulman.thinking.request", "soulman.dev.thinking.request"},
		MaxAge:   30 * 24 * time.Hour,
	})
	if err != nil {
		return fmt.Errorf("natsclient: ensure THINKING_REQUEST stream: %w", err)
	}

	cons, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Name:          c.consumerName,
		Durable:       c.consumerName,
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: c.subject,
	})
	if err != nil {
		return fmt.Errorf("natsclient: create consumer %s: %w", c.consumerName, err)
	}

	cc, err := cons.Consume(func(msg jetstream.Msg) {
		c.handler(msg.Data())
		msg.Ack()
	})
	if err != nil {
		return fmt.Errorf("natsclient: consume: %w", err)
	}

	c.cc = cc
	log.Printf("nats: consuming THINKING_REQUEST stream as %q (subject %q)", c.consumerName, c.subject)
	return nil
}

func (c *Consumer) Close() {
	if c.cc != nil {
		c.cc.Stop()
	}
}
```

- [ ] **Step 4: Remove the now-unused `Subscribe`/`Subscriber`**

Replace `action-svc/natsclient/subscriber.go` in full with (keeping only `Connect`, which is still used):

```go
package natsclient

import (
	"fmt"
	"log"
	"time"

	"github.com/nats-io/nats.go"
)

// Connect dials url with self-healing options: if NATS is unreachable on the
// very first attempt, RetryOnFailedConnect makes nats.Connect return
// successfully anyway (with the *nats.Conn in a reconnecting state) instead
// of failing outright, and MaxReconnects(-1) means neither that initial
// outage nor a later one ever causes nats.go to give up retrying. This is
// what lets the dispatch side in main.go come alive on its own once NATS
// becomes reachable, per the design spec's "only the dispatch side is
// degraded until reconnect" — no restart required.
func Connect(url string) (*nats.Conn, error) {
	nc, err := nats.Connect(url,
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			log.Printf("natsclient: disconnected: %v", err)
		}),
		nats.ReconnectHandler(func(c *nats.Conn) {
			log.Printf("natsclient: reconnected to %s", c.ConnectedUrl())
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("natsclient: connect to %s: %w", url, err)
	}
	return nc, nil
}
```

- [ ] **Step 5: Remove the test for the deleted `Subscribe` function**

In `action-svc/natsclient/natsclient_test.go`, delete the entire `TestSubscribe_ReceivesMessage` function (it tested `natsclient.Subscribe`, which no longer exists). Leave `natsURL()`, `TestConnect_RetriesInBackgroundWhenServerUnreachable`, and `TestPublisher_PublishOutcome` untouched — Task 5 updates `TestPublisher_PublishOutcome` separately.

- [ ] **Step 6: Run tests and build to verify**

Run: `go -C action-svc test ./natsclient/...`
Expected: the new consumer tests PASS; `TestPublisher_PublishOutcome` will still be using the old `natsclient.NewPublisher(nc, subject)` two-arg signature at this point in the plan and is expected to still pass as-is (Task 5 changes that function, not this task).

Run: `go -C action-svc build ./...`
Expected: FAILS at this point — `main.go` still calls the now-deleted `natsclient.Subscribe`. This is expected; Task 6 fixes `main.go`. Do not attempt to fix `main.go` in this task.

- [ ] **Step 7: Commit**

```bash
git add action-svc/natsclient/consumer.go action-svc/natsclient/consumer_test.go action-svc/natsclient/subscriber.go action-svc/natsclient/natsclient_test.go
git commit -m "feat(action-svc): add durable JetStream consumer for thinking.request"
```

(This commit intentionally leaves `action-svc` non-building until Task 6 updates `main.go` — this repo's existing convention is small reviewable commits, and `main.go`'s wiring update is a distinct, separately-reviewable change touching a different file.)

---

### Task 5: `action-svc`'s outcome publisher becomes JetStream-backed

**Files:**
- Modify: `action-svc/natsclient/publisher.go`
- Modify: `action-svc/natsclient/natsclient_test.go`

**Interfaces:**
- Produces: `natsclient.NewPublisher(ctx context.Context, nc *nats.Conn, subject string) (*Publisher, error)` (signature gains a leading `ctx` and now returns an error) — Task 6 updates the `main.go` call site.

- [ ] **Step 1: Write the failing test**

In `action-svc/natsclient/natsclient_test.go`, replace the existing `TestPublisher_PublishOutcome` function with:

```go
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
	if err := pub.PublishOutcome("append_daily_report_entry", "success", id); err != nil {
		t.Fatalf("PublishOutcome: %v", err)
	}

	select {
	case msg := <-ch:
		var rec natsclient.OutcomeRecord
		if err := json.Unmarshal(msg.Data, &rec); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if rec.TaskID != id || rec.Status != "success" || rec.Type != "action_log" {
			t.Errorf("outcome = %+v, want task_id=%s status=success type=action_log", rec, id)
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
	if err := pub.PublishOutcome("probe", "success", id); err != nil {
		t.Errorf("PublishOutcome after NewPublisher: %v, want nil (MEMORY_WRITE stream should exist)", err)
	}
}
```

Add `"context"` to this file's import block if not already present (it is, from `TestConnect_RetriesInBackgroundWhenServerUnreachable`'s neighbors — verify and add if missing).

- [ ] **Step 2: Run test to verify it fails**

Run: `go -C action-svc test ./natsclient/...`
Expected: FAIL — compile error, `NewPublisher` called with 3 args (`ctx, nc, subject`) but the current signature takes 2 and returns only `*Publisher` (no error).

- [ ] **Step 3: Implement the JetStream outcome publisher**

Replace `action-svc/natsclient/publisher.go` in full with:

```go
package natsclient

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

type OutcomeRecord struct {
	Type       string `json:"type"`
	ActionType string `json:"action_type"`
	Status     string `json:"status"`
	TaskID     string `json:"task_id"`
}

// Publisher publishes outcome records to the configured subject
// (soulman.memory.write by default) via JetStream — durable, so records
// aren't lost even though nothing consumes this subject yet (kept for
// future use and manual inspection via `nats stream view MEMORY_WRITE`).
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
// still a durable JetStream publish under the hood) an action_log record to
// the configured subject.
func (p *Publisher) PublishOutcome(actionType, status, taskID string) error {
	rec := OutcomeRecord{Type: "action_log", ActionType: actionType, Status: status, TaskID: taskID}
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

- [ ] **Step 4: Run tests to verify they pass**

Run: `go -C action-svc test ./natsclient/...`
Expected: PASS (both the updated `TestPublisher_PublishOutcome` and the new `TestNewPublisher_CreatesMemoryWriteStream`, plus everything from Task 4)

Note: `go -C action-svc build ./...` still fails at this point (`main.go` not yet updated) — expected, same as Task 4's note.

- [ ] **Step 5: Commit**

```bash
git add action-svc/natsclient/publisher.go action-svc/natsclient/natsclient_test.go
git commit -m "feat(action-svc): publish outcome records via durable JetStream stream"
```

---

### Task 6: Wire the new consumer/publisher into `action-svc`'s config and `main.go`

**Files:**
- Modify: `action-svc/config/config.go`
- Modify: `action-svc/config/config_test.go`
- Modify: `action-svc/main.go`

**Interfaces:**
- Consumes: `sharedconfig.ConsumerNames.ActionSvc` (Task 1), `natsclient.NewConsumer`/`(*Consumer).Start`/`(*Consumer).Close` (Task 4), `natsclient.NewPublisher(ctx, nc, subject)` (Task 5).
- Produces: `config.Config.ActionSvcConsumerName string` — this task's own `main.go` change is the only consumer.

- [ ] **Step 1: Write the failing test**

In `action-svc/config/config_test.go`, the `sharedFields`/`writeConfigFile` helper needs a `consumer_names.action_svc` field threaded through. Replace the file in full with:

```go
package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"soulman/action-svc/config"
)

type consumerNames struct {
	ActionSvc string `json:"action_svc"`
}

type sharedFields struct {
	NATSURL                string        `json:"nats_url"`
	ThinkingRequestSubject string        `json:"thinking_request_subject"`
	MemoryWriteSubject     string        `json:"memory_write_subject"`
	ConsumerNames          consumerNames `json:"consumer_names"`
}

func writeConfigFile(t *testing.T, natsURL, thinkingRequestSubject, memoryWriteSubject, actionSvcConsumerName string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	data, err := json.Marshal(sharedFields{
		NATSURL:                natsURL,
		ThinkingRequestSubject: thinkingRequestSubject,
		MemoryWriteSubject:     memoryWriteSubject,
		ConsumerNames:          consumerNames{ActionSvc: actionSvcConsumerName},
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

	configPath := writeConfigFile(t, "nats://localhost:4222", "soulman.thinking.request", "soulman.memory.write", "action-svc")
	os.Setenv("CONFIG_PATH", configPath)
	defer os.Unsetenv("CONFIG_PATH")

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
	if cfg.ActionSvcConsumerName != "action-svc" {
		t.Errorf("ActionSvcConsumerName = %q, want action-svc", cfg.ActionSvcConsumerName)
	}
}

func TestLoad_SharedConfigValues(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://remote:4222", "soulman.dev.thinking.request", "soulman.dev.memory.write", "action-svc-dev")
	os.Setenv("CONFIG_PATH", configPath)
	defer os.Unsetenv("CONFIG_PATH")
	os.Setenv("SOULMAN_ROOT", `C:\Users\Lenovo\soulman-prod`)
	defer os.Unsetenv("SOULMAN_ROOT")
	os.Setenv("REPORT_SEND_TIME", "09:30")
	defer os.Unsetenv("REPORT_SEND_TIME")

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
	if cfg.ActionSvcConsumerName != "action-svc-dev" {
		t.Errorf("ActionSvcConsumerName = %q, want action-svc-dev", cfg.ActionSvcConsumerName)
	}
}

func TestLoad_MissingConfigFile_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	dir := t.TempDir()
	os.Setenv("CONFIG_PATH", filepath.Join(dir, "does-not-exist.json"))
	defer os.Unsetenv("CONFIG_PATH")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for missing config file, got nil")
	}
}

func TestLoad_EmptyThinkingRequestSubject_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://localhost:4222", "", "soulman.memory.write", "action-svc")
	os.Setenv("CONFIG_PATH", configPath)
	defer os.Unsetenv("CONFIG_PATH")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty thinking_request_subject, got nil")
	}
}

func TestLoad_EmptyMemoryWriteSubject_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://localhost:4222", "soulman.thinking.request", "", "action-svc")
	os.Setenv("CONFIG_PATH", configPath)
	defer os.Unsetenv("CONFIG_PATH")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty memory_write_subject, got nil")
	}
}

func TestLoad_EmptyActionSvcConsumerName_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://localhost:4222", "soulman.thinking.request", "soulman.memory.write", "")
	os.Setenv("CONFIG_PATH", configPath)
	defer os.Unsetenv("CONFIG_PATH")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty consumer_names.action_svc, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go -C action-svc test ./config/...`
Expected: FAIL — compile error, `cfg.ActionSvcConsumerName` undefined.

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
	ActionSvcConsumerName  string
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
	if shared.ConsumerNames.ActionSvc == "" {
		return nil, fmt.Errorf("shared config %s has no consumer_names.action_svc configured", configPath)
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
		ActionSvcConsumerName:  shared.ConsumerNames.ActionSvc,
	}, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
```

- [ ] **Step 4: Update `action-svc/main.go`**

Add `"context"` to the import block, and update the NATS wiring block. Replace:

```go
	// NATS is non-fatal at startup: the dispatch side degrades until
	// reconnect, but the HTTP server and the daily cron don't depend on it.
	var publisher *natsclient.Publisher
	nc, natsErr := natsclient.Connect(cfg.NATSURL)
	if natsErr != nil {
		log.Printf("WARNING: nats unavailable (%v) — dispatch degraded until reconnect", natsErr)
	} else {
		publisher = natsclient.NewPublisher(nc, cfg.MemoryWriteSubject)
		disp := dispatch.New(cfg.SoulmanRoot, publisher, batcher)
		sub, subErr := natsclient.Subscribe(nc, cfg.ThinkingRequestSubject, disp.Handle)
		if subErr != nil {
			log.Printf("WARNING: nats subscribe failed: %v", subErr)
		} else {
			defer sub.Close()
			log.Printf("nats: subscribed to %s", cfg.ThinkingRequestSubject)
		}
		defer nc.Close()
	}
```

with:

```go
	// NATS is non-fatal at startup: the dispatch side degrades until
	// reconnect, but the HTTP server and the daily cron don't depend on it.
	var publisher *natsclient.Publisher
	nc, natsErr := natsclient.Connect(cfg.NATSURL)
	if natsErr != nil {
		log.Printf("WARNING: nats unavailable (%v) — dispatch degraded until reconnect", natsErr)
	} else {
		var pubErr error
		publisher, pubErr = natsclient.NewPublisher(ctx, nc, cfg.MemoryWriteSubject)
		if pubErr != nil {
			log.Printf("WARNING: nats publisher setup failed (%v) — dispatch degraded", pubErr)
		} else {
			disp := dispatch.New(cfg.SoulmanRoot, publisher, batcher)
			consumer, consErr := natsclient.NewConsumer(nc, cfg.ActionSvcConsumerName, cfg.ThinkingRequestSubject, disp.Handle)
			if consErr != nil {
				log.Printf("WARNING: nats consumer setup failed: %v", consErr)
			} else if startErr := consumer.Start(ctx); startErr != nil {
				log.Printf("WARNING: nats consumer start failed: %v", startErr)
			} else {
				defer consumer.Close()
			}
		}
		defer nc.Close()
	}
```

**Important:** `publisher, pubErr = natsclient.NewPublisher(...)` uses `=` (plain assignment, with `pubErr` pre-declared via `var pubErr error`), not `:=`. Using `:=` here would declare a *new* inner-scope `publisher` that shadows the outer `var publisher *natsclient.Publisher` — `schedPublisher` (below, unchanged) reads the outer variable after this block, so the outer `publisher` must actually be the one that gets set.

Also add `ctx, cancel := context.WithCancel(context.Background())` near the top of `main()` (action-svc's `main.go` doesn't currently have one) — insert it right after the `config.Load()` error check, and add `defer cancel()` immediately after, matching `thinking-svc/main.go`'s and `perception-svc/main.go`'s existing pattern:

```go
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
```

- [ ] **Step 5: Run tests and build to verify**

Run: `go -C action-svc test ./...`
Expected: PASS (all packages, including Tasks 4 and 5's tests)

Run: `go -C action-svc build ./...`
Expected: builds with no errors — this is the point where `action-svc` becomes buildable again after Tasks 4-5's intentional interim breakage.

- [ ] **Step 6: Commit**

```bash
git add action-svc/config/config.go action-svc/config/config_test.go action-svc/main.go
git commit -m "feat(action-svc): wire durable JetStream consumer and outcome publisher into main"
```

---

### Task 7: `perception-svc` gains `POST /api/perceive/raw`

**Files:**
- Create: `perception-svc/httpserver/raw.go`
- Create: `perception-svc/httpserver/raw_test.go`
- Modify: `perception-svc/httpserver/server.go`

**Interfaces:**
- Consumes: `common.Stimulus` (from `soulman/common`), `s.publisher.Publish(ctx, stimulus)` (existing `Server.publisher` field).
- Produces: nothing new for other tasks — Task 8 (the CLI) talks to this endpoint over HTTP, not as a Go import.

- [ ] **Step 1: Write the failing test**

Create `perception-svc/httpserver/raw_test.go`:

```go
package httpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"soulman/common"
	"soulman/perception-svc/httpserver"
)

type fakeRawPublisher struct {
	published []*common.Stimulus
	err       error
}

func (f *fakeRawPublisher) Publish(_ context.Context, s *common.Stimulus) error {
	if f.err != nil {
		return f.err
	}
	f.published = append(f.published, s)
	return nil
}

func TestPerceiveRaw_FullStimulus_PublishedVerbatim(t *testing.T) {
	pub := &fakeRawPublisher{}
	srv := httpserver.NewWithPublisher(pub)

	body := `{
		"channel": "gmail",
		"source": {"identity": "test@example.com", "authenticated": false, "auth_method": "none"},
		"content": {"raw_text": "hello", "content_type": "text"},
		"hints": {"priority": "high"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/perceive/raw", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body: %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %d stimuli, want 1", len(pub.published))
	}
	got := pub.published[0]
	if got.Channel != "gmail" {
		t.Errorf("Channel = %q, want gmail", got.Channel)
	}
	if got.Source.Identity != "test@example.com" {
		t.Errorf("Source.Identity = %q, want test@example.com", got.Source.Identity)
	}
	if got.Content.RawText != "hello" {
		t.Errorf("Content.RawText = %q, want hello", got.Content.RawText)
	}
	if got.Hints.Priority != "high" {
		t.Errorf("Hints.Priority = %q, want high", got.Hints.Priority)
	}
	if got.StimulusID == "" {
		t.Error("StimulusID = \"\", want a generated UUID since none was supplied")
	}
	if got.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", got.SchemaVersion)
	}
	if got.ReceivedAt.IsZero() {
		t.Error("ReceivedAt is zero, want a generated timestamp since none was supplied")
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["stimulus_id"] != got.StimulusID {
		t.Errorf("response stimulus_id = %q, want %q", resp["stimulus_id"], got.StimulusID)
	}
}

func TestPerceiveRaw_ExplicitStimulusID_Preserved(t *testing.T) {
	pub := &fakeRawPublisher{}
	srv := httpserver.NewWithPublisher(pub)

	body := `{"stimulus_id": "custom-id-123", "channel": "gmail", "content": {"raw_text": "x", "content_type": "text"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/perceive/raw", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body: %s", rec.Code, rec.Body.String())
	}
	if pub.published[0].StimulusID != "custom-id-123" {
		t.Errorf("StimulusID = %q, want custom-id-123 (explicit value must be preserved, not overwritten)", pub.published[0].StimulusID)
	}
}

func TestPerceiveRaw_MissingChannel_Returns400(t *testing.T) {
	pub := &fakeRawPublisher{}
	srv := httpserver.NewWithPublisher(pub)

	body := `{"content": {"raw_text": "x", "content_type": "text"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/perceive/raw", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if len(pub.published) != 0 {
		t.Error("expected nothing to be published for a request missing channel")
	}
}

func TestPerceiveRaw_InvalidJSON_Returns400(t *testing.T) {
	pub := &fakeRawPublisher{}
	srv := httpserver.NewWithPublisher(pub)

	req := httptest.NewRequest(http.MethodPost, "/api/perceive/raw", bytes.NewReader([]byte("not json")))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestPerceiveRaw_PublishFails_Returns503(t *testing.T) {
	pub := &fakeRawPublisher{err: errPublishBoom}
	srv := httpserver.NewWithPublisher(pub)

	body := `{"channel": "gmail", "content": {"raw_text": "x", "content_type": "text"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/perceive/raw", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

var errPublishBoom = errPublish{}

type errPublish struct{}

func (errPublish) Error() string { return "boom" }
```

This test file needs a `httpserver.NewWithPublisher(pub Publisher) *Server` constructor — a test-only convenience that builds a `Server` with zero-valued port/watchedPaths/natsStatus (irrelevant to this endpoint) and just the given publisher. Add it in Step 3 alongside the main change (it's a small addition to `server.go`, not `raw.go`, since it constructs `Server`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go -C perception-svc test ./httpserver/...`
Expected: FAIL — compile error, `httpserver.NewWithPublisher` and the `/api/perceive/raw` route don't exist yet.

- [ ] **Step 3: Implement the endpoint**

Add to `perception-svc/httpserver/server.go`: register the route in `buildRouter`, and add the test constructor. Update `buildRouter`:

```go
func (s *Server) buildRouter() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Get("/health", s.health)
	r.Post("/api/perceive/cli", s.perceiveCLI)
	r.Post("/api/perceive/raw", s.perceiveRaw)
	return r
}
```

Add this constructor to `server.go` (below `New`):

```go
// NewWithPublisher builds a Server for tests that only exercise
// publisher-dependent handlers (like perceiveRaw) and don't care about
// port/watchedPaths/natsStatus.
func NewWithPublisher(publisher Publisher) *Server {
	return New("0", nil, nil, publisher)
}
```

Create `perception-svc/httpserver/raw.go`:

```go
package httpserver

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"soulman/common"
)

// perceiveRaw implements docs/superpowers/specs/2026-07-18-pipeline-debugging-tools-design.md's
// generic Stimulus injection endpoint: the request body may be a complete
// common.Stimulus or just the essentials — any required field left blank
// gets a sensible default filled in, matching what buildCLIStimulus already
// does for the CLI channel. channel has no default: it's the one thing the
// caller must always specify, since "which channel is this pretending to
// be" can't be inferred.
func (s *Server) perceiveRaw(w http.ResponseWriter, r *http.Request) {
	var stimulus common.Stimulus
	if err := json.NewDecoder(r.Body).Decode(&stimulus); err != nil {
		writeCLIError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if stimulus.Channel == "" {
		writeCLIError(w, http.StatusBadRequest, "channel is required")
		return
	}

	if stimulus.StimulusID == "" {
		id, err := uuid.NewV7()
		if err != nil {
			id = uuid.New()
		}
		stimulus.StimulusID = id.String()
	}
	if stimulus.SchemaVersion == 0 {
		stimulus.SchemaVersion = 1
	}
	if stimulus.ReceivedAt.IsZero() {
		stimulus.ReceivedAt = time.Now().UTC()
	}

	if err := s.publisher.Publish(r.Context(), &stimulus); err != nil {
		writeCLIError(w, http.StatusServiceUnavailable, "failed to publish stimulus")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"stimulus_id": stimulus.StimulusID})
}
```

This reuses `writeCLIError` (already defined in `cli.go`, same package) rather than duplicating it.

- [ ] **Step 4: Run tests and build to verify**

Run: `go -C perception-svc test ./httpserver/...`
Expected: PASS

Run: `go -C perception-svc build ./...`
Expected: builds with no errors

- [ ] **Step 5: Commit**

```bash
git add perception-svc/httpserver/raw.go perception-svc/httpserver/raw_test.go perception-svc/httpserver/server.go
git commit -m "feat(perception-svc): add POST /api/perceive/raw generic Stimulus injection endpoint"
```

---

### Task 8: `soulman inject <file>` CLI subcommand

**Files:**
- Modify: `cli/args.go`
- Modify: `cli/args_test.go`
- Modify: `cli/client/client.go`
- Modify: `cli/client/client_test.go`
- Modify: `cli/main.go`

**Interfaces:**
- Consumes: `perception-svc`'s `POST /api/perceive/raw` (Task 7), over HTTP — no Go-level dependency.
- Produces: nothing new for other tasks.

- [ ] **Step 1: Write the failing tests**

Add to `cli/args_test.go` (a new test function; do not modify existing ones):

```go
func TestParseArgs_InjectMode(t *testing.T) {
	got, err := parseArgs([]string{"inject", "path/to/file.json"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if got.Mode != "inject" {
		t.Errorf("Mode = %q, want inject", got.Mode)
	}
	if got.InjectFile != "path/to/file.json" {
		t.Errorf("InjectFile = %q, want path/to/file.json", got.InjectFile)
	}
}

func TestParseArgs_InjectMode_WithDevFlag(t *testing.T) {
	got, err := parseArgs([]string{"--dev", "inject", "path/to/file.json"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if got.Mode != "inject" || got.InjectFile != "path/to/file.json" || !got.Dev {
		t.Errorf("got %+v, want Mode=inject InjectFile=path/to/file.json Dev=true", got)
	}
}

func TestParseArgs_InjectMode_MissingFile_ReturnsError(t *testing.T) {
	_, err := parseArgs([]string{"inject"})
	if err == nil {
		t.Fatal("parseArgs: want error for inject with no file argument, got nil")
	}
}
```

Add to `cli/client/client_test.go` (check the existing file first for its exact test style — e.g. does it spin up an `httptest.Server`? Match that pattern):

```go
func TestSendRaw_PostsFileBytesAndReturnsStimulusID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/perceive/raw" {
			t.Errorf("path = %q, want /api/perceive/raw", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"channel":"gmail","content":{"raw_text":"hi","content_type":"text"}}` {
			t.Errorf("body = %s, want the raw file bytes unchanged", body)
		}
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"stimulus_id": "injected-id-1"})
	}))
	defer srv.Close()

	id, err := client.SendRaw(srv.URL, []byte(`{"channel":"gmail","content":{"raw_text":"hi","content_type":"text"}}`))
	if err != nil {
		t.Fatalf("SendRaw: %v", err)
	}
	if id != "injected-id-1" {
		t.Errorf("id = %q, want injected-id-1", id)
	}
}

func TestSendRaw_ServerError_ReturnsErrorMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "channel is required"})
	}))
	defer srv.Close()

	_, err := client.SendRaw(srv.URL, []byte(`{}`))
	if err == nil {
		t.Fatal("SendRaw: want error, got nil")
	}
}
```

`client_test.go` already imports `"encoding/json"`, `"net/http"`, `"net/http/httptest"`, `"strings"`, and `"testing"` — add only `"io"` to the import block (needed for `io.ReadAll` in `TestSendRaw_PostsFileBytesAndReturnsStimulusID`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go -C cli test ./...`
Expected: FAIL — compile errors (`parsedArgs.Mode`/`InjectFile` fields and `client.SendRaw` don't exist yet).

- [ ] **Step 3: Implement `parseArgs`'s `inject` mode**

In `cli/args.go`, add an `InjectFile` field to `parsedArgs`:

```go
type parsedArgs struct {
	Text       string
	Mode       string
	Priority   string
	Dev        bool
	InjectFile string
}
```

In `parseArgs`, after the existing flag-parsing loop and the `if len(positional) == 0` check, add `inject` handling before the existing `note` handling:

```go
	if len(positional) == 0 {
		return parsedArgs{}, fmt.Errorf(`usage: soulman [--dev] [--priority low|normal|high|critical] [note] "<text>"`)
	}

	if positional[0] == "inject" {
		if len(positional) < 2 {
			return parsedArgs{}, fmt.Errorf("usage: soulman inject <file>")
		}
		res.Mode = "inject"
		res.InjectFile = positional[1]
		return res, nil
	}

	res.Mode = "stimulus"
	if positional[0] == "note" {
		res.Mode = "note"
		positional = positional[1:]
	}
```

- [ ] **Step 4: Implement `client.SendRaw`**

Add to `cli/client/client.go`:

```go
// SendRaw POSTs the raw bytes of body (typically a file's contents,
// unparsed and unvalidated client-side — perception-svc's
// /api/perceive/raw endpoint owns all validation) to baseURL+"/api/perceive/raw"
// and returns the resulting stimulus_id, or an error describing why the
// request failed.
func SendRaw(baseURL string, body []byte) (string, error) {
	resp, err := http.Post(baseURL+"/api/perceive/raw", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("client: request to %s failed: %w", baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		var out response
		_ = json.NewDecoder(resp.Body).Decode(&out)

		msg := out.Error
		if msg == "" {
			msg = fmt.Sprintf("unexpected status %d", resp.StatusCode)
		}
		return "", fmt.Errorf("client: %s", strings.TrimSpace(msg))
	}

	var out response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("client: decode response: %w", err)
	}

	return out.StimulusID, nil
}
```

(Reuses the existing `response` type already defined in this file for `Send`.)

- [ ] **Step 5: Wire `inject` mode into `cli/main.go`**

Update `main()`:

```go
func main() {
	args, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	baseURL := prodURL
	if args.Dev {
		baseURL = devURL
	}

	if args.Mode == "inject" {
		fileBytes, err := os.ReadFile(args.InjectFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "reading %s: %v\n", args.InjectFile, err)
			os.Exit(1)
		}
		id, err := client.SendRaw(baseURL, fileBytes)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("injected (stimulus_id: %s)\n", id)
		return
	}

	id, err := client.Send(baseURL, client.Request{
		Text:     args.Text,
		Mode:     args.Mode,
		Priority: args.Priority,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	verb := "sent"
	if args.Mode == "note" {
		verb = "logged"
	}
	fmt.Printf("%s (stimulus_id: %s)\n", verb, id)
}
```

- [ ] **Step 6: Run tests and build to verify**

Run: `go -C cli test ./...`
Expected: PASS

Run: `go -C cli build ./...`
Expected: builds with no errors

- [ ] **Step 7: Commit**

```bash
git add cli/args.go cli/args_test.go cli/client/client.go cli/client/client_test.go cli/main.go
git commit -m "feat(cli): add 'soulman inject <file>' subcommand for raw Stimulus injection"
```

---

### Task 9: `discordread` package for fetching Discord message history

**Files:**
- Create: `perception-svc/discordread/discordread.go`

**Interfaces:**
- Produces: `discordread.Message{ID, Author, Content string; Timestamp time.Time}`, `discordread.FetchHistory(ctx context.Context, botToken, channelID string, limit int) ([]Message, error)` — used by `perception-svc` (a future Discord perception channel, not built in this plan). Task 10 does NOT import this package; see Task 10 Step 1 for why it deliberately duplicates the logic instead.

**No automated test for this task** — per the design spec's explicit scope boundary, this thin HTTP-calling code is verified manually against the real bot/channel, mirroring the precedent `perception-svc/gmailwatcher/client.go` already established.

- [ ] **Step 1: Write the implementation**

Create `perception-svc/discordread/discordread.go`:

```go
// Package discordread reads a Discord channel's message history via
// Discord's REST API, using the same bot token action-svc already uses to
// send. Placed under perception-svc (not action-svc/notify, which stays
// send-only) since a future "Discord as a perception channel" direction
// would naturally build on this same read capability — see
// docs/superpowers/specs/2026-07-18-pipeline-debugging-tools-design.md.
// This package only reads; it never sends anything.
package discordread

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// Message is the minimal shape needed for debugging — not a full Discord
// API model.
type Message struct {
	ID        string
	Author    string
	Content   string
	Timestamp time.Time
}

type discordAPIMessage struct {
	ID        string `json:"id"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
	Author    struct {
		Username string `json:"username"`
	} `json:"author"`
}

// FetchHistory fetches up to limit most-recent messages from channelID
// using botToken, via GET /channels/{id}/messages. Returned in the same
// order Discord returns them (newest-first); callers that want
// chronological order should reverse the slice themselves.
func FetchHistory(ctx context.Context, botToken, channelID string, limit int) ([]Message, error) {
	url := fmt.Sprintf("https://discord.com/api/v10/channels/%s/messages?limit=%s", channelID, strconv.Itoa(limit))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("discordread: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bot "+botToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("discordread: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discordread: status %d", resp.StatusCode)
	}

	var raw []discordAPIMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("discordread: decode response: %w", err)
	}

	messages := make([]Message, 0, len(raw))
	for _, m := range raw {
		ts, err := time.Parse(time.RFC3339, m.Timestamp)
		if err != nil {
			ts = time.Time{}
		}
		messages = append(messages, Message{
			ID:        m.ID,
			Author:    m.Author.Username,
			Content:   m.Content,
			Timestamp: ts,
		})
	}
	return messages, nil
}
```

- [ ] **Step 2: Verify it builds**

Run: `go -C perception-svc build ./...`
Expected: builds with no errors.

- [ ] **Step 3: Commit**

```bash
git add perception-svc/discordread/discordread.go
git commit -m "feat(perception-svc): add discordread package for fetching Discord message history"
```

---

### Task 10: `soulman discord-history` CLI subcommand

**Files:**
- Modify: `cli/args.go`
- Modify: `cli/args_test.go`
- Modify: `cli/main.go`
- Create: `cli/discordread.go` (a small, deliberate duplication of `perception-svc/discordread`'s logic — see Step 1 — not a new cross-module dependency, so `cli/go.mod`/`go.sum` are untouched by this task)

**Interfaces:**
- Consumes: reads `DISCORD_BOT_TOKEN`/`DISCORD_CHANNEL_ID` from the process environment directly (same vars `action-svc` already uses) — no config-file plumbing, since this is a one-off manual debug command, not a long-running service.

- [ ] **Step 1: Decide on code reuse vs. duplication (read this before writing anything)**

`cli` is its own Go module (`cli/go.mod`), separate from `perception-svc`'s module — unlike `common`, there's no existing `replace` directive wiring them together, and creating one just for a ~30-line debug helper is disproportionate. Duplicate `discordread`'s ~40 lines directly into a new `cli/discordread.go` (package `main`, unexported helper) rather than adding a new cross-module dependency. This is a deliberate, small, justified duplication — not a violation of DRY given the alternative (a new module dependency for one debug subcommand) is worse. Note this explicitly in a comment.

- [ ] **Step 2: Write the failing test**

Add to `cli/args_test.go`:

```go
func TestParseArgs_DiscordHistoryMode(t *testing.T) {
	got, err := parseArgs([]string{"discord-history"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if got.Mode != "discord-history" {
		t.Errorf("Mode = %q, want discord-history", got.Mode)
	}
	if got.DiscordHistoryLimit != 20 {
		t.Errorf("DiscordHistoryLimit = %d, want default 20", got.DiscordHistoryLimit)
	}
}

func TestParseArgs_DiscordHistoryMode_WithLimit(t *testing.T) {
	got, err := parseArgs([]string{"discord-history", "--limit", "50"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if got.DiscordHistoryLimit != 50 {
		t.Errorf("DiscordHistoryLimit = %d, want 50", got.DiscordHistoryLimit)
	}
}

func TestParseArgs_DiscordHistoryMode_InvalidLimit_ReturnsError(t *testing.T) {
	_, err := parseArgs([]string{"discord-history", "--limit", "not-a-number"})
	if err == nil {
		t.Fatal("parseArgs: want error for non-numeric --limit, got nil")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go -C cli test ./...`
Expected: FAIL — compile error, `parsedArgs.DiscordHistoryLimit` doesn't exist.

- [ ] **Step 4: Implement `parseArgs`'s `discord-history` mode**

In `cli/args.go`, add to `parsedArgs`:

```go
type parsedArgs struct {
	Text                string
	Mode                string
	Priority            string
	Dev                 bool
	InjectFile          string
	DiscordHistoryLimit int
}
```

Add `--limit` flag handling to the flag-parsing loop (alongside the existing `--priority` cases):

```go
		case !endOfFlags && a == "--limit":
			if i+1 >= len(args) {
				return parsedArgs{}, fmt.Errorf("--limit requires a value")
			}
			i++
			n, convErr := strconv.Atoi(args[i])
			if convErr != nil {
				return parsedArgs{}, fmt.Errorf("--limit must be a number, got %q", args[i])
			}
			res.DiscordHistoryLimit = n
```

(Add `"strconv"` to this file's imports.)

Initialize the default in `parseArgs`'s first line:

```go
	res := parsedArgs{Priority: "normal", DiscordHistoryLimit: 20}
```

Add `discord-history` mode detection, in the same place `inject` was added (before the `note`/`stimulus` fallback, but this one has no required positional argument so it can short-circuit even earlier — right after flag parsing, before the "at least one positional argument" check that `note`/plain-text mode requires):

```go
	if len(positional) > 0 && positional[0] == "discord-history" {
		res.Mode = "discord-history"
		return res, nil
	}

	if len(positional) == 0 {
		return parsedArgs{}, fmt.Errorf(`usage: soulman [--dev] [--priority low|normal|high|critical] [note] "<text>"`)
	}
```

- [ ] **Step 5: Implement the duplicated Discord-fetch helper**

Create `cli/discordread.go`:

```go
package main

// Deliberately duplicated from perception-svc/discordread rather than
// imported: cli is its own Go module with no existing cross-module
// dependency on perception-svc (unlike common, which every service already
// imports via a replace directive), and adding one just for this ~40-line
// debug helper isn't worth it. Keep this in sync by hand if
// perception-svc/discordread's shape changes.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

type discordMessage struct {
	ID        string
	Author    string
	Content   string
	Timestamp time.Time
}

type discordAPIMessage struct {
	ID        string `json:"id"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
	Author    struct {
		Username string `json:"username"`
	} `json:"author"`
}

func fetchDiscordHistory(ctx context.Context, botToken, channelID string, limit int) ([]discordMessage, error) {
	url := fmt.Sprintf("https://discord.com/api/v10/channels/%s/messages?limit=%s", channelID, strconv.Itoa(limit))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("discord-history: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bot "+botToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("discord-history: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discord-history: status %d", resp.StatusCode)
	}

	var raw []discordAPIMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("discord-history: decode response: %w", err)
	}

	messages := make([]discordMessage, 0, len(raw))
	for _, m := range raw {
		ts, err := time.Parse(time.RFC3339, m.Timestamp)
		if err != nil {
			ts = time.Time{}
		}
		messages = append(messages, discordMessage{
			ID:        m.ID,
			Author:    m.Author.Username,
			Content:   m.Content,
			Timestamp: ts,
		})
	}
	return messages, nil
}
```

- [ ] **Step 6: Wire `discord-history` mode into `cli/main.go`**

Add `"context"` and `"os"` (already imported) to the imports, and this branch in `main()` (alongside the `inject` branch added in Task 8):

```go
	if args.Mode == "discord-history" {
		token := os.Getenv("DISCORD_BOT_TOKEN")
		channelID := os.Getenv("DISCORD_CHANNEL_ID")
		if token == "" || channelID == "" {
			fmt.Fprintln(os.Stderr, "DISCORD_BOT_TOKEN and DISCORD_CHANNEL_ID must both be set in the environment")
			os.Exit(1)
		}

		messages, err := fetchDiscordHistory(context.Background(), token, channelID, args.DiscordHistoryLimit)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		// Discord returns newest-first; print chronologically (oldest-first),
		// easiest to read top-to-bottom in a terminal.
		for i := len(messages) - 1; i >= 0; i-- {
			m := messages[i]
			fmt.Printf("[%s] %s: %s\n", m.Timestamp.Format("2006-01-02 15:04:05"), m.Author, m.Content)
		}
		return
	}
```

- [ ] **Step 7: Run tests and build to verify**

Run: `go -C cli test ./...`
Expected: PASS

Run: `go -C cli build ./...`
Expected: builds with no errors

- [ ] **Step 8: Commit**

```bash
git add cli/args.go cli/args_test.go cli/main.go cli/discordread.go
git commit -m "feat(cli): add 'soulman discord-history' subcommand"
```

---

### Task 11: End-to-end smoke test

**Files:**
- None modified — this task only builds and runs binaries.

**Interfaces:**
- Consumes: everything from Tasks 1-10.

- [ ] **Step 1: Build everything**

Run:
```bash
go -C common build ./...
go -C perception-svc build ./...
go -C memory-svc build ./...
go -C thinking-svc build ./...
go -C action-svc build ./...
go -C cli build ./...
```
Expected: all succeed.

- [ ] **Step 2: Run every module's full test suite**

Run:
```bash
go -C common test ./...
go -C perception-svc test ./...
go -C memory-svc test ./...
go -C thinking-svc test ./...
go -C action-svc test ./...
go -C cli test ./...
```
Expected: all PASS.

- [ ] **Step 3: Verify the durability fix — the actual bug this plan exists to close**

With `action-svc` NOT running, publish a `thinking.request` message (via `thinking-svc` processing any stimulus, or directly via `nats pub` / a small script using the `THINKING_REQUEST` stream), then start `action-svc` and confirm the message is still delivered (check for the corresponding report entry or outcome record) instead of being silently lost. This is the direct regression test for tonight's incident.

- [ ] **Step 4: Verify the injection endpoint end-to-end**

```bash
echo '{"channel": "gmail", "content": {"raw_text": "smoke test body", "content_type": "text"}}' > /tmp/inject-smoke-test.json
go -C cli run . --dev inject /tmp/inject-smoke-test.json
```
Expected: prints `injected (stimulus_id: ...)`. Confirm the stimulus flows through to a report entry (with `perception-svc`, `thinking-svc`, `action-svc` all running in dev).

- [ ] **Step 5: Verify Discord history reading manually**

```bash
go -C cli run . discord-history --limit 5
```
Expected: prints up to 5 recent messages from the Soulman Reports Discord channel, oldest-first, with no error — confirms `DISCORD_BOT_TOKEN`/`DISCORD_CHANNEL_ID` are read correctly and the real Discord API call succeeds.

No commit for this task — it's verification only, no file changes.
