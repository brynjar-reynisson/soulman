# Soulman CLI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the `soulman` CLI tool (the CLI push channel from `Perception module.md`), plus the two small backend pieces it depends on: a new `POST /api/perceive/cli` endpoint on `perception-svc`, and a new mechanical `cli-note` rule on `thinking-svc`.

**Architecture:** The CLI is a dependency-free Go binary that POSTs JSON to `perception-svc`'s new endpoint; `perception-svc` builds and publishes the `Stimulus` (same as every other channel). `channel: "cli-note"` stimuli get a new thinking-svc rule that mechanically produces the same `append_daily_report_entry` action `ErrorReportRule` already produces for folder-watcher — no LLM call, no changes to `action-svc`. `channel: "cli"` stimuli (general free text) match no rule yet and are simply logged, exactly like any other currently-unmatched stimulus.

**Tech Stack:** Go 1.25, stdlib `net/http`/`encoding/json` for both the new endpoint and the CLI client, existing `github.com/google/uuid` and `soulman/common` for Stimulus construction.

## Global Constraints

- New endpoint: `POST /api/perceive/cli`, request body `{"text": string (required), "mode": "note"|"stimulus" (default "stimulus"), "priority": "low"|"normal"|"high"|"critical" (default "normal")}`.
- Success response: `202 Accepted`, body `{"stimulus_id": "<id>"}`. Validation failure: `400` with `{"error": "..."}`. Publish failure: `503` with `{"error": "..."}`.
- Stimulus fields for both modes: `Channel` = `"cli-note"` (mode=note) or `"cli"` (mode=stimulus); `Source` = `{identity: "cli", authenticated: true, auth_method: "system"}`; `Content` = `{raw_text: text, content_type: "text", raw_payload: {}, attachments: []}`; `Hints.Priority` = request priority; `Override.IsOverride` = false.
- `cli-note` rule's `ActionRequest.Parameters.source_path` must be the literal string `"cli/note"` (not `"cli"` — `filepath.Dir("cli")` is `"."`, and the report line must read `[cli]`).
- No changes to `action-svc` — it already dispatches `append_daily_report_entry` regardless of which rule produced it.
- The `cli` module has no external dependencies (stdlib only) and is not added to `start-everything.ps1`'s build step — built on demand via `go build ./cli`.
- Override commands (pause/stop/resume/status) are out of scope.

---

### Task 1: perception-svc — `POST /api/perceive/cli` endpoint

**Files:**
- Modify: `perception-svc/httpserver/server.go`
- Modify: `perception-svc/httpserver/server_test.go`
- Create: `perception-svc/httpserver/cli.go`
- Create: `perception-svc/httpserver/cli_test.go`
- Modify: `perception-svc/main.go`

**Interfaces:**
- Consumes: `common.Stimulus`, `common.Content`, `common.Source`, `common.Hints`, `common.Override`, `common.ChannelMeta` (from `soulman/common`, already imported elsewhere in this repo); `github.com/google/uuid` (`uuid.NewV7()`, `uuid.New()`) — already a dependency of `perception-svc`.
- Produces: `httpserver.Publisher` interface (`Publish(ctx context.Context, s *common.Stimulus) error`); `httpserver.New(port string, watchedPaths []string, natsStatus func() string, publisher Publisher) *Server` (signature change — was 3 args, now 4). Later tasks do not depend on this, but this is the shape `main.go` and all existing tests must be updated to.

- [ ] **Step 1: Update existing tests to the new 4-arg `New()` signature (this alone will not compile yet — expected)**

Edit `perception-svc/httpserver/server_test.go`, changing both existing calls:

```go
func TestHealth_ReportsStatusAndWatchedPaths(t *testing.T) {
	srv := httpserver.New("9001", []string{`C:\errors`, `C:\other`}, func() string { return "connected" }, nil)
	// ... rest unchanged
```

```go
func TestHealth_NilStatusFunc_DefaultsToDisconnected(t *testing.T) {
	srv := httpserver.New("9001", nil, nil, nil)
	// ... rest unchanged
```

- [ ] **Step 2: Write the new endpoint's failing tests**

Create `perception-svc/httpserver/cli_test.go`:

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

type fakePublisher struct {
	published *common.Stimulus
	err       error
}

func (f *fakePublisher) Publish(_ context.Context, s *common.Stimulus) error {
	f.published = s
	return f.err
}

func postCLI(t *testing.T, srv *httpserver.Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/perceive/cli", bytes.NewBufferString(body))
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestPerceiveCLI_DefaultMode_PublishesCLIChannel(t *testing.T) {
	pub := &fakePublisher{}
	srv := httpserver.New("9001", nil, nil, pub)

	rec := postCLI(t, srv, `{"text":"remind me to check logs"}`)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body=%s", rec.Code, rec.Body.String())
	}
	if pub.published == nil {
		t.Fatal("expected Publish to be called")
	}
	if pub.published.Channel != "cli" {
		t.Errorf("Channel = %q, want cli", pub.published.Channel)
	}
	if pub.published.Content.RawText != "remind me to check logs" {
		t.Errorf("RawText = %q, want the request text", pub.published.Content.RawText)
	}
	if pub.published.Hints.Priority != "normal" {
		t.Errorf("Priority = %q, want normal (default)", pub.published.Hints.Priority)
	}
	if pub.published.Source.Identity != "cli" || !pub.published.Source.Authenticated || pub.published.Source.AuthMethod != "system" {
		t.Errorf("Source = %+v, want {cli, true, system}", pub.published.Source)
	}
	if pub.published.Override.IsOverride {
		t.Error("IsOverride = true, want false")
	}

	var respBody map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &respBody); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if respBody["stimulus_id"] == "" {
		t.Error("expected non-empty stimulus_id in response")
	}
	if respBody["stimulus_id"] != pub.published.StimulusID {
		t.Errorf("response stimulus_id = %q, want match with published %q", respBody["stimulus_id"], pub.published.StimulusID)
	}
}

func TestPerceiveCLI_NoteMode_PublishesCLINoteChannel(t *testing.T) {
	pub := &fakePublisher{}
	srv := httpserver.New("9001", nil, nil, pub)

	rec := postCLI(t, srv, `{"text":"disk cleanup done","mode":"note","priority":"high"}`)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body=%s", rec.Code, rec.Body.String())
	}
	if pub.published.Channel != "cli-note" {
		t.Errorf("Channel = %q, want cli-note", pub.published.Channel)
	}
	if pub.published.Hints.Priority != "high" {
		t.Errorf("Priority = %q, want high", pub.published.Hints.Priority)
	}
}

func TestPerceiveCLI_MissingText_Returns400(t *testing.T) {
	srv := httpserver.New("9001", nil, nil, &fakePublisher{})

	rec := postCLI(t, srv, `{"mode":"note"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestPerceiveCLI_InvalidMode_Returns400(t *testing.T) {
	srv := httpserver.New("9001", nil, nil, &fakePublisher{})

	rec := postCLI(t, srv, `{"text":"hi","mode":"bogus"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestPerceiveCLI_InvalidPriority_Returns400(t *testing.T) {
	srv := httpserver.New("9001", nil, nil, &fakePublisher{})

	rec := postCLI(t, srv, `{"text":"hi","priority":"urgent"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestPerceiveCLI_MalformedJSON_Returns400(t *testing.T) {
	srv := httpserver.New("9001", nil, nil, &fakePublisher{})

	rec := postCLI(t, srv, `{not json`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestPerceiveCLI_PublishFails_Returns503(t *testing.T) {
	pub := &fakePublisher{err: context.DeadlineExceeded}
	srv := httpserver.New("9001", nil, nil, pub)

	rec := postCLI(t, srv, `{"text":"hi"}`)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}
```

- [ ] **Step 3: Run the tests to confirm they fail to compile**

Run: `cd perception-svc && go test ./httpserver/...`
Expected: FAIL — compile error, `httpserver.New` called with 4 arguments but expects 3 (or `pub.published` / `httpserver.Server.Handler` undefined depending on what's missing); `httpserver.Publisher`/`perceiveCLI` route not found.

- [ ] **Step 4: Implement the Publisher interface and update `New()`**

Edit `perception-svc/httpserver/server.go` to match:

```go
package httpserver

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"soulman/common"
)

// Publisher is satisfied by *natspublish.Publisher. Declared here (not
// imported from natspublish) to avoid an import cycle, mirroring the same
// pattern watcher.Publisher already uses.
type Publisher interface {
	Publish(ctx context.Context, s *common.Stimulus) error
}

type Server struct {
	port         string
	watchedPaths []string
	natsStatus   func() string
	publisher    Publisher
	router       chi.Router
}

func New(port string, watchedPaths []string, natsStatus func() string, publisher Publisher) *Server {
	s := &Server{port: port, watchedPaths: watchedPaths, natsStatus: natsStatus, publisher: publisher}
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
	r.Post("/api/perceive/cli", s.perceiveCLI)
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

Note the added `"context"` import is needed for the `Publisher` interface — add it to the import block (`"context"` alongside `"encoding/json"`).

- [ ] **Step 5: Implement the CLI endpoint handler**

Create `perception-svc/httpserver/cli.go`:

```go
package httpserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"soulman/common"
)

type cliRequest struct {
	Text     string `json:"text"`
	Mode     string `json:"mode"`
	Priority string `json:"priority"`
}

var validCLIPriorities = map[string]bool{"low": true, "normal": true, "high": true, "critical": true}

// perceiveCLI implements docs/superpowers/specs/2026-07-18-soulman-cli-design.md's
// POST /api/perceive/cli endpoint — the CLI push channel from
// Perception module.md. mode "note" produces a channel: "cli-note" stimulus
// (thinking-svc's CLINoteRule handles it mechanically); mode "stimulus"
// (the default) produces channel: "cli" for future goal-driven reasoning.
func (s *Server) perceiveCLI(w http.ResponseWriter, r *http.Request) {
	var req cliRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeCLIError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if req.Text == "" {
		writeCLIError(w, http.StatusBadRequest, "text is required")
		return
	}
	if req.Mode == "" {
		req.Mode = "stimulus"
	}
	if req.Mode != "stimulus" && req.Mode != "note" {
		writeCLIError(w, http.StatusBadRequest, `mode must be "note" or "stimulus"`)
		return
	}
	if req.Priority == "" {
		req.Priority = "normal"
	}
	if !validCLIPriorities[req.Priority] {
		writeCLIError(w, http.StatusBadRequest, "priority must be one of low, normal, high, critical")
		return
	}

	stimulus := buildCLIStimulus(req)

	if err := s.publisher.Publish(r.Context(), stimulus); err != nil {
		writeCLIError(w, http.StatusServiceUnavailable, "failed to publish stimulus")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"stimulus_id": stimulus.StimulusID})
}

func writeCLIError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func buildCLIStimulus(req cliRequest) *common.Stimulus {
	channel := "cli"
	if req.Mode == "note" {
		channel = "cli-note"
	}

	now := time.Now().UTC()
	id, err := uuid.NewV7()
	if err != nil {
		// Extremely unlikely (crypto/rand failure); fall back to a random v4
		// rather than fail the request over id generation.
		id = uuid.New()
	}

	return &common.Stimulus{
		StimulusID:    id.String(),
		SchemaVersion: 1,
		ReceivedAt:    now,
		OccurredAt:    &now,
		Channel:       channel,
		Source: common.Source{
			Identity:      "cli",
			Authenticated: true,
			AuthMethod:    "system",
		},
		Content: common.Content{
			RawText:     req.Text,
			ContentType: "text",
			RawPayload:  json.RawMessage(`{}`),
			Attachments: []common.Attachment{},
		},
		ChannelMeta: common.ChannelMeta{
			MessageID: computeCLIMessageID(req.Text, now),
		},
		Hints: common.Hints{
			Priority: req.Priority,
			Tags:     []string{},
		},
		Override: common.Override{
			IsOverride: false,
			Params:     json.RawMessage(`{}`),
		},
	}
}

// computeCLIMessageID gives downstream consumers a stable dedup key. CLI
// input has no natural external id (unlike folder-watcher's
// filename+mtime), so this hashes the text plus received-at timestamp.
func computeCLIMessageID(text string, receivedAt time.Time) string {
	sum := sha256.Sum256([]byte(text + receivedAt.Format(time.RFC3339Nano)))
	return hex.EncodeToString(sum[:])
}
```

- [ ] **Step 6: Run the tests to confirm they pass**

Run: `cd perception-svc && go test ./httpserver/...`
Expected: PASS (all tests in `server_test.go` and `cli_test.go`)

- [ ] **Step 7: Wire the publisher into `main.go`**

Edit `perception-svc/main.go`, changing:

```go
	srv := httpserver.New(cfg.HTTPPort, cfg.WatchPaths, pub.Status)
```

to:

```go
	srv := httpserver.New(cfg.HTTPPort, cfg.WatchPaths, pub.Status, pub)
```

- [ ] **Step 8: Confirm the whole module builds**

Run: `cd perception-svc && go build ./...`
Expected: exits 0, no output

- [ ] **Step 9: Commit**

```bash
git add perception-svc/httpserver/server.go perception-svc/httpserver/server_test.go perception-svc/httpserver/cli.go perception-svc/httpserver/cli_test.go perception-svc/main.go
git commit -m "feat(perception-svc): add POST /api/perceive/cli endpoint for the CLI push channel"
```

---

### Task 2: thinking-svc — `cli-note` mechanical rule

**Files:**
- Create: `thinking-svc/rules/cli_note.go`
- Create: `thinking-svc/rules/cli_note_test.go`
- Modify: `thinking-svc/rules/rule.go`

**Interfaces:**
- Consumes: `rules.Rule`, `rules.Registry`, `rules.Match`, `rules.Process` (defined in `thinking-svc/rules/rule.go`); `errorReportParams` struct (defined in `thinking-svc/rules/error_report.go`, same package — reused as-is, no changes); `llm.Summarizer` (unused parameter, threaded through only because `Rule.Handle`'s signature requires it); `fakeSummarizer` (already defined in `thinking-svc/rules/rule_test.go`, same `rules_test` package — reused, not redefined).
- Produces: `rules.CLINoteRule` (a `Rule` value with `Name: "cli-note"`), appended to `rules.Registry`.

- [ ] **Step 1: Write the failing test**

Create `thinking-svc/rules/cli_note_test.go`:

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

func newCLINoteStimulus(text string, occurredAt time.Time) *common.Stimulus {
	return &common.Stimulus{
		StimulusID: "stim-cli-001",
		Channel:    "cli-note",
		ReceivedAt: time.Now().UTC(),
		OccurredAt: &occurredAt,
		Content: common.Content{
			RawText:     text,
			ContentType: "text",
			RawPayload:  json.RawMessage(`{}`),
		},
		Hints:    common.Hints{Priority: "normal"},
		Override: common.Override{Params: json.RawMessage(`{}`)},
	}
}

func TestCLINoteRule_Match_CLINoteChannel(t *testing.T) {
	s := newCLINoteStimulus("disk cleanup done", time.Now())
	if !rules.CLINoteRule.Match(s) {
		t.Error("expected match for cli-note channel")
	}
}

func TestCLINoteRule_Match_PlainCLIChannel_NoMatch(t *testing.T) {
	s := newCLINoteStimulus("disk cleanup done", time.Now())
	s.Channel = "cli"
	if rules.CLINoteRule.Match(s) {
		t.Error("expected no match for plain cli channel")
	}
}

func TestCLINoteRule_Handle_BuildsActionRequest(t *testing.T) {
	occurred := time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)
	s := newCLINoteStimulus("disk cleanup done", occurred)

	req, err := rules.CLINoteRule.Handle(context.Background(), s, &fakeSummarizer{})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if req.ActionHint != "append_daily_report_entry" {
		t.Errorf("ActionHint = %q, want append_daily_report_entry", req.ActionHint)
	}
	if req.Intent != "Log this note to today's daily report" {
		t.Errorf("Intent = %q, want the spec's intent text", req.Intent)
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
	if params.Summary != "disk cleanup done" {
		t.Errorf("Summary = %q, want verbatim text", params.Summary)
	}
	if params.RawContent != "disk cleanup done" {
		t.Errorf("RawContent = %q, want verbatim text", params.RawContent)
	}
	if params.SourcePath != "cli/note" {
		t.Errorf(`SourcePath = %q, want "cli/note"`, params.SourcePath)
	}
}

func TestMatch_FindsCLINoteRule(t *testing.T) {
	s := newCLINoteStimulus("disk cleanup done", time.Now())
	r := rules.Match(s)
	if r == nil {
		t.Fatal("Match = nil, want CLINoteRule for cli-note stimulus")
	}
	if r.Name != "cli-note" {
		t.Errorf("Name = %q, want cli-note", r.Name)
	}
}

func TestProcess_PlainCLIChannel_NoMatchYet(t *testing.T) {
	s := newCLINoteStimulus("remind me to check logs", time.Now())
	s.Channel = "cli"

	req, err := rules.Process(context.Background(), s, &fakeSummarizer{})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if req != nil {
		t.Errorf("Process = %v, want nil for plain cli channel (no reasoning rule yet)", req)
	}
}
```

- [ ] **Step 2: Run the test to confirm it fails**

Run: `cd thinking-svc && go test ./rules/... -run CLINote`
Expected: FAIL — `undefined: rules.CLINoteRule`

- [ ] **Step 3: Implement the rule**

Create `thinking-svc/rules/cli_note.go`:

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

// CLINoteRule implements docs/superpowers/specs/2026-07-18-soulman-cli-design.md's
// mechanical rule: any stimulus from the cli-note channel becomes an
// append_daily_report_entry Action Request, the same shape ErrorReportRule
// produces for folder-watcher — but built directly from the CLI-typed text,
// with no filename/watched-path extraction since there is no source file.
var CLINoteRule = Rule{
	Name: "cli-note",
	Match: func(s *common.Stimulus) bool {
		return s.Channel == "cli-note"
	},
	Handle: handleCLINote,
}

// handleCLINote builds the report entry mechanically — no LLM call. A short
// human-typed note doesn't need summarization, same reasoning as
// handleErrorReport for folder-watcher stimuli.
func handleCLINote(_ context.Context, s *common.Stimulus, _ llm.Summarizer) (*common.ActionRequest, error) {
	params, err := json.Marshal(errorReportParams{
		Summary:    s.Content.RawText,
		RawContent: s.Content.RawText,
		SourcePath: "cli/note",
		OccurredAt: s.OccurredAt,
	})
	if err != nil {
		return nil, fmt.Errorf("rules: marshal cli note parameters: %w", err)
	}

	req := &common.ActionRequest{
		CorrelationID:   uuid.NewString(),
		Intent:          "Log this note to today's daily report",
		ActionHint:      "append_daily_report_entry",
		Parameters:      params,
		RiskLevel:       "low",
		Urgency:         "normal",
		ExpectedOutcome: "one entry appended to today's report file",
		Fallback:        "if fs-agent fails, retry once; if it fails again, log to episodic memory with error:execution tag and give up silently — a missed report entry is not worth interrupting the human",
	}
	return req, nil
}
```

- [ ] **Step 4: Register the rule**

Edit `thinking-svc/rules/rule.go`, changing:

```go
var Registry = []Rule{
	ErrorReportRule,
}
```

to:

```go
var Registry = []Rule{
	ErrorReportRule,
	CLINoteRule,
}
```

- [ ] **Step 5: Run the tests to confirm they pass**

Run: `cd thinking-svc && go test ./rules/...`
Expected: PASS (all tests, including the pre-existing `error_report_test.go` and `rule_test.go` ones)

- [ ] **Step 6: Commit**

```bash
git add thinking-svc/rules/cli_note.go thinking-svc/rules/cli_note_test.go thinking-svc/rules/rule.go
git commit -m "feat(thinking-svc): add mechanical cli-note rule for CLI-logged report entries"
```

---

### Task 3: `cli` module — HTTP client

**Files:**
- Create: `cli/go.mod`
- Create: `cli/client/client.go`
- Create: `cli/client/client_test.go`

**Interfaces:**
- Produces: `client.Request{Text, Mode, Priority string}` (JSON tags `text`, `mode`, `priority`); `client.Send(baseURL string, req Request) (stimulusID string, err error)`. Task 4's `main.go` calls `client.Send` with the `parsedArgs` values.

- [ ] **Step 1: Create the module file**

Create `cli/go.mod`:

```
module soulman/cli

go 1.25.0
```

- [ ] **Step 2: Write the failing test**

Create `cli/client/client_test.go`:

```go
package client_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"soulman/cli/client"
)

func TestSend_Success_ReturnsStimulusID(t *testing.T) {
	var gotBody client.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/perceive/cli" {
			t.Errorf("path = %q, want /api/perceive/cli", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"stimulus_id": "stim-123"})
	}))
	defer srv.Close()

	id, err := client.Send(srv.URL, client.Request{Text: "hello", Mode: "note", Priority: "normal"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if id != "stim-123" {
		t.Errorf("id = %q, want stim-123", id)
	}
	if gotBody.Text != "hello" || gotBody.Mode != "note" || gotBody.Priority != "normal" {
		t.Errorf("request body = %+v, want {hello note normal}", gotBody)
	}
}

func TestSend_ServerError_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "text is required"})
	}))
	defer srv.Close()

	_, err := client.Send(srv.URL, client.Request{Text: "", Mode: "note", Priority: "normal"})
	if err == nil {
		t.Fatal("expected an error for a 400 response")
	}
}

func TestSend_ServerUnreachable_ReturnsError(t *testing.T) {
	_, err := client.Send("http://127.0.0.1:1", client.Request{Text: "hi", Mode: "stimulus", Priority: "normal"})
	if err == nil {
		t.Fatal("expected an error for an unreachable server")
	}
}
```

- [ ] **Step 3: Run the test to confirm it fails**

Run: `cd cli && go test ./client/...`
Expected: FAIL — `no required module provides package soulman/cli/client` (package doesn't exist yet)

- [ ] **Step 4: Implement the client**

Create `cli/client/client.go`:

```go
package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Request is the body sent to perception-svc's POST /api/perceive/cli
// endpoint, per docs/superpowers/specs/2026-07-18-soulman-cli-design.md.
type Request struct {
	Text     string `json:"text"`
	Mode     string `json:"mode"`
	Priority string `json:"priority"`
}

type response struct {
	StimulusID string `json:"stimulus_id"`
	Error      string `json:"error"`
}

// Send POSTs req to baseURL+"/api/perceive/cli" and returns the resulting
// stimulus_id, or an error describing why the request failed. There is no
// retry — this is an interactive CLI tool; the human re-running the
// command is the retry mechanism.
func Send(baseURL string, req Request) (string, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("client: marshal request: %w", err)
	}

	resp, err := http.Post(baseURL+"/api/perceive/cli", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("client: request to %s failed: %w", baseURL, err)
	}
	defer resp.Body.Close()

	var out response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("client: decode response: %w", err)
	}

	if resp.StatusCode != http.StatusAccepted {
		msg := out.Error
		if msg == "" {
			msg = fmt.Sprintf("unexpected status %d", resp.StatusCode)
		}
		return "", fmt.Errorf("client: %s", strings.TrimSpace(msg))
	}

	return out.StimulusID, nil
}
```

- [ ] **Step 5: Run the tests to confirm they pass**

Run: `cd cli && go test ./client/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add cli/go.mod cli/client/client.go cli/client/client_test.go
git commit -m "feat(cli): add HTTP client for perception-svc's /api/perceive/cli endpoint"
```

---

### Task 4: `cli` module — argument parsing and `main`

**Files:**
- Create: `cli/args.go`
- Create: `cli/args_test.go`
- Create: `cli/main.go`

**Interfaces:**
- Consumes: `client.Request`, `client.Send` (from Task 3, `soulman/cli/client`).
- Produces: `parsedArgs{Text, Mode, Priority string, Dev bool}`; `parseArgs(args []string) (parsedArgs, error)`.

- [ ] **Step 1: Write the failing test**

Create `cli/args_test.go`:

```go
package main

import "testing"

func TestParseArgs_PlainText_DefaultsToStimulusMode(t *testing.T) {
	got, err := parseArgs([]string{"remind me to check logs"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if got.Mode != "stimulus" {
		t.Errorf("Mode = %q, want stimulus", got.Mode)
	}
	if got.Text != "remind me to check logs" {
		t.Errorf("Text = %q, want the input text", got.Text)
	}
	if got.Priority != "normal" {
		t.Errorf("Priority = %q, want normal (default)", got.Priority)
	}
	if got.Dev {
		t.Error("Dev = true, want false (default)")
	}
}

func TestParseArgs_NoteSubcommand_SetsNoteMode(t *testing.T) {
	got, err := parseArgs([]string{"note", "disk cleanup done"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if got.Mode != "note" {
		t.Errorf("Mode = %q, want note", got.Mode)
	}
	if got.Text != "disk cleanup done" {
		t.Errorf("Text = %q, want the input text", got.Text)
	}
}

func TestParseArgs_PriorityFlag(t *testing.T) {
	got, err := parseArgs([]string{"--priority", "high", "note", "server is on fire"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if got.Priority != "high" {
		t.Errorf("Priority = %q, want high", got.Priority)
	}
	if got.Mode != "note" || got.Text != "server is on fire" {
		t.Errorf("Mode/Text = %q/%q, want note/'server is on fire'", got.Mode, got.Text)
	}
}

func TestParseArgs_DevFlag(t *testing.T) {
	got, err := parseArgs([]string{"--dev", "hello"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if !got.Dev {
		t.Error("Dev = false, want true")
	}
}

func TestParseArgs_InvalidPriority_Errors(t *testing.T) {
	_, err := parseArgs([]string{"--priority", "urgent", "hello"})
	if err == nil {
		t.Fatal("expected an error for an invalid --priority value")
	}
}

func TestParseArgs_NoText_Errors(t *testing.T) {
	_, err := parseArgs([]string{})
	if err == nil {
		t.Fatal("expected an error when no text is given")
	}
}

func TestParseArgs_NoteWithNoText_Errors(t *testing.T) {
	_, err := parseArgs([]string{"note"})
	if err == nil {
		t.Fatal("expected an error when 'note' has no following text")
	}
}

func TestParseArgs_MultiWordText_JoinsWithSpace(t *testing.T) {
	got, err := parseArgs([]string{"remind", "me", "to", "check", "logs"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if got.Text != "remind me to check logs" {
		t.Errorf("Text = %q, want joined words", got.Text)
	}
}
```

- [ ] **Step 2: Run the test to confirm it fails**

Run: `cd cli && go test ./... -run TestParseArgs`
Expected: FAIL — `undefined: parseArgs` (compile error)

- [ ] **Step 3: Implement argument parsing**

Create `cli/args.go`:

```go
package main

import (
	"fmt"
	"strings"
)

type parsedArgs struct {
	Text     string
	Mode     string
	Priority string
	Dev      bool
}

var validPriorities = map[string]bool{"low": true, "normal": true, "high": true, "critical": true}

// parseArgs parses os.Args[1:] into a parsedArgs. Supported forms:
//
//	soulman "<text>"                      -> Mode: stimulus
//	soulman note "<text>"                  -> Mode: note
//	soulman [--priority P] [--dev] ...     -> flags may appear anywhere
//
// A hand-rolled parser (not the stdlib flag package) because flag doesn't
// cleanly support flags interleaved with a "note" subcommand followed by
// free-form positional text.
func parseArgs(args []string) (parsedArgs, error) {
	res := parsedArgs{Priority: "normal"}
	var positional []string

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--dev":
			res.Dev = true
		case a == "--priority":
			if i+1 >= len(args) {
				return parsedArgs{}, fmt.Errorf("--priority requires a value")
			}
			i++
			res.Priority = args[i]
		case strings.HasPrefix(a, "--priority="):
			res.Priority = strings.TrimPrefix(a, "--priority=")
		default:
			positional = append(positional, a)
		}
	}

	if !validPriorities[res.Priority] {
		return parsedArgs{}, fmt.Errorf("invalid --priority %q: must be one of low, normal, high, critical", res.Priority)
	}

	if len(positional) == 0 {
		return parsedArgs{}, fmt.Errorf(`usage: soulman [--dev] [--priority low|normal|high|critical] [note] "<text>"`)
	}

	res.Mode = "stimulus"
	if positional[0] == "note" {
		res.Mode = "note"
		positional = positional[1:]
	}

	if len(positional) == 0 {
		return parsedArgs{}, fmt.Errorf("missing text argument")
	}
	res.Text = strings.Join(positional, " ")

	return res, nil
}
```

- [ ] **Step 4: Run the tests to confirm they pass**

Run: `cd cli && go test ./... -run TestParseArgs`
Expected: PASS

- [ ] **Step 5: Implement `main`**

Create `cli/main.go`:

```go
package main

import (
	"fmt"
	"os"

	"soulman/cli/client"
)

const (
	prodURL = "http://localhost:9001"
	devURL  = "http://localhost:9011"
)

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

- [ ] **Step 6: Confirm the whole module builds**

Run: `cd cli && go build ./...`
Expected: exits 0, produces a `cli` (or `cli.exe` on Windows) binary; no output

- [ ] **Step 7: Run the full module test suite**

Run: `cd cli && go test ./...`
Expected: PASS (all of `args_test.go` and `client/client_test.go`)

- [ ] **Step 8: Commit**

```bash
git add cli/args.go cli/args_test.go cli/main.go
git commit -m "feat(cli): add argument parsing and main entrypoint for the soulman command"
```

---

### Task 5: Update `CLAUDE.md`

**Files:**
- Modify: `CLAUDE.md` (vault root)

**Interfaces:** None — documentation only.

- [ ] **Step 1: Add a `cli/` row to the Repository Structure table**

In the `## Repository Structure` table, add a row after the `common/` row:

```
| `cli/`                          | Go module — `soulman` CLI tool, the CLI push channel (see `docs/superpowers/specs/2026-07-18-soulman-cli-design.md`) |
```

- [ ] **Step 2: Add a "The `cli` module" subsection**

In `## Services`, after the existing `### Shared config file` subsection, add:

```markdown
### The `cli` module

`cli/` is a fifth Go module, but not a service — it's the `soulman` command-line tool implementing the CLI push channel from `Perception module.md`. `soulman note "<text>"` appends directly to today's daily report (mechanical, no LLM — the same `append_daily_report_entry` path folder-watcher errors use, via a `cli-note`-channel rule in `thinking-svc`). `soulman "<text>"` (no `note`) sends a general stimulus on the `cli` channel for future goal-driven reasoning; no rule matches it yet, so today it's logged to Memory's raw input log only. The CLI POSTs to perception-svc's `POST /api/perceive/cli` endpoint — prod on `:9001` by default, `:9011` (`soulman-dev`) with the `--dev` flag. Built on demand (`go build ./cli`), not part of `start-everything.ps1`'s pre-build step.
```

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: document the soulman CLI module in CLAUDE.md"
```

---

## Self-Review Notes

- **Spec coverage:** Component 1 (perception-svc endpoint) → Task 1. Component 2 (thinking-svc rule) → Task 2. Component 3 (CLI tool: client + commands + flags + output/error handling) → Tasks 3–4. Testing section's four rows → covered by each task's own test steps. Out-of-scope items (override commands, shared Stimulus builder, general `cli` reasoning rule, CLI config file) are correctly absent from all tasks.
- **Placeholder scan:** none found — every step has complete, runnable code.
- **Type consistency:** `client.Request{Text, Mode, Priority}` (Task 3) matches the fields `main.go` populates in Task 4 and the JSON body `cli_test.go` asserts against in Task 1. `parsedArgs{Text, Mode, Priority, Dev}` (Task 4) is used consistently across `args.go`/`args_test.go`/`main.go`. `errorReportParams` (reused from `error_report.go`) has fields `Summary`/`RawContent`/`SourcePath`/`OccurredAt` matching what `cli_note.go` and `cli_note_test.go` both reference.
