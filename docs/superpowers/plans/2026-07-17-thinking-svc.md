# thinking-svc Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go service that consumes Stimulus messages from the NATS STIMULUS stream, matches each against a small rule table, and for the one v1 rule (`folder-watcher` → `append_daily_report_entry`), calls DeepSeek for a one-line summary and publishes an `INVOKE_ACTION` Action Request to `soulman.thinking.request`.

**Architecture:** Single binary with a JetStream consumer on the `STIMULUS` stream feeding a rule matcher; a match triggers a DeepSeek summarization call (behind a `Summarizer` interface for testability) and builds an `ActionRequest`, published via core (non-JetStream) NATS. Unmatched stimuli and any per-message failure are ACKed and dropped — `soulman.thinking.request` has no redelivery mechanism in v1, so the consumer never NAKs. An HTTP server exposes `GET /health` only.

**Tech Stack:** Go 1.25+, `github.com/nats-io/nats.go` (JetStream v2 API for the consumer, core NATS for the publisher), `github.com/go-chi/chi/v5` (HTTP router), `github.com/google/uuid` (correlation IDs), stdlib `net/http` for the DeepSeek client.

## Prerequisites

Before starting:
1. **NATS** must be running with the `STIMULUS` stream already created (bootstrapped by `memory-svc`/`perception-svc`'s setup): `nats stream ls` should show `STIMULUS` with subjects `input.>,soulman.stimulus.raw`.
2. **Go 1.25+** installed: `go version`
3. `DEEPSEEK_API_KEY` is **not** available in this environment yet — it will be provided by the repo owner later. Nothing in this plan hardcodes a key; DeepSeek-dependent tests skip cleanly when the env var is unset.

## Global Constraints

- Working directory: `C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\thinking-svc\thinking-svc\`
- Go module: `soulman/thinking-svc`
- All git commands use `git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\thinking-svc` (the vault repo root inside the worktree — `thinking-svc/` is a plain subdirectory, not its own git repo, matching how `memory-svc/` is checked in)
- HTTP port: `9003` (env `HTTP_PORT`)
- NATS stream consumed: `STIMULUS` (durable consumer name `thinking-svc`) — must already exist before the service starts
- NATS subject published: `soulman.thinking.request` — core NATS publish, not JetStream (ephemeral, fire-and-forget per `Messaging Bus.md`)
- The consumer **always ACKs**, even on handler/publish failure — no redelivery mechanism exists for `soulman.thinking.request` in v1 (see the design spec's Error Handling table), so NAKing would only cause duplicate processing without recovering anything
- DeepSeek endpoint: `https://api.deepseek.com/chat/completions` (OpenAI-compatible Chat Completions API), model `deepseek-chat`, single non-streaming call per matched stimulus, 15s timeout (all configurable via env)
- Summarization input (`stimulus.content.raw_text`) truncated to 4000 characters before sending to DeepSeek; the untruncated text still travels through in the Action Request's `raw_content` parameter
- On DeepSeek timeout/error/empty response: fall back to `"<filename> (summary unavailable: <error>)"` — never fail the action over a missing summary
- `correlation_id` is generated fresh (UUID) per Action Request via `github.com/google/uuid`, even though v1 never uses it for resumption (forward-compatible with the full protocol)
- Tests needing live NATS read `NATS_URL` env var; default `nats://localhost:4222`; skip cleanly (`t.Skipf`) if NATS is unreachable
- Tests needing a real DeepSeek call read `DEEPSEEK_API_KEY`; skip cleanly (`t.Skipf`) if unset — **never hardcode a key or a fallback key anywhere in this codebase**
- Package names: `natsclient` (not `nats`), `httpserver` (not `http`) to avoid stdlib name collisions
- No Memory RETRIEVE queries, no multi-rule priority logic, no other `Thinking module.md` decision types besides `INVOKE_ACTION`, no result round-trip subscription — all explicitly out of scope per the design spec

---

## File Structure

```
thinking-svc/
├── main.go                    # wiring: config → llm → nats publisher → handler → nats consumer → http
├── go.mod                     # module: soulman/thinking-svc
├── go.sum
├── config/
│   ├── config.go               # Load() → Config from env vars
│   └── config_test.go
├── model/
│   ├── stimulus.go             # Stimulus struct — own copy, same schema as memory-svc/perception-svc
│   └── stimulus_test.go
├── llm/
│   ├── deepseek.go             # Summarizer interface + DeepSeekClient (satisfies it)
│   └── deepseek_test.go
├── rules/
│   ├── rule.go                 # Rule type, ActionRequest type, Registry, Match(), Process()
│   ├── rule_test.go
│   ├── error_report.go         # Rule 1: folder-watcher → append_daily_report_entry
│   └── error_report_test.go
├── natsclient/
│   ├── consumer.go             # Consumer: JetStream push consumer on STIMULUS, always ACKs
│   ├── consumer_test.go
│   ├── publisher.go            # Publisher: core NATS publish to soulman.thinking.request
│   └── publisher_test.go
└── httpserver/
    ├── server.go                # Server: New, Handler(), Start() — chi router, GET /health only
    └── server_test.go
```

**Dependency flow** (no cycles): `model` ← `rules`, `natsclient`; `llm` ← `rules`; `rules` ← `main`; `natsclient` ← `main`; `httpserver` ← `main`. `natsclient` does not import `rules` (its `Publisher.Publish` takes `any` and its `Handler` interface only needs `model.Stimulus`) — `main.go` is the only place that wires `rules` output into `natsclient`.

---

### Task 1: Scaffold + Config

**Files:**
- Create: `main.go` (stub only)
- Create: `go.mod`
- Create: `config/config.go`
- Create: `config/config_test.go`

**Interfaces:**
- Produces: `config.Config{NATSURL, HTTPPort, DeepSeekAPIKey, DeepSeekModel, DeepSeekBaseURL string; DeepSeekTimeoutSeconds int}`, `config.Load() *Config`

- [x] **Step 1: Create directory and init go module**

```powershell
New-Item -ItemType Directory -Force "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\thinking-svc\thinking-svc"
cd C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\thinking-svc\thinking-svc
go mod init soulman/thinking-svc
```

Expected: `go.mod` created containing `module soulman/thinking-svc`

- [x] **Step 2: Write `config/config.go`**

```go
package config

import (
	"os"
	"strconv"
)

type Config struct {
	NATSURL                string
	HTTPPort               string
	DeepSeekAPIKey         string
	DeepSeekModel          string
	DeepSeekBaseURL        string
	DeepSeekTimeoutSeconds int
}

func Load() *Config {
	return &Config{
		NATSURL:                env("NATS_URL", "nats://localhost:4222"),
		HTTPPort:                env("HTTP_PORT", "9003"),
		DeepSeekAPIKey:          env("DEEPSEEK_API_KEY", ""),
		DeepSeekModel:           env("DEEPSEEK_MODEL", "deepseek-chat"),
		DeepSeekBaseURL:         env("DEEPSEEK_BASE_URL", "https://api.deepseek.com"),
		DeepSeekTimeoutSeconds:  envInt("DEEPSEEK_TIMEOUT_SECONDS", 15),
	}
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

- [x] **Step 3: Write `config/config_test.go`**

```go
package config_test

import (
	"os"
	"testing"

	"soulman/thinking-svc/config"
)

func TestLoad_Defaults(t *testing.T) {
	os.Unsetenv("NATS_URL")
	os.Unsetenv("HTTP_PORT")
	os.Unsetenv("DEEPSEEK_API_KEY")
	os.Unsetenv("DEEPSEEK_MODEL")
	os.Unsetenv("DEEPSEEK_BASE_URL")
	os.Unsetenv("DEEPSEEK_TIMEOUT_SECONDS")

	cfg := config.Load()

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
}

func TestLoad_EnvOverride(t *testing.T) {
	os.Setenv("NATS_URL", "nats://remote:4222")
	os.Setenv("HTTP_PORT", "9099")
	os.Setenv("DEEPSEEK_API_KEY", "sk-test")
	os.Setenv("DEEPSEEK_TIMEOUT_SECONDS", "30")
	defer os.Unsetenv("NATS_URL")
	defer os.Unsetenv("HTTP_PORT")
	defer os.Unsetenv("DEEPSEEK_API_KEY")
	defer os.Unsetenv("DEEPSEEK_TIMEOUT_SECONDS")

	cfg := config.Load()

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
}

func TestLoad_InvalidTimeoutFallsBackToDefault(t *testing.T) {
	os.Setenv("DEEPSEEK_TIMEOUT_SECONDS", "not-a-number")
	defer os.Unsetenv("DEEPSEEK_TIMEOUT_SECONDS")

	cfg := config.Load()
	if cfg.DeepSeekTimeoutSeconds != 15 {
		t.Errorf("DeepSeekTimeoutSeconds = %d, want default 15 on invalid input", cfg.DeepSeekTimeoutSeconds)
	}
}
```

- [x] **Step 4: Write `main.go` stub**

```go
package main

func main() {}
```

- [x] **Step 5: Run tests**

```
go test ./config/...
```

Expected output: `ok  	soulman/thinking-svc/config`

- [x] **Step 6: Commit**

```
git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\thinking-svc add thinking-svc/main.go thinking-svc/go.mod thinking-svc/config
git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\thinking-svc commit -m "feat(thinking-svc): scaffold service with config package"
```

---

### Task 2: Stimulus model

**Files:**
- Create: `model/stimulus.go`
- Create: `model/stimulus_test.go`

**Interfaces:**
- Produces: `model.Stimulus` (and nested types `Source`, `Content`, `Attachment`, `ChannelMeta`, `Hints`, `Override`) — shared by `rules` and `natsclient`

- [x] **Step 1: Write `model/stimulus.go`**

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

- [x] **Step 2: Write `model/stimulus_test.go`**

```go
package model_test

import (
	"encoding/json"
	"testing"
	"time"

	"soulman/thinking-svc/model"
)

func TestStimulus_JSONRoundtrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	s := model.Stimulus{
		StimulusID:    "018f1a2b-3c4d-7e8f-9a0b-1c2d3e4f5a6b",
		SchemaVersion: 1,
		ReceivedAt:    now,
		Channel:       "folder-watcher",
		Source:        model.Source{Identity: "folder-watcher", Authenticated: true, AuthMethod: "system"},
		Content: model.Content{
			RawText:     "connection timeout",
			ContentType: "text",
			RawPayload:  json.RawMessage(`{}`),
		},
		ChannelMeta: model.ChannelMeta{
			ChannelSpecific: json.RawMessage(`{"watched_path":"C:\\Users\\Lenovo\\DigitalMe\\errors"}`),
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

	var m map[string]interface{}
	json.Unmarshal(b, &m)
	if _, ok := m["occurred_at"]; ok {
		t.Error("occurred_at should be omitted when nil")
	}
}

func TestStimulus_OccurredAt_Present(t *testing.T) {
	occurred := time.Date(2026, 7, 17, 14, 32, 0, 0, time.UTC)
	s := model.Stimulus{
		StimulusID: "id-occurred",
		ReceivedAt: time.Now().UTC(),
		OccurredAt: &occurred,
		Channel:    "folder-watcher",
	}
	b, _ := json.Marshal(s)

	var got model.Stimulus
	json.Unmarshal(b, &got)
	if got.OccurredAt == nil || !got.OccurredAt.Equal(occurred) {
		t.Errorf("OccurredAt = %v, want %v", got.OccurredAt, occurred)
	}
}
```

- [x] **Step 3: Run tests**

```
go test ./model/...
```

Expected: `ok  	soulman/thinking-svc/model`

- [x] **Step 4: Commit**

```
git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\thinking-svc add thinking-svc/model
git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\thinking-svc commit -m "feat(thinking-svc): add Stimulus model with JSON roundtrip"
```

---

### Task 3: LLM Summarizer (DeepSeek client)

**Files:**
- Create: `llm/deepseek.go`
- Create: `llm/deepseek_test.go`

**Interfaces:**
- Produces:
  - `llm.Summarizer` interface: `Summarize(ctx context.Context, text string) (string, error)`
  - `llm.NewDeepSeekClient(apiKey, baseURL, model string, timeout time.Duration) *DeepSeekClient` — satisfies `Summarizer`

- [x] **Step 1: Write `llm/deepseek.go`**

```go
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Summarizer produces a one-line summary of text. *DeepSeekClient is the
// production implementation; tests inject a fake to exercise the fallback
// paths in rules.ErrorReportRule without a network call or a real
// DEEPSEEK_API_KEY.
type Summarizer interface {
	Summarize(ctx context.Context, text string) (string, error)
}

const systemPrompt = "Summarize this error in one line, under 120 characters, plain text, no markdown."

// DeepSeekClient calls the DeepSeek Chat Completions API
// (https://api.deepseek.com/chat/completions, OpenAI-compatible).
type DeepSeekClient struct {
	apiKey     string
	baseURL    string
	model      string
	timeout    time.Duration
	httpClient *http.Client
}

func NewDeepSeekClient(apiKey, baseURL, model string, timeout time.Duration) *DeepSeekClient {
	return &DeepSeekClient{
		apiKey:     apiKey,
		baseURL:    baseURL,
		model:      model,
		timeout:    timeout,
		httpClient: &http.Client{},
	}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

// Summarize sends a single non-streaming Chat Completions request. Callers
// are responsible for truncating text before calling (thinking-svc's design
// spec truncates to 4000 characters before summarization; the full text
// still travels through in the Action Request separately).
func (c *DeepSeekClient) Summarize(ctx context.Context, text string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	reqBody, err := json.Marshal(chatRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: text},
		},
		Stream: false,
	})
	if err != nil {
		return "", fmt.Errorf("deepseek: marshal request: %w", err)
	}

	url := c.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("deepseek: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("deepseek: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("deepseek: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("deepseek: status %d: %s", resp.StatusCode, string(body))
	}

	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("deepseek: unmarshal response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("deepseek: empty choices in response")
	}

	return parsed.Choices[0].Message.Content, nil
}
```

- [x] **Step 2: Write `llm/deepseek_test.go`**

```go
package llm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"soulman/thinking-svc/llm"
)

func TestDeepSeekClient_Summarize_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %s, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want Bearer test-key", got)
		}

		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "deepseek-chat" {
			t.Errorf("model = %v, want deepseek-chat", body["model"])
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"one line summary"}}]}`))
	}))
	defer srv.Close()

	client := llm.NewDeepSeekClient("test-key", srv.URL, "deepseek-chat", 5*time.Second)
	summary, err := client.Summarize(context.Background(), "some error text")
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if summary != "one line summary" {
		t.Errorf("summary = %q, want %q", summary, "one line summary")
	}
}

func TestDeepSeekClient_Summarize_NonOKStatus_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()

	client := llm.NewDeepSeekClient("test-key", srv.URL, "deepseek-chat", 5*time.Second)
	_, err := client.Summarize(context.Background(), "some error text")
	if err == nil {
		t.Fatal("expected error for non-200 response")
	}
}

func TestDeepSeekClient_Summarize_Timeout_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte(`{"choices":[{"message":{"content":"too late"}}]}`))
	}))
	defer srv.Close()

	client := llm.NewDeepSeekClient("test-key", srv.URL, "deepseek-chat", 50*time.Millisecond)
	_, err := client.Summarize(context.Background(), "some error text")
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestDeepSeekClient_Summarize_EmptyChoices_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()

	client := llm.NewDeepSeekClient("test-key", srv.URL, "deepseek-chat", 5*time.Second)
	_, err := client.Summarize(context.Background(), "some error text")
	if err == nil {
		t.Fatal("expected error for empty choices")
	}
}

// TestDeepSeekClient_LiveAPI exercises the real DeepSeek API. It requires a
// real DEEPSEEK_API_KEY and is skipped otherwise. The repo owner provides
// the key outside of this environment — it is never hardcoded here.
func TestDeepSeekClient_LiveAPI(t *testing.T) {
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		t.Skip("DEEPSEEK_API_KEY not set — skipping live DeepSeek API test")
	}

	client := llm.NewDeepSeekClient(apiKey, "https://api.deepseek.com", "deepseek-chat", 15*time.Second)
	summary, err := client.Summarize(context.Background(), "connection timeout to remote host at 10.0.0.5:443")
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if strings.TrimSpace(summary) == "" {
		t.Error("expected non-empty summary from live API")
	}
	if len(summary) > 500 {
		t.Errorf("summary unexpectedly long (%d chars): %q", len(summary), summary)
	}
}
```

- [x] **Step 3: Run tests**

```
go test ./llm/... -v
```

Expected: the four `httptest`-backed tests pass; `TestDeepSeekClient_LiveAPI` prints `--- SKIP` with `DEEPSEEK_API_KEY not set`

- [x] **Step 4: Commit**

```
git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\thinking-svc add thinking-svc/llm
git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\thinking-svc commit -m "feat(thinking-svc): DeepSeek Summarizer with fake-server tests and skip-if-no-key live test"
```

---

### Task 4: Rules (rule table, ActionRequest, error-report rule)

**Files:**
- Create: `rules/rule.go`
- Create: `rules/rule_test.go`
- Create: `rules/error_report.go`
- Create: `rules/error_report_test.go`

**Interfaces:**
- Consumes: `model.Stimulus`, `llm.Summarizer`
- Produces:
  - `rules.ActionRequest{CorrelationID, Intent, ActionHint, RiskLevel, Urgency, ExpectedOutcome, Fallback string; Parameters map[string]any}` — JSON tags per the `INVOKE_ACTION` shape in `error-report-action-design.md`
  - `rules.Rule{Name string; Match func(*model.Stimulus) bool; Handle func(ctx, *model.Stimulus, llm.Summarizer) (*ActionRequest, error)}`
  - `rules.Registry []Rule` — ordered list, `ErrorReportRule` is the sole v1 entry
  - `rules.Match(s *model.Stimulus) *Rule` — first matching rule, or nil
  - `rules.Process(ctx, s *model.Stimulus, summarizer llm.Summarizer) (*ActionRequest, error)` — nil, nil if no rule matches
  - `rules.ErrorReportRule Rule` — Rule 1 from `error-report-action-design.md`

**Note on `source_path`:** `error-report-action-design.md` requires `source_path = "<watched_path>/<filename>"`, but `perception-svc-design.md`'s Stimulus Construction table only populates `content.attachments` (which carries `filename`) for the binary-attachment case — inlined text content has no filename field anywhere in the Stimulus schema. This is a genuine gap between the two approved specs. **Assumption made here:** when no attachment is present, fall back to the literal filename `"unknown-file"` so `source_path`'s *containing directory* (the only part `fs-agent`'s report line actually displays, per `error-report-action-design.md`) still resolves correctly to `watched_path`. Revisit if perception-svc's Stimulus schema gains an explicit filename field for inlined text.

- [x] **Step 1: Add uuid dependency**

```
go get github.com/google/uuid
```

- [x] **Step 2: Write `rules/rule.go`**

```go
package rules

import (
	"context"

	"soulman/thinking-svc/llm"
	"soulman/thinking-svc/model"
)

// ActionRequest is the payload published to soulman.thinking.request — the
// INVOKE_ACTION handoff shape from Thinking module.md's DECIDE step,
// specialized to the fields error-report-action-design.md's Thinking Rule
// defines.
type ActionRequest struct {
	CorrelationID   string         `json:"correlation_id"`
	Intent          string         `json:"intent"`
	ActionHint      string         `json:"action_hint"`
	Parameters      map[string]any `json:"parameters"`
	RiskLevel       string         `json:"risk_level"`
	Urgency         string         `json:"urgency"`
	ExpectedOutcome string         `json:"expected_outcome"`
	Fallback        string         `json:"fallback"`
}

// Rule matches a Stimulus and, on match, builds the ActionRequest to
// publish. Expressed as a value (not just a function) so future rules can
// carry a Name for logging/debugging.
type Rule struct {
	Name   string
	Match  func(*model.Stimulus) bool
	Handle func(ctx context.Context, s *model.Stimulus, summarizer llm.Summarizer) (*ActionRequest, error)
}

// Registry is the ordered list of rules evaluated against each Stimulus.
// Ordered (not a map) so future rules can be appended without restructuring;
// the first match wins. v1 ships exactly one entry.
var Registry = []Rule{
	ErrorReportRule,
}

// Match returns a pointer to the first rule in Registry whose Match
// function returns true for s, or nil if no rule matches.
func Match(s *model.Stimulus) *Rule {
	for i := range Registry {
		if Registry[i].Match(s) {
			return &Registry[i]
		}
	}
	return nil
}

// Process matches s against Registry and, on match, runs the rule's Handle.
// Returns (nil, nil) when no rule matches — the stimulus is a no-op, ACKed
// and dropped by the caller.
func Process(ctx context.Context, s *model.Stimulus, summarizer llm.Summarizer) (*ActionRequest, error) {
	r := Match(s)
	if r == nil {
		return nil, nil
	}
	return r.Handle(ctx, s, summarizer)
}
```

- [x] **Step 3: Write `rules/rule_test.go`**

```go
package rules_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"soulman/thinking-svc/model"
	"soulman/thinking-svc/rules"
)

type fakeSummarizer struct {
	summary string
	err     error
}

func (f *fakeSummarizer) Summarize(_ context.Context, _ string) (string, error) {
	return f.summary, f.err
}

func newFolderWatcherStimulus(rawText string, occurredAt time.Time) *model.Stimulus {
	return &model.Stimulus{
		StimulusID: "stim-001",
		Channel:    "folder-watcher",
		ReceivedAt: time.Now().UTC(),
		OccurredAt: &occurredAt,
		Content: model.Content{
			RawText:     rawText,
			ContentType: "text",
			RawPayload:  json.RawMessage(`{}`),
		},
		ChannelMeta: model.ChannelMeta{
			ChannelSpecific: json.RawMessage(`{"watched_path":"C:\\Users\\Lenovo\\DigitalMe\\errors"}`),
		},
		Hints:    model.Hints{Priority: "high", Tags: []string{"error", "folder-watcher"}},
		Override: model.Override{Params: json.RawMessage(`{}`)},
	}
}

func TestMatch_NoRuleForUnknownChannel(t *testing.T) {
	s := newFolderWatcherStimulus("boom", time.Now())
	s.Channel = "webhook"

	if r := rules.Match(s); r != nil {
		t.Errorf("Match = %v, want nil for unmatched channel", r)
	}
}

func TestMatch_FindsErrorReportRule(t *testing.T) {
	s := newFolderWatcherStimulus("boom", time.Now())

	r := rules.Match(s)
	if r == nil {
		t.Fatal("Match = nil, want ErrorReportRule for folder-watcher stimulus")
	}
	if r.Name != "error-report" {
		t.Errorf("Name = %q, want error-report", r.Name)
	}
}

func TestProcess_NoMatch_ReturnsNilNil(t *testing.T) {
	s := newFolderWatcherStimulus("boom", time.Now())
	s.Channel = "webhook"

	req, err := rules.Process(context.Background(), s, &fakeSummarizer{})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if req != nil {
		t.Errorf("Process = %v, want nil for unmatched stimulus", req)
	}
}

func TestProcess_Match_ReturnsActionRequest(t *testing.T) {
	s := newFolderWatcherStimulus("boom", time.Now())

	req, err := rules.Process(context.Background(), s, &fakeSummarizer{summary: "boom summary"})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if req == nil {
		t.Fatal("Process = nil, want ActionRequest for folder-watcher stimulus")
	}
	if req.ActionHint != "append_daily_report_entry" {
		t.Errorf("ActionHint = %q, want append_daily_report_entry", req.ActionHint)
	}
}
```

- [x] **Step 4: Run tests to verify they fail (ErrorReportRule not yet defined)**

```
go test ./rules/... 2>&1
```

Expected: build failure — `undefined: rules.ErrorReportRule`

- [x] **Step 5: Write `rules/error_report.go`**

```go
package rules

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"soulman/thinking-svc/llm"
	"soulman/thinking-svc/model"
)

const maxSummarizeInputChars = 4000

// ErrorReportRule implements the single v1 rule from
// docs/superpowers/specs/2026-07-17-error-report-action-design.md: any
// stimulus from the folder-watcher channel becomes an
// append_daily_report_entry Action Request.
var ErrorReportRule = Rule{
	Name: "error-report",
	Match: func(s *model.Stimulus) bool {
		return s.Channel == "folder-watcher"
	},
	Handle: handleErrorReport,
}

func handleErrorReport(ctx context.Context, s *model.Stimulus, summarizer llm.Summarizer) (*ActionRequest, error) {
	filename := attachmentFilename(s)
	watchedPath := watchedPath(s)

	var summary string
	if s.Content.RawText == "" {
		// Binary-attachment case (perception-svc's design): no raw text to
		// summarize or log.
		summary = fmt.Sprintf("%s (binary, see attachment)", filename)
	} else {
		input := s.Content.RawText
		if len(input) > maxSummarizeInputChars {
			input = input[:maxSummarizeInputChars]
		}
		got, err := summarizer.Summarize(ctx, input)
		if err != nil || strings.TrimSpace(got) == "" {
			summary = fmt.Sprintf("%s (summary unavailable: %v)", filename, err)
		} else {
			summary = got
		}
	}

	sourcePath := watchedPath + "/" + filename

	req := &ActionRequest{
		CorrelationID: uuid.NewString(),
		Intent:        "Log this error to today's daily report",
		ActionHint:    "append_daily_report_entry",
		Parameters: map[string]any{
			"summary":     summary,
			"raw_content": s.Content.RawText,
			"source_path": sourcePath,
			"occurred_at": occurredAtValue(s),
		},
		RiskLevel:       "low",
		Urgency:         "normal",
		ExpectedOutcome: "one entry appended to today's report file",
		Fallback:        "if fs-agent fails, retry once; if it fails again, log to episodic memory with error:execution tag and give up silently — a missed report entry is not worth interrupting the human",
	}
	return req, nil
}

// attachmentFilename extracts a filename for the source_path parameter.
// perception-svc's design only populates content.attachments for binary
// files; inlined text content has no filename anywhere in the Stimulus
// schema. See this plan's Task 4 note for the assumption behind the
// "unknown-file" fallback.
func attachmentFilename(s *model.Stimulus) string {
	if len(s.Content.Attachments) > 0 && s.Content.Attachments[0].Filename != "" {
		return s.Content.Attachments[0].Filename
	}
	return "unknown-file"
}

// watchedPath extracts channel_metadata.channel_specific.watched_path, the
// only key perception-svc-design.md guarantees for folder-watcher stimuli.
func watchedPath(s *model.Stimulus) string {
	var meta struct {
		WatchedPath string `json:"watched_path"`
	}
	if len(s.ChannelMeta.ChannelSpecific) > 0 {
		_ = json.Unmarshal(s.ChannelMeta.ChannelSpecific, &meta)
	}
	return meta.WatchedPath
}

// occurredAtValue passes stimulus.occurred_at through verbatim; nil marshals
// to JSON null (folder-watcher stimuli always set it per perception-svc's
// design, so this should not occur in practice).
func occurredAtValue(s *model.Stimulus) *time.Time {
	return s.OccurredAt
}
```

- [x] **Step 6: Run rule/registry tests to verify they pass**

```
go test ./rules/... -run "TestMatch|TestProcess" -v
```

Expected: all pass

- [x] **Step 7: Write `rules/error_report_test.go`**

```go
package rules_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"soulman/thinking-svc/model"
	"soulman/thinking-svc/rules"
)

func TestErrorReportRule_Match_FolderWatcher(t *testing.T) {
	s := newFolderWatcherStimulus("boom", time.Now())
	if !rules.ErrorReportRule.Match(s) {
		t.Error("expected match for folder-watcher channel")
	}
}

func TestErrorReportRule_Match_OtherChannel(t *testing.T) {
	s := newFolderWatcherStimulus("boom", time.Now())
	s.Channel = "webhook"
	if rules.ErrorReportRule.Match(s) {
		t.Error("expected no match for non folder-watcher channel")
	}
}

func TestErrorReportRule_Handle_BuildsActionRequest(t *testing.T) {
	occurred := time.Date(2026, 7, 17, 14, 32, 0, 0, time.UTC)
	s := newFolderWatcherStimulus("connection timeout to remote host", occurred)

	summarizer := &fakeSummarizer{summary: "DigitalMe sync failed: connection timeout to remote host."}

	req, err := rules.ErrorReportRule.Handle(context.Background(), s, summarizer)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if req.ActionHint != "append_daily_report_entry" {
		t.Errorf("ActionHint = %q, want append_daily_report_entry", req.ActionHint)
	}
	if req.Intent != "Log this error to today's daily report" {
		t.Errorf("Intent = %q, want the spec's intent text", req.Intent)
	}
	if req.RiskLevel != "low" {
		t.Errorf("RiskLevel = %q, want low", req.RiskLevel)
	}
	if req.Urgency != "normal" {
		t.Errorf("Urgency = %q, want normal", req.Urgency)
	}
	if req.ExpectedOutcome != "one entry appended to today's report file" {
		t.Errorf("ExpectedOutcome = %q, want the spec's text", req.ExpectedOutcome)
	}
	if req.CorrelationID == "" {
		t.Error("CorrelationID must be generated")
	}
	if req.Parameters["summary"] != "DigitalMe sync failed: connection timeout to remote host." {
		t.Errorf("summary = %v, want the fake summary", req.Parameters["summary"])
	}
	if req.Parameters["raw_content"] != "connection timeout to remote host" {
		t.Errorf("raw_content = %v, want verbatim raw text", req.Parameters["raw_content"])
	}
	wantPath := "C:\\Users\\Lenovo\\DigitalMe\\errors/unknown-file"
	if req.Parameters["source_path"] != wantPath {
		t.Errorf("source_path = %v, want %v", req.Parameters["source_path"], wantPath)
	}
}

func TestErrorReportRule_Handle_SummarizerError_FallsBack(t *testing.T) {
	s := newFolderWatcherStimulus("boom", time.Now())
	summarizer := &fakeSummarizer{err: errors.New("timeout")}

	req, err := rules.ErrorReportRule.Handle(context.Background(), s, summarizer)
	if err != nil {
		t.Fatalf("Handle should not error on summarizer failure: %v", err)
	}

	summary, _ := req.Parameters["summary"].(string)
	if !strings.Contains(summary, "summary unavailable") {
		t.Errorf("summary = %q, want fallback containing 'summary unavailable'", summary)
	}
	if !strings.Contains(summary, "timeout") {
		t.Errorf("summary = %q, want fallback to include underlying error", summary)
	}
}

func TestErrorReportRule_Handle_EmptyRawText_BinaryFallback(t *testing.T) {
	s := newFolderWatcherStimulus("", time.Now())
	s.Content.Attachments = []model.Attachment{{Filename: "screenshot.png", MIMEType: "image/png"}}

	summarizer := &fakeSummarizer{summary: "should not be called"}

	req, err := rules.ErrorReportRule.Handle(context.Background(), s, summarizer)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	summary, _ := req.Parameters["summary"].(string)
	if summary != "screenshot.png (binary, see attachment)" {
		t.Errorf("summary = %q, want binary fallback with attachment filename", summary)
	}
	if req.Parameters["raw_content"] != "" {
		t.Errorf("raw_content = %v, want empty for binary case", req.Parameters["raw_content"])
	}
}

func TestErrorReportRule_Handle_LongRawText_TruncatedForSummarizer(t *testing.T) {
	longText := strings.Repeat("a", 5000)
	s := newFolderWatcherStimulus(longText, time.Now())

	var gotLen int
	summarizer := &capturingSummarizer{onSummarize: func(text string) { gotLen = len(text) }}

	_, err := rules.ErrorReportRule.Handle(context.Background(), s, summarizer)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if gotLen != 4000 {
		t.Errorf("summarizer received %d chars, want truncated to 4000", gotLen)
	}
}

type capturingSummarizer struct {
	onSummarize func(text string)
}

func (c *capturingSummarizer) Summarize(_ context.Context, text string) (string, error) {
	c.onSummarize(text)
	return "captured", nil
}
```

- [x] **Step 8: Run full rules package tests**

```
go test ./rules/... -v
```

Expected: all tests pass

- [x] **Step 9: Commit**

```
git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\thinking-svc add thinking-svc/rules thinking-svc/go.mod thinking-svc/go.sum
git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\thinking-svc commit -m "feat(thinking-svc): rule table and error-report rule producing INVOKE_ACTION requests"
```

---

### Task 5: NATS client (Consumer + Publisher)

**Files:**
- Create: `natsclient/consumer.go`
- Create: `natsclient/consumer_test.go`
- Create: `natsclient/publisher.go`
- Create: `natsclient/publisher_test.go`

**Interfaces:**
- Consumes: `model.Stimulus`
- Produces:
  - `natsclient.Handler` interface: `Handle(ctx context.Context, s *model.Stimulus) error` (satisfied by `main.go`'s orchestrator in Task 7)
  - `natsclient.NewConsumer(natsURL, consumerName string, h Handler) (*Consumer, error)`
  - `(*Consumer).Start(ctx context.Context) error` — non-blocking; always ACKs, even when `Handler.Handle` returns an error
  - `(*Consumer).Close()`
  - `natsclient.NewPublisher(natsURL string) (*Publisher, error)`
  - `(*Publisher).Publish(ctx context.Context, v any) error` — marshals `v` to JSON, core NATS publish to `soulman.thinking.request`
  - `(*Publisher).Close()`

- [x] **Step 1: Add nats.go dependency**

```
go get github.com/nats-io/nats.go
```

- [x] **Step 2: Write `natsclient/consumer.go`**

```go
package natsclient

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"soulman/thinking-svc/model"
)

// Handler processes each parsed Stimulus. The consumer ACKs every message
// regardless of the returned error — soulman.thinking.request has no
// redelivery mechanism in v1 (see thinking-svc's design spec's Error
// Handling table), so NAKing here would only cause duplicate downstream
// actions without recovering anything.
type Handler interface {
	Handle(ctx context.Context, s *model.Stimulus) error
}

type Consumer struct {
	nc           *nats.Conn
	js           jetstream.JetStream
	handler      Handler
	consumerName string
	cc           jetstream.ConsumeContext
}

func NewConsumer(natsURL, consumerName string, h Handler) (*Consumer, error) {
	nc, err := nats.Connect(natsURL)
	if err != nil {
		return nil, fmt.Errorf("nats: connect to %s: %w", natsURL, err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats: jetstream: %w", err)
	}

	return &Consumer{nc: nc, js: js, handler: h, consumerName: consumerName}, nil
}

// Start subscribes to the STIMULUS stream and processes messages in the NATS
// library goroutine. Returns after the subscription is established;
// messages arrive asynchronously. Call Close to stop.
func (c *Consumer) Start(ctx context.Context) error {
	stream, err := c.js.Stream(ctx, "STIMULUS")
	if err != nil {
		return fmt.Errorf("nats: get STIMULUS stream: %w", err)
	}

	cons, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Name:      c.consumerName,
		Durable:   c.consumerName,
		AckPolicy: jetstream.AckExplicitPolicy,
	})
	if err != nil {
		return fmt.Errorf("nats: create consumer %s: %w", c.consumerName, err)
	}

	cc, err := cons.Consume(func(msg jetstream.Msg) {
		var s model.Stimulus
		if err := json.Unmarshal(msg.Data(), &s); err != nil {
			log.Printf("nats: unparseable stimulus (subject %s), ACKing to skip: %v", msg.Subject(), err)
			msg.Ack()
			return
		}

		if err := c.handler.Handle(ctx, &s); err != nil {
			log.Printf("nats: handling failed for %s, ACKing anyway (no redelivery for thinking.request): %v", s.StimulusID, err)
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

- [x] **Step 3: Write `natsclient/publisher.go`**

```go
package natsclient

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nats-io/nats.go"
)

const thinkingRequestSubject = "soulman.thinking.request"

// Publisher publishes Action Requests to soulman.thinking.request via core
// (non-JetStream) NATS — ephemeral, fire-and-forget, per Messaging Bus.md.
type Publisher struct {
	nc *nats.Conn
}

func NewPublisher(natsURL string) (*Publisher, error) {
	nc, err := nats.Connect(natsURL)
	if err != nil {
		return nil, fmt.Errorf("nats: connect to %s: %w", natsURL, err)
	}
	return &Publisher{nc: nc}, nil
}

// Publish marshals v to JSON and publishes it to soulman.thinking.request.
// v is typically a *rules.ActionRequest; this package accepts any to avoid
// depending on the rules package.
func (p *Publisher) Publish(_ context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("nats: marshal action request: %w", err)
	}
	if err := p.nc.Publish(thinkingRequestSubject, b); err != nil {
		return fmt.Errorf("nats: publish to %s: %w", thinkingRequestSubject, err)
	}
	return nil
}

func (p *Publisher) Close() {
	p.nc.Drain()
}
```

- [x] **Step 4: Write `natsclient/consumer_test.go`**

```go
package natsclient_test

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

	"soulman/thinking-svc/model"
	"soulman/thinking-svc/natsclient"
)

type mockHandler struct {
	mu       sync.Mutex
	received []*model.Stimulus
	err      error
}

func (m *mockHandler) Handle(_ context.Context, s *model.Stimulus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.received = append(m.received, s)
	return m.err
}

func (m *mockHandler) countOf(id string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, r := range m.received {
		if r.StimulusID == id {
			count++
		}
	}
	return count
}

func natsURL() string {
	if u := os.Getenv("NATS_URL"); u != "" {
		return u
	}
	return "nats://localhost:4222"
}

func TestConsumer_ReceivesAndHandlesMessage(t *testing.T) {
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

	consName := fmt.Sprintf("test-%d", time.Now().UnixNano())
	h := &mockHandler{}
	cons, err := natsclient.NewConsumer(url, consName, h)
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	defer cons.Close()

	if err := cons.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	id := fmt.Sprintf("cons-test-%d", time.Now().UnixNano())
	s := &model.Stimulus{
		StimulusID: id,
		ReceivedAt: time.Now().UTC(),
		Channel:    "folder-watcher",
		Content:    model.Content{RawText: "hi", RawPayload: json.RawMessage(`{}`)},
		Override:   model.Override{Params: json.RawMessage(`{}`)},
	}
	b, _ := json.Marshal(s)
	if _, err := js.Publish(ctx, "soulman.stimulus.raw", b); err != nil {
		t.Fatalf("publish: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if h.countOf(id) > 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("stimulus %s not received by handler within 5 seconds", id)
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
	h := &mockHandler{}
	cons, err := natsclient.NewConsumer(url, consName, h)
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	defer cons.Close()

	if err := cons.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := js.Publish(ctx, "soulman.stimulus.raw", []byte("not json")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	time.Sleep(2 * time.Second)

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
		if h.countOf(id) > 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("consumer did not recover after bad JSON message")
}

func TestConsumer_HandlerError_StillACKsExactlyOnce(t *testing.T) {
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

	consName := fmt.Sprintf("test-err-%d", time.Now().UnixNano())
	h := &mockHandler{err: fmt.Errorf("boom")}
	cons, err := natsclient.NewConsumer(url, consName, h)
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	defer cons.Close()

	if err := cons.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	id := fmt.Sprintf("handler-err-%d", time.Now().UnixNano())
	s := &model.Stimulus{
		StimulusID: id,
		ReceivedAt: time.Now().UTC(),
		Channel:    "folder-watcher",
		Content:    model.Content{RawPayload: json.RawMessage(`{}`)},
		Override:   model.Override{Params: json.RawMessage(`{}`)},
	}
	b, _ := json.Marshal(s)
	js.Publish(ctx, "soulman.stimulus.raw", b)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if h.countOf(id) > 0 {
			// Message arrived; give any (incorrect) redelivery a moment to
			// arrive too, then verify the count stayed at exactly 1 — proof
			// the consumer ACKed despite the handler error.
			time.Sleep(1500 * time.Millisecond)
			if count := h.countOf(id); count != 1 {
				t.Errorf("handler invoked %d times for %s, want exactly 1 (must ACK despite handler error)", count, id)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("stimulus %s not received by handler within 3 seconds", id)
}
```

- [x] **Step 5: Write `natsclient/publisher_test.go`**

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

	pub, err := natsclient.NewPublisher(url)
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	defer pub.Close()

	type testRequest struct {
		CorrelationID string `json:"correlation_id"`
		ActionHint    string `json:"action_hint"`
	}
	req := testRequest{CorrelationID: "corr-001", ActionHint: "append_daily_report_entry"}

	if err := pub.Publish(context.Background(), req); err != nil {
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
```

- [x] **Step 6: Run tests**

```
go test ./natsclient/... -v -timeout 30s
```

Expected: all tests pass if NATS is running locally; otherwise each test prints `--- SKIP` with `NATS not available`

- [x] **Step 7: Commit**

```
git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\thinking-svc add thinking-svc/natsclient thinking-svc/go.mod thinking-svc/go.sum
git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\thinking-svc commit -m "feat(thinking-svc): NATS consumer (always-ACK) and core-NATS publisher"
```

---

### Task 6: HTTP server

**Files:**
- Create: `httpserver/server.go`
- Create: `httpserver/server_test.go`

**Interfaces:**
- Produces:
  - `httpserver.New(port string) *Server`
  - `(*Server).Handler() http.Handler` — used by tests
  - `(*Server).Start() error` — blocks; calls `http.ListenAndServe`

- [x] **Step 1: Add chi dependency**

```
go get github.com/go-chi/chi/v5
```

- [x] **Step 2: Write `httpserver/server.go`**

```go
package httpserver

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Server struct {
	port   string
	router chi.Router
}

func New(port string) *Server {
	s := &Server{port: port}
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
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
```

- [x] **Step 3: Write `httpserver/server_test.go`**

```go
package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"soulman/thinking-svc/httpserver"
)

func TestHealth_ReturnsOK(t *testing.T) {
	srv := httpserver.New("9003")
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
}

func TestUnknownRoute_Returns404(t *testing.T) {
	srv := httpserver.New("9003")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/nope", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
```

- [x] **Step 4: Run tests**

```
go test ./httpserver/... -v
```

Expected: both tests pass

- [x] **Step 5: Commit**

```
git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\thinking-svc add thinking-svc/httpserver thinking-svc/go.mod thinking-svc/go.sum
git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\thinking-svc commit -m "feat(thinking-svc): chi HTTP server with GET /health"
```

---

### Task 7: Main wiring + smoke test

**Files:**
- Modify: `main.go` (replace stub)

**Interfaces:**
- Consumes: all packages — `config`, `llm`, `rules`, `natsclient`, `httpserver`, `model`
- Produces: a working binary; startup sequence is config → summarizer → publisher → consumer (with an in-`main` `Handler` that runs `rules.Process` then `Publisher.Publish`) → http.Start → wait for signal

- [x] **Step 1: Write `main.go`**

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"soulman/thinking-svc/config"
	"soulman/thinking-svc/httpserver"
	"soulman/thinking-svc/llm"
	"soulman/thinking-svc/model"
	"soulman/thinking-svc/natsclient"
	"soulman/thinking-svc/rules"
)

// stimulusHandler wires rules.Process's output into the NATS publisher. It
// implements natsclient.Handler. Kept in main so natsclient never needs to
// import rules (see this plan's File Structure note on dependency flow).
type stimulusHandler struct {
	summarizer llm.Summarizer
	publisher  *natsclient.Publisher
}

func (h *stimulusHandler) Handle(ctx context.Context, s *model.Stimulus) error {
	req, err := rules.Process(ctx, s, h.summarizer)
	if err != nil {
		return fmt.Errorf("rule handling failed for %s: %w", s.StimulusID, err)
	}
	if req == nil {
		return nil // no rule matched; no-op per the design spec
	}
	if err := h.publisher.Publish(ctx, req); err != nil {
		return fmt.Errorf("publish action request for %s: %w", s.StimulusID, err)
	}
	return nil
}

func main() {
	cfg := config.Load()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if cfg.DeepSeekAPIKey == "" {
		log.Printf("WARNING: DEEPSEEK_API_KEY not set — summarization calls will fail and fall back to deterministic summaries")
	}
	summarizer := llm.NewDeepSeekClient(
		cfg.DeepSeekAPIKey,
		cfg.DeepSeekBaseURL,
		cfg.DeepSeekModel,
		time.Duration(cfg.DeepSeekTimeoutSeconds)*time.Second,
	)

	publisher, err := natsclient.NewPublisher(cfg.NATSURL)
	if err != nil {
		log.Fatalf("nats publisher: %v", err)
	}
	defer publisher.Close()

	handler := &stimulusHandler{summarizer: summarizer, publisher: publisher}

	consumer, err := natsclient.NewConsumer(cfg.NATSURL, "thinking-svc", handler)
	if err != nil {
		log.Fatalf("nats consumer: %v", err)
	}
	defer consumer.Close()

	if err := consumer.Start(ctx); err != nil {
		log.Fatalf("nats consumer start: %v", err)
	}

	srv := httpserver.New(cfg.HTTPPort)
	go func() {
		log.Printf("HTTP listening on :%s", cfg.HTTPPort)
		if err := srv.Start(); err != nil {
			log.Printf("http: %v", err)
		}
	}()

	log.Printf("thinking-svc started (NATS=%s, HTTP=:%s, DeepSeek model=%s)",
		cfg.NATSURL, cfg.HTTPPort, cfg.DeepSeekModel)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Printf("thinking-svc shutting down")
}
```

- [x] **Step 2: Run `go mod tidy` to finalize dependencies**

```
go mod tidy
```

Expected: `go.sum` updated, no errors

- [x] **Step 3: Build to verify compilation**

```
go build ./...
```

Expected: no errors; `thinking-svc.exe` produced in the working directory

- [x] **Step 4: Smoke test — start the service and verify HTTP**

Ensure NATS is running with the `STIMULUS` stream created (see Prerequisites). Run the service:

```
cd C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\thinking-svc\thinking-svc
.\thinking-svc.exe
```

Expected log output (DEEPSEEK_API_KEY unset is fine — only summarization calls need it):

```
WARNING: DEEPSEEK_API_KEY not set — summarization calls will fail and fall back to deterministic summaries
nats: consuming STIMULUS stream as "thinking-svc"
HTTP listening on :9003
thinking-svc started (NATS=nats://localhost:4222, HTTP=:9003, DeepSeek model=deepseek-chat)
```

In another terminal, verify health endpoint:

```
curl http://localhost:9003/health
```

Expected:
```json
{"status":"ok"}
```

- [x] **Step 5: Smoke test — publish a folder-watcher stimulus and verify an Action Request is published**

In a third terminal, subscribe to the output subject:

```
nats sub soulman.thinking.request
```

In another terminal, publish a test Stimulus matching Rule 1:

```
nats pub soulman.stimulus.raw '{
  "stimulus_id": "smoke-test-002",
  "schema_version": 1,
  "received_at": "2026-07-17T14:32:00Z",
  "occurred_at": "2026-07-17T14:32:00Z",
  "channel": "folder-watcher",
  "source": {"identity": "folder-watcher", "authenticated": true, "auth_method": "system"},
  "content": {"raw_text": "connection timeout to remote host", "raw_payload": {}, "content_type": "text", "attachments": []},
  "channel_metadata": {"message_id": "", "thread_id": "", "reply_to": "", "channel_specific": {"watched_path": "C:\\Users\\Lenovo\\DigitalMe\\errors"}},
  "hints": {"intent": null, "priority": "high", "tags": ["error", "folder-watcher"]},
  "override": {"is_override": false, "command": null, "params": {}}
}'
```

Expected: the `nats sub` terminal prints a JSON message on `soulman.thinking.request` with `"action_hint":"append_daily_report_entry"` and a `"summary"` field containing the deterministic fallback text (since no `DEEPSEEK_API_KEY` is set): `"unknown-file (summary unavailable: ...)"`.

- [x] **Step 6: Run full test suite**

```
go test ./... -timeout 60s
```

Expected: all packages pass or skip (no failures). NATS-dependent tests pass if NATS is running, skip otherwise. `TestDeepSeekClient_LiveAPI` skips (no key in this environment).

- [x] **Step 7: Commit**

```
git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\thinking-svc add thinking-svc/main.go thinking-svc/go.mod thinking-svc/go.sum
git -C C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\thinking-svc commit -m "feat(thinking-svc): main wiring — consumer to rule matcher to DeepSeek summary to Action Request publish"
```

---

## Self-Review

**Spec coverage check** (against `2026-07-17-thinking-svc-design.md` and `2026-07-17-error-report-action-design.md`):

| Spec section | Covered in |
|---|---|
| JetStream consumer on `STIMULUS` stream | Task 5 |
| Rule table as ordered list (`Match`/`Handle`) | Task 4 |
| Rule 1: `channel == "folder-watcher"` match | Task 4 (`rules/error_report.go`) |
| DeepSeek call: endpoint, model, system prompt, 15s timeout | Task 3 |
| Input truncated to 4000 chars before summarization; untruncated text still in `raw_content` | Task 4 (`handleErrorReport`), tested in Task 4 Step 7 |
| Fallback summary `"<filename> (summary unavailable: <error>)"` on timeout/error/empty response | Task 3 (client returns error), Task 4 (fallback text) |
| Binary-attachment case: `"<filename> (binary, see attachment)"`, no raw content | Task 4 |
| `INVOKE_ACTION` JSON shape (`intent`, `action_hint`, `parameters`, `risk_level`, `urgency`, `expected_outcome`, `fallback`) | Task 4 (`ActionRequest`) |
| `parameters.summary/raw_content/source_path/occurred_at` | Task 4 |
| `correlation_id` generated (UUID), included even though unused for resumption in v1 | Task 4 |
| Publish to `soulman.thinking.request`, core NATS (not JetStream) | Task 5 (`Publisher`) |
| No subscription to `soulman.action.result.*` (v1 simplification) | Not implemented anywhere — correctly absent |
| `GET /health` | Task 6 |
| Config: `NATS_URL`, `HTTP_PORT` (9003), `DEEPSEEK_API_KEY`, `DEEPSEEK_MODEL`, `DEEPSEEK_BASE_URL`, `DEEPSEEK_TIMEOUT_SECONDS` | Task 1 |
| Malformed/unparseable stimulus → log, ACK, skip | Task 5 (`Consumer.Start`) |
| NATS publish of Action Request fails → log, stimulus still ACKed | Task 5 (`Consumer.Start` always ACKs regardless of `Handler.Handle` error) |
| No rule match → ACK, no-op, no episodic log write | Task 4 (`Process` returns `nil, nil`), Task 7 (`stimulusHandler.Handle` returns nil on nil req) |
| Module path `soulman/thinking-svc` | Task 1 |
| No hardcoded `DEEPSEEK_API_KEY`; live-API tests skip cleanly when unset | Task 3 (`TestDeepSeekClient_LiveAPI`) |

**Out-of-scope items confirmed absent:** Memory RETRIEVE queries, multi-rule priority conflicts, other decision types (`NO_ACTION`, `ACKNOWLEDGE`, `ASK_USER`, `UPDATE_GOAL`, `LEARN`, `REFUSE`), result round-trip/reasoning resumption, override handling (PAUSE/STOP/RESUME) — none of these appear anywhere in the plan.

**Type consistency:**
- `config.Load() *Config` with fields `NATSURL, HTTPPort, DeepSeekAPIKey, DeepSeekModel, DeepSeekBaseURL string; DeepSeekTimeoutSeconds int` — consistent across Tasks 1, 7
- `llm.Summarizer` interface (`Summarize(ctx, text) (string, error)`) — defined Task 3, consumed by `rules.Rule.Handle`'s signature (Task 4) and `stimulusHandler` (Task 7); `*llm.DeepSeekClient` satisfies it structurally (no explicit assertion needed, but the type used in Task 7 is `llm.Summarizer`, matching)
- `rules.ActionRequest` fields — defined Task 4, matched field-for-field by the fake `testRequest` shape used in Task 5's publisher test (independent struct, same JSON tags used for `correlation_id`/`action_hint` — publisher test intentionally doesn't import `rules` to keep the dependency-flow constraint honest)
- `rules.Rule{Name, Match, Handle}` — consistent across Tasks 4
- `natsclient.Handler` interface (`Handle(ctx, *model.Stimulus) error`) — defined Task 5, implemented by `stimulusHandler` in Task 7
- `natsclient.NewConsumer(natsURL, consumerName string, h Handler)`, `natsclient.NewPublisher(natsURL string)`, `(*Publisher).Publish(ctx, v any) error` — consistent across Tasks 5, 7
- `httpserver.New(port string) *Server` — consistent across Tasks 6, 7

**No placeholders found.**
