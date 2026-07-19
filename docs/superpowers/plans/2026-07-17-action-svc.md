# action-svc Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go service that consolidates the Action module's Routing Agent, `fs-agent`, and `comm-agent` roles for v1: it consumes `soulman.thinking.request` messages over core NATS and appends `append_daily_report_entry` actions to a daily report file, and independently runs a 10:00 AM daily cron that sends yesterday's non-empty report via a pluggable `Notifier` (Discord first).

**Architecture:** Single binary, two independent concurrent paths sharing one `report` package for file path/format logic. Path 1: a core NATS subscriber on `soulman.thinking.request` dispatches on `action_type` (one handler in v1), retries once on failure, and fire-and-forgets an outcome record to `soulman.memory.write`. Path 2: a goroutine-based daily timer reads yesterday's report file and sends it through a `Notifier` interface, retrying 3x with exponential backoff. An HTTP server exposes `GET /health` only. NATS being down at startup degrades only the dispatch path — the cron and HTTP server still start.

**Tech Stack:** Go 1.21+, `github.com/nats-io/nats.go` (core NATS, not JetStream), `github.com/go-chi/chi/v5` (HTTP router), stdlib `net/http` for the Discord Bot REST API call.

## Global Constraints

- Working directory: `<worktree>\action-svc\` (worktree root is `C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\action-svc`, already on branch `feature/action-svc`)
- Go module: `soulman/action-svc`
- `action-svc/` is a plain subdirectory of the vault repo (like `memory-svc/`) — **not** a nested git repo. All git commands use `git -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\action-svc"` (the worktree root), never a separate `git init` inside `action-svc/`.
- NATS subject in: `soulman.thinking.request` (core NATS, not JetStream — ephemeral per `Messaging Bus.md`)
- NATS subject out: `soulman.memory.write` (fire-and-forget outcome log)
- HTTP port: `9004`
- `SOULMAN_ROOT` default: `C:\Users\Lenovo\soulman-dev` — may not exist on this machine; all report file code must create `$SOULMAN_ROOT\reports\` (parents included) on demand. Tests must always use `t.TempDir()` as the root override and must never touch the real default path.
- Report filename convention: `daily-report-<YYYY-MM-DD>.txt` under `$SOULMAN_ROOT\reports\`, dated by `occurred_at`, not "today"
- Report entry format: `<YYYY-MM-DD HH:MM local>  [<dir of source_path>]  <summary>` header line, optionally followed by `\n<raw_content verbatim>`; a second-or-later entry is preceded by exactly one blank line
- `DISCORD_BOT_TOKEN` / `DISCORD_CHANNEL_ID` are **not present in this environment** — never hardcode them; `DiscordNotifier` must be built behind the `Notifier` interface so unit tests inject a fake; any test that needs real Discord credentials must `t.Skipf` cleanly when the env vars are unset
- Tests needing a live NATS server read `NATS_URL` (default `nats://localhost:4222`) and must `t.Skipf` cleanly if unavailable
- Package names avoid stdlib collisions: `natsclient` (not `nats`), `httpserver` (not `http`)
- Task order matters: `report/` (shared by both consumers) is built before `dispatch/` and `scheduler/`, which both depend on it

---

## File Structure

```
action-svc/
├── main.go                 # wiring: config → notifier → nats(dispatch) → scheduler → http → wait for signal
├── go.mod
├── go.sum
├── config/
│   └── config.go            # Load() → Config from env vars
├── report/
│   └── report.go             # shared report file path/format logic (Entry, PathForDate, Append, Read)
├── notify/
│   ├── notifier.go           # Notifier interface
│   └── discord.go            # DiscordNotifier (Discord Bot REST API, 2000-char split)
├── dispatch/
│   ├── dispatch.go           # Dispatcher: action_type → handler map, retry-once, outcome publish
│   └── report_entry.go       # append_daily_report_entry handler (uses report package)
├── natsclient/
│   ├── subscriber.go         # core NATS subscribe on soulman.thinking.request
│   └── publisher.go          # core NATS publish to soulman.memory.write
├── scheduler/
│   └── daily.go               # 10:00 AM timer loop, reads yesterday's report, sends via Notifier
└── httpserver/
    └── server.go               # GET /health only
```

**Dependency flow** (no cycles): `report` ← `dispatch`, `report` ← `scheduler`, `notify` ← `scheduler`, `notify` ← `main`, `dispatch` ← `natsclient` (via `dispatch.Publisher` interface satisfied structurally, no import needed), all ← `main`

---

### Task 1: Scaffold + Config

**Files:**
- Create: `action-svc/main.go` (stub only)
- Create: `action-svc/go.mod`
- Create: `action-svc/config/config.go`
- Create: `action-svc/config/config_test.go`

**Interfaces:**
- Produces: `config.Config{NATSURL, HTTPPort, SoulmanRoot, ReportSendTime, ReportNotifier, DiscordBotToken, DiscordChannelID string}`, `config.Load() *Config`

- [ ] **Step 1: Create directory and init go module**

```powershell
New-Item -ItemType Directory -Force "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\action-svc\action-svc"
cd C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\action-svc\action-svc
go mod init soulman/action-svc
```

Expected: `go.mod` created containing `module soulman/action-svc`

- [ ] **Step 2: Write `config/config.go`**

```go
package config

import "os"

type Config struct {
	NATSURL          string
	HTTPPort         string
	SoulmanRoot      string
	ReportSendTime   string
	ReportNotifier   string
	DiscordBotToken  string
	DiscordChannelID string
}

func Load() *Config {
	return &Config{
		NATSURL:          env("NATS_URL", "nats://localhost:4222"),
		HTTPPort:         env("HTTP_PORT", "9004"),
		SoulmanRoot:      env("SOULMAN_ROOT", `C:\Users\Lenovo\soulman-dev`),
		ReportSendTime:   env("REPORT_SEND_TIME", "10:00"),
		ReportNotifier:   env("REPORT_NOTIFIER", "discord"),
		DiscordBotToken:  env("DISCORD_BOT_TOKEN", ""),
		DiscordChannelID: env("DISCORD_CHANNEL_ID", ""),
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

	"soulman/action-svc/config"
)

func TestLoad_Defaults(t *testing.T) {
	os.Unsetenv("NATS_URL")
	os.Unsetenv("HTTP_PORT")
	os.Unsetenv("SOULMAN_ROOT")
	os.Unsetenv("REPORT_SEND_TIME")
	os.Unsetenv("REPORT_NOTIFIER")
	os.Unsetenv("DISCORD_BOT_TOKEN")
	os.Unsetenv("DISCORD_CHANNEL_ID")

	cfg := config.Load()

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
}

func TestLoad_EnvOverride(t *testing.T) {
	os.Setenv("NATS_URL", "nats://remote:4222")
	os.Setenv("SOULMAN_ROOT", `C:\Users\Lenovo\soulman-prod`)
	os.Setenv("REPORT_SEND_TIME", "09:30")
	defer os.Unsetenv("NATS_URL")
	defer os.Unsetenv("SOULMAN_ROOT")
	defer os.Unsetenv("REPORT_SEND_TIME")

	cfg := config.Load()

	if cfg.NATSURL != "nats://remote:4222" {
		t.Errorf("NATSURL = %q, want nats://remote:4222", cfg.NATSURL)
	}
	if cfg.SoulmanRoot != `C:\Users\Lenovo\soulman-prod` {
		t.Errorf("SoulmanRoot = %q, want C:\\Users\\Lenovo\\soulman-prod", cfg.SoulmanRoot)
	}
	if cfg.ReportSendTime != "09:30" {
		t.Errorf("ReportSendTime = %q, want 09:30", cfg.ReportSendTime)
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

Expected output: `ok  	soulman/action-svc/config`

- [ ] **Step 6: Commit**

```
git -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\action-svc" add action-svc/main.go action-svc/go.mod action-svc/config/
git -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\action-svc" commit -m "feat(action-svc): scaffold action-svc with config package"
```

---

### Task 2: Report package (shared path/format logic)

**Files:**
- Create: `action-svc/report/report.go`
- Create: `action-svc/report/report_test.go`

**Interfaces:**
- Produces:
  - `report.Entry{Summary, RawContent, SourcePath string; OccurredAt time.Time}`
  - `report.PathForDate(root string, date time.Time) string`
  - `report.Append(root string, e Entry) (string, error)` — returns the report file path
  - `report.Read(root string, date time.Time) (string, error)` — returns `""` (no error) if the file doesn't exist

This package is consumed by both `dispatch` (writes, Task 4) and `scheduler` (reads, Task 6) — building it first per the shared-convention requirement in the action-svc spec.

- [ ] **Step 1: Write `report/report.go`**

```go
package report

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Entry is one daily-report entry, matching the format from
// error-report-action-design.md: a header line (timestamp, source
// directory, one-line summary) optionally followed by verbatim raw content.
type Entry struct {
	Summary    string
	RawContent string
	SourcePath string
	OccurredAt time.Time
}

// PathForDate returns the report file path for the given date, using the
// YYYY-MM-DD of the *occurred* date (not "today") per the error-report
// spec. Shared by the dispatch handler (writes) and the scheduler (reads)
// so the two can never disagree on the filename convention.
func PathForDate(root string, date time.Time) string {
	filename := fmt.Sprintf("daily-report-%s.txt", date.Format("2006-01-02"))
	return filepath.Join(root, "reports", filename)
}

// Append writes one entry to the report file for entry.OccurredAt's date,
// creating $root/reports/ (and the file) if either is missing. If the file
// already has content, the new entry is preceded by exactly one blank line.
func Append(root string, e Entry) (string, error) {
	dir := filepath.Join(root, "reports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("report: mkdir %s: %w", dir, err)
	}

	path := PathForDate(root, e.OccurredAt)

	info, statErr := os.Stat(path)
	nonEmpty := statErr == nil && info.Size() > 0

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("report: open %s: %w", path, err)
	}
	defer f.Close()

	text := formatEntry(e)
	if nonEmpty {
		text = "\n\n" + text
	}

	if _, err := f.WriteString(text); err != nil {
		return "", fmt.Errorf("report: write %s: %w", path, err)
	}
	return path, nil
}

func formatEntry(e Entry) string {
	header := fmt.Sprintf("%s  [%s]  %s",
		e.OccurredAt.Format("2006-01-02 15:04"), filepath.Dir(e.SourcePath), e.Summary)
	if e.RawContent == "" {
		return header
	}
	return header + "\n" + e.RawContent
}

// Read returns the full contents of the report file for the given date, or
// "" (with no error) if the file doesn't exist yet.
func Read(root string, date time.Time) (string, error) {
	path := PathForDate(root, date)
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("report: read %s: %w", path, err)
	}
	return string(b), nil
}
```

- [ ] **Step 2: Write `report/report_test.go`**

```go
package report_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"soulman/action-svc/report"
)

func TestAppend_CreatesReportsDirAndFile(t *testing.T) {
	root := t.TempDir()
	occurred := time.Date(2026, 7, 17, 14, 32, 0, 0, time.Local)

	path, err := report.Append(root, report.Entry{
		Summary:    "DigitalMe sync failed: connection timeout to remote host.",
		RawContent: "full stack trace",
		SourcePath: `C:\Users\Lenovo\DigitalMe\errors\err1.txt`,
		OccurredAt: occurred,
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	want := filepath.Join(root, "reports", "daily-report-2026-07-17.txt")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	got := string(b)

	if !strings.Contains(got, "2026-07-17 14:32") {
		t.Errorf("entry missing timestamp: %q", got)
	}
	if !strings.Contains(got, `[C:\Users\Lenovo\DigitalMe\errors]`) {
		t.Errorf("entry missing bracketed source dir: %q", got)
	}
	if !strings.Contains(got, "DigitalMe sync failed") {
		t.Errorf("entry missing summary: %q", got)
	}
	if !strings.Contains(got, "full stack trace") {
		t.Errorf("entry missing raw content: %q", got)
	}
}

func TestAppend_UsesOccurredAtDate_NotToday(t *testing.T) {
	root := t.TempDir()
	occurred := time.Date(2020, 1, 1, 9, 0, 0, 0, time.Local)

	path, err := report.Append(root, report.Entry{Summary: "s", OccurredAt: occurred})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if filepath.Base(path) != "daily-report-2020-01-01.txt" {
		t.Errorf("filename = %q, want daily-report-2020-01-01.txt", filepath.Base(path))
	}
}

func TestAppend_SecondEntry_PrecededByExactlyOneBlankLine(t *testing.T) {
	root := t.TempDir()
	day := time.Date(2026, 7, 17, 8, 0, 0, 0, time.Local)

	if _, err := report.Append(root, report.Entry{Summary: "first", OccurredAt: day}); err != nil {
		t.Fatalf("first append: %v", err)
	}
	day2 := time.Date(2026, 7, 17, 9, 0, 0, 0, time.Local)
	if _, err := report.Append(root, report.Entry{Summary: "second", OccurredAt: day2}); err != nil {
		t.Fatalf("second append: %v", err)
	}

	b, err := os.ReadFile(report.PathForDate(root, day))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	lines := strings.Split(string(b), "\n")
	idx := -1
	for i, l := range lines {
		if strings.Contains(l, "second") {
			idx = i
			break
		}
	}
	if idx < 2 {
		t.Fatalf("could not locate second entry with a preceding blank line in:\n%s", string(b))
	}
	if lines[idx-1] != "" {
		t.Errorf("line before second entry = %q, want blank", lines[idx-1])
	}
	if lines[idx-2] == "" {
		t.Errorf("expected exactly one blank line, found two")
	}
}

func TestAppend_EmptyRawContent_NoTrailingBodyLine(t *testing.T) {
	root := t.TempDir()
	day := time.Date(2026, 7, 17, 8, 0, 0, 0, time.Local)

	path, err := report.Append(root, report.Entry{
		Summary:    "err1.bin (binary, see attachment)",
		RawContent: "",
		SourcePath: `C:\errors\err1.bin`,
		OccurredAt: day,
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	b, _ := os.ReadFile(path)
	if strings.Count(string(b), "\n") != 0 {
		t.Errorf("expected a single line with no raw content, got: %q", string(b))
	}
}

func TestRead_MissingFile_ReturnsEmptyNoError(t *testing.T) {
	root := t.TempDir()
	content, err := report.Read(root, time.Date(2026, 7, 17, 0, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if content != "" {
		t.Errorf("content = %q, want empty for missing file", content)
	}
}

func TestRead_ReturnsWrittenContent(t *testing.T) {
	root := t.TempDir()
	day := time.Date(2026, 7, 17, 0, 0, 0, 0, time.Local)
	if _, err := report.Append(root, report.Entry{Summary: "hello", OccurredAt: day}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	content, err := report.Read(root, day)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(content, "hello") {
		t.Errorf("content = %q, want it to contain %q", content, "hello")
	}
}

func TestAppend_CreatesSoulmanRootIfMissing(t *testing.T) {
	// SOULMAN_ROOT may not exist on this machine — Append must create the
	// whole path, including root itself, not just reports/.
	root := filepath.Join(t.TempDir(), "does-not-exist-yet", "soulman-dev")
	day := time.Date(2026, 7, 17, 0, 0, 0, 0, time.Local)

	if _, err := report.Append(root, report.Entry{Summary: "s", OccurredAt: day}); err != nil {
		t.Fatalf("Append should create missing root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "reports")); err != nil {
		t.Errorf("reports dir was not created: %v", err)
	}
}
```

- [ ] **Step 3: Run tests**

```
go test ./report/... -v
```

Expected: all tests pass

- [ ] **Step 4: Commit**

```
git -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\action-svc" add action-svc/report/
git -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\action-svc" commit -m "feat(action-svc): report package with shared path/format logic"
```

---

### Task 3: Notify package (Notifier interface + DiscordNotifier)

**Files:**
- Create: `action-svc/notify/notifier.go`
- Create: `action-svc/notify/discord.go`
- Create: `action-svc/notify/discord_test.go`

**Interfaces:**
- Produces:
  - `notify.Notifier` interface: `Send(message string) error`
  - `notify.DiscordNotifier{BotToken, ChannelID, BaseURL string}`, `notify.NewDiscordNotifier(botToken, channelID string) *DiscordNotifier`
  - `(*DiscordNotifier).Send(message string) error` — splits into ≤2000-char chunks at blank-line boundaries, POSTs each to `{BaseURL}/channels/{ChannelID}/messages`

- [ ] **Step 1: Write `notify/notifier.go`**

```go
package notify

// Notifier is implemented by anything capable of delivering a report as a
// single logical message. Selected via REPORT_NOTIFIER config; Discord is
// the only implementation in v1 — the interface exists so sms/email can be
// added later without touching the scheduler.
type Notifier interface {
	Send(message string) error
}
```

- [ ] **Step 2: Write `notify/discord.go`**

```go
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const discordMaxMessageLen = 2000

// DiscordNotifier sends messages via Discord's Bot REST API. BotToken and
// ChannelID come from DISCORD_BOT_TOKEN / DISCORD_CHANNEL_ID — never
// hardcoded, and may be empty in environments where Discord isn't
// configured yet (Send will simply fail, which the caller handles like any
// other notifier failure).
type DiscordNotifier struct {
	BotToken  string
	ChannelID string
	BaseURL   string // overridable in tests; defaults to Discord's API
	client    *http.Client
}

func NewDiscordNotifier(botToken, channelID string) *DiscordNotifier {
	return &DiscordNotifier{
		BotToken:  botToken,
		ChannelID: channelID,
		BaseURL:   "https://discord.com/api/v10",
		client:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (d *DiscordNotifier) Send(message string) error {
	for _, chunk := range splitMessage(message, discordMaxMessageLen) {
		if err := d.sendOne(chunk); err != nil {
			return err
		}
	}
	return nil
}

func (d *DiscordNotifier) sendOne(content string) error {
	url := fmt.Sprintf("%s/channels/%s/messages", d.BaseURL, d.ChannelID)
	body, err := json.Marshal(map[string]string{"content": content})
	if err != nil {
		return fmt.Errorf("notify: marshal discord payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("notify: build discord request: %w", err)
	}
	req.Header.Set("Authorization", "Bot "+d.BotToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("notify: discord request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("notify: discord returned %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// splitMessage splits message into chunks no longer than maxLen, breaking
// only at blank-line (paragraph) boundaries — never mid-entry. If a single
// paragraph itself exceeds maxLen, it is kept whole as an oversized chunk
// rather than truncated (report entries are expected to stay well under the
// limit; this is a defensive fallback, not the common case).
func splitMessage(message string, maxLen int) []string {
	if len(message) <= maxLen {
		return []string{message}
	}

	parts := strings.Split(message, "\n\n")
	var chunks []string
	var current string

	for _, part := range parts {
		candidate := part
		if current != "" {
			candidate = current + "\n\n" + part
		}
		if len(candidate) > maxLen && current != "" {
			chunks = append(chunks, current)
			current = part
		} else {
			current = candidate
		}
	}
	if current != "" {
		chunks = append(chunks, current)
	}
	return chunks
}
```

- [ ] **Step 3: Write `notify/discord_test.go`**

```go
package notify_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"soulman/action-svc/notify"
)

func TestDiscordNotifier_Send_PostsToMessagesEndpoint(t *testing.T) {
	var mu sync.Mutex
	var gotPath, gotAuth, gotContent string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		gotContent = body["content"]
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := notify.NewDiscordNotifier("test-token", "12345")
	n.BaseURL = srv.URL

	if err := n.Send("hello world"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotPath != "/channels/12345/messages" {
		t.Errorf("path = %q, want /channels/12345/messages", gotPath)
	}
	if gotAuth != "Bot test-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bot test-token")
	}
	if gotContent != "hello world" {
		t.Errorf("content = %q, want %q", gotContent, "hello world")
	}
}

func TestDiscordNotifier_Send_NonOKStatus_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"message":"rate limited"}`))
	}))
	defer srv.Close()

	n := notify.NewDiscordNotifier("test-token", "12345")
	n.BaseURL = srv.URL

	if err := n.Send("hi"); err == nil {
		t.Error("expected error on non-2xx response")
	}
}

func TestDiscordNotifier_Send_LongMessage_SplitsAtBlankLines(t *testing.T) {
	var mu sync.Mutex
	var received []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		received = append(received, body["content"])
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := notify.NewDiscordNotifier("test-token", "12345")
	n.BaseURL = srv.URL

	var entries []string
	for i := 0; i < 40; i++ {
		entries = append(entries, strings.Repeat("x", 60))
	}
	message := strings.Join(entries, "\n\n")

	if err := n.Send(message); err != nil {
		t.Fatalf("Send: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) < 2 {
		t.Fatalf("expected message to be split into multiple sends, got %d", len(received))
	}
	for _, chunk := range received {
		if len(chunk) > 2000 {
			t.Errorf("chunk length %d exceeds 2000-char limit", len(chunk))
		}
		for _, part := range strings.Split(chunk, "\n\n") {
			if part != strings.Repeat("x", 60) {
				t.Errorf("chunk contains a mangled entry: %q", part)
			}
		}
	}
}

// Real Discord API integration test — DISCORD_BOT_TOKEN and
// DISCORD_CHANNEL_ID are not present in this environment (provided later by
// the repo owner), so this must skip cleanly rather than fail.
func TestDiscordNotifier_RealAPI_RequiresCredentials(t *testing.T) {
	token := os.Getenv("DISCORD_BOT_TOKEN")
	channel := os.Getenv("DISCORD_CHANNEL_ID")
	if token == "" || channel == "" {
		t.Skip("DISCORD_BOT_TOKEN / DISCORD_CHANNEL_ID not set — skipping live Discord integration test")
	}

	n := notify.NewDiscordNotifier(token, channel)
	if err := n.Send("action-svc integration test — please ignore"); err != nil {
		t.Fatalf("Send to real Discord API: %v", err)
	}
}
```

- [ ] **Step 4: Run tests**

```
go test ./notify/... -v
```

Expected: all tests pass or skip (the real-API test skips since Discord credentials are unset)

- [ ] **Step 5: Commit**

```
git -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\action-svc" add action-svc/notify/
git -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\action-svc" commit -m "feat(action-svc): Notifier interface and DiscordNotifier with 2000-char split"
```

---

### Task 4: Dispatch package (action_type dispatch + append_daily_report_entry handler)

**Files:**
- Create: `action-svc/dispatch/report_entry.go`
- Create: `action-svc/dispatch/dispatch.go`
- Create: `action-svc/dispatch/dispatch_test.go`

**Interfaces:**
- Consumes: `report.Entry`, `report.Append` (from Task 2)
- Produces:
  - `dispatch.ReportEntryParams{Summary, RawContent, SourcePath, OccurredAt string}`
  - `dispatch.AppendReportEntry` — package-level `var` of type `func(root string, params json.RawMessage) (string, error)`, overridable in tests
  - `dispatch.Request{TaskID, ActionType, Intent string; Parameters json.RawMessage}`
  - `dispatch.Publisher` interface: `PublishOutcome(actionType, status, taskID string) error` (satisfied by `*natsclient.Publisher`, Task 5 — defined here so this package doesn't import `natsclient`)
  - `dispatch.New(root string, publisher Publisher) *Dispatcher`
  - `(*Dispatcher).Handle(msg []byte)` — matches the `natsclient.Handler` signature used in Task 5

- [ ] **Step 1: Write `dispatch/report_entry.go`**

```go
package dispatch

import (
	"encoding/json"
	"fmt"
	"time"

	"soulman/action-svc/report"
)

type ReportEntryParams struct {
	Summary    string `json:"summary"`
	RawContent string `json:"raw_content"`
	SourcePath string `json:"source_path"`
	OccurredAt string `json:"occurred_at"`
}

// AppendReportEntry implements the append_daily_report_entry action. It is a
// package-level var (not a plain function) so tests can inject a failing
// stand-in to deterministically exercise Dispatcher's retry-then-give-up
// behaviour without needing to force a real filesystem failure.
var AppendReportEntry = func(root string, params json.RawMessage) (string, error) {
	var p ReportEntryParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("dispatch: unmarshal params: %w", err)
	}
	occurredAt, err := time.Parse(time.RFC3339, p.OccurredAt)
	if err != nil {
		return "", fmt.Errorf("dispatch: parse occurred_at %q: %w", p.OccurredAt, err)
	}
	entry := report.Entry{
		Summary:    p.Summary,
		RawContent: p.RawContent,
		SourcePath: p.SourcePath,
		OccurredAt: occurredAt.Local(),
	}
	path, err := report.Append(root, entry)
	if err != nil {
		return "", fmt.Errorf("dispatch: append report entry: %w", err)
	}
	return path, nil
}
```

- [ ] **Step 2: Write `dispatch/dispatch.go`**

```go
package dispatch

import (
	"encoding/json"
	"log"
)

type Request struct {
	TaskID     string          `json:"task_id"`
	ActionType string          `json:"action_type"`
	Intent     string          `json:"intent"`
	Parameters json.RawMessage `json:"parameters"`
}

// Publisher is satisfied by *natsclient.Publisher. Defined here (not in
// natsclient) so this package doesn't need to import natsclient.
type Publisher interface {
	PublishOutcome(actionType, status, taskID string) error
}

type Dispatcher struct {
	root      string
	publisher Publisher
}

func New(root string, publisher Publisher) *Dispatcher {
	return &Dispatcher{root: root, publisher: publisher}
}

// Handle is the NATS message handler for soulman.thinking.request. It never
// returns an error — all failures are logged and/or published as outcome
// records, per the "a missed report entry isn't worth interrupting the
// human" decision in the error-report-action spec.
func (d *Dispatcher) Handle(msg []byte) {
	var req Request
	if err := json.Unmarshal(msg, &req); err != nil {
		log.Printf("dispatch: unparseable request, dropping: %v", err)
		return
	}

	switch req.ActionType {
	case "append_daily_report_entry":
		d.dispatchAppendDailyReportEntry(req)
	default:
		log.Printf("dispatch: unknown action_type %q, dropping (task_id=%s)", req.ActionType, req.TaskID)
	}
}

func (d *Dispatcher) dispatchAppendDailyReportEntry(req Request) {
	_, err := AppendReportEntry(d.root, req.Parameters)
	if err != nil {
		log.Printf("dispatch: append_daily_report_entry failed for task %s, retrying once: %v", req.TaskID, err)
		_, err = AppendReportEntry(d.root, req.Parameters)
	}

	status := "success"
	if err != nil {
		status = "failed"
		log.Printf("dispatch: append_daily_report_entry failed for task %s after retry, giving up: %v", req.TaskID, err)
	}

	if d.publisher == nil {
		return
	}
	if pubErr := d.publisher.PublishOutcome(req.ActionType, status, req.TaskID); pubErr != nil {
		log.Printf("dispatch: outcome publish failed for task %s: %v", req.TaskID, pubErr)
	}
}
```

- [ ] **Step 3: Write `dispatch/dispatch_test.go`**

```go
package dispatch_test

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"soulman/action-svc/dispatch"
)

type fakePublisher struct {
	mu      sync.Mutex
	records []record
}

type record struct{ actionType, status, taskID string }

func (f *fakePublisher) PublishOutcome(actionType, status, taskID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = append(f.records, record{actionType, status, taskID})
	return nil
}

func (f *fakePublisher) last() (record, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.records) == 0 {
		return record{}, false
	}
	return f.records[len(f.records)-1], true
}

func TestHandle_UnknownActionType_DroppedWithoutPublish(t *testing.T) {
	pub := &fakePublisher{}
	d := dispatch.New(t.TempDir(), pub)

	req := dispatch.Request{TaskID: "t1", ActionType: "does_not_exist"}
	b, _ := json.Marshal(req)
	d.Handle(b)

	if _, ok := pub.last(); ok {
		t.Error("unknown action_type should not publish an outcome record")
	}
}

func TestHandle_AppendSuccess_PublishesSuccessOutcome(t *testing.T) {
	orig := dispatch.AppendReportEntry
	dispatch.AppendReportEntry = func(root string, params json.RawMessage) (string, error) {
		return "fake/path.txt", nil
	}
	defer func() { dispatch.AppendReportEntry = orig }()

	pub := &fakePublisher{}
	d := dispatch.New(t.TempDir(), pub)

	req := dispatch.Request{TaskID: "t2", ActionType: "append_daily_report_entry", Parameters: json.RawMessage(`{}`)}
	b, _ := json.Marshal(req)
	d.Handle(b)

	rec, ok := pub.last()
	if !ok {
		t.Fatal("expected an outcome record to be published")
	}
	if rec.status != "success" || rec.taskID != "t2" || rec.actionType != "append_daily_report_entry" {
		t.Errorf("outcome = %+v, want success/t2/append_daily_report_entry", rec)
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
	d := dispatch.New(t.TempDir(), pub)

	req := dispatch.Request{TaskID: "t3", ActionType: "append_daily_report_entry", Parameters: json.RawMessage(`{}`)}
	b, _ := json.Marshal(req)
	d.Handle(b)

	if calls != 2 {
		t.Errorf("AppendReportEntry called %d times, want 2 (one retry)", calls)
	}
	rec, ok := pub.last()
	if !ok {
		t.Fatal("expected an outcome record to be published")
	}
	if rec.status != "failed" {
		t.Errorf("status = %q, want failed", rec.status)
	}
}

func TestHandle_BadJSON_DoesNotPanicOrPublish(t *testing.T) {
	pub := &fakePublisher{}
	d := dispatch.New(t.TempDir(), pub)
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

- [ ] **Step 4: Run tests**

```
go test ./dispatch/... -v
```

Expected: all tests pass

- [ ] **Step 5: Commit**

```
git -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\action-svc" add action-svc/dispatch/
git -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\action-svc" commit -m "feat(action-svc): dispatch package with append_daily_report_entry and retry-once-then-log"
```

---

### Task 5: NATS client (core subscribe + outcome publisher)

**Files:**
- Create: `action-svc/natsclient/subscriber.go`
- Create: `action-svc/natsclient/publisher.go`
- Create: `action-svc/natsclient/natsclient_test.go`

**Interfaces:**
- Consumes: nothing from this codebase (wraps `github.com/nats-io/nats.go` directly); `*natsclient.Publisher` satisfies `dispatch.Publisher` and `scheduler.OutcomePublisher` structurally (duck typing — no import needed either direction)
- Produces:
  - `natsclient.Handler` — `func(data []byte)`, matches `(*dispatch.Dispatcher).Handle`
  - `natsclient.Connect(url string) (*nats.Conn, error)`
  - `natsclient.Subscribe(nc *nats.Conn, subject string, handler Handler) (*Subscriber, error)`
  - `(*Subscriber).Close() error`
  - `natsclient.OutcomeRecord{Type, ActionType, Status, TaskID string}`
  - `natsclient.NewPublisher(nc *nats.Conn) *Publisher`
  - `(*Publisher).PublishOutcome(actionType, status, taskID string) error` — publishes `{"type":"action_log","action_type":...,"status":...,"task_id":...}` to `soulman.memory.write`

- [ ] **Step 1: Add nats.go dependency**

```
go get github.com/nats-io/nats.go
```

- [ ] **Step 2: Write `natsclient/subscriber.go`**

```go
package natsclient

import (
	"fmt"

	"github.com/nats-io/nats.go"
)

type Handler func(data []byte)

type Subscriber struct {
	sub *nats.Subscription
}

func Connect(url string) (*nats.Conn, error) {
	nc, err := nats.Connect(url)
	if err != nil {
		return nil, fmt.Errorf("natsclient: connect to %s: %w", url, err)
	}
	return nc, nil
}

// Subscribe does a core NATS (non-JetStream) subscribe on subject —
// ephemeral per Messaging Bus.md; action-svc must be running to receive
// requests, the same accepted trade-off thinking-svc's publish side makes.
func Subscribe(nc *nats.Conn, subject string, handler Handler) (*Subscriber, error) {
	sub, err := nc.Subscribe(subject, func(msg *nats.Msg) {
		handler(msg.Data)
	})
	if err != nil {
		return nil, fmt.Errorf("natsclient: subscribe to %s: %w", subject, err)
	}
	return &Subscriber{sub: sub}, nil
}

func (s *Subscriber) Close() error {
	return s.sub.Unsubscribe()
}
```

- [ ] **Step 3: Write `natsclient/publisher.go`**

```go
package natsclient

import (
	"encoding/json"
	"fmt"

	"github.com/nats-io/nats.go"
)

type OutcomeRecord struct {
	Type       string `json:"type"`
	ActionType string `json:"action_type"`
	Status     string `json:"status"`
	TaskID     string `json:"task_id"`
}

type Publisher struct {
	nc *nats.Conn
}

func NewPublisher(nc *nats.Conn) *Publisher {
	return &Publisher{nc: nc}
}

// PublishOutcome fire-and-forgets an action_log record to
// soulman.memory.write. Nothing subscribes to this subject yet
// (memory-svc doesn't handle it) — this is forward-compatible logging per
// the action-svc spec, not a hard dependency.
func (p *Publisher) PublishOutcome(actionType, status, taskID string) error {
	rec := OutcomeRecord{Type: "action_log", ActionType: actionType, Status: status, TaskID: taskID}
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("natsclient: marshal outcome: %w", err)
	}
	if err := p.nc.Publish("soulman.memory.write", b); err != nil {
		return fmt.Errorf("natsclient: publish outcome: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Write `natsclient/natsclient_test.go`**

```go
package natsclient_test

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"soulman/action-svc/natsclient"
)

func natsURL() string {
	if u := os.Getenv("NATS_URL"); u != "" {
		return u
	}
	return "nats://localhost:4222"
}

func TestSubscribe_ReceivesMessage(t *testing.T) {
	url := natsURL()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer nc.Close()

	subject := fmt.Sprintf("soulman.thinking.request.test.%d", time.Now().UnixNano())

	var mu sync.Mutex
	var received []byte
	sub, err := natsclient.Subscribe(nc, subject, func(data []byte) {
		mu.Lock()
		received = data
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()

	payload := []byte(`{"task_id":"nats-test-1"}`)
	if err := nc.Publish(subject, payload); err != nil {
		t.Fatalf("publish: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := received
		mu.Unlock()
		if got != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("message not received within 3 seconds")
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

	pub := natsclient.NewPublisher(nc)
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
```

- [ ] **Step 5: Run tests**

```
go test ./natsclient/... -v -timeout 30s
```

Expected: both tests pass, or skip with "NATS not available" if no local NATS server is running

- [ ] **Step 6: Commit**

```
git -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\action-svc" add action-svc/natsclient/ action-svc/go.sum
git -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\action-svc" commit -m "feat(action-svc): core NATS subscriber and outcome publisher"
```

---

### Task 6: Scheduler (daily report delivery cron)

**Files:**
- Create: `action-svc/scheduler/daily.go`
- Create: `action-svc/scheduler/daily_test.go`

**Interfaces:**
- Consumes: `report.Read`, `report.PathForDate` (Task 2), `notify.Notifier` (Task 3)
- Produces:
  - `scheduler.OutcomePublisher` interface: `PublishOutcome(actionType, status, taskID string) error` (satisfied by `*natsclient.Publisher`)
  - `scheduler.New(root, sendTime string, notifier notify.Notifier, publisher OutcomePublisher) *Scheduler`
  - `(*Scheduler).Now func() time.Time` and `(*Scheduler).BackoffBase time.Duration` — exported fields, overridable by tests for deterministic time and fast retries
  - `(*Scheduler).Start()` — begins the daily timer loop in a goroutine
  - `(*Scheduler).Stop()`
  - `(*Scheduler).RunOnce()` — exported so tests can trigger one check-and-send cycle directly without waiting for the timer

- [ ] **Step 1: Write `scheduler/daily.go`**

```go
package scheduler

import (
	"log"
	"strconv"
	"strings"
	"time"

	"soulman/action-svc/notify"
	"soulman/action-svc/report"
)

// OutcomePublisher is satisfied by *natsclient.Publisher. Defined here (not
// in natsclient) so this package doesn't need to import natsclient.
type OutcomePublisher interface {
	PublishOutcome(actionType, status, taskID string) error
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
	if err != nil {
		status = "failed"
		log.Printf("scheduler: notifier send failed after 3 attempts: %v", err)
	}

	if s.publisher == nil {
		return
	}
	if pubErr := s.publisher.PublishOutcome("daily_report_delivery", status, ""); pubErr != nil {
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

- [ ] **Step 2: Write `scheduler/daily_test.go`**

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
)

type fakeNotifier struct {
	mu       sync.Mutex
	messages []string
	failN    int // number of Send calls to fail before succeeding
}

func (f *fakeNotifier) Send(message string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
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

type fakePublisher struct {
	mu      sync.Mutex
	records []record
}

type record struct{ actionType, status, taskID string }

func (f *fakePublisher) PublishOutcome(actionType, status, taskID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = append(f.records, record{actionType, status, taskID})
	return nil
}

func (f *fakePublisher) last() (record, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.records) == 0 {
		return record{}, false
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
	if !ok || rec.status != "success" || rec.actionType != "daily_report_delivery" {
		t.Errorf("outcome = %+v, ok=%v, want status=success actionType=daily_report_delivery", rec, ok)
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

	rec, ok := pub.last()
	if !ok || rec.status != "failed" {
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
	if !ok || rec.status != "success" {
		t.Errorf("outcome = %+v, ok=%v, want status=success", rec, ok)
	}
}
```

- [ ] **Step 3: Run tests**

```
go test ./scheduler/... -v
```

Expected: all tests pass

- [ ] **Step 4: Commit**

```
git -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\action-svc" add action-svc/scheduler/
git -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\action-svc" commit -m "feat(action-svc): daily report delivery scheduler with 3x exponential-backoff retry"
```

---

### Task 7: HTTP server

**Files:**
- Create: `action-svc/httpserver/server.go`
- Create: `action-svc/httpserver/server_test.go`

**Interfaces:**
- Produces:
  - `httpserver.New(port string) *Server`
  - `(*Server).Handler() http.Handler`
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

- [ ] **Step 3: Write `httpserver/server_test.go`**

```go
package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"soulman/action-svc/httpserver"
)

func TestHealth_ReturnsOK(t *testing.T) {
	srv := httpserver.New("9004")
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
	srv := httpserver.New("9004")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
```

- [ ] **Step 4: Run tests**

```
go test ./httpserver/... -v
```

Expected: both tests pass

- [ ] **Step 5: Commit**

```
git -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\action-svc" add action-svc/httpserver/ action-svc/go.sum
git -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\action-svc" commit -m "feat(action-svc): chi HTTP server with GET /health"
```

---

### Task 8: Main wiring + smoke test

**Files:**
- Modify: `action-svc/main.go` (replace stub)

**Interfaces:**
- Consumes: all packages — `config`, `notify`, `dispatch`, `natsclient`, `scheduler`, `httpserver`
- Produces: a working binary; startup sequence is: config → notifier → nats connect+subscribe (non-fatal if down) → scheduler.Start → http.Start (goroutine) → wait for signal

- [ ] **Step 1: Write `main.go`**

```go
package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"soulman/action-svc/config"
	"soulman/action-svc/dispatch"
	"soulman/action-svc/httpserver"
	"soulman/action-svc/natsclient"
	"soulman/action-svc/notify"
	"soulman/action-svc/scheduler"
)

func main() {
	cfg := config.Load()

	// Notifier — Discord is the only implementation in v1. Built regardless
	// of whether DISCORD_BOT_TOKEN/DISCORD_CHANNEL_ID are set; a missing
	// token surfaces as a Send failure, handled like any other notifier
	// failure (retried, then logged) rather than a startup crash.
	var notifier notify.Notifier
	switch cfg.ReportNotifier {
	case "discord":
		notifier = notify.NewDiscordNotifier(cfg.DiscordBotToken, cfg.DiscordChannelID)
	default:
		log.Fatalf("unsupported REPORT_NOTIFIER %q", cfg.ReportNotifier)
	}

	// NATS is non-fatal at startup: the dispatch side degrades until
	// reconnect, but the HTTP server and the daily cron don't depend on it.
	var publisher *natsclient.Publisher
	nc, natsErr := natsclient.Connect(cfg.NATSURL)
	if natsErr != nil {
		log.Printf("WARNING: nats unavailable (%v) — dispatch degraded until reconnect", natsErr)
	} else {
		publisher = natsclient.NewPublisher(nc)
		disp := dispatch.New(cfg.SoulmanRoot, publisher)
		sub, subErr := natsclient.Subscribe(nc, "soulman.thinking.request", disp.Handle)
		if subErr != nil {
			log.Printf("WARNING: nats subscribe failed: %v", subErr)
		} else {
			defer sub.Close()
			log.Printf("nats: subscribed to soulman.thinking.request")
		}
		defer nc.Close()
	}

	// Scheduler runs independently of NATS — a stalled cron doesn't block
	// new error entries, and a NATS outage doesn't prevent yesterday's
	// report from being sent.
	var schedPublisher scheduler.OutcomePublisher
	if publisher != nil {
		schedPublisher = publisher
	}
	sched := scheduler.New(cfg.SoulmanRoot, cfg.ReportSendTime, notifier, schedPublisher)
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

	log.Printf("action-svc started (NATS=%s connected=%v, HTTP=:%s, root=%s, notifier=%s)",
		cfg.NATSURL, natsErr == nil, cfg.HTTPPort, cfg.SoulmanRoot, cfg.ReportNotifier)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Printf("action-svc shutting down")
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

Expected: no errors; `action-svc.exe` produced in the working directory

- [ ] **Step 4: Smoke test — start the service and verify HTTP (best-effort; NATS/Discord may be unavailable in this environment)**

Run the service:
```
cd C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\action-svc\action-svc
.\action-svc.exe
```

Expected log output (NATS unavailable case, since no local NATS server is assumed running in this sandbox):
```
WARNING: nats unavailable (...) — dispatch degraded until reconnect
HTTP listening on :9004
action-svc started (NATS=nats://localhost:4222 connected=false, HTTP=:9004, root=C:\Users\Lenovo\soulman-dev, notifier=discord)
```

In another terminal, verify health endpoint:
```
curl http://localhost:9004/health
```

Expected:
```json
{"status":"ok"}
```

Stop the service with Ctrl+C. Expected final log line: `action-svc shutting down`

If a local NATS server *is* running (`nats-server` on `nats://localhost:4222`), optionally verify the dispatch path end-to-end:
```
nats pub soulman.thinking.request '{
  "task_id": "smoke-001",
  "action_type": "append_daily_report_entry",
  "intent": "Log this error to today'\''s daily report",
  "parameters": {
    "summary": "smoke test entry",
    "raw_content": "smoke test raw content",
    "source_path": "C:\\Users\\Lenovo\\DigitalMe\\errors\\smoke.txt",
    "occurred_at": "2026-07-17T14:32:00-06:00"
  }
}'
```

Then verify:
```powershell
Get-Content C:\Users\Lenovo\soulman-dev\reports\daily-report-2026-07-17.txt
```

Expected: a line containing `smoke test entry` and `smoke test raw content`.

- [ ] **Step 5: Run full test suite**

```
go test ./... -timeout 30s
```

Expected: all packages pass or skip (no failures) — NATS and Discord tests skip cleanly if those dependencies aren't available in this environment

- [ ] **Step 6: Commit**

```
git -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\action-svc" add action-svc/main.go action-svc/go.sum
git -C "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\.worktrees\action-svc" commit -m "feat(action-svc): main wiring — dispatch + scheduler + HTTP, NATS-optional startup"
```

---

## Self-Review

**Spec coverage check:**

| Spec section | Covered in |
|---|---|
| Core NATS (not JetStream) subscribe on `soulman.thinking.request` | Task 5 (`natsclient.Subscribe`), wired in Task 8 |
| Dispatch on `action_type` via map/switch | Task 4 (`Dispatcher.Handle`) |
| Only handler: `append_daily_report_entry` | Task 4 |
| Unknown `action_type`: log and drop | Task 4 (`TestHandle_UnknownActionType_DroppedWithoutPublish`) |
| Publish outcome record to `soulman.memory.write` on success | Task 5 (`Publisher.PublishOutcome`), Task 4 (`dispatchAppendDailyReportEntry`) |
| Outcome record shape `{"type":"action_log","action_type":...,"status":...,"task_id":...}` | Task 5 (`OutcomeRecord`) |
| Retry once on failure, then publish `"status":"failed"` and give up | Task 4 (`TestHandle_AppendFailsTwice_RetriesOnceThenPublishesFailedOutcome`) |
| Resolve `$SOULMAN_ROOT/reports/daily-report-<YYYY-MM-DD>.txt` using `occurred_at`'s date | Task 2 (`report.PathForDate`), Task 4 (`AppendReportEntry` parses `occurred_at`) |
| Create `reports/` dir (and root) if missing | Task 2 (`Append`'s `os.MkdirAll`, `TestAppend_CreatesSoulmanRootIfMissing`) |
| Entry format: timestamp, bracketed source dir, summary, raw content | Task 2 (`formatEntry`) |
| Entry preceded by exactly one blank line if file non-empty | Task 2 (`TestAppend_SecondEntry_PrecededByExactlyOneBlankLine`) |
| Binary-attachment case: no raw content line | Task 2 (`TestAppend_EmptyRawContent_NoTrailingBodyLine`) |
| File format: plain UTF-8, no headers/JSON | Task 2 (`report.go` writes plain text) |
| Daily cron at `REPORT_SEND_TIME` (default 10:00 local) | Task 6 (`Scheduler.nextRun`, `parseSendTime`) |
| Reads yesterday's report; skip if missing/empty/whitespace-only | Task 6 (`RunOnce`, `TestRunOnce_MissingReport_SkipsSend`, `TestRunOnce_WhitespaceOnlyReport_SkipsSend`) |
| Send via configured `Notifier`; log outcome to memory | Task 6 (`RunOnce` → `sendWithRetry` → `publisher.PublishOutcome`) |
| Report file never modified/deleted by the cron | Task 6 (`TestRunOnce_ReportNeverModifiedOrDeleted`) — `RunOnce` only calls `report.Read` |
| `Notifier` interface: `Send(message string) error` | Task 3 (`notifier.go`) |
| `DiscordNotifier`: POST `/channels/{channel_id}/messages` via Bot REST API | Task 3 (`discord.go`, `TestDiscordNotifier_Send_PostsToMessagesEndpoint`) |
| Discord config: `DISCORD_BOT_TOKEN`, `DISCORD_CHANNEL_ID`, never hardcoded | Task 1 (`config.go`), Task 3 (constructor takes them as params) |
| Split messages >2000 chars at blank-line boundaries | Task 3 (`splitMessage`, `TestDiscordNotifier_Send_LongMessage_SplitsAtBlankLines`) |
| Notifier send retry: 3 attempts, exponential backoff | Task 6 (`sendWithRetry`, `TestRunOnce_SendFailsAllThreeAttempts_PublishesFailedOutcome`, `TestRunOnce_SendFailsTwiceThenSucceeds_PublishesSuccessOutcome`) |
| Both dev/prod send independently based on own `reports/` dir | Architectural — no shared state between environments; `SOULMAN_ROOT` env var scopes everything (Task 1) |
| `GET /health` | Task 7 |
| Config vars: `NATS_URL`, `HTTP_PORT`, `SOULMAN_ROOT`, `REPORT_SEND_TIME`, `REPORT_NOTIFIER`, `DISCORD_BOT_TOKEN`, `DISCORD_CHANNEL_ID` | Task 1 |
| NATS unavailable at startup: warn, HTTP + cron still start | Task 8 (`main.go`) |
| Report-writing failures → `{"status":"failed","error":...}` semantics, retried once then dropped | Task 4 (mirrors error-report-action spec's fallback via `Dispatcher`) |
| Notifier send failures → retry w/ backoff, then log and give up, no escalation | Task 6 |
| Tests skip cleanly without live NATS | Task 5 (`t.Skipf`) |
| Tests skip cleanly without Discord credentials | Task 3 (`t.Skipf` in `TestDiscordNotifier_RealAPI_RequiresCredentials`) |
| `SOULMAN_ROOT` may not exist locally; tests never touch real default path | Task 2, 4, 6 (`t.TempDir()` everywhere) |

**Type consistency:**
- `report.Entry{Summary, RawContent, SourcePath string; OccurredAt time.Time}` — defined Task 2, consumed identically in Task 4 (`dispatch/report_entry.go`) and Task 6 tests
- `report.PathForDate(root string, date time.Time) string` — defined Task 2, used in Task 6 (`scheduler`) and Task 6 tests
- `report.Append(root string, e Entry) (string, error)` / `report.Read(root string, date time.Time) (string, error)` — consistent across Tasks 2, 4, 6
- `notify.Notifier` interface (`Send(message string) error`) — defined Task 3, consumed by `scheduler.New`'s `notifier` param (Task 6) and `main.go` (Task 8)
- `notify.NewDiscordNotifier(botToken, channelID string) *DiscordNotifier` — consistent across Tasks 3, 8
- `dispatch.Publisher` / `scheduler.OutcomePublisher` — both declare the identical method set `PublishOutcome(actionType, status, taskID string) error`, both satisfied structurally by `*natsclient.Publisher` (Task 5) without either package importing `natsclient`
- `dispatch.New(root string, publisher Publisher) *Dispatcher` and `(*Dispatcher).Handle(msg []byte)` — consistent across Tasks 4, 8; `Handle`'s signature (`func([]byte)`) matches `natsclient.Handler` exactly, so `disp.Handle` passes directly into `natsclient.Subscribe` in Task 8 with no adapter
- `natsclient.Connect(url string) (*nats.Conn, error)`, `natsclient.Subscribe(nc, subject, handler) (*Subscriber, error)`, `natsclient.NewPublisher(nc) *Publisher` — consistent across Tasks 5, 8
- `httpserver.New(port string) *Server` — consistent across Tasks 7, 8 (no `db` param, unlike the memory-svc precedent, since action-svc's `/health` has no dependency to report on)
- `scheduler.New(root, sendTime string, notifier notify.Notifier, publisher OutcomePublisher) *Scheduler` — consistent across Tasks 6, 8

**No placeholders found.**
