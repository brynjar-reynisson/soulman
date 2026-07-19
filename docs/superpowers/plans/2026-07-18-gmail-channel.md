# Gmail Channel (Perception) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Gmail channel to `perception-svc` — a new `gmailwatcher` package that polls a Gmail inbox via the REST API (OAuth2 offline refresh token, no repeated consent) and publishes matching messages as `Stimulus` events, exactly like `watcher` (folder-watcher) already does for filesystem events.

**Architecture:** `common/sharedconfig.Config` gains a `Gmail` field (query/seen-label/poll-interval). `perception-svc/config.Config` gains that plus three env-var-driven OAuth secrets. A new `gmailwatcher` package splits into `stimulus.go` (pure Gmail-message → `common.Stimulus` mapping, unit tested against fixtures), `client.go` (a small `gmailClient` interface plus a real implementation wrapping the official `gmail/v1` Go client — verified manually per the design spec, not unit tested), and `gmailwatcher.go` (the poll-loop orchestration, unit tested against a fake `gmailClient`). `perception-svc/main.go` wires it in alongside the existing folder watcher, skipping it (with a log warning, not a fatal error) if OAuth credentials aren't configured yet.

**Tech Stack:** Go, `golang.org/x/oauth2` + `golang.org/x/oauth2/google` (offline-refresh-token token source), `google.golang.org/api/gmail/v1` + `google.golang.org/api/option` (official Gmail REST client).

## Global Constraints

- Gmail credentials (`GMAIL_CLIENT_ID`, `GMAIL_CLIENT_SECRET`, `GMAIL_REFRESH_TOKEN`) are env-var-driven secrets, shared by dev and prod — not part of `common/sharedconfig`.
- `gmail.query`, `gmail.seen_label`, `gmail.poll_interval_seconds` live in `common/sharedconfig.Config` and are validated fatal-fast in `perception-svc/config.Load()` exactly like `watch_paths`/`nats_url`/`stimulus_subject` already are — this holds regardless of whether the OAuth secrets are configured.
- Missing/blank OAuth secrets are NOT fatal at startup — `gmailwatcher` is simply not constructed, logged as a warning; folder-watcher and the HTTP server start normally either way (adapter isolation, per [[Perception module]]).
- Attachments: metadata only (`filename`/`mime_type`/`size_bytes` + a synthetic `gmail://<message_id>/attachments/<attachment_id>` URI) — never download attachment bytes.
- No local checkpoint file for Gmail — Gmail's own labels are the checkpoint (the configured `query` always excludes the environment's own `seen_label`).
- `client.go`'s real Gmail-API-calling code has no automated test — verified manually against a live account, per the approved design spec's Testing section. Do not attempt to add HTTP-mocking infrastructure for it; that's explicitly out of scope.

---

### Task 1: Extend `common/sharedconfig.Config` with a `Gmail` field

**Files:**
- Modify: `common/sharedconfig/config.go`
- Modify: `common/sharedconfig/config_test.go`

**Interfaces:**
- Produces: `sharedconfig.Config.Gmail` of type `sharedconfig.GmailConfig{Query, SeenLabel string; PollIntervalSeconds int}` — Task 2 reads these exact field names.

- [ ] **Step 1: Write the failing test**

Add to `common/sharedconfig/config_test.go` (append after the existing tests):

```go
func TestLoad_GmailFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"watch_paths": ["C:\\a\\errors"],
		"gmail": {
			"query": "in:inbox is:unread -label:soulman/seen",
			"seen_label": "soulman/seen",
			"poll_interval_seconds": 60
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := sharedconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Gmail.Query != "in:inbox is:unread -label:soulman/seen" {
		t.Errorf("Gmail.Query = %q, want in:inbox is:unread -label:soulman/seen", cfg.Gmail.Query)
	}
	if cfg.Gmail.SeenLabel != "soulman/seen" {
		t.Errorf("Gmail.SeenLabel = %q, want soulman/seen", cfg.Gmail.SeenLabel)
	}
	if cfg.Gmail.PollIntervalSeconds != 60 {
		t.Errorf("Gmail.PollIntervalSeconds = %d, want 60", cfg.Gmail.PollIntervalSeconds)
	}
}

func TestLoad_MissingGmailField_ZeroValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"watch_paths": []}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := sharedconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Gmail.Query != "" {
		t.Errorf("Gmail.Query = %q, want empty when gmail block absent from JSON", cfg.Gmail.Query)
	}
	if cfg.Gmail.PollIntervalSeconds != 0 {
		t.Errorf("Gmail.PollIntervalSeconds = %d, want 0 when gmail block absent from JSON", cfg.Gmail.PollIntervalSeconds)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd "<worktree-root>" && go -C common test ./sharedconfig/...`
Expected: FAIL — compile error, `cfg.Gmail` undefined on `sharedconfig.Config`.

- [ ] **Step 3: Implement the schema change**

In `common/sharedconfig/config.go`, add `Gmail GmailConfig` to the `Config` struct (after `ConsumerNames`) and add the new type below it:

```go
type Config struct {
	WatchPaths             []string      `json:"watch_paths"`
	NATSURL                string        `json:"nats_url"`
	StimulusSubject        string        `json:"stimulus_subject"`
	ThinkingRequestSubject string        `json:"thinking_request_subject"`
	MemoryWriteSubject     string        `json:"memory_write_subject"`
	ConsumerNames          ConsumerNames `json:"consumer_names"`
	Gmail                  GmailConfig   `json:"gmail"`
}
```

```go
// GmailConfig holds perception-svc's Gmail channel settings: the search
// query used to find matching messages, the label applied to mark them
// processed (Gmail's own labels are the dedup checkpoint — no local state
// file), and how often to poll. Both dev and prod populate this — only the
// query/seen_label values differ, since both watch the same real inbox and
// each marks what it processes with its own label so neither re-processes
// the other's work.
type GmailConfig struct {
	Query               string `json:"query"`
	SeenLabel           string `json:"seen_label"`
	PollIntervalSeconds int    `json:"poll_interval_seconds"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go -C common test ./sharedconfig/...`
Expected: PASS (all tests, old and new)

- [ ] **Step 5: Commit**

```bash
git add common/sharedconfig/config.go common/sharedconfig/config_test.go
git commit -m "feat(common): add Gmail channel settings to shared config schema"
```

(Run `git` from the worktree root, not from inside `common/`.)

---

### Task 2: `perception-svc` config gains Gmail secrets + shared Gmail fields

**Files:**
- Modify: `perception-svc/config/config.go`
- Modify: `perception-svc/config/config_test.go`

**Interfaces:**
- Consumes: `sharedconfig.Config.Gmail` (Task 1).
- Produces: `config.Config{..., GmailClientID, GmailClientSecret, GmailRefreshToken, GmailQuery, GmailSeenLabel string; GmailPollIntervalSeconds int}` — Task 6 (`main.go`) reads these exact field names.

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

type gmailFields struct {
	Query               string `json:"query"`
	SeenLabel           string `json:"seen_label"`
	PollIntervalSeconds int    `json:"poll_interval_seconds"`
}

type sharedFields struct {
	WatchPaths      []string    `json:"watch_paths"`
	NATSURL         string      `json:"nats_url"`
	StimulusSubject string      `json:"stimulus_subject"`
	Gmail           gmailFields `json:"gmail"`
}

// validGmail is a ready-to-use gmailFields value for tests that aren't
// specifically exercising Gmail validation — every test needs a valid one
// since Load validates the gmail block fatally regardless of whether the
// GMAIL_CLIENT_ID/SECRET/REFRESH_TOKEN secrets are set.
var validGmail = gmailFields{
	Query:               "in:inbox is:unread -label:soulman/seen",
	SeenLabel:           "soulman/seen",
	PollIntervalSeconds: 60,
}

func writeConfigFile(t *testing.T, watchPaths []string, natsURL, stimulusSubject string, gmail gmailFields) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	data, err := json.Marshal(sharedFields{
		WatchPaths:      watchPaths,
		NATSURL:         natsURL,
		StimulusSubject: stimulusSubject,
		Gmail:           gmail,
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
	os.Unsetenv("GMAIL_CLIENT_ID")
	os.Unsetenv("GMAIL_CLIENT_SECRET")
	os.Unsetenv("GMAIL_REFRESH_TOKEN")
}

func TestLoad_Defaults(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, []string{`C:\Users\Lenovo\DigitalMe\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail)
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
	if cfg.GmailQuery != "in:inbox is:unread -label:soulman/seen" {
		t.Errorf("GmailQuery = %q, want in:inbox is:unread -label:soulman/seen", cfg.GmailQuery)
	}
	if cfg.GmailSeenLabel != "soulman/seen" {
		t.Errorf("GmailSeenLabel = %q, want soulman/seen", cfg.GmailSeenLabel)
	}
	if cfg.GmailPollIntervalSeconds != 60 {
		t.Errorf("GmailPollIntervalSeconds = %d, want 60", cfg.GmailPollIntervalSeconds)
	}
	if cfg.GmailClientID != "" {
		t.Errorf("GmailClientID = %q, want empty when GMAIL_CLIENT_ID unset", cfg.GmailClientID)
	}
	if cfg.GmailClientSecret != "" {
		t.Errorf("GmailClientSecret = %q, want empty when GMAIL_CLIENT_SECRET unset", cfg.GmailClientSecret)
	}
	if cfg.GmailRefreshToken != "" {
		t.Errorf("GmailRefreshToken = %q, want empty when GMAIL_REFRESH_TOKEN unset", cfg.GmailRefreshToken)
	}
}

func TestLoad_SharedConfigValues(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	gmail := gmailFields{
		Query:               "in:inbox is:unread -label:soulman/seen-dev",
		SeenLabel:           "soulman/seen-dev",
		PollIntervalSeconds: 60,
	}
	configPath := writeConfigFile(t, []string{`C:\a\errors`, `C:\b\errors`, `C:\c\errors`}, "nats://remote:4222", "soulman.dev.stimulus.raw", gmail)
	os.Setenv("CONFIG_PATH", configPath)
	os.Setenv("HTTP_PORT", "9999")
	os.Setenv("CHECKPOINT_PATH", "./data/checkpoints.json")
	os.Setenv("RECONCILE_INTERVAL_SECONDS", "45")
	os.Setenv("GMAIL_CLIENT_ID", "client-123")
	os.Setenv("GMAIL_CLIENT_SECRET", "secret-456")
	os.Setenv("GMAIL_REFRESH_TOKEN", "refresh-789")

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
	if cfg.GmailQuery != "in:inbox is:unread -label:soulman/seen-dev" {
		t.Errorf("GmailQuery = %q, want in:inbox is:unread -label:soulman/seen-dev", cfg.GmailQuery)
	}
	if cfg.GmailSeenLabel != "soulman/seen-dev" {
		t.Errorf("GmailSeenLabel = %q, want soulman/seen-dev", cfg.GmailSeenLabel)
	}
	if cfg.GmailPollIntervalSeconds != 60 {
		t.Errorf("GmailPollIntervalSeconds = %d, want 60", cfg.GmailPollIntervalSeconds)
	}
	if cfg.GmailClientID != "client-123" {
		t.Errorf("GmailClientID = %q, want client-123", cfg.GmailClientID)
	}
	if cfg.GmailClientSecret != "secret-456" {
		t.Errorf("GmailClientSecret = %q, want secret-456", cfg.GmailClientSecret)
	}
	if cfg.GmailRefreshToken != "refresh-789" {
		t.Errorf("GmailRefreshToken = %q, want refresh-789", cfg.GmailRefreshToken)
	}
}

func TestLoad_InvalidReconcileInterval_FallsBackToDefault(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail)
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

	configPath := writeConfigFile(t, []string{}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty watch_paths, got nil")
	}
}

func TestLoad_EmptyNATSURL_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "", "soulman.stimulus.raw", validGmail)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty nats_url, got nil")
	}
}

func TestLoad_EmptyStimulusSubject_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "", validGmail)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty stimulus_subject, got nil")
	}
}

func TestLoad_EmptyGmailQuery_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	gmail := validGmail
	gmail.Query = ""
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", gmail)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty gmail.query, got nil")
	}
}

func TestLoad_EmptyGmailSeenLabel_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	gmail := validGmail
	gmail.SeenLabel = ""
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", gmail)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty gmail.seen_label, got nil")
	}
}

func TestLoad_ZeroGmailPollInterval_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	gmail := validGmail
	gmail.PollIntervalSeconds = 0
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", gmail)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for zero gmail.poll_interval_seconds, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go -C perception-svc test ./config/...`
Expected: FAIL — compile error, `cfg.GmailQuery` etc. undefined on `config.Config`.

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

	GmailClientID            string
	GmailClientSecret        string
	GmailRefreshToken        string
	GmailQuery               string
	GmailSeenLabel           string
	GmailPollIntervalSeconds int
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
	if shared.Gmail.Query == "" {
		return nil, fmt.Errorf("shared config %s has no gmail.query configured", configPath)
	}
	if shared.Gmail.SeenLabel == "" {
		return nil, fmt.Errorf("shared config %s has no gmail.seen_label configured", configPath)
	}
	if shared.Gmail.PollIntervalSeconds <= 0 {
		return nil, fmt.Errorf("shared config %s has no positive gmail.poll_interval_seconds configured", configPath)
	}

	return &Config{
		NATSURL:           shared.NATSURL,
		HTTPPort:          env("HTTP_PORT", "9001"),
		WatchPaths:        shared.WatchPaths,
		CheckpointPath:    env("CHECKPOINT_PATH", "./checkpoints.json"),
		ReconcileInterval: envInt("RECONCILE_INTERVAL_SECONDS", 30),
		StimulusSubject:   shared.StimulusSubject,

		GmailClientID:            env("GMAIL_CLIENT_ID", ""),
		GmailClientSecret:        env("GMAIL_CLIENT_SECRET", ""),
		GmailRefreshToken:        env("GMAIL_REFRESH_TOKEN", ""),
		GmailQuery:               shared.Gmail.Query,
		GmailSeenLabel:           shared.Gmail.SeenLabel,
		GmailPollIntervalSeconds: shared.Gmail.PollIntervalSeconds,
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
git add perception-svc/config/config.go perception-svc/config/config_test.go
git commit -m "feat(perception-svc): add Gmail OAuth secrets and shared Gmail config fields"
```

---

### Task 3: `gmailwatcher/stimulus.go` — pure Gmail message → Stimulus mapping

**Files:**
- Create: `perception-svc/gmailwatcher/stimulus.go`
- Create: `perception-svc/gmailwatcher/stimulus_test.go`
- Modify: `perception-svc/go.mod`, `perception-svc/go.sum` (adds `google.golang.org/api/gmail/v1`)

**Interfaces:**
- Consumes: `*gmail.Message` (from `google.golang.org/api/gmail/v1`), `common.Stimulus`/`common.Content`/`common.Attachment`/etc. (from `soulman/common`).
- Produces: `buildStimulus(msg *gmail.Message) (*common.Stimulus, error)` — Task 5 calls this exact function.

Both test file and implementation are in `package gmailwatcher` (not `gmailwatcher_test`) because `buildStimulus` is unexported — this matches how `perception-svc/watcher`'s own test files (`folderwatcher_test.go`, `checkpoint_test.go`) are also in-package, not external `_test` packages.

- [ ] **Step 1: Add the Gmail API dependency**

Run: `cd perception-svc && go get google.golang.org/api/gmail/v1@latest`
Expected: `go.mod`/`go.sum` gain `google.golang.org/api` and its transitive deps.

- [ ] **Step 2: Write the failing test**

Create `perception-svc/gmailwatcher/stimulus_test.go`:

```go
package gmailwatcher

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"google.golang.org/api/gmail/v1"
)

func encodeBody(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

func TestBuildStimulus_PlainTextMessage(t *testing.T) {
	msg := &gmail.Message{
		Id:           "msg-1",
		ThreadId:     "thread-1",
		LabelIds:     []string{"INBOX", "UNREAD"},
		InternalDate: 1700000000000,
		Payload: &gmail.MessagePart{
			MimeType: "text/plain",
			Headers: []*gmail.MessagePartHeader{
				{Name: "From", Value: `"Jane Doe" <jane@example.com>`},
				{Name: "Subject", Value: "Server is down"},
			},
			Body: &gmail.MessagePartBody{Data: encodeBody("Everything is on fire.")},
		},
	}

	s, err := buildStimulus(msg)
	if err != nil {
		t.Fatalf("buildStimulus: %v", err)
	}

	if s.Channel != "gmail" {
		t.Errorf("Channel = %q, want gmail", s.Channel)
	}
	if s.Source.Identity != "jane@example.com" {
		t.Errorf("Source.Identity = %q, want jane@example.com", s.Source.Identity)
	}
	if s.Source.Authenticated {
		t.Error("Source.Authenticated = true, want false (sender identity unverified)")
	}
	if s.Source.AuthMethod != "none" {
		t.Errorf("Source.AuthMethod = %q, want none", s.Source.AuthMethod)
	}
	if s.Content.RawText != "Everything is on fire." {
		t.Errorf("Content.RawText = %q, want %q", s.Content.RawText, "Everything is on fire.")
	}
	if s.Content.ContentType != "text" {
		t.Errorf("Content.ContentType = %q, want text", s.Content.ContentType)
	}
	if len(s.Content.Attachments) != 0 {
		t.Errorf("Content.Attachments = %v, want empty", s.Content.Attachments)
	}
	if s.ChannelMeta.MessageID != "msg-1" {
		t.Errorf("ChannelMeta.MessageID = %q, want msg-1", s.ChannelMeta.MessageID)
	}
	if s.ChannelMeta.ThreadID != "thread-1" {
		t.Errorf("ChannelMeta.ThreadID = %q, want thread-1", s.ChannelMeta.ThreadID)
	}
	if s.ChannelMeta.ReplyTo != "jane@example.com" {
		t.Errorf("ChannelMeta.ReplyTo = %q, want jane@example.com", s.ChannelMeta.ReplyTo)
	}
	var channelSpecific struct {
		Subject  string   `json:"subject"`
		LabelIDs []string `json:"label_ids"`
	}
	if err := json.Unmarshal(s.ChannelMeta.ChannelSpecific, &channelSpecific); err != nil {
		t.Fatalf("unmarshal channel_specific: %v", err)
	}
	if channelSpecific.Subject != "Server is down" {
		t.Errorf("channel_specific.subject = %q, want %q", channelSpecific.Subject, "Server is down")
	}
	if len(channelSpecific.LabelIDs) != 2 {
		t.Errorf("channel_specific.label_ids = %v, want 2 entries", channelSpecific.LabelIDs)
	}
	wantOccurred := time.UnixMilli(1700000000000).UTC()
	if s.OccurredAt == nil || !s.OccurredAt.Equal(wantOccurred) {
		t.Errorf("OccurredAt = %v, want %v", s.OccurredAt, wantOccurred)
	}
	if s.Hints.Priority != "normal" {
		t.Errorf("Hints.Priority = %q, want normal", s.Hints.Priority)
	}
	if len(s.Hints.Tags) != 2 || s.Hints.Tags[0] != "email" || s.Hints.Tags[1] != "gmail" {
		t.Errorf("Hints.Tags = %v, want [email gmail]", s.Hints.Tags)
	}
	if s.Override.IsOverride {
		t.Error("Override.IsOverride = true, want false")
	}
}

func TestBuildStimulus_MultipartWithHTMLFallbackAndAttachment(t *testing.T) {
	msg := &gmail.Message{
		Id:           "msg-2",
		ThreadId:     "thread-2",
		InternalDate: 1700000000000,
		Payload: &gmail.MessagePart{
			MimeType: "multipart/mixed",
			Headers: []*gmail.MessagePartHeader{
				{Name: "From", Value: "noreply@example.com"},
				{Name: "Subject", Value: "Your invoice"},
			},
			Parts: []*gmail.MessagePart{
				{
					MimeType: "text/html",
					Body:     &gmail.MessagePartBody{Data: encodeBody("<p>Invoice attached</p>")},
				},
				{
					MimeType: "application/pdf",
					Filename: "invoice.pdf",
					Body: &gmail.MessagePartBody{
						AttachmentId: "att-1",
						Size:         54321,
					},
				},
			},
		},
	}

	s, err := buildStimulus(msg)
	if err != nil {
		t.Fatalf("buildStimulus: %v", err)
	}

	if s.Content.RawText != "<p>Invoice attached</p>" {
		t.Errorf("Content.RawText = %q, want %q", s.Content.RawText, "<p>Invoice attached</p>")
	}
	if s.Content.ContentType != "html" {
		t.Errorf("Content.ContentType = %q, want html", s.Content.ContentType)
	}
	if len(s.Content.Attachments) != 1 {
		t.Fatalf("Content.Attachments = %v, want 1 entry", s.Content.Attachments)
	}
	att := s.Content.Attachments[0]
	if att.Filename != "invoice.pdf" {
		t.Errorf("Attachment.Filename = %q, want invoice.pdf", att.Filename)
	}
	if att.SizeBytes != 54321 {
		t.Errorf("Attachment.SizeBytes = %d, want 54321", att.SizeBytes)
	}
	wantURI := "gmail://msg-2/attachments/att-1"
	if att.URI != wantURI {
		t.Errorf("Attachment.URI = %q, want %q", att.URI, wantURI)
	}
	if s.Source.Identity != "noreply@example.com" {
		t.Errorf("Source.Identity = %q, want noreply@example.com", s.Source.Identity)
	}
}

func TestBuildStimulus_PrefersPlainTextOverHTML(t *testing.T) {
	msg := &gmail.Message{
		Id:           "msg-3",
		InternalDate: 1700000000000,
		Payload: &gmail.MessagePart{
			MimeType: "multipart/alternative",
			Headers: []*gmail.MessagePartHeader{
				{Name: "From", Value: "sender@example.com"},
			},
			Parts: []*gmail.MessagePart{
				{MimeType: "text/html", Body: &gmail.MessagePartBody{Data: encodeBody("<p>html body</p>")}},
				{MimeType: "text/plain", Body: &gmail.MessagePartBody{Data: encodeBody("plain body")}},
			},
		},
	}

	s, err := buildStimulus(msg)
	if err != nil {
		t.Fatalf("buildStimulus: %v", err)
	}
	if s.Content.RawText != "plain body" {
		t.Errorf("Content.RawText = %q, want plain body (text/plain preferred over text/html)", s.Content.RawText)
	}
	if s.Content.ContentType != "text" {
		t.Errorf("Content.ContentType = %q, want text", s.Content.ContentType)
	}
}

func TestBuildStimulus_MalformedFromHeader_FallsBackToRawValue(t *testing.T) {
	msg := &gmail.Message{
		Id:           "msg-4",
		InternalDate: 1700000000000,
		Payload: &gmail.MessagePart{
			MimeType: "text/plain",
			Headers: []*gmail.MessagePartHeader{
				{Name: "From", Value: "not a valid address!!!"},
			},
			Body: &gmail.MessagePartBody{Data: encodeBody("body")},
		},
	}

	s, err := buildStimulus(msg)
	if err != nil {
		t.Fatalf("buildStimulus: %v", err)
	}
	if s.Source.Identity != "not a valid address!!!" {
		t.Errorf("Source.Identity = %q, want raw header fallback %q", s.Source.Identity, "not a valid address!!!")
	}
}

func TestBuildStimulus_NoPayload_ReturnsError(t *testing.T) {
	msg := &gmail.Message{Id: "msg-5"}

	_, err := buildStimulus(msg)
	if err == nil {
		t.Fatal("buildStimulus: want error for message with nil Payload, got nil")
	}
}

func TestBuildStimulus_NoTextBody_EmptyRawText(t *testing.T) {
	msg := &gmail.Message{
		Id:           "msg-6",
		InternalDate: 1700000000000,
		Payload: &gmail.MessagePart{
			MimeType: "multipart/mixed",
			Headers: []*gmail.MessagePartHeader{
				{Name: "From", Value: "sender@example.com"},
			},
			Parts: []*gmail.MessagePart{
				{
					MimeType: "application/pdf",
					Filename: "report.pdf",
					Body:     &gmail.MessagePartBody{AttachmentId: "att-2", Size: 1024},
				},
			},
		},
	}

	s, err := buildStimulus(msg)
	if err != nil {
		t.Fatalf("buildStimulus: %v", err)
	}
	if s.Content.RawText != "" {
		t.Errorf("Content.RawText = %q, want empty for attachment-only message", s.Content.RawText)
	}
	if len(s.Content.Attachments) != 1 {
		t.Errorf("Content.Attachments = %v, want 1 entry", s.Content.Attachments)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go -C perception-svc test ./gmailwatcher/...`
Expected: FAIL — compile error, `buildStimulus` undefined (package `gmailwatcher` has no non-test files yet).

- [ ] **Step 4: Write the implementation**

Create `perception-svc/gmailwatcher/stimulus.go`:

```go
package gmailwatcher

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/mail"
	"time"

	"github.com/google/uuid"
	"google.golang.org/api/gmail/v1"

	"soulman/common"
)

// buildStimulus maps a fully-fetched Gmail message (format "full") into the
// canonical Stimulus. Pure function — no network calls, no side effects —
// so it's testable against fixture messages without a live Gmail account.
func buildStimulus(msg *gmail.Message) (*common.Stimulus, error) {
	if msg.Payload == nil {
		return nil, fmt.Errorf("gmailwatcher: message %s has no payload", msg.Id)
	}

	headers := headerMap(msg.Payload.Headers)
	fromAddr := parseFromAddress(headers["From"])
	subject := headers["Subject"]

	rawText, contentType, err := extractBody(msg.Payload)
	if err != nil {
		return nil, fmt.Errorf("gmailwatcher: extract body for message %s: %w", msg.Id, err)
	}

	attachments := extractAttachments(msg.Id, msg.Payload)

	occurredAt := time.UnixMilli(msg.InternalDate).UTC()

	channelSpecific, err := json.Marshal(struct {
		Subject  string   `json:"subject"`
		LabelIDs []string `json:"label_ids"`
	}{Subject: subject, LabelIDs: msg.LabelIds})
	if err != nil {
		return nil, fmt.Errorf("gmailwatcher: marshal channel_specific for message %s: %w", msg.Id, err)
	}

	rawPayload, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("gmailwatcher: marshal raw_payload for message %s: %w", msg.Id, err)
	}

	id, err := uuid.NewV7()
	if err != nil {
		// Extremely unlikely (crypto/rand failure); fall back to a random v4
		// rather than drop the message, mirroring watcher.buildStimulus.
		id = uuid.New()
	}

	return &common.Stimulus{
		StimulusID:    id.String(),
		SchemaVersion: 1,
		ReceivedAt:    time.Now().UTC(),
		OccurredAt:    &occurredAt,
		Channel:       "gmail",
		Source: common.Source{
			Identity:      fromAddr,
			Authenticated: false,
			AuthMethod:    "none",
		},
		Content: common.Content{
			RawText:     rawText,
			RawPayload:  json.RawMessage(rawPayload),
			ContentType: contentType,
			Attachments: attachments,
		},
		ChannelMeta: common.ChannelMeta{
			MessageID:       msg.Id,
			ThreadID:        msg.ThreadId,
			ReplyTo:         fromAddr,
			ChannelSpecific: json.RawMessage(channelSpecific),
		},
		Hints: common.Hints{
			Priority: "normal",
			Tags:     []string{"email", "gmail"},
		},
		Override: common.Override{
			IsOverride: false,
			Params:     json.RawMessage(`{}`),
		},
	}, nil
}

func headerMap(headers []*gmail.MessagePartHeader) map[string]string {
	m := make(map[string]string, len(headers))
	for _, h := range headers {
		m[h.Name] = h.Value
	}
	return m
}

// parseFromAddress extracts just the email address from a raw "From"
// header value (e.g. `"Jane Doe" <jane@example.com>` -> "jane@example.com").
// Falls back to the raw header value if it doesn't parse as an RFC 5322
// address, rather than dropping the sender entirely.
func parseFromAddress(from string) string {
	if from == "" {
		return ""
	}
	addr, err := mail.ParseAddress(from)
	if err != nil {
		return from
	}
	return addr.Address
}

// extractBody walks the MIME part tree, preferring the first text/plain
// part found; if none exists, falls back to the first text/html part.
// Returns ("", "text", nil) if the message has no text body at all (e.g.
// an attachment-only message).
func extractBody(part *gmail.MessagePart) (text, contentType string, err error) {
	plain, html, err := findTextParts(part)
	if err != nil {
		return "", "", err
	}
	if plain != "" {
		return plain, "text", nil
	}
	if html != "" {
		return html, "html", nil
	}
	return "", "text", nil
}

// findTextParts walks part depth-first, returning the first text/plain and
// first text/html bodies it finds (either may be empty). Container parts
// (e.g. multipart/mixed) have neither MIME type and just recurse into
// their children.
func findTextParts(part *gmail.MessagePart) (plain, html string, err error) {
	switch part.MimeType {
	case "text/plain":
		decoded, decErr := decodeBody(part.Body)
		if decErr != nil {
			return "", "", decErr
		}
		return decoded, "", nil
	case "text/html":
		decoded, decErr := decodeBody(part.Body)
		if decErr != nil {
			return "", "", decErr
		}
		return "", decoded, nil
	}

	for _, child := range part.Parts {
		p, h, err := findTextParts(child)
		if err != nil {
			return "", "", err
		}
		if plain == "" {
			plain = p
		}
		if html == "" {
			html = h
		}
		if plain != "" {
			return plain, html, nil
		}
	}
	return plain, html, nil
}

// decodeBody decodes a MIME part body's base64url-encoded Data field.
// Gmail's API returns this unpadded, so RawURLEncoding is required (plain
// URLEncoding expects padding and would fail to decode it).
func decodeBody(body *gmail.MessagePartBody) (string, error) {
	if body == nil || body.Data == "" {
		return "", nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(body.Data)
	if err != nil {
		return "", fmt.Errorf("decode body: %w", err)
	}
	return string(decoded), nil
}

// extractAttachments collects metadata (never bytes) for every MIME part
// that names a file, per the approved design decision — a synthetic
// gmail:// URI lets a future consumer fetch the real bytes via the Gmail
// API without perception-svc downloading them now.
func extractAttachments(messageID string, part *gmail.MessagePart) []common.Attachment {
	var out []common.Attachment
	var walk func(p *gmail.MessagePart)
	walk = func(p *gmail.MessagePart) {
		if p.Filename != "" && p.Body != nil && p.Body.AttachmentId != "" {
			out = append(out, common.Attachment{
				Filename:  p.Filename,
				MIMEType:  p.MimeType,
				SizeBytes: p.Body.Size,
				URI:       fmt.Sprintf("gmail://%s/attachments/%s", messageID, p.Body.AttachmentId),
			})
		}
		for _, child := range p.Parts {
			walk(child)
		}
	}
	walk(part)
	if out == nil {
		out = []common.Attachment{}
	}
	return out
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go -C perception-svc test ./gmailwatcher/...`
Expected: PASS (all 6 tests)

- [ ] **Step 6: Commit**

```bash
git add perception-svc/gmailwatcher/stimulus.go perception-svc/gmailwatcher/stimulus_test.go perception-svc/go.mod perception-svc/go.sum
git commit -m "feat(perception-svc): map Gmail API messages to Stimulus"
```

---

### Task 4: `gmailwatcher/client.go` — Gmail API client seam

**Files:**
- Create: `perception-svc/gmailwatcher/client.go`
- Modify: `perception-svc/go.mod`, `perception-svc/go.sum` (adds `golang.org/x/oauth2`, `golang.org/x/oauth2/google`, `google.golang.org/api/option`)

**Interfaces:**
- Produces: the unexported `gmailClient` interface (`ListMatching`, `GetMessage`, `EnsureLabel`, `AddLabel`) and `newRealGmailClient(ctx, clientID, clientSecret, refreshToken string) (*realGmailClient, error)` — Task 5 depends on both.

**No automated test for this file** — per the approved design spec's Testing section, the real Gmail-API-calling code is verified manually against a live account, not via HTTP mocking. This is a deliberate scope boundary, not an oversight: the `gmailClient` interface exists specifically so Task 5's orchestration logic *is* unit-testable (via a fake), while this file's actual network calls are thin enough (one Gmail Go client call each) that the risk/effort of building HTTP-fake test infrastructure isn't justified for this iteration.

- [ ] **Step 1: Add the OAuth dependencies**

Run:
```bash
cd perception-svc
go get golang.org/x/oauth2@latest
go get golang.org/x/oauth2/google@latest
```
Expected: `go.mod`/`go.sum` gain `golang.org/x/oauth2` (which includes its `google` subpackage — no separate module).

- [ ] **Step 2: Write the implementation**

Create `perception-svc/gmailwatcher/client.go`:

```go
package gmailwatcher

import (
	"context"
	"fmt"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

const (
	gmailReadonlyScope = "https://www.googleapis.com/auth/gmail.readonly"
	gmailModifyScope   = "https://www.googleapis.com/auth/gmail.modify"

	gmailUser = "me" // "me" always refers to the authenticated account in the Gmail API
)

// gmailClient is the seam between the poll loop and the real Gmail API —
// small and hand-rolled (not the full *gmail.Service surface) so
// gmailwatcher.go's orchestration logic is testable against a fake without
// a live Gmail account, mirroring how watcher.Publisher lets folder-watcher's
// tests avoid a real NATS server.
type gmailClient interface {
	// ListMatching returns the message IDs currently matching query.
	ListMatching(ctx context.Context, query string) ([]string, error)
	// GetMessage fetches the full message body/headers for id.
	GetMessage(ctx context.Context, id string) (*gmail.Message, error)
	// EnsureLabel resolves name to a label ID, creating the label if it
	// doesn't exist yet.
	EnsureLabel(ctx context.Context, name string) (string, error)
	// AddLabel applies labelID to message id.
	AddLabel(ctx context.Context, id, labelID string) error
}

// realGmailClient implements gmailClient against the live Gmail API.
type realGmailClient struct {
	svc *gmail.Service
}

// newRealGmailClient builds an OAuth2 token source from clientID/clientSecret
// and a long-lived refresh token, then constructs a Gmail API client using
// it. The token source refreshes the access token automatically and
// indefinitely — no interactive re-consent — per this channel's design
// (see docs/superpowers/specs/2026-07-18-gmail-channel-design.md's OAuth
// Setup section on why Production app status is what actually prevents
// the refresh token itself from expiring).
func newRealGmailClient(ctx context.Context, clientID, clientSecret, refreshToken string) (*realGmailClient, error) {
	conf := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     google.Endpoint,
		Scopes:       []string{gmailReadonlyScope, gmailModifyScope},
	}
	tokenSource := conf.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken})
	httpClient := oauth2.NewClient(ctx, tokenSource)

	svc, err := gmail.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("gmailwatcher: build gmail service: %w", err)
	}
	return &realGmailClient{svc: svc}, nil
}

func (c *realGmailClient) ListMatching(ctx context.Context, query string) ([]string, error) {
	resp, err := c.svc.Users.Messages.List(gmailUser).Q(query).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("gmailwatcher: list messages: %w", err)
	}
	var ids []string
	for _, m := range resp.Messages {
		ids = append(ids, m.Id)
	}
	return ids, nil
}

func (c *realGmailClient) GetMessage(ctx context.Context, id string) (*gmail.Message, error) {
	msg, err := c.svc.Users.Messages.Get(gmailUser, id).Format("full").Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("gmailwatcher: get message %s: %w", id, err)
	}
	return msg, nil
}

// EnsureLabel looks up name among the account's existing labels; if not
// found, creates it. Gmail label names containing "/" (e.g.
// "soulman/seen-dev") are nested labels — Gmail creates the parent
// automatically, no special handling needed here.
func (c *realGmailClient) EnsureLabel(ctx context.Context, name string) (string, error) {
	resp, err := c.svc.Users.Labels.List(gmailUser).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("gmailwatcher: list labels: %w", err)
	}
	for _, l := range resp.Labels {
		if l.Name == name {
			return l.Id, nil
		}
	}

	created, err := c.svc.Users.Labels.Create(gmailUser, &gmail.Label{
		Name:                  name,
		LabelListVisibility:   "labelShow",
		MessageListVisibility: "show",
	}).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("gmailwatcher: create label %s: %w", name, err)
	}
	return created.Id, nil
}

func (c *realGmailClient) AddLabel(ctx context.Context, id, labelID string) error {
	_, err := c.svc.Users.Messages.Modify(gmailUser, id, &gmail.ModifyMessageRequest{
		AddLabelIds: []string{labelID},
	}).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("gmailwatcher: add label %s to message %s: %w", labelID, id, err)
	}
	return nil
}
```

- [ ] **Step 3: Verify it builds**

Run: `go -C perception-svc build ./...`
Expected: builds with no errors (no tests to run for this file — see the note above).

- [ ] **Step 4: Commit**

```bash
git add perception-svc/gmailwatcher/client.go perception-svc/go.mod perception-svc/go.sum
git commit -m "feat(perception-svc): add Gmail API client wrapping OAuth2 offline refresh token"
```

---

### Task 5: `gmailwatcher/gmailwatcher.go` — poll loop orchestration

**Files:**
- Create: `perception-svc/gmailwatcher/gmailwatcher.go`
- Create: `perception-svc/gmailwatcher/gmailwatcher_test.go`

**Interfaces:**
- Consumes: `gmailClient` interface and `newRealGmailClient` (Task 4), `buildStimulus` (Task 3).
- Produces: `gmailwatcher.Config{ClientID, ClientSecret, RefreshToken, Query, SeenLabel string; PollInterval time.Duration}`, `gmailwatcher.New(ctx, cfg, publisher) (*Watcher, error)`, `(*Watcher).Start(ctx)`, `(*Watcher).Close() error` — Task 6 (`main.go`) calls these exact names.

- [ ] **Step 1: Write the failing test**

Create `perception-svc/gmailwatcher/gmailwatcher_test.go`:

```go
package gmailwatcher

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/api/gmail/v1"

	"soulman/common"
)

type fakeClient struct {
	listIDs       []string
	listErr       error
	messages      map[string]*gmail.Message
	getErr        error
	ensureLabelID string
	ensureErr     error
	addedLabels   map[string]string
	addLabelErr   error
}

func (f *fakeClient) ListMatching(ctx context.Context, query string) ([]string, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.listIDs, nil
}

func (f *fakeClient) GetMessage(ctx context.Context, id string) (*gmail.Message, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	msg, ok := f.messages[id]
	if !ok {
		return nil, errors.New("fakeClient: no fixture for message " + id)
	}
	return msg, nil
}

func (f *fakeClient) EnsureLabel(ctx context.Context, name string) (string, error) {
	if f.ensureErr != nil {
		return "", f.ensureErr
	}
	return f.ensureLabelID, nil
}

func (f *fakeClient) AddLabel(ctx context.Context, id, labelID string) error {
	if f.addLabelErr != nil {
		return f.addLabelErr
	}
	if f.addedLabels == nil {
		f.addedLabels = map[string]string{}
	}
	f.addedLabels[id] = labelID
	return nil
}

type fakePublisher struct {
	published     []*common.Stimulus
	publishErrFor map[string]error
}

func (f *fakePublisher) Publish(ctx context.Context, s *common.Stimulus) error {
	if err, ok := f.publishErrFor[s.ChannelMeta.MessageID]; ok {
		return err
	}
	f.published = append(f.published, s)
	return nil
}

func validMessage(id string) *gmail.Message {
	return &gmail.Message{
		Id:           id,
		ThreadId:     "thread-" + id,
		InternalDate: 1700000000000,
		Payload: &gmail.MessagePart{
			MimeType: "text/plain",
			Headers: []*gmail.MessagePartHeader{
				{Name: "From", Value: "sender@example.com"},
				{Name: "Subject", Value: "test"},
			},
			Body: &gmail.MessagePartBody{Data: encodeBody("body for " + id)},
		},
	}
}

func TestPoll_PublishesEachMatchAndLabelsIt(t *testing.T) {
	client := &fakeClient{
		listIDs:       []string{"m1", "m2"},
		messages:      map[string]*gmail.Message{"m1": validMessage("m1"), "m2": validMessage("m2")},
		ensureLabelID: "Label_1",
	}
	pub := &fakePublisher{}
	w := newWatcher(client, pub, "in:inbox is:unread", "soulman/seen", time.Second)

	w.poll(context.Background())

	if len(pub.published) != 2 {
		t.Fatalf("published = %d messages, want 2", len(pub.published))
	}
	if client.addedLabels["m1"] != "Label_1" || client.addedLabels["m2"] != "Label_1" {
		t.Errorf("addedLabels = %v, want both m1 and m2 labeled Label_1", client.addedLabels)
	}
}

func TestPoll_PublishFailure_SkipsLabelAndWillRetryNextPoll(t *testing.T) {
	client := &fakeClient{
		listIDs:       []string{"m1"},
		messages:      map[string]*gmail.Message{"m1": validMessage("m1")},
		ensureLabelID: "Label_1",
	}
	pub := &fakePublisher{publishErrFor: map[string]error{"m1": errors.New("nats down")}}
	w := newWatcher(client, pub, "in:inbox is:unread", "soulman/seen", time.Second)

	w.poll(context.Background())

	if len(pub.published) != 0 {
		t.Errorf("published = %d messages, want 0 (publish failed)", len(pub.published))
	}
	if _, labeled := client.addedLabels["m1"]; labeled {
		t.Error("m1 was labeled despite a failed publish — should be left unlabeled so it's retried next poll")
	}
}

func TestPoll_LabelFailure_MessageStillCountsAsPublished(t *testing.T) {
	client := &fakeClient{
		listIDs:       []string{"m1"},
		messages:      map[string]*gmail.Message{"m1": validMessage("m1")},
		ensureLabelID: "Label_1",
		addLabelErr:   errors.New("modify failed"),
	}
	pub := &fakePublisher{}
	w := newWatcher(client, pub, "in:inbox is:unread", "soulman/seen", time.Second)

	w.poll(context.Background())

	if len(pub.published) != 1 {
		t.Fatalf("published = %d messages, want 1 (label failure shouldn't erase an already-successful publish)", len(pub.published))
	}
}

func TestPoll_ListError_SkipsCycleWithoutPanicking(t *testing.T) {
	client := &fakeClient{listErr: errors.New("list failed"), ensureLabelID: "Label_1"}
	pub := &fakePublisher{}
	w := newWatcher(client, pub, "in:inbox is:unread", "soulman/seen", time.Second)

	w.poll(context.Background())

	if len(pub.published) != 0 {
		t.Errorf("published = %d messages, want 0", len(pub.published))
	}
}

func TestPoll_GetMessageError_SkipsThatMessageOnly(t *testing.T) {
	client := &fakeClient{
		listIDs:       []string{"m1", "m2"},
		messages:      map[string]*gmail.Message{"m2": validMessage("m2")},
		ensureLabelID: "Label_1",
	}
	pub := &fakePublisher{}
	w := newWatcher(client, pub, "in:inbox is:unread", "soulman/seen", time.Second)

	w.poll(context.Background())

	if len(pub.published) != 1 {
		t.Fatalf("published = %d messages, want 1 (only m2 should succeed)", len(pub.published))
	}
	if pub.published[0].ChannelMeta.MessageID != "m2" {
		t.Errorf("published message = %s, want m2", pub.published[0].ChannelMeta.MessageID)
	}
}

func TestPoll_SeenLabelResolutionFails_SkipsPollEntirely(t *testing.T) {
	client := &fakeClient{ensureErr: errors.New("labels.list failed")}
	pub := &fakePublisher{}
	w := newWatcher(client, pub, "in:inbox is:unread", "soulman/seen", time.Second)

	w.poll(context.Background())

	if w.seenLabelID != "" {
		t.Errorf("seenLabelID = %q, want empty (resolution failed)", w.seenLabelID)
	}
	if len(pub.published) != 0 {
		t.Errorf("published = %d messages, want 0", len(pub.published))
	}
}

func TestStart_ResolvesSeenLabelAndRunsImmediatePoll(t *testing.T) {
	client := &fakeClient{
		listIDs:       []string{"m1"},
		messages:      map[string]*gmail.Message{"m1": validMessage("m1")},
		ensureLabelID: "Label_1",
	}
	pub := &fakePublisher{}
	w := newWatcher(client, pub, "in:inbox is:unread", "soulman/seen", time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	if len(pub.published) != 1 {
		t.Fatalf("published = %d messages after Start, want 1 from the immediate poll", len(pub.published))
	}
	if w.seenLabelID != "Label_1" {
		t.Errorf("seenLabelID = %q, want Label_1", w.seenLabelID)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go -C perception-svc test ./gmailwatcher/...`
Expected: FAIL — compile error, `newWatcher`/`Watcher`/`Start`/`poll` undefined.

- [ ] **Step 3: Write the implementation**

Create `perception-svc/gmailwatcher/gmailwatcher.go`:

```go
package gmailwatcher

import (
	"context"
	"log"
	"time"

	"soulman/common"
)

// Publisher is satisfied by *natspublish.Publisher. Declared here (not
// imported from natspublish) to avoid an import cycle, mirroring
// watcher.Publisher's same rationale.
type Publisher interface {
	Publish(ctx context.Context, s *common.Stimulus) error
}

// Config holds everything the Watcher needs to poll Gmail and publish
// matching messages. Populated from perception-svc/config.Config's Gmail*
// fields by main.go.
type Config struct {
	ClientID     string
	ClientSecret string
	RefreshToken string
	Query        string
	SeenLabel    string
	PollInterval time.Duration
}

// Watcher polls a Gmail inbox for messages matching Query, publishes each
// as a Stimulus, then labels it with SeenLabel so it drops out of future
// poll results — Gmail's own labels are the checkpoint, no local state
// file is needed (unlike folder-watcher's hash-based checkpoint).
type Watcher struct {
	client    gmailClient
	publisher Publisher
	query     string
	seenLabel string
	interval  time.Duration

	seenLabelID string
}

// New builds a Watcher backed by the real Gmail API, authenticated via an
// OAuth2 offline refresh token (auto-refreshing, no interactive consent
// after the initial one-time setup — see the Gmail channel design spec).
func New(ctx context.Context, cfg Config, publisher Publisher) (*Watcher, error) {
	client, err := newRealGmailClient(ctx, cfg.ClientID, cfg.ClientSecret, cfg.RefreshToken)
	if err != nil {
		return nil, err
	}
	return newWatcher(client, publisher, cfg.Query, cfg.SeenLabel, cfg.PollInterval), nil
}

// newWatcher builds a Watcher against any gmailClient — the seam
// gmailwatcher_test.go uses to inject a fake instead of a live Gmail
// account, mirroring watcher.New's Publisher-interface seam.
func newWatcher(client gmailClient, publisher Publisher, query, seenLabel string, interval time.Duration) *Watcher {
	return &Watcher{
		client:    client,
		publisher: publisher,
		query:     query,
		seenLabel: seenLabel,
		interval:  interval,
	}
}

// Start resolves the seen-label to its Gmail label ID (creating it if
// needed), runs one immediate poll so messages already unread at startup
// aren't stuck waiting a full interval, then launches the ticker-driven
// poll loop in a background goroutine.
func (w *Watcher) Start(ctx context.Context) {
	w.poll(ctx)
	go w.pollLoop(ctx)
}

func (w *Watcher) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.poll(ctx)
		}
	}
}

// poll runs one list -> get -> publish -> label cycle. Every step logs and
// moves on rather than aborting the whole cycle on a single message's
// failure, so one bad message doesn't block the rest of the batch.
func (w *Watcher) poll(ctx context.Context) {
	if w.seenLabelID == "" {
		labelID, err := w.client.EnsureLabel(ctx, w.seenLabel)
		if err != nil {
			log.Printf("gmailwatcher: seen label %q still unresolved, skipping poll: %v", w.seenLabel, err)
			return
		}
		w.seenLabelID = labelID
	}

	ids, err := w.client.ListMatching(ctx, w.query)
	if err != nil {
		log.Printf("gmailwatcher: list matching messages failed, will retry next poll: %v", err)
		return
	}

	for _, id := range ids {
		w.handleMessage(ctx, id)
	}
}

func (w *Watcher) handleMessage(ctx context.Context, id string) {
	msg, err := w.client.GetMessage(ctx, id)
	if err != nil {
		log.Printf("gmailwatcher: get message %s failed, will retry next poll: %v", id, err)
		return
	}

	stimulus, err := buildStimulus(msg)
	if err != nil {
		log.Printf("gmailwatcher: build stimulus for message %s failed, skipping: %v", id, err)
		return
	}

	if err := w.publisher.Publish(ctx, stimulus); err != nil {
		log.Printf("gmailwatcher: publish failed for message %s (seen-label left unset, will retry): %v", id, err)
		return
	}

	if err := w.client.AddLabel(ctx, id, w.seenLabelID); err != nil {
		log.Printf("gmailwatcher: label message %s as seen failed (will be re-published next poll): %v", id, err)
	}
}

func (w *Watcher) Close() error {
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go -C perception-svc test ./gmailwatcher/...`
Expected: PASS (all tests from Task 3 and Task 5)

- [ ] **Step 5: Commit**

```bash
git add perception-svc/gmailwatcher/gmailwatcher.go perception-svc/gmailwatcher/gmailwatcher_test.go
git commit -m "feat(perception-svc): add Gmail poll-loop orchestration"
```

---

### Task 6: Wire `gmailwatcher` into `perception-svc/main.go`

**Files:**
- Modify: `perception-svc/main.go`

**Interfaces:**
- Consumes: `config.Config.Gmail*` fields (Task 2), `gmailwatcher.Config`/`gmailwatcher.New`/`(*Watcher).Start`/`(*Watcher).Close` (Task 5).

- [ ] **Step 1: Update the import block and add the Gmail wiring**

In `perception-svc/main.go`, add `"soulman/perception-svc/gmailwatcher"` to the import block, then insert the following between the existing `w.Start(ctx)` call and the `srv := httpserver.New(...)` line:

```go
	// Gmail channel is optional: if credentials aren't configured yet (the
	// one-time OAuth bootstrap hasn't been done), skip it entirely rather
	// than failing startup — folder-watcher stays fully functional either
	// way, per Perception module.md's adapter-isolation principle.
	if cfg.GmailClientID == "" || cfg.GmailClientSecret == "" || cfg.GmailRefreshToken == "" {
		log.Printf("gmailwatcher: GMAIL_CLIENT_ID/SECRET/REFRESH_TOKEN not fully set, Gmail channel disabled")
	} else {
		gw, err := gmailwatcher.New(ctx, gmailwatcher.Config{
			ClientID:     cfg.GmailClientID,
			ClientSecret: cfg.GmailClientSecret,
			RefreshToken: cfg.GmailRefreshToken,
			Query:        cfg.GmailQuery,
			SeenLabel:    cfg.GmailSeenLabel,
			PollInterval: time.Duration(cfg.GmailPollIntervalSeconds) * time.Second,
		}, pub)
		if err != nil {
			log.Printf("gmailwatcher: setup failed, Gmail channel disabled: %v", err)
		} else {
			defer gw.Close()
			gw.Start(ctx)
			log.Printf("gmailwatcher: started (query=%q, seen_label=%q, poll_interval=%ds)",
				cfg.GmailQuery, cfg.GmailSeenLabel, cfg.GmailPollIntervalSeconds)
		}
	}
```

The full `main` function body, for reference (only the block above and the new import are new — everything else is unchanged from before this task):

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
	"soulman/perception-svc/gmailwatcher"
	"soulman/perception-svc/httpserver"
	"soulman/perception-svc/natspublish"
	"soulman/perception-svc/watcher"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cp := watcher.LoadCheckpoint(cfg.CheckpointPath)

	// NATS — non-fatal at startup for unreachable hosts; RetryOnFailedConnect
	// keeps trying in the background while the watcher and HTTP server start.
	pub, err := natspublish.New(cfg.NATSURL, cfg.StimulusSubject)
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

	// Gmail channel is optional: if credentials aren't configured yet (the
	// one-time OAuth bootstrap hasn't been done), skip it entirely rather
	// than failing startup — folder-watcher stays fully functional either
	// way, per Perception module.md's adapter-isolation principle.
	if cfg.GmailClientID == "" || cfg.GmailClientSecret == "" || cfg.GmailRefreshToken == "" {
		log.Printf("gmailwatcher: GMAIL_CLIENT_ID/SECRET/REFRESH_TOKEN not fully set, Gmail channel disabled")
	} else {
		gw, err := gmailwatcher.New(ctx, gmailwatcher.Config{
			ClientID:     cfg.GmailClientID,
			ClientSecret: cfg.GmailClientSecret,
			RefreshToken: cfg.GmailRefreshToken,
			Query:        cfg.GmailQuery,
			SeenLabel:    cfg.GmailSeenLabel,
			PollInterval: time.Duration(cfg.GmailPollIntervalSeconds) * time.Second,
		}, pub)
		if err != nil {
			log.Printf("gmailwatcher: setup failed, Gmail channel disabled: %v", err)
		} else {
			defer gw.Close()
			gw.Start(ctx)
			log.Printf("gmailwatcher: started (query=%q, seen_label=%q, poll_interval=%ds)",
				cfg.GmailQuery, cfg.GmailSeenLabel, cfg.GmailPollIntervalSeconds)
		}
	}

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

- [ ] **Step 2: Build to verify it compiles**

Run: `go -C perception-svc build ./...`
Expected: builds with no errors.

- [ ] **Step 3: Commit**

```bash
git add perception-svc/main.go
git commit -m "feat(perception-svc): wire gmailwatcher into main alongside folder-watcher"
```

---

### Task 7: Add the `gmail` block to `config/dev.json` and `config/prod.json`

**Files:**
- Modify: `config/dev.json`
- Modify: `config/prod.json`

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
  },
  "gmail": {
    "query": "in:inbox is:unread -label:soulman/seen-dev",
    "seen_label": "soulman/seen-dev",
    "poll_interval_seconds": 60
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
  },
  "gmail": {
    "query": "in:inbox is:unread -label:soulman/seen",
    "seen_label": "soulman/seen",
    "poll_interval_seconds": 60
  }
}
```

- [ ] **Step 3: Verify both files parse as valid JSON**

Run: `powershell -Command "Get-Content config/dev.json | ConvertFrom-Json | Out-Null; Get-Content config/prod.json | ConvertFrom-Json | Out-Null; Write-Output OK"`
Expected: prints `OK` with no error.

- [ ] **Step 4: Commit**

```bash
git add config/dev.json config/prod.json
git commit -m "chore: add gmail channel settings to config/dev.json and config/prod.json"
```

---

### Task 8: End-to-end smoke test

**Files:**
- None modified — this task only builds and runs the binary.

**Interfaces:**
- Consumes: everything from Tasks 1-7.

- [ ] **Step 1: Build perception-svc**

Run: `go -C perception-svc build -o "$env:TEMP\gmail-smoke\perception-svc.exe" .` (PowerShell; adjust for your shell)
Expected: builds with no errors.

- [ ] **Step 2: Verify perception-svc starts cleanly with blank Gmail credentials (the real current state, until the manual OAuth bootstrap is done)**

Run (PowerShell):
```powershell
$env:CONFIG_PATH = "<worktree-root>\config\dev.json"
$env:CHECKPOINT_PATH = "$env:TEMP\gmail-smoke-checkpoints.json"
$proc = Start-Process -FilePath "$env:TEMP\gmail-smoke\perception-svc.exe" -PassThru -RedirectStandardOutput "$env:TEMP\gmail-smoke-out.log" -RedirectStandardError "$env:TEMP\gmail-smoke-err.log" -WindowStyle Hidden
Start-Sleep -Seconds 2
Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
Get-Content "$env:TEMP\gmail-smoke-out.log", "$env:TEMP\gmail-smoke-err.log"
```
Expected: log line `gmailwatcher: GMAIL_CLIENT_ID/SECRET/REFRESH_TOKEN not fully set, Gmail channel disabled`, followed by the normal `perception-svc started (...)` line — folder-watcher and the HTTP server come up exactly as before this feature existed.

- [ ] **Step 3: Verify fatal-fast behavior when the `gmail` config block is malformed**

Run (PowerShell):
```powershell
$badConfig = "$env:TEMP\gmail-smoke-bad-config.json"
'{"watch_paths": ["C:\\a\\errors"], "nats_url": "nats://localhost:4222", "stimulus_subject": "soulman.stimulus.raw", "gmail": {"seen_label": "soulman/seen", "poll_interval_seconds": 60}}' | Set-Content -Path $badConfig -Encoding ascii
$env:CONFIG_PATH = $badConfig
& "$env:TEMP\gmail-smoke\perception-svc.exe"
```
Expected: process exits immediately (non-zero exit code), logging `config: shared config ...gmail-smoke-bad-config.json has no gmail.query configured` (the `gmail.query` field is missing from this fixture, triggering the Task 2 validation).

- [ ] **Step 4: Run the full test suite**

Run:
```bash
go -C common test ./...
go -C perception-svc test ./...
```
Expected: all PASS, including the new `sharedconfig` (Task 1), `perception-svc/config` (Task 2), and `perception-svc/gmailwatcher` (Tasks 3 and 5) tests.

No commit for this task — it's verification only, no file changes.

---

## Manual Setup Required (not part of this plan's tasks)

Once this plan's tasks are complete and merged, the Gmail channel will build and run but stay disabled until you:

1. Complete the one-time OAuth bootstrap described in the design spec's "OAuth Setup" section (Google Cloud project, Gmail API enabled, OAuth consent screen in Production status, one manual consent flow to obtain a refresh token).
2. Add `GMAIL_CLIENT_ID`, `GMAIL_CLIENT_SECRET`, `GMAIL_REFRESH_TOKEN` to both `soulman-dev\.env` and `soulman-prod\.env`.
3. Restart `perception-svc` in each environment (each `run-perception-svc.ps1` already loads `.env` via `load-env.ps1` and copies the updated `config/dev.json`/`config/prod.json` — no script changes are needed for this feature).

At that point, watch the logs for `gmailwatcher: started (...)` instead of the disabled-channel warning, and confirm a real unread email gets published and labeled.
