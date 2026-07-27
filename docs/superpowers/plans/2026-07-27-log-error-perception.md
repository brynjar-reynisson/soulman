# Log Error Perception Channel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `perception-svc` pull channel that tails every sibling service's `*-startup-err.log` file for new slog `ERROR` lines and publishes a deduplicated `Stimulus` the first time each `(service, message)` pair is seen, paired with a mechanical `thinking-svc` rule and a generalized `action-svc` dispatch path so any important `append_daily_report_entry` (not just Gmail triage) triggers a real-time batched Discord notification.

**Architecture:** A new `perception-svc/logmonitor` package mirrors `watcher` (fsnotify + reconciliation-poll tailing, persisted byte-offset checkpoint) and `sysmonitor` (edge-triggered, in-memory dedup state) to detect and parse ERROR lines and publish `Stimulus`es on the existing `soulman.stimulus.raw` pipeline. A new `thinking-svc` rule matches `channel == "log-error"` and always emits an `append_daily_report_entry` Action Request with `Important: true`. `action-svc`'s `notifybatch.Item`/`formatBatch` are generalized beyond Gmail-only, and `dispatchAppendDailyReportEntry` gains the same `batcher.Add` call `dispatchGmailTriage` already has.

**Tech Stack:** Go 1.25, `log/slog` (the line format being parsed), `github.com/fsnotify/fsnotify`, `github.com/google/uuid`, NATS JetStream (existing STIMULUS/THINKING_REQUEST streams, unchanged), stdlib `regexp`/`bufio`/`encoding/json`.

## Global Constraints

- Only `LEVEL == ERROR` lines are tracked; `WARN`/`INFO`/non-matching lines are silently skipped, not logged as noise.
- Dedup key is `service + "\x00" + msg`, in-memory only, not persisted across restarts.
- First run for a tracked file starts at the file's current size (EOF), never replaying old history.
- File truncation (current size < stored offset) resets the offset to 0 and is not treated as an error.
- `hints.priority` is always `"critical"` for log-error stimuli — no lower tier this iteration.
- The Log Error rule in thinking-svc always sets `Important: true` — no LLM call, since Error-level *is* the importance signal.
- `feign_mode` (already `true` in both `config/dev.json` and `config/prod.json`) gates all `Notifier.Send` calls regardless of caller; no change needed to feign wiring.
- No cross-environment correlation — dev and prod each independently tail their own logs and dedup independently, same accepted duplication as every other channel.
- No "recovered"/resolution notification — recovery is silent, per explicit decision.
- No log-file rotation/archival policy beyond truncation-detection — this feature only concerns detection/alerting, not log lifecycle management.
- `LogMonitorConfig.ReconciliationIntervalSeconds` defaults to 30, validated fatal-fast (non-positive is a startup error) — same treatment as `system_monitor.poll_interval_seconds`.

---

## File Structure

| Path | Action | Responsibility |
|---|---|---|
| `perception-svc/logmonitor/parser.go` | Create | Pure function: parse one slog-default-formatted line into `{Level, Msg}`, or reject it |
| `perception-svc/logmonitor/parser_test.go` | Create | Tests for `parseLine` |
| `perception-svc/logmonitor/dedup.go` | Create | In-memory, mutex-guarded `(service, msg)` dedup state machine |
| `perception-svc/logmonitor/dedup_test.go` | Create | Tests for `dedupState` |
| `perception-svc/logmonitor/checkpoint.go` | Create | Persisted per-file byte-offset checkpoint; first-run-at-EOF and truncation-reset resolution logic |
| `perception-svc/logmonitor/checkpoint_test.go` | Create | Tests for `checkpoint` |
| `perception-svc/logmonitor/watcher.go` | Create | `Watcher`: fsnotify + reconciliation poll, glob discovery, tailing, Stimulus construction — wires parser+dedup+checkpoint together |
| `perception-svc/logmonitor/watcher_test.go` | Create | Integration-style tests against real temp-dir files |
| `common/sharedconfig/config.go` | Modify | Add `LogMonitorConfig` type and `Config.LogMonitor` field |
| `perception-svc/config/config.go` | Modify | Add `LogDir`, `LogMonitorCheckpointPath`, `LogMonitorReconcileIntervalSeconds`; validate the new shared config block fatal-fast |
| `perception-svc/config/config_test.go` | Modify | Extend the `writeConfigFile` fixture helper and every call site for the new config block; add validation tests |
| `config/dev.json` | Modify | Add `log_monitor` block |
| `config/prod.json` | Modify | Add `log_monitor` block |
| `perception-svc/main.go` | Modify | Construct and start the `logmonitor.Watcher` alongside the other three channels |
| `perception-svc/NOTES.md` | Modify | Document the new channel |
| `CLAUDE.md` | Modify | Update perception-svc's, thinking-svc's, and action-svc's Services bullets |
| `thinking-svc/rules/log_error.go` | Create | `LogErrorRule`: mechanical `channel == "log-error"` → `append_daily_report_entry`, always important |
| `thinking-svc/rules/log_error_test.go` | Create | Tests for `LogErrorRule` |
| `thinking-svc/rules/rule.go` | Modify | Register `LogErrorRule` in `Registry` |
| `thinking-svc/NOTES.md` | Modify | Document the new rule |
| `action-svc/notifybatch/batcher.go` | Modify | Generalize `Item`/`formatBatch` beyond Gmail-only (`Kind` discriminator) |
| `action-svc/notifybatch/batcher_test.go` | Modify | Extend for `Kind: "report"` and mixed-kind batches |
| `action-svc/dispatch/dispatch.go` | Modify | `dispatchAppendDailyReportEntry` reads `important` and calls `batcher.Add` |
| `action-svc/dispatch/gmail_triage.go` | Modify | Set `Kind: "gmail"` explicitly on the existing `batcher.Add` call |
| `action-svc/dispatch/dispatch_test.go` | Modify | Tests for the new `batcher.Add` wiring |
| `action-svc/dispatch/gmail_triage_test.go` | Modify | Assert `Kind == "gmail"` on the existing test |
| `action-svc/NOTES.md` | Modify | Document the real-time-notification generalization |

---

### Task 1: `logmonitor` line parser

**Files:**
- Create: `perception-svc/logmonitor/parser.go`
- Test: `perception-svc/logmonitor/parser_test.go`

**Interfaces:**
- Consumes: nothing (first task)
- Produces: `type ParsedLine struct { Level string; Msg string }`, `func parseLine(line string) (ParsedLine, bool)` — used by Task 4's `watcher.go`

- [ ] **Step 1: Write the failing test**

```go
package logmonitor

import "testing"

func TestParseLine_ErrorLineWithAttrs_ExtractsLevelAndMsg(t *testing.T) {
	line := `2026/07/27 10:05:00 ERROR writer: DB insert failed, will replay on restart stimulus_id=abc123 error="dial tcp 127.0.0.1:5432: connect: connection refused"`

	got, ok := parseLine(line)
	if !ok {
		t.Fatalf("parseLine(%q) ok = false, want true", line)
	}
	if got.Level != "ERROR" {
		t.Errorf("Level = %q, want ERROR", got.Level)
	}
	if got.Msg != "writer: DB insert failed, will replay on restart" {
		t.Errorf("Msg = %q, want %q", got.Msg, "writer: DB insert failed, will replay on restart")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./perception-svc/logmonitor/... -run TestParseLine_ErrorLineWithAttrs_ExtractsLevelAndMsg -v`
Expected: FAIL — `parseLine` is undefined (the package doesn't exist yet, build fails)

- [ ] **Step 3: Write minimal implementation**

```go
// Package logmonitor implements perception-svc's Log Error pull channel:
// tails every sibling service's *-startup-err.log file for new slog
// ERROR-level lines and publishes a Stimulus the first time a given
// (service, message) pair is seen. See
// docs/superpowers/specs/2026-07-27-log-error-perception-design.md.
package logmonitor

import "regexp"

// lineRe matches one line in log/slog's default (unconfigured) handler
// format: "<date> <time> <LEVEL> <msg> [key=value ...]" — the classic Go
// log package timestamp (two space-separated tokens: date, then time)
// followed by the level, then everything else. This is what every Soulman
// service produces by calling the slog package-level functions directly
// against the default logger, per the 2026-07-27 logging conversion (see
// root CLAUDE.md's "Logging" section) — none of the five services install
// a custom handler or call slog.SetDefault.
var lineRe = regexp.MustCompile(`^\S+\s+\S+\s+(ERROR|WARN|INFO|DEBUG)\s+(.*)$`)

// attrStartRe finds the first slog key=value attribute boundary within the
// "msg [key=value ...]" remainder — a space followed by a bare identifier
// (letters/digits/underscore, no spaces) followed directly by "=". Every
// attribute key in this codebase is a simple snake_case identifier (see the
// 2026-07-27 logging conversion's call sites), and no message text in this
// codebase contains a literal "identifier=" substring, so the first match
// reliably marks where msg ends and attrs begin.
var attrStartRe = regexp.MustCompile(`\s[A-Za-z_][A-Za-z0-9_]*=`)

// ParsedLine is one successfully parsed ERROR-level log line.
type ParsedLine struct {
	Level string
	Msg   string
}

// parseLine attempts to parse line as one slog default-handler-formatted
// log line. Returns ok=false for any line that doesn't match this shape
// (stack-trace continuations, panics, or any other non-slog output) or
// whose level isn't ERROR — logmonitor only tracks Error-level lines, per
// the design spec's explicit out-of-scope decision on WARN monitoring.
func parseLine(line string) (ParsedLine, bool) {
	m := lineRe.FindStringSubmatch(line)
	if m == nil {
		return ParsedLine{}, false
	}
	level := m[1]
	if level != "ERROR" {
		return ParsedLine{}, false
	}
	rest := m[2]
	msg := rest
	if loc := attrStartRe.FindStringIndex(rest); loc != nil {
		msg = rest[:loc[0]]
	}
	return ParsedLine{Level: level, Msg: msg}, true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./perception-svc/logmonitor/... -run TestParseLine_ErrorLineWithAttrs_ExtractsLevelAndMsg -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add perception-svc/logmonitor/parser.go perception-svc/logmonitor/parser_test.go
git commit -m "perception-svc/logmonitor: add slog default-format line parser"
```

---

### Task 2: `logmonitor` dedup state machine

**Files:**
- Create: `perception-svc/logmonitor/dedup.go`
- Test: `perception-svc/logmonitor/dedup_test.go`

**Interfaces:**
- Consumes: nothing (independent of Task 1)
- Produces: `type dedupState struct{...}`, `func newDedupState() *dedupState`, `func (d *dedupState) seenBefore(service, msg string) bool`, `func (d *dedupState) markSeen(service, msg string)` — used by Task 4's `watcher.go`

- [ ] **Step 1: Write the failing test**

```go
package logmonitor

import "testing"

func TestDedupState_SeenBefore_UnseenPair_False(t *testing.T) {
	d := newDedupState()
	if d.seenBefore("memory-svc", "DB insert failed") {
		t.Error("seenBefore should be false for a never-marked pair")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./perception-svc/logmonitor/... -run TestDedupState_SeenBefore_UnseenPair_False -v`
Expected: FAIL — `newDedupState` is undefined

- [ ] **Step 3: Write minimal implementation**

```go
package logmonitor

import "sync"

// dedupKey uniquely identifies one (service, message) pair for the life of
// this process. Built with a NUL separator (not simple concatenation) so a
// service name that happens to end where a message begins can never
// collide with a different split of the same concatenated string.
type dedupKey string

func newDedupKey(service, msg string) dedupKey {
	return dedupKey(service + "\x00" + msg)
}

// dedupState tracks which (service, msg) pairs have already fired a
// Stimulus this process lifetime. Mutex-guarded because Watcher's fsnotify
// event loop and its periodic reconciliation loop both call into it from
// separate goroutines. Not persisted across restarts — see the design
// spec's Dedup section for why that's an accepted tradeoff, the same one
// sysmonitor's in-memory severity state already makes.
type dedupState struct {
	mu   sync.Mutex
	seen map[dedupKey]struct{}
}

func newDedupState() *dedupState {
	return &dedupState{seen: map[dedupKey]struct{}{}}
}

// seenBefore reports whether (service, msg) has already fired this process
// lifetime, without marking it seen — callers check this before deciding
// whether to build and publish a Stimulus.
func (d *dedupState) seenBefore(service, msg string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.seen[newDedupKey(service, msg)]
	return ok
}

// markSeen records (service, msg) as fired. Callers must only call this
// after a successful publish — see Watcher.handleLine, which deliberately
// skips this call on a publish failure so the same line is retried on the
// next matching read rather than being permanently swallowed.
func (d *dedupState) markSeen(service, msg string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seen[newDedupKey(service, msg)] = struct{}{}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./perception-svc/logmonitor/... -run TestDedupState_SeenBefore_UnseenPair_False -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add perception-svc/logmonitor/dedup.go perception-svc/logmonitor/dedup_test.go
git commit -m "perception-svc/logmonitor: add in-memory dedup state machine"
```

Then add the remaining test cases (still part of this task — write, run, confirm green, before moving to Task 3):

```go
func TestDedupState_MarkSeen_ThenSeenBefore_True(t *testing.T) {
	d := newDedupState()
	d.markSeen("memory-svc", "DB insert failed")
	if !d.seenBefore("memory-svc", "DB insert failed") {
		t.Error("seenBefore should be true after markSeen")
	}
}

func TestDedupState_DifferentService_SameMsg_TrackedIndependently(t *testing.T) {
	d := newDedupState()
	d.markSeen("memory-svc", "DB insert failed")
	if d.seenBefore("action-svc", "DB insert failed") {
		t.Error("seenBefore should be false for the same msg from a different service")
	}
}

func TestDedupState_SameService_DifferentMsg_TrackedIndependently(t *testing.T) {
	d := newDedupState()
	d.markSeen("memory-svc", "DB insert failed")
	if d.seenBefore("memory-svc", "nats consumer start failed") {
		t.Error("seenBefore should be false for a different msg from the same service")
	}
}

func TestDedupState_NoServiceMsgBoundaryCollision(t *testing.T) {
	d := newDedupState()
	d.markSeen("ab", "cd")
	if d.seenBefore("a", "bcd") {
		t.Error("seenBefore should not collide across a different service/msg split of the same concatenated string")
	}
}
```

Run: `go test ./perception-svc/logmonitor/... -run TestDedupState -v`
Expected: PASS (all 5 dedup tests)

```bash
git add perception-svc/logmonitor/dedup_test.go
git commit -m "perception-svc/logmonitor: cover dedup independence and key-collision cases"
```

---

### Task 3: `logmonitor` checkpoint

**Files:**
- Create: `perception-svc/logmonitor/checkpoint.go`
- Test: `perception-svc/logmonitor/checkpoint_test.go`

**Interfaces:**
- Consumes: nothing (independent of Tasks 1-2)
- Produces: `type checkpoint struct{...}`, `func loadCheckpoint(path string) *checkpoint`, `func (c *checkpoint) offsetFor(filename string) (int64, bool)`, `func (c *checkpoint) resolveStart(filename string, currentSize int64) int64`, `func (c *checkpoint) mark(filename string, offset int64) error` — used by Task 4's `watcher.go`

- [ ] **Step 1: Write the failing test**

```go
package logmonitor

import (
	"path/filepath"
	"testing"
)

func TestLoadCheckpoint_MissingFile_StartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logmonitor-checkpoint.json")
	c := loadCheckpoint(path)
	if _, ok := c.offsetFor("memory-svc-startup-err.log"); ok {
		t.Error("offsetFor should report no entry for a checkpoint with no data")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./perception-svc/logmonitor/... -run TestLoadCheckpoint_MissingFile_StartsEmpty -v`
Expected: FAIL — `loadCheckpoint` is undefined

- [ ] **Step 3: Write minimal implementation**

```go
package logmonitor

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
)

// checkpoint tracks the byte offset each tracked log file has been read up
// to, persisted as JSON so no ERROR line is silently skipped if
// perception-svc is briefly down while another service keeps writing —
// a materially different failure mode than watcher's own checkpoint (which
// exists so file *identity* survives restarts, not so no byte is missed).
type checkpoint struct {
	mu     sync.Mutex
	path   string
	offset map[string]int64 // filename -> byte offset already read
}

// loadCheckpoint reads the checkpoint file at path. A missing, unreadable,
// or corrupt file logs and starts empty — same accepted tradeoff as
// watcher.LoadCheckpoint (worst case: the affected file's first read starts
// at 0 instead of its last-known offset, but combined with the in-memory
// dedup map this just means each distinct historical error type in that
// file alerts once more, not a flood).
func loadCheckpoint(path string) *checkpoint {
	c := &checkpoint{path: path, offset: map[string]int64{}}

	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("logmonitor: checkpoint read failed, starting empty", "path", path, "error", err)
		}
		return c
	}

	var data map[string]int64
	if err := json.Unmarshal(b, &data); err != nil {
		slog.Warn("logmonitor: checkpoint parse failed, starting empty", "path", path, "error", err)
		return c
	}

	c.offset = data
	return c
}

// offsetFor returns the stored byte offset for filename, and whether an
// entry existed at all.
func (c *checkpoint) offsetFor(filename string) (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	off, ok := c.offset[filename]
	return off, ok
}

// resolveStart returns the byte offset a read should start from for
// filename, given the file's current size on disk. Three cases: no
// checkpoint entry yet (first run) starts at the current size (EOF) so old
// history is never replayed; a stored offset within the current size
// resumes from there; a stored offset beyond the current size means the
// file was truncated (e.g. manually cleared, as memory-svc-startup-err.log
// was on 2026-07-27) and resets to 0.
func (c *checkpoint) resolveStart(filename string, currentSize int64) int64 {
	off, ok := c.offsetFor(filename)
	if !ok {
		return currentSize
	}
	if off > currentSize {
		return 0
	}
	return off
}

// mark records filename's new byte offset and persists the checkpoint to
// disk. Called after every successful read (not every line), so a crash
// mid-batch re-reads from the last fully-processed offset rather than
// losing partial progress.
func (c *checkpoint) mark(filename string, offset int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.offset[filename] = offset
	return c.saveLocked()
}

func (c *checkpoint) saveLocked() error {
	b, err := json.MarshalIndent(c.offset, "", "  ")
	if err != nil {
		return fmt.Errorf("logmonitor: checkpoint marshal: %w", err)
	}
	if err := os.WriteFile(c.path, b, 0o644); err != nil {
		return fmt.Errorf("logmonitor: checkpoint write %s: %w", c.path, err)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./perception-svc/logmonitor/... -run TestLoadCheckpoint_MissingFile_StartsEmpty -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add perception-svc/logmonitor/checkpoint.go perception-svc/logmonitor/checkpoint_test.go
git commit -m "perception-svc/logmonitor: add persisted byte-offset checkpoint"
```

Then add the remaining checkpoint tests (same task, before moving to Task 4):

```go
func TestLoadCheckpoint_CorruptFile_StartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logmonitor-checkpoint.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	c := loadCheckpoint(path)
	if _, ok := c.offsetFor("memory-svc-startup-err.log"); ok {
		t.Error("offsetFor should report no entry after falling back to empty checkpoint on corrupt file")
	}
}

func TestCheckpoint_Mark_PersistsToDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logmonitor-checkpoint.json")
	c := loadCheckpoint(path)
	if err := c.mark("memory-svc-startup-err.log", 1024); err != nil {
		t.Fatalf("mark: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var data map[string]int64
	if err := json.Unmarshal(b, &data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if data["memory-svc-startup-err.log"] != 1024 {
		t.Errorf("persisted offset = %d, want 1024", data["memory-svc-startup-err.log"])
	}
}

func TestLoadCheckpoint_ReloadsFromDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logmonitor-checkpoint.json")
	c1 := loadCheckpoint(path)
	c1.mark("memory-svc-startup-err.log", 2048)

	c2 := loadCheckpoint(path)
	off, ok := c2.offsetFor("memory-svc-startup-err.log")
	if !ok || off != 2048 {
		t.Errorf("offsetFor after reload = (%d, %v), want (2048, true)", off, ok)
	}
}

func TestResolveStart_NoEntry_StartsAtCurrentSize(t *testing.T) {
	c := loadCheckpoint(filepath.Join(t.TempDir(), "logmonitor-checkpoint.json"))
	if got := c.resolveStart("new-file-startup-err.log", 5000); got != 5000 {
		t.Errorf("resolveStart = %d, want 5000 (first run starts at EOF)", got)
	}
}

func TestResolveStart_StoredOffsetWithinCurrentSize_ResumesFromOffset(t *testing.T) {
	c := loadCheckpoint(filepath.Join(t.TempDir(), "logmonitor-checkpoint.json"))
	c.mark("memory-svc-startup-err.log", 1000)
	if got := c.resolveStart("memory-svc-startup-err.log", 2000); got != 1000 {
		t.Errorf("resolveStart = %d, want 1000 (resume from stored offset)", got)
	}
}

func TestResolveStart_StoredOffsetBeyondCurrentSize_TruncationResetsToZero(t *testing.T) {
	c := loadCheckpoint(filepath.Join(t.TempDir(), "logmonitor-checkpoint.json"))
	c.mark("memory-svc-startup-err.log", 5000)
	if got := c.resolveStart("memory-svc-startup-err.log", 100); got != 0 {
		t.Errorf("resolveStart = %d, want 0 (file truncated below stored offset)", got)
	}
}

func TestResolveStart_StoredOffsetEqualsCurrentSize_NoNewContent(t *testing.T) {
	c := loadCheckpoint(filepath.Join(t.TempDir(), "logmonitor-checkpoint.json"))
	c.mark("memory-svc-startup-err.log", 3000)
	if got := c.resolveStart("memory-svc-startup-err.log", 3000); got != 3000 {
		t.Errorf("resolveStart = %d, want 3000 (no new content, offset unchanged)", got)
	}
}
```

Run: `go test ./perception-svc/logmonitor/... -run "TestLoadCheckpoint|TestCheckpoint_Mark|TestResolveStart" -v`
Expected: PASS (all 7 checkpoint tests)

```bash
git add perception-svc/logmonitor/checkpoint_test.go
git commit -m "perception-svc/logmonitor: cover checkpoint reload, truncation, and first-run cases"
```

---

### Task 4: `logmonitor` Watcher (integration)

**Files:**
- Create: `perception-svc/logmonitor/watcher.go`
- Test: `perception-svc/logmonitor/watcher_test.go`

**Interfaces:**
- Consumes: `parseLine(line string) (ParsedLine, bool)` (Task 1); `newDedupState() *dedupState`, `(*dedupState).seenBefore`, `(*dedupState).markSeen` (Task 2); `loadCheckpoint(path string) *checkpoint`, `(*checkpoint).resolveStart`, `(*checkpoint).mark` (Task 3)
- Produces: `type Publisher interface { Publish(ctx context.Context, s *common.Stimulus) error }`, `type Watcher struct{...}`, `func New(logDir string, publisher Publisher, checkpointPath string, reconcileInterval time.Duration) (*Watcher, error)`, `func (w *Watcher) Start(ctx context.Context)`, `func (w *Watcher) Close() error` — used by Task 6's `main.go`

- [ ] **Step 1: Write the failing test**

```go
package logmonitor

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"soulman/common"
)

type fakePublisher struct {
	mu         sync.Mutex
	published  []*common.Stimulus
	publishErr error
}

func (f *fakePublisher) Publish(_ context.Context, s *common.Stimulus) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.publishErr != nil {
		return f.publishErr
	}
	f.published = append(f.published, s)
	return nil
}

func (f *fakePublisher) publishedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.published)
}

func newTestWatcher(t *testing.T, logDir string, pub Publisher) *Watcher {
	t.Helper()
	cpPath := filepath.Join(t.TempDir(), "logmonitor-checkpoint.json")
	w, err := New(logDir, pub, cpPath, time.Hour)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return w
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func TestReconcileAll_FirstRun_IgnoresPreExistingContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory-svc-startup-err.log")
	writeFile(t, path, "2026/07/20 09:00:00 ERROR old error before perception-svc ever ran\n")

	pub := &fakePublisher{}
	w := newTestWatcher(t, dir, pub)

	w.reconcileAll(context.Background())

	if got := pub.publishedCount(); got != 0 {
		t.Fatalf("published = %d, want 0 (first run must start at EOF, ignoring pre-existing content)", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./perception-svc/logmonitor/... -run TestReconcileAll_FirstRun_IgnoresPreExistingContent -v`
Expected: FAIL — `New`/`Watcher`/`reconcileAll` are undefined

- [ ] **Step 3: Write minimal implementation**

```go
package logmonitor

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/google/uuid"

	"soulman/common"
)

// Publisher is satisfied by *natspublish.Publisher. Declared here (not
// imported from natspublish) to avoid an import cycle — same rationale as
// every other channel package (watcher.Publisher, gmailwatcher.Publisher,
// sysmonitor.Publisher).
type Publisher interface {
	Publish(ctx context.Context, s *common.Stimulus) error
}

// logSuffix is the filename suffix every tracked file must end with;
// service is this suffix stripped from the filename.
const logSuffix = "-startup-err.log"

// Watcher tails every sibling service's *-startup-err.log file in logDir
// for new ERROR-level slog lines, publishing a Stimulus the first time a
// given (service, msg) pair is seen this process lifetime. See
// docs/superpowers/specs/2026-07-27-log-error-perception-design.md.
type Watcher struct {
	logDir            string
	publisher         Publisher
	checkpoint        *checkpoint
	dedup             *dedupState
	reconcileInterval time.Duration

	// mu serializes processFile across the fsnotify event-loop goroutine and
	// the periodic reconciliation goroutine, so the same file is never read
	// concurrently from two stale offsets at once.
	mu sync.Mutex

	fsw    *fsnotify.Watcher
	cancel context.CancelFunc
}

// New creates a Watcher tailing *-startup-err.log files in logDir,
// persisting its byte-offset checkpoint to checkpointPath. Mirrors
// watcher.New's constructor shape (fsnotify setup that can fail returns an
// error; everything else is deferred to Start).
func New(logDir string, publisher Publisher, checkpointPath string, reconcileInterval time.Duration) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("logmonitor: create fsnotify watcher: %w", err)
	}

	return &Watcher{
		logDir:            logDir,
		publisher:         publisher,
		checkpoint:        loadCheckpoint(checkpointPath),
		dedup:             newDedupState(),
		reconcileInterval: reconcileInterval,
		fsw:               fsw,
	}, nil
}

// Start adds an fsnotify watch on logDir (logging and continuing if it
// doesn't exist yet — retried automatically by the next reconciliation
// scan, mirroring watcher.Start's per-path tolerance), then launches the
// fsnotify event loop and the periodic reconciliation loop in background
// goroutines. It also runs one immediate reconciliation pass before
// returning, so files already present at startup are picked up without
// waiting a full interval.
func (w *Watcher) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	w.cancel = cancel

	if err := w.fsw.Add(w.logDir); err != nil {
		slog.Warn("logmonitor: cannot watch log dir, will retry via reconciliation", "dir", w.logDir, "error", err)
	}

	go w.fsEventLoop(ctx)
	go w.reconcileLoop(ctx)

	w.reconcileAll(ctx)
}

// fsEventLoop drains fsnotify's Events/Errors channels and reconciles the
// specific file that changed on any Write event — instant reaction, backed
// by reconcileLoop's periodic full-directory scan as a safety net for any
// event fsnotify misses.
func (w *Watcher) fsEventLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			if ev.Op&fsnotify.Write == 0 {
				continue
			}
			if !strings.HasSuffix(ev.Name, logSuffix) {
				continue
			}
			w.processFile(ctx, ev.Name)
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			slog.Error("logmonitor: fsnotify error", "error", err)
		}
	}
}

// reconcileLoop runs a full-directory scan every reconcileInterval.
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

// reconcileAll globs logDir for every tracked file and processes each —
// catches files changed while perception-svc was down or any fsnotify
// event that was missed (a known OS-level gap on some network drives, per
// watcher's own precedent).
func (w *Watcher) reconcileAll(ctx context.Context) {
	matches, err := filepath.Glob(filepath.Join(w.logDir, "*"+logSuffix))
	if err != nil {
		slog.Error("logmonitor: glob log dir failed", "dir", w.logDir, "error", err)
		return
	}
	for _, path := range matches {
		w.processFile(ctx, path)
	}
}

// processFile reads path from its checkpointed offset to current EOF,
// splits into complete lines (a trailing partial line is left for the next
// read), parses and dedups each ERROR line, and advances the checkpoint
// after a successful read.
func (w *Watcher) processFile(ctx context.Context, path string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	filename := filepath.Base(path)
	service := strings.TrimSuffix(filename, logSuffix)

	info, err := os.Stat(path)
	if err != nil {
		slog.Error("logmonitor: stat failed, skipping this cycle", "path", path, "error", err)
		return
	}
	size := info.Size()

	start := w.checkpoint.resolveStart(filename, size)
	if start >= size {
		return // nothing new to read
	}

	f, err := os.Open(path)
	if err != nil {
		slog.Error("logmonitor: open failed, skipping this cycle", "path", path, "error", err)
		return
	}
	defer f.Close()

	if _, err := f.Seek(start, 0); err != nil {
		slog.Error("logmonitor: seek failed, skipping this cycle", "path", path, "error", err)
		return
	}

	reader := bufio.NewReader(f)
	offset := start
	for {
		line, readErr := reader.ReadString('\n')
		if len(line) > 0 && strings.HasSuffix(line, "\n") {
			offset += int64(len(line))
			w.handleLine(ctx, service, strings.TrimRight(line, "\n"))
		}
		if readErr != nil {
			break // EOF or read error: stop, leaving any trailing partial line for next time
		}
	}

	if err := w.checkpoint.mark(filename, offset); err != nil {
		slog.Warn("logmonitor: checkpoint write failed", "path", path, "error", err)
	}
}

// handleLine parses one line; if it's a new (service, msg) ERROR pair,
// builds and publishes a Stimulus. A publish failure is logged and the
// dedup key is deliberately left unmarked, so the same line is retried on
// the next matching read rather than being permanently swallowed.
func (w *Watcher) handleLine(ctx context.Context, service, line string) {
	parsed, ok := parseLine(line)
	if !ok {
		return
	}
	if w.dedup.seenBefore(service, parsed.Msg) {
		return
	}

	stimulus := buildStimulus(service, parsed.Msg, line, time.Now())
	if err := w.publisher.Publish(ctx, stimulus); err != nil {
		slog.Error("logmonitor: publish failed, will retry on next matching line", "service", service, "error", err)
		return
	}
	w.dedup.markSeen(service, parsed.Msg)
}

func (w *Watcher) Close() error {
	if w.cancel != nil {
		w.cancel()
	}
	return w.fsw.Close()
}

// buildStimulus constructs a Stimulus per the log-error perception design
// spec's Stimulus Construction table.
func buildStimulus(service, msg, rawLine string, now time.Time) *common.Stimulus {
	specific, _ := json.Marshal(struct {
		Service string `json:"service"`
		Msg     string `json:"msg"`
	}{Service: service, Msg: msg})

	id, err := uuid.NewV7()
	if err != nil {
		// Extremely unlikely (crypto/rand failure); fall back to a random v4
		// rather than crash the watcher over one reading.
		id = uuid.New()
	}

	return &common.Stimulus{
		StimulusID:    id.String(),
		SchemaVersion: 1,
		ReceivedAt:    now,
		OccurredAt:    &now,
		Channel:       "log-error",
		Source: common.Source{
			Identity:      "log-error",
			Authenticated: true,
			AuthMethod:    "system",
		},
		Content: common.Content{
			RawText:     rawLine,
			RawPayload:  json.RawMessage(`{}`),
			ContentType: "text",
			Attachments: []common.Attachment{},
		},
		ChannelMeta: common.ChannelMeta{
			MessageID:       computeMessageID(service, msg, now),
			ChannelSpecific: specific,
		},
		Hints: common.Hints{
			Priority: "critical",
			Tags:     []string{"system", "log-error", service},
		},
		Override: common.Override{
			IsOverride: false,
			Params:     json.RawMessage(`{}`),
		},
	}
}

// computeMessageID mirrors every other channel's dedup-key-for-the-wire
// convention: sha256(service + msg + occurred_at).
func computeMessageID(service, msg string, occurredAt time.Time) string {
	sum := sha256.Sum256([]byte(service + msg + occurredAt.Format(time.RFC3339)))
	return hex.EncodeToString(sum[:])
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./perception-svc/logmonitor/... -run TestReconcileAll_FirstRun_IgnoresPreExistingContent -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add perception-svc/logmonitor/watcher.go perception-svc/logmonitor/watcher_test.go
git commit -m "perception-svc/logmonitor: add Watcher wiring parser+dedup+checkpoint"
```

Then add the remaining watcher tests (same task, before moving to Task 5):

```go
func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("OpenFile %s: %v", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
}

func TestReconcileAll_NewErrorLine_PublishesOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory-svc-startup-err.log")
	writeFile(t, path, "")

	pub := &fakePublisher{}
	w := newTestWatcher(t, dir, pub)
	w.reconcileAll(context.Background()) // establishes EOF baseline (empty file)

	appendLine(t, path, `2026/07/27 10:05:00 ERROR writer: DB insert failed, will replay on restart stimulus_id=abc error="connection refused"`)
	w.reconcileAll(context.Background())

	if got := pub.publishedCount(); got != 1 {
		t.Fatalf("published = %d, want 1", got)
	}
	s := pub.published[0]
	if s.Channel != "log-error" {
		t.Errorf("Channel = %q, want log-error", s.Channel)
	}
	if s.Hints.Priority != "critical" {
		t.Errorf("Hints.Priority = %q, want critical", s.Hints.Priority)
	}
	wantTags := []string{"system", "log-error", "memory-svc"}
	if len(s.Hints.Tags) != 3 || s.Hints.Tags[0] != wantTags[0] || s.Hints.Tags[1] != wantTags[1] || s.Hints.Tags[2] != wantTags[2] {
		t.Errorf("Hints.Tags = %v, want %v", s.Hints.Tags, wantTags)
	}
	if s.Content.RawText != `2026/07/27 10:05:00 ERROR writer: DB insert failed, will replay on restart stimulus_id=abc error="connection refused"` {
		t.Errorf("Content.RawText = %q, want the full matched line verbatim", s.Content.RawText)
	}
}

func TestReconcileAll_RepeatedIdenticalPair_PublishesOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory-svc-startup-err.log")
	writeFile(t, path, "")

	pub := &fakePublisher{}
	w := newTestWatcher(t, dir, pub)
	w.reconcileAll(context.Background())

	appendLine(t, path, `2026/07/27 10:05:00 ERROR writer: DB insert failed, will replay on restart error="connection refused"`)
	w.reconcileAll(context.Background())

	appendLine(t, path, `2026/07/27 10:05:05 ERROR writer: DB insert failed, will replay on restart error="connection refused"`)
	w.reconcileAll(context.Background())

	if got := pub.publishedCount(); got != 1 {
		t.Fatalf("published = %d, want 1 (repeated identical service+msg absorbed)", got)
	}
}

func TestReconcileAll_TwoDistinctMessagesFromSameService_PublishesTwice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory-svc-startup-err.log")
	writeFile(t, path, "")

	pub := &fakePublisher{}
	w := newTestWatcher(t, dir, pub)
	w.reconcileAll(context.Background())

	appendLine(t, path, `2026/07/27 10:05:00 ERROR writer: DB insert failed, will replay on restart error="connection refused"`)
	w.reconcileAll(context.Background())
	appendLine(t, path, `2026/07/27 10:06:00 ERROR nats consumer start failed error="dial tcp: timeout"`)
	w.reconcileAll(context.Background())

	if got := pub.publishedCount(); got != 2 {
		t.Fatalf("published = %d, want 2 (two distinct messages)", got)
	}
}

func TestReconcileAll_WarnInfoAndMalformedLines_Skipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory-svc-startup-err.log")
	writeFile(t, path, "")

	pub := &fakePublisher{}
	w := newTestWatcher(t, dir, pub)
	w.reconcileAll(context.Background())

	appendLine(t, path, "2026/07/27 10:05:00 WARN checkpoint: parse failed, starting empty path=x error=y")
	appendLine(t, path, "2026/07/27 10:05:01 INFO memory-svc started nats_url=nats://localhost:4222")
	appendLine(t, path, "goroutine 1 [running]:")
	appendLine(t, path, "\tmain.main() /home/user/main.go:42 +0x1a")
	w.reconcileAll(context.Background())

	if got := pub.publishedCount(); got != 0 {
		t.Fatalf("published = %d, want 0 (WARN/INFO/malformed lines must all be skipped)", got)
	}
}

func TestReconcileAll_PublishFailure_DedupKeyNotMarked_RetriesNextRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory-svc-startup-err.log")
	writeFile(t, path, "")

	pub := &fakePublisher{}
	w := newTestWatcher(t, dir, pub)
	w.reconcileAll(context.Background())

	pub.publishErr = errors.New("nats down")
	appendLine(t, path, `2026/07/27 10:05:00 ERROR writer: DB insert failed, will replay on restart error="connection refused"`)
	w.reconcileAll(context.Background())

	if got := pub.publishedCount(); got != 0 {
		t.Fatalf("published = %d, want 0 (publish failed)", got)
	}

	pub.publishErr = nil
	appendLine(t, path, `2026/07/27 10:06:00 ERROR writer: DB insert failed, will replay on restart error="connection refused"`)
	w.reconcileAll(context.Background())

	if got := pub.publishedCount(); got != 1 {
		t.Errorf("published = %d, want 1 (same line retried on next matching line after publish recovered)", got)
	}
}

func TestReconcileAll_Truncation_OffsetResetsAndTailingContinues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory-svc-startup-err.log")
	writeFile(t, path, strings.Repeat("padding to establish a real starting offset\n", 50))

	pub := &fakePublisher{}
	w := newTestWatcher(t, dir, pub)
	w.reconcileAll(context.Background()) // baseline: checkpoint offset = current EOF (well past 0)

	// Simulate a manual truncation (e.g. what happened to
	// memory-svc-startup-err.log on 2026-07-27) followed by a fresh error.
	writeFile(t, path, `2026/07/27 11:00:00 ERROR writer: DB insert failed, will replay on restart error="connection refused"`+"\n")
	w.reconcileAll(context.Background())

	if got := pub.publishedCount(); got != 1 {
		t.Fatalf("published = %d, want 1 (truncation must reset offset to 0 and pick up the new line)", got)
	}
}

func TestReconcileAll_TrailingPartialLine_LeftForNextRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory-svc-startup-err.log")
	writeFile(t, path, "")

	pub := &fakePublisher{}
	w := newTestWatcher(t, dir, pub)
	w.reconcileAll(context.Background())

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	partial := `2026/07/27 10:05:00 ERROR writer: DB insert failed, will replay on restart error="connection refused"`
	if _, err := f.WriteString(partial); err != nil { // no trailing newline yet
		t.Fatalf("WriteString: %v", err)
	}
	f.Close()

	w.reconcileAll(context.Background())
	if got := pub.publishedCount(); got != 0 {
		t.Fatalf("published = %d, want 0 (a trailing partial line must not be processed yet)", got)
	}

	f, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if _, err := f.WriteString("\n"); err != nil { // now complete
		t.Fatalf("WriteString: %v", err)
	}
	f.Close()

	w.reconcileAll(context.Background())
	if got := pub.publishedCount(); got != 1 {
		t.Errorf("published = %d, want 1 (the now-completed line must be processed)", got)
	}
}
```

Add `"errors"` and `"strings"` to the test file's imports.

Run: `go test ./perception-svc/logmonitor/... -v`
Expected: PASS (all tests in the package)

```bash
git add perception-svc/logmonitor/watcher_test.go
git commit -m "perception-svc/logmonitor: cover dedup, truncation, and partial-line cases end to end"
```

---

### Task 5: `sharedconfig` + `perception-svc/config` wiring

**Files:**
- Modify: `common/sharedconfig/config.go`
- Modify: `perception-svc/config/config.go`
- Modify: `perception-svc/config/config_test.go`
- Modify: `config/dev.json`
- Modify: `config/prod.json`

**Interfaces:**
- Consumes: nothing code-level (data/config layer only)
- Produces: `sharedconfig.LogMonitorConfig{ReconciliationIntervalSeconds int}`, `sharedconfig.Config.LogMonitor`, `config.Config.LogDir string`, `config.Config.LogMonitorCheckpointPath string`, `config.Config.LogMonitorReconcileIntervalSeconds int` — used by Task 6's `main.go`

- [ ] **Step 1: Write the failing test**

```go
func TestLoad_ZeroLogMonitorReconciliationInterval_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	logMon := validLogMonitor
	logMon.ReconciliationIntervalSeconds = 0
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, validSystemMonitor, logMon)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for zero log_monitor.reconciliation_interval_seconds, got nil")
	}
}
```

(This references `validLogMonitor` and a 6-argument `writeConfigFile` that don't exist yet — Step 2 confirms the resulting compile failure.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./perception-svc/config/... -run TestLoad_ZeroLogMonitorReconciliationInterval_ReturnsError -v`
Expected: FAIL — build fails: `undefined: validLogMonitor` and `too many arguments in call to writeConfigFile`

- [ ] **Step 3: Write minimal implementation**

First, `common/sharedconfig/config.go` — add the new type and field (insert after `SystemMonitorConfig`'s doc comment block and struct, before `WebConfig`; add the `LogMonitor` field to `Config`):

```go
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
	LogMonitor    LogMonitorConfig    `json:"log_monitor"`
	Web           WebConfig           `json:"web"`
}
```

```go
// LogMonitorConfig holds perception-svc's Log Error channel settings: how
// often the reconciliation poll safety-net runs, alongside fsnotify's
// instant-reaction detection. Unlike GmailConfig, this channel has no
// external credential dependency and no reason to ever be optional — same
// fatal-fast-if-absent-or-non-positive treatment as
// system_monitor.poll_interval_seconds. See
// docs/superpowers/specs/2026-07-27-log-error-perception-design.md.
type LogMonitorConfig struct {
	ReconciliationIntervalSeconds int `json:"reconciliation_interval_seconds"`
}
```

Next, `perception-svc/config/config.go` — full new file:

```go
package config

import (
	"fmt"
	"os"
	"path/filepath"
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

	SystemMonitorPollIntervalSeconds int
	SystemMonitorChecks              []sharedconfig.CheckConfig

	LogDir                             string
	LogMonitorCheckpointPath           string
	LogMonitorReconcileIntervalSeconds int
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
	if shared.SystemMonitor.PollIntervalSeconds <= 0 {
		return nil, fmt.Errorf("shared config %s has no positive system_monitor.poll_interval_seconds configured", configPath)
	}
	if len(shared.SystemMonitor.Checks) == 0 {
		return nil, fmt.Errorf("shared config %s has no system_monitor.checks configured", configPath)
	}
	for i, c := range shared.SystemMonitor.Checks {
		switch c.Type {
		case "disk_space":
			if c.Path == "" {
				return nil, fmt.Errorf("shared config %s: system_monitor.checks[%d] (disk_space) has no path configured", configPath, i)
			}
		case "memory", "cpu":
		case "service_health":
			if c.Name == "" {
				return nil, fmt.Errorf("shared config %s: system_monitor.checks[%d] (service_health) has no name configured", configPath, i)
			}
			if c.Target == "" {
				return nil, fmt.Errorf("shared config %s: system_monitor.checks[%d] (service_health) has no target configured", configPath, i)
			}
		default:
			return nil, fmt.Errorf("shared config %s: system_monitor.checks[%d] has unknown type %q", configPath, i, c.Type)
		}
		if c.Type == "service_health" {
			continue // binary check: no percent thresholds to validate
		}
		if c.WarningThresholdPercent <= 0 {
			return nil, fmt.Errorf("shared config %s: system_monitor.checks[%d] (%s) has no positive warning_threshold_percent configured", configPath, i, c.Type)
		}
		if c.CriticalThresholdPercent > 0 && c.CriticalThresholdPercent < c.WarningThresholdPercent {
			return nil, fmt.Errorf("shared config %s: system_monitor.checks[%d] (%s) has critical_threshold_percent below warning_threshold_percent", configPath, i, c.Type)
		}
	}
	if shared.LogMonitor.ReconciliationIntervalSeconds <= 0 {
		return nil, fmt.Errorf("shared config %s has no positive log_monitor.reconciliation_interval_seconds configured", configPath)
	}

	checkpointPath := env("CHECKPOINT_PATH", "./checkpoints.json")

	return &Config{
		NATSURL:           shared.NATSURL,
		HTTPPort:          env("HTTP_PORT", "9001"),
		WatchPaths:        shared.WatchPaths,
		CheckpointPath:    checkpointPath,
		ReconcileInterval: envInt("RECONCILE_INTERVAL_SECONDS", 30),
		StimulusSubject:   shared.StimulusSubject,

		GmailClientID:            env("GMAIL_CLIENT_ID", ""),
		GmailClientSecret:        env("GMAIL_CLIENT_SECRET", ""),
		GmailRefreshToken:        env("GMAIL_REFRESH_TOKEN", ""),
		GmailQuery:               shared.Gmail.Query,
		GmailSeenLabel:           shared.Gmail.SeenLabel,
		GmailPollIntervalSeconds: shared.Gmail.PollIntervalSeconds,

		SystemMonitorPollIntervalSeconds: shared.SystemMonitor.PollIntervalSeconds,
		SystemMonitorChecks:              shared.SystemMonitor.Checks,

		// LOG_DIR is not currently set by perception-svc's run-perception-svc.ps1
		// launchers in soulman-dev/soulman-prod (verified against the live
		// scripts while writing this plan) — only memory-svc's launcher sets
		// it, for its own unrelated file-log purpose. This default lets local
		// `go run .` work out of the box; see this plan's self-review for the
		// one-line manual addition needed in both environments' launcher
		// scripts before this channel finds real sibling logs.
		LogDir:                             env("LOG_DIR", "./logs"),
		LogMonitorCheckpointPath:           filepath.Join(filepath.Dir(checkpointPath), "logmonitor-checkpoint.json"),
		LogMonitorReconcileIntervalSeconds: shared.LogMonitor.ReconciliationIntervalSeconds,
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

Next, `perception-svc/config/config_test.go` — full new file (extends `sharedFields`/`writeConfigFile` with the new block and every existing call site):

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

type checkFields struct {
	Type                     string  `json:"type"`
	Path                     string  `json:"path,omitempty"`
	Name                     string  `json:"name,omitempty"`
	Target                   string  `json:"target,omitempty"`
	WarningThresholdPercent  float64 `json:"warning_threshold_percent,omitempty"`
	CriticalThresholdPercent float64 `json:"critical_threshold_percent,omitempty"`
}

type systemMonitorFields struct {
	PollIntervalSeconds int           `json:"poll_interval_seconds"`
	Checks              []checkFields `json:"checks"`
}

type logMonitorFields struct {
	ReconciliationIntervalSeconds int `json:"reconciliation_interval_seconds"`
}

type sharedFields struct {
	WatchPaths      []string            `json:"watch_paths"`
	NATSURL         string              `json:"nats_url"`
	StimulusSubject string              `json:"stimulus_subject"`
	Gmail           gmailFields         `json:"gmail"`
	SystemMonitor   systemMonitorFields `json:"system_monitor"`
	LogMonitor      logMonitorFields    `json:"log_monitor"`
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

// validSystemMonitor is the same kind of ready-to-use fixture for
// system_monitor, which is fatally validated regardless of any credential
// (it has none).
var validSystemMonitor = systemMonitorFields{
	PollIntervalSeconds: 300,
	Checks: []checkFields{
		{Type: "disk_space", Path: `C:\`, WarningThresholdPercent: 80, CriticalThresholdPercent: 95},
	},
}

// validLogMonitor is the same kind of ready-to-use fixture for log_monitor,
// fatally validated regardless of any credential (it has none).
var validLogMonitor = logMonitorFields{ReconciliationIntervalSeconds: 30}

func writeConfigFile(t *testing.T, watchPaths []string, natsURL, stimulusSubject string, gmail gmailFields, sysMonitor systemMonitorFields, logMonitor logMonitorFields) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	data, err := json.Marshal(sharedFields{
		WatchPaths:      watchPaths,
		NATSURL:         natsURL,
		StimulusSubject: stimulusSubject,
		Gmail:           gmail,
		SystemMonitor:   sysMonitor,
		LogMonitor:      logMonitor,
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
	os.Unsetenv("LOG_DIR")
}

func TestLoad_Defaults(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, []string{`C:\Users\Lenovo\DigitalMe\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, validSystemMonitor, validLogMonitor)
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
	if cfg.SystemMonitorPollIntervalSeconds != 300 {
		t.Errorf("SystemMonitorPollIntervalSeconds = %d, want 300", cfg.SystemMonitorPollIntervalSeconds)
	}
	if len(cfg.SystemMonitorChecks) != 1 || cfg.SystemMonitorChecks[0].Type != "disk_space" {
		t.Errorf("SystemMonitorChecks = %+v, want one disk_space check", cfg.SystemMonitorChecks)
	}
	if cfg.LogDir != "./logs" {
		t.Errorf("LogDir = %q, want ./logs", cfg.LogDir)
	}
	if cfg.LogMonitorCheckpointPath != filepath.Join(".", "logmonitor-checkpoint.json") {
		t.Errorf("LogMonitorCheckpointPath = %q, want %q", cfg.LogMonitorCheckpointPath, filepath.Join(".", "logmonitor-checkpoint.json"))
	}
	if cfg.LogMonitorReconcileIntervalSeconds != 30 {
		t.Errorf("LogMonitorReconcileIntervalSeconds = %d, want 30", cfg.LogMonitorReconcileIntervalSeconds)
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
	configPath := writeConfigFile(t, []string{`C:\a\errors`, `C:\b\errors`, `C:\c\errors`}, "nats://remote:4222", "soulman.dev.stimulus.raw", gmail, validSystemMonitor, validLogMonitor)
	os.Setenv("CONFIG_PATH", configPath)
	os.Setenv("HTTP_PORT", "9999")
	os.Setenv("CHECKPOINT_PATH", "./data/checkpoints.json")
	os.Setenv("RECONCILE_INTERVAL_SECONDS", "45")
	os.Setenv("GMAIL_CLIENT_ID", "client-123")
	os.Setenv("GMAIL_CLIENT_SECRET", "secret-456")
	os.Setenv("GMAIL_REFRESH_TOKEN", "refresh-789")
	os.Setenv("LOG_DIR", "./data/logs")

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
	if cfg.LogDir != "./data/logs" {
		t.Errorf("LogDir = %q, want ./data/logs", cfg.LogDir)
	}
	if cfg.LogMonitorCheckpointPath != filepath.Join("data", "logmonitor-checkpoint.json") {
		t.Errorf("LogMonitorCheckpointPath = %q, want %q", cfg.LogMonitorCheckpointPath, filepath.Join("data", "logmonitor-checkpoint.json"))
	}
}

func TestLoad_InvalidReconcileInterval_FallsBackToDefault(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, validSystemMonitor, validLogMonitor)
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

	configPath := writeConfigFile(t, []string{}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, validSystemMonitor, validLogMonitor)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty watch_paths, got nil")
	}
}

func TestLoad_EmptyNATSURL_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "", "soulman.stimulus.raw", validGmail, validSystemMonitor, validLogMonitor)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty nats_url, got nil")
	}
}

func TestLoad_EmptyStimulusSubject_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "", validGmail, validSystemMonitor, validLogMonitor)
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
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", gmail, validSystemMonitor, validLogMonitor)
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
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", gmail, validSystemMonitor, validLogMonitor)
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
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", gmail, validSystemMonitor, validLogMonitor)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for zero gmail.poll_interval_seconds, got nil")
	}
}

func TestLoad_EmptySystemMonitorChecks_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	sysMon := validSystemMonitor
	sysMon.Checks = nil
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, sysMon, validLogMonitor)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for empty system_monitor.checks, got nil")
	}
}

func TestLoad_ZeroSystemMonitorPollInterval_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	sysMon := validSystemMonitor
	sysMon.PollIntervalSeconds = 0
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, sysMon, validLogMonitor)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for zero system_monitor.poll_interval_seconds, got nil")
	}
}

func TestLoad_UnknownSystemMonitorCheckType_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	sysMon := validSystemMonitor
	sysMon.Checks = []checkFields{{Type: "network", WarningThresholdPercent: 80}}
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, sysMon, validLogMonitor)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for unknown system_monitor check type, got nil")
	}
}

func TestLoad_DiskSpaceCheckMissingPath_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	sysMon := validSystemMonitor
	sysMon.Checks = []checkFields{{Type: "disk_space", WarningThresholdPercent: 80}}
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, sysMon, validLogMonitor)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for disk_space check with no path, got nil")
	}
}

func TestLoad_ZeroWarningThreshold_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	sysMon := validSystemMonitor
	sysMon.Checks = []checkFields{{Type: "cpu", WarningThresholdPercent: 0}}
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, sysMon, validLogMonitor)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for zero warning_threshold_percent, got nil")
	}
}

func TestLoad_CriticalThresholdBelowWarning_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	sysMon := validSystemMonitor
	sysMon.Checks = []checkFields{{Type: "disk_space", Path: `C:\`, WarningThresholdPercent: 90, CriticalThresholdPercent: 80}}
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, sysMon, validLogMonitor)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for critical_threshold_percent below warning_threshold_percent, got nil")
	}
}

func TestLoad_ValidMemoryAndCPUChecks_NoPathRequired(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	sysMon := validSystemMonitor
	sysMon.Checks = []checkFields{
		{Type: "memory", WarningThresholdPercent: 85},
		{Type: "cpu", WarningThresholdPercent: 90},
	}
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, sysMon, validLogMonitor)
	os.Setenv("CONFIG_PATH", configPath)

	if _, err := config.Load(); err != nil {
		t.Fatalf("Load: want no error for valid memory/cpu checks without path, got %v", err)
	}
}

func TestLoad_ServiceHealthCheckMissingName_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	sysMon := validSystemMonitor
	sysMon.Checks = []checkFields{{Type: "service_health", Target: "localhost:5176"}}
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, sysMon, validLogMonitor)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for service_health check with no name, got nil")
	}
}

func TestLoad_ServiceHealthCheckMissingTarget_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	sysMon := validSystemMonitor
	sysMon.Checks = []checkFields{{Type: "service_health", Name: "agent-suite-backend"}}
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, sysMon, validLogMonitor)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for service_health check with no target, got nil")
	}
}

func TestLoad_ValidServiceHealthCheck_NoThresholdRequired(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	sysMon := validSystemMonitor
	sysMon.Checks = []checkFields{
		{Type: "service_health", Name: "agent-suite-backend", Target: "http://localhost:8091/health"},
		{Type: "service_health", Name: "digital-me-frontend", Target: "localhost:5173"},
	}
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, sysMon, validLogMonitor)
	os.Setenv("CONFIG_PATH", configPath)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: want no error for valid service_health checks without thresholds, got %v", err)
	}
	if len(cfg.SystemMonitorChecks) != 2 {
		t.Fatalf("SystemMonitorChecks = %d entries, want 2", len(cfg.SystemMonitorChecks))
	}
	if cfg.SystemMonitorChecks[0].Name != "agent-suite-backend" || cfg.SystemMonitorChecks[0].Target != "http://localhost:8091/health" {
		t.Errorf("SystemMonitorChecks[0] = %+v, want agent-suite-backend/http://localhost:8091/health", cfg.SystemMonitorChecks[0])
	}
}

func TestLoad_ZeroLogMonitorReconciliationInterval_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	logMon := validLogMonitor
	logMon.ReconciliationIntervalSeconds = 0
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, validSystemMonitor, logMon)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for zero log_monitor.reconciliation_interval_seconds, got nil")
	}
}
```

Finally, add the `log_monitor` block to `config/dev.json` (insert after `system_monitor`, before `web`):

```json
  "log_monitor": {
    "reconciliation_interval_seconds": 30
  },
```

and the identical block to `config/prod.json` in the same position.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./perception-svc/config/... ./common/sharedconfig/... -v`
Expected: PASS (all tests, including the new `TestLoad_ZeroLogMonitorReconciliationInterval_ReturnsError`)

- [ ] **Step 5: Commit**

```bash
git add common/sharedconfig/config.go perception-svc/config/config.go perception-svc/config/config_test.go config/dev.json config/prod.json
git commit -m "sharedconfig/perception-svc: add log_monitor config block and validation"
```

---

### Task 6: `perception-svc/main.go` wiring

**Files:**
- Modify: `perception-svc/main.go`
- Modify: `perception-svc/NOTES.md`
- Modify: `CLAUDE.md`

**Interfaces:**
- Consumes: `logmonitor.New(logDir string, publisher logmonitor.Publisher, checkpointPath string, reconcileInterval time.Duration) (*logmonitor.Watcher, error)`, `(*logmonitor.Watcher).Start(ctx)`, `(*logmonitor.Watcher).Close()` (Task 4); `cfg.LogDir`, `cfg.LogMonitorCheckpointPath`, `cfg.LogMonitorReconcileIntervalSeconds` (Task 5); the existing `pub *natspublish.Publisher` (already satisfies `logmonitor.Publisher` — same `Publish(ctx, *common.Stimulus) error` shape every channel already uses)
- Produces: perception-svc, when run, publishes `channel: "log-error"` Stimuli — no new Go symbols later tasks import (thinking-svc and action-svc consume this only via the NATS wire format)

- [ ] **Step 1: Write the failing test**

`perception-svc/main.go` has no existing test file (it's an entrypoint, exercised via the package-level tests of the packages it wires together, all already covered by Tasks 1-5). This task's verification step is a full-service build, not a unit test — matching how `folderwatcher`/`gmailwatcher`/`sysmonitor` wiring into `main.go` was verified in the original perception-svc design.

```bash
go build ./perception-svc/... 
```

Expected: FAIL — `main.go` doesn't yet reference `logmonitor`, but this step's real purpose is to confirm the *baseline* build is currently green before the edit, so Step 4's build-passes check is meaningful. Run it now and confirm it currently succeeds (nothing to fail yet — proceed to Step 3).

- [ ] **Step 2: Run test to verify it fails**

Run: `go build ./perception-svc/...`
Expected: PASS (baseline, pre-edit — recorded here only so Step 4's post-edit build is a meaningful before/after comparison, per this task's nature as a wiring task rather than a new-behavior unit)

- [ ] **Step 3: Write minimal implementation**

Add `"soulman/perception-svc/logmonitor"` to `perception-svc/main.go`'s import block (alphabetically, after `httpserver`, before `natspublish`):

```go
import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"soulman/perception-svc/config"
	"soulman/perception-svc/gmailwatcher"
	"soulman/perception-svc/httpserver"
	"soulman/perception-svc/logmonitor"
	"soulman/perception-svc/natspublish"
	"soulman/perception-svc/sysmonitor"
	"soulman/perception-svc/watcher"
)
```

Insert the Log Error channel construction right after the System Monitor block and before the HTTP server block:

```go
	sm := sysmonitor.New(smChecks, pub, time.Duration(cfg.SystemMonitorPollIntervalSeconds)*time.Second)
	defer sm.Close()
	sm.Start(ctx)
	slog.Info("sysmonitor: started", "checks", len(smChecks), "poll_interval_s", cfg.SystemMonitorPollIntervalSeconds)

	lm, err := logmonitor.New(cfg.LogDir, pub, cfg.LogMonitorCheckpointPath, time.Duration(cfg.LogMonitorReconcileIntervalSeconds)*time.Second)
	if err != nil {
		slog.Error("logmonitor init failed", "error", err)
		os.Exit(1)
	}
	defer lm.Close()
	lm.Start(ctx)
	slog.Info("logmonitor: started", "log_dir", cfg.LogDir, "reconcile_interval_s", cfg.LogMonitorReconcileIntervalSeconds)

	srv := httpserver.New(cfg.HTTPPort, cfg.WatchPaths, pub.Status, pub, sm.Status)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go build ./perception-svc/... && go vet ./perception-svc/...`
Expected: PASS — build succeeds, `logmonitor` is now wired into `main.go`

- [ ] **Step 5: Commit**

```bash
git add perception-svc/main.go
git commit -m "perception-svc: wire up the Log Error channel alongside folder-watcher, Gmail, and System Monitor"
```

Then update docs (same task — this is the wiring task that makes the channel real, so its doc footprint belongs here per this plan's task-sizing decision, see the plan's self-review notes):

In `perception-svc/NOTES.md`, add a new section (after the "System Monitor channel" section, before "Pipeline debugging tools"):

```markdown
## Log Error channel (`logmonitor` package, added 2026-07-27)

Tails every sibling service's `*-startup-err.log` file in `LOG_DIR` for new `slog` `ERROR`-level lines (fsnotify + a 30s reconciliation poll, mirroring `watcher`'s dual detection mechanism), publishing a `Stimulus` the first time a given `(service, msg)` pair is seen this process lifetime — dedup is in-memory only, not persisted, same accepted tradeoff as `sysmonitor`'s severity state. See `docs/superpowers/specs/2026-07-27-log-error-perception-design.md`.

**Deployment gap found while building this feature:** the design spec assumed `LOG_DIR` was already set for every service via its `run-<svc>.ps1` launcher. That's only true for `memory-svc` (which sets it for its own unrelated file-log purpose) — `perception-svc`'s own launcher in both `soulman-dev` and `soulman-prod` does not set `LOG_DIR` today. `perception-svc/config.Load()` defaults it to `./logs` (matching every other env var's local-dev-friendly relative-default pattern in this file), but that only resolves correctly if the process's working directory happens to be the environment root when launched. **Before this channel will find real sibling logs in either environment, add `$env:LOG_DIR = Join-Path $PSScriptRoot "logs"` to `perception-svc`'s `run-perception-svc.ps1` in both `soulman-dev\` and `soulman-prod\`** (mirroring the line `memory-svc`'s launcher already has) — those files live outside this git repo, so this plan cannot make that edit itself.

The checkpoint file (`logmonitor-checkpoint.json`) is derived from `CHECKPOINT_PATH`'s directory rather than needing its own env var — it lives alongside `watcher`'s own `perception-svc-checkpoints.json` in the `state\` folder.
```

In `CLAUDE.md`, update the perception-svc bullet (item 2 under "Services") from:

```markdown
2. **`perception-svc`** — normalizes external input into `Stimulus` events on `soulman.stimulus.raw`. Three input channels: **folder-watcher** (`fsnotify` on paths from the shared config file's `watch_paths`), **Gmail** (`gmailwatcher` package — polls the inbox via OAuth2 offline refresh token, dedups via a per-environment Gmail label), and **System Monitor** (`sysmonitor` package — polls disk/memory/CPU via `golang.org/x/sys/windows` plus external `service_health` targets via TCP dial/HTTP GET, publishes only on a severity transition). Also serves `POST /api/perceive/cli` (CLI push channel) and `POST /api/perceive/raw` (generic Stimulus injection for debugging).
   - Specs: `2026-07-17-perception-svc-design.md`, `2026-07-18-gmail-channel-design.md`, `2026-07-18-soulman-cli-design.md`, `2026-07-18-pipeline-debugging-tools-design.md`, `2026-07-18-system-monitor-channel-design.md`, `2026-07-19-system-monitor-service-health-design.md`, `2026-07-20-system-monitor-dashboard-panel-design.md`
   - Notes: `perception-svc/NOTES.md` — real incidents (padded Gmail base64 bodies, a blocking-startup-poll bug, the unbounded-backlog incident that motivated the debugging tools)
```

to:

```markdown
2. **`perception-svc`** — normalizes external input into `Stimulus` events on `soulman.stimulus.raw`. Four input channels: **folder-watcher** (`fsnotify` on paths from the shared config file's `watch_paths`), **Gmail** (`gmailwatcher` package — polls the inbox via OAuth2 offline refresh token, dedups via a per-environment Gmail label), **System Monitor** (`sysmonitor` package — polls disk/memory/CPU via `golang.org/x/sys/windows` plus external `service_health` targets via TCP dial/HTTP GET, publishes only on a severity transition), and **Log Error** (`logmonitor` package — tails every sibling service's `*-startup-err.log` for new `slog` `ERROR` lines, publishing once per distinct `(service, msg)` pair per process lifetime). Also serves `POST /api/perceive/cli` (CLI push channel) and `POST /api/perceive/raw` (generic Stimulus injection for debugging).
   - Specs: `2026-07-17-perception-svc-design.md`, `2026-07-18-gmail-channel-design.md`, `2026-07-18-soulman-cli-design.md`, `2026-07-18-pipeline-debugging-tools-design.md`, `2026-07-18-system-monitor-channel-design.md`, `2026-07-19-system-monitor-service-health-design.md`, `2026-07-20-system-monitor-dashboard-panel-design.md`, `2026-07-27-log-error-perception-design.md`
   - Notes: `perception-svc/NOTES.md` — real incidents (padded Gmail base64 bodies, a blocking-startup-poll bug, the unbounded-backlog incident that motivated the debugging tools), the Log Error channel's `LOG_DIR` deployment gap
```

```bash
git add perception-svc/NOTES.md CLAUDE.md
git commit -m "perception-svc/docs: document the Log Error channel and its LOG_DIR deployment gap"
```

---

### Task 7: `thinking-svc` Log Error rule

**Files:**
- Create: `thinking-svc/rules/log_error.go`
- Test: `thinking-svc/rules/log_error_test.go`
- Modify: `thinking-svc/rules/rule.go`
- Modify: `thinking-svc/NOTES.md`
- Modify: `CLAUDE.md`

**Interfaces:**
- Consumes: `common.Stimulus`, `common.ActionRequest` (existing, `soulman/common`); `errorReportParams` (existing type in `thinking-svc/rules/error_report.go`, same package); `Rule` type and `Registry` (existing, `thinking-svc/rules/rule.go`)
- Produces: `var LogErrorRule Rule`, registered in `Registry` — action-svc doesn't import this Go symbol; it only ever sees the resulting `soulman.thinking.request` wire message

- [ ] **Step 1: Write the failing test**

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

func newLogErrorStimulus(rawText, service string, occurredAt time.Time) *common.Stimulus {
	specific, _ := json.Marshal(struct {
		Service string `json:"service"`
		Msg     string `json:"msg"`
	}{Service: service, Msg: rawText})

	return &common.Stimulus{
		StimulusID: "stim-logerror-001",
		Channel:    "log-error",
		ReceivedAt: time.Now().UTC(),
		OccurredAt: &occurredAt,
		Content: common.Content{
			RawText:     rawText,
			ContentType: "text",
			RawPayload:  json.RawMessage(`{}`),
		},
		ChannelMeta: common.ChannelMeta{
			ChannelSpecific: specific,
		},
		Hints:    common.Hints{Priority: "critical", Tags: []string{"system", "log-error", service}},
		Override: common.Override{Params: json.RawMessage(`{}`)},
	}
}

func TestLogErrorRule_Match_LogErrorChannel(t *testing.T) {
	s := newLogErrorStimulus("2026/07/27 10:00:00 ERROR writer: DB insert failed", "memory-svc", time.Now())
	if !rules.LogErrorRule.Match(s) {
		t.Error("expected match for log-error channel")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./thinking-svc/rules/... -run TestLogErrorRule_Match_LogErrorChannel -v`
Expected: FAIL — `rules.LogErrorRule` is undefined

- [ ] **Step 3: Write minimal implementation**

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

// LogErrorRule implements the single rule from
// docs/superpowers/specs/2026-07-27-log-error-perception-design.md: any
// stimulus from the log-error channel becomes an append_daily_report_entry
// Action Request, always important — no LLM call, since logmonitor already
// built a complete raw_text and Error-level itself is the importance
// signal (unlike system-monitor, there is no non-important tier here at
// all: only Error-level lines ever reach this channel).
var LogErrorRule = Rule{
	Name: "log-error",
	Match: func(s *common.Stimulus) bool {
		return s.Channel == "log-error"
	},
	Handle: handleLogError,
}

func handleLogError(_ context.Context, s *common.Stimulus, _ llm.Client) (*common.ActionRequest, error) {
	params, err := json.Marshal(errorReportParams{
		Summary:    s.Content.RawText,
		RawContent: s.Content.RawText,
		SourcePath: logErrorSourcePath(s),
		OccurredAt: s.OccurredAt,
		Important:  true,
	})
	if err != nil {
		return nil, fmt.Errorf("rules: marshal log error parameters: %w", err)
	}

	req := &common.ActionRequest{
		CorrelationID:   uuid.NewString(),
		Intent:          "Log this error-level log line to today's daily report",
		ActionHint:      "append_daily_report_entry",
		Parameters:      params,
		RiskLevel:       "low",
		Urgency:         "normal",
		ExpectedOutcome: "one entry appended to today's report file",
		Fallback:        "if fs-agent fails, retry once; if it fails again, log to episodic memory with error:execution tag and give up silently — a missed report entry is not worth interrupting the human",
	}
	return req, nil
}

// logErrorSourcePath builds "log-error/<service>" from
// channel_metadata.channel_specific.service — parallels
// systemMonitorSourcePath/watchedPath's same channel_specific extraction
// pattern.
func logErrorSourcePath(s *common.Stimulus) string {
	var meta struct {
		Service string `json:"service"`
	}
	if len(s.ChannelMeta.ChannelSpecific) > 0 {
		_ = json.Unmarshal(s.ChannelMeta.ChannelSpecific, &meta)
	}
	return "log-error/" + meta.Service
}
```

Register in `thinking-svc/rules/rule.go` — change:

```go
var Registry = []Rule{
	ErrorReportRule,
	GmailTriageRule,
	CLINoteRule,
	SystemMonitorRule,
}
```

to:

```go
var Registry = []Rule{
	ErrorReportRule,
	GmailTriageRule,
	CLINoteRule,
	SystemMonitorRule,
	LogErrorRule,
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./thinking-svc/rules/... -run TestLogErrorRule_Match_LogErrorChannel -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add thinking-svc/rules/log_error.go thinking-svc/rules/log_error_test.go thinking-svc/rules/rule.go
git commit -m "thinking-svc: add LogErrorRule, always-important append_daily_report_entry"
```

Then add the remaining rule tests (same task, before moving to Task 8):

```go
func TestLogErrorRule_Match_OtherChannel_NoMatch(t *testing.T) {
	s := newLogErrorStimulus("x", "memory-svc", time.Now())
	s.Channel = "system-monitor"
	if rules.LogErrorRule.Match(s) {
		t.Error("expected no match for system-monitor channel")
	}
}

func TestLogErrorRule_Handle_BuildsActionRequest(t *testing.T) {
	occurred := time.Date(2026, 7, 27, 10, 5, 0, 0, time.UTC)
	rawText := `2026/07/27 10:05:00 ERROR writer: DB insert failed stimulus_id=abc error="dial tcp: connect: connection refused"`
	s := newLogErrorStimulus(rawText, "memory-svc", occurred)

	req, err := rules.LogErrorRule.Handle(context.Background(), s, &fakeSummarizer{})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if req.ActionHint != "append_daily_report_entry" {
		t.Errorf("ActionHint = %q, want append_daily_report_entry", req.ActionHint)
	}
	if req.CorrelationID == "" {
		t.Error("CorrelationID must be generated")
	}

	var params struct {
		Summary    string     `json:"summary"`
		RawContent string     `json:"raw_content"`
		SourcePath string     `json:"source_path"`
		OccurredAt *time.Time `json:"occurred_at"`
		Important  bool       `json:"important"`
	}
	if err := json.Unmarshal(req.Parameters, &params); err != nil {
		t.Fatalf("decode Parameters: %v", err)
	}
	if params.Summary != rawText {
		t.Errorf("Summary = %q, want %q", params.Summary, rawText)
	}
	if params.RawContent != rawText {
		t.Errorf("RawContent = %q, want %q", params.RawContent, rawText)
	}
	if params.SourcePath != "log-error/memory-svc" {
		t.Errorf("SourcePath = %q, want log-error/memory-svc", params.SourcePath)
	}
	if !params.Important {
		t.Error("Important must always be true for log-error")
	}
}

func TestLogErrorRule_Handle_AlwaysImportant(t *testing.T) {
	cases := []string{"memory-svc", "perception-svc", "thinking-svc", "action-svc", "web-svc"}
	for _, svc := range cases {
		t.Run(svc, func(t *testing.T) {
			occurred := time.Now()
			s := newLogErrorStimulus("some error line", svc, occurred)

			req, err := rules.LogErrorRule.Handle(context.Background(), s, &fakeSummarizer{})
			if err != nil {
				t.Fatalf("Handle: %v", err)
			}
			var params struct {
				Important bool `json:"important"`
			}
			if err := json.Unmarshal(req.Parameters, &params); err != nil {
				t.Fatalf("decode Parameters: %v", err)
			}
			if !params.Important {
				t.Errorf("service=%q: important = %v, want true", svc, params.Important)
			}
		})
	}
}

func TestMatch_FindsLogErrorRule(t *testing.T) {
	s := newLogErrorStimulus("x", "memory-svc", time.Now())
	r := rules.Match(s)
	if r == nil {
		t.Fatal("Match = nil, want LogErrorRule for log-error stimulus")
	}
	if r.Name != "log-error" {
		t.Errorf("Name = %q, want log-error", r.Name)
	}
}
```

Run: `go test ./thinking-svc/rules/... -v`
Expected: PASS (all tests in the package, including every pre-existing rule's tests — `LogErrorRule`'s registration must not change any other rule's `Match` result)

```bash
git add thinking-svc/rules/log_error_test.go
git commit -m "thinking-svc: cover LogErrorRule's action request shape and always-important guarantee"
```

Then update docs (same task):

In `thinking-svc/NOTES.md`, add a new section (after "System Monitor importance: ok is always a recovery"):

```markdown
## Log Error rule has no non-important tier (added 2026-07-27)

Unlike `SystemMonitorRule` (`warning` is not important) or `GmailTriageRule` (DeepSeek judges), `LogErrorRule` always sets `Important: true` — there's no lower tier to distinguish because `logmonitor` only ever publishes `channel: "log-error"` stimuli for `ERROR`-level lines in the first place (`WARN`/`INFO` never reach thinking-svc at all, filtered at the source in `perception-svc/logmonitor`). See `docs/superpowers/specs/2026-07-27-log-error-perception-design.md`.
```

In `CLAUDE.md`, update the thinking-svc bullet (item 3 under "Services") from:

```markdown
3. **`thinking-svc`** — matches stimuli against a rule table, publishes an Action Request to `soulman.thinking.request` (durable JetStream stream). Rules today: `folder-watcher`, `cli-note`, and `system-monitor` → mechanical report-entry (no LLM); `gmail` → DeepSeek-judged importance triage, always logs, notifies Discord (batched) only if judged important. `DEEPSEEK_API_KEY` is non-fatal if blank (logs a warning; DeepSeek calls then fail and summarization falls back to deterministic text) but the Gmail triage classifier needs it to actually classify anything.
   - Specs: `2026-07-17-thinking-svc-design.md`, `2026-07-18-gmail-triage-action-design.md`, `2026-07-18-system-monitor-channel-design.md`, `2026-07-20-daily-report-importance-split-design.md`
   - Notes: `thinking-svc/NOTES.md` — the classifier prompt was rewritten with explicit criteria after real false positives (newsletters flagged important)
```

to:

```markdown
3. **`thinking-svc`** — matches stimuli against a rule table, publishes an Action Request to `soulman.thinking.request` (durable JetStream stream). Rules today: `folder-watcher`, `cli-note`, `system-monitor`, and `log-error` → mechanical report-entry (no LLM); `gmail` → DeepSeek-judged importance triage, always logs, notifies Discord (batched) only if judged important. `log-error` (the only rule with no non-important tier at all) and `folder-watcher` always set `Important: true`; `system-monitor` sets it for `critical`/`ok` only. `DEEPSEEK_API_KEY` is non-fatal if blank (logs a warning; DeepSeek calls then fail and summarization falls back to deterministic text) but the Gmail triage classifier needs it to actually classify anything.
   - Specs: `2026-07-17-thinking-svc-design.md`, `2026-07-18-gmail-triage-action-design.md`, `2026-07-18-system-monitor-channel-design.md`, `2026-07-20-daily-report-importance-split-design.md`, `2026-07-27-log-error-perception-design.md`
   - Notes: `thinking-svc/NOTES.md` — the classifier prompt was rewritten with explicit criteria after real false positives (newsletters flagged important)
```

```bash
git add thinking-svc/NOTES.md CLAUDE.md
git commit -m "thinking-svc/docs: document LogErrorRule's always-important behavior"
```

---

### Task 8: `notifybatch` generalization

**Files:**
- Modify: `action-svc/notifybatch/batcher.go`
- Modify: `action-svc/notifybatch/batcher_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks in this plan (independent track — the existing `notify.Notifier` interface and `Batcher` type are unchanged)
- Produces: `type Item struct { Kind, Sender, Subject, ThreadID, Summary, SourcePath, Reason, BodyExcerpt string }` (generalized), `func formatBatch(items []Item) string` (unchanged signature, new branching) — used by Task 9

- [ ] **Step 1: Write the failing test**

```go
func TestBatcher_ReportItem_FlushesWithReportFormat(t *testing.T) {
	notifier := newFakeNotifier()
	b := notifybatch.New(40*time.Millisecond, 2*time.Second, notifier)

	b.Add(notifybatch.Item{Kind: "report", Summary: `Disk space C:\ critical: 97% used`, SourcePath: `system-monitor/disk_space/C:\`, BodyExcerpt: `Disk space C:\ critical: 97% used (threshold 95%)`})

	msg := waitForSend(t, notifier, time.Second)
	if !strings.Contains(msg, `[system-monitor/disk_space/C:\]`) {
		t.Errorf("message = %q, want the source_path in brackets", msg)
	}
	if !strings.Contains(msg, `Disk space C:\ critical: 97% used`) {
		t.Errorf("message = %q, want the summary", msg)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./action-svc/notifybatch/... -run TestBatcher_ReportItem_FlushesWithReportFormat -v`
Expected: FAIL — `notifybatch.Item` has no field `Kind` (compile error)

- [ ] **Step 3: Write minimal implementation**

Full new `action-svc/notifybatch/batcher.go`:

```go
package notifybatch

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"soulman/action-svc/notify"
)

// DefaultGrace and DefaultMaxWait are the hardcoded (not environment-
// configurable, per the design spec) debounce durations action-svc's
// main.go constructs its Batcher with.
const (
	DefaultGrace   = 30 * time.Second
	DefaultMaxWait = 2 * time.Minute
)

// Item is one important notification queued for the next flush — either a
// Gmail triage verdict or a generic append_daily_report_entry entry judged
// important (system-monitor, log-error, folder-watcher, or any future
// mechanical rule that sets Important: true). Kind discriminates which
// fields formatBatch reads: "gmail" uses Sender/Subject/ThreadID/Reason/
// BodyExcerpt (unchanged from before this generalization, added
// 2026-07-27 per docs/superpowers/specs/2026-07-27-log-error-perception-design.md);
// "report" uses Summary/SourcePath/BodyExcerpt, mirroring report.Entry's
// own field names.
type Item struct {
	Kind        string // "gmail" | "report"
	Sender      string // gmail only
	Subject     string // gmail only
	ThreadID    string // gmail only
	Summary     string // report only — mirrors report.Entry.Summary
	SourcePath  string // report only — mirrors report.Entry.SourcePath
	Reason      string // shared: gmail's triage reason, or empty for report
	BodyExcerpt string // shared: gmail body excerpt, or report's raw_content
}

// Batcher collects important Items and flushes them as a single Discord
// message once either the grace period (no new item has arrived recently)
// or the max-wait cap (measured from the first item in the pending batch)
// elapses — whichever comes first. See
// docs/superpowers/specs/2026-07-18-gmail-triage-action-design.md's
// "Notification batching" section for the rationale behind the two
// timers. The queue is in-memory only: a process restart with a batch
// pending loses it (an accepted v1 limitation).
type Batcher struct {
	grace    time.Duration
	maxWait  time.Duration
	notifier notify.Notifier

	mu         sync.Mutex
	items      []Item
	graceTimer *time.Timer
	maxTimer   *time.Timer
}

func New(grace, maxWait time.Duration, notifier notify.Notifier) *Batcher {
	return &Batcher{grace: grace, maxWait: maxWait, notifier: notifier}
}

// Add queues item for the next flush. The first item in a new batch starts
// both timers; later items reset only the grace timer — the max-wait
// timer keeps counting from the first item and is never reset, bounding
// worst-case delay during a steady trickle of arrivals.
func (b *Batcher) Add(item Item) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.items = append(b.items, item)

	if b.graceTimer == nil {
		b.maxTimer = time.AfterFunc(b.maxWait, b.Flush)
		b.graceTimer = time.AfterFunc(b.grace, b.Flush)
		return
	}

	b.graceTimer.Stop()
	b.graceTimer = time.AfterFunc(b.grace, b.Flush)
}

// Flush sends all currently-queued items as one message and clears the
// batch. Safe to call when the batch is already empty — a no-op — which is
// how the timer that loses the grace-vs-max-wait race resolves once it
// fires after the other timer already flushed.
func (b *Batcher) Flush() {
	b.mu.Lock()
	items := b.items
	b.items = nil
	if b.graceTimer != nil {
		b.graceTimer.Stop()
		b.graceTimer = nil
	}
	if b.maxTimer != nil {
		b.maxTimer.Stop()
		b.maxTimer = nil
	}
	b.mu.Unlock()

	if len(items) == 0 {
		return
	}
	_ = b.notifier.Send(formatBatch(items))
}

// formatBatch branches per item on Kind: "report" items render as a plain
// "[<source_path>] <summary>\n<body_excerpt>" block; everything else
// (including "gmail" and, for backward compatibility, an unset Kind)
// preserves the original Gmail-shaped block exactly. The count header was
// "N important email(s):" before this generalization — renamed to "N
// important item(s):" since a batch can now legitimately contain zero
// emails (an all-report-items flush).
func formatBatch(items []Item) string {
	blocks := make([]string, 0, len(items)+1)
	blocks = append(blocks, fmt.Sprintf("%d important item(s):", len(items)))
	for _, it := range items {
		switch it.Kind {
		case "report":
			blocks = append(blocks, fmt.Sprintf("[%s] %s\n%s", it.SourcePath, it.Summary, it.BodyExcerpt))
		default:
			blocks = append(blocks, fmt.Sprintf(
				"From: %s\nSubject: %s\nWhy: %s\n\"%s\"\nhttps://mail.google.com/mail/u/0/#inbox/%s",
				it.Sender, it.Subject, it.Reason, it.BodyExcerpt, it.ThreadID))
		}
	}
	return strings.Join(blocks, "\n\n")
}
```

Full new `action-svc/notifybatch/batcher_test.go`:

```go
package notifybatch_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"soulman/action-svc/notifybatch"
)

type fakeNotifier struct {
	mu       sync.Mutex
	messages []string
	sendCh   chan string
}

func newFakeNotifier() *fakeNotifier {
	return &fakeNotifier{sendCh: make(chan string, 10)}
}

func (f *fakeNotifier) Send(message string) error {
	f.mu.Lock()
	f.messages = append(f.messages, message)
	f.mu.Unlock()
	f.sendCh <- message
	return nil
}

func (f *fakeNotifier) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.messages...)
}

func waitForSend(t *testing.T, f *fakeNotifier, timeout time.Duration) string {
	t.Helper()
	select {
	case msg := <-f.sendCh:
		return msg
	case <-time.After(timeout):
		t.Fatal("timed out waiting for a Send call")
		return ""
	}
}

func TestBatcher_SingleItem_FlushesAfterGracePeriod(t *testing.T) {
	notifier := newFakeNotifier()
	b := notifybatch.New(40*time.Millisecond, 2*time.Second, notifier)

	b.Add(notifybatch.Item{Kind: "gmail", Sender: "a@b.com", Subject: "hi", Reason: "r", BodyExcerpt: "excerpt", ThreadID: "t1"})

	msg := waitForSend(t, notifier, time.Second)
	if !strings.Contains(msg, "1 important item(s):") {
		t.Errorf("message = %q, want a 1-item header", msg)
	}
	if !strings.Contains(msg, "a@b.com") {
		t.Errorf("message = %q, want it to contain the sender", msg)
	}

	time.Sleep(100 * time.Millisecond)
	if len(notifier.sent()) != 1 {
		t.Errorf("Send called %d times, want exactly 1", len(notifier.sent()))
	}
}

func TestBatcher_MultipleItemsWithinGracePeriod_CombineIntoOneSend(t *testing.T) {
	notifier := newFakeNotifier()
	b := notifybatch.New(60*time.Millisecond, 2*time.Second, notifier)

	b.Add(notifybatch.Item{Kind: "gmail", Sender: "a@b.com", Subject: "one", Reason: "r1", BodyExcerpt: "e1", ThreadID: "t1"})
	time.Sleep(15 * time.Millisecond)
	b.Add(notifybatch.Item{Kind: "gmail", Sender: "c@d.com", Subject: "two", Reason: "r2", BodyExcerpt: "e2", ThreadID: "t2"})

	msg := waitForSend(t, notifier, time.Second)
	if !strings.Contains(msg, "2 important item(s):") {
		t.Errorf("message = %q, want a 2-item header", msg)
	}
	if !strings.Contains(msg, "one") || !strings.Contains(msg, "two") {
		t.Errorf("message = %q, want both subjects present", msg)
	}

	time.Sleep(150 * time.Millisecond)
	if len(notifier.sent()) != 1 {
		t.Errorf("Send called %d times, want exactly 1 combined send", len(notifier.sent()))
	}
}

func TestBatcher_SteadyTrickle_FlushesAtMaxWaitCap(t *testing.T) {
	notifier := newFakeNotifier()
	maxWait := 150 * time.Millisecond
	b := notifybatch.New(50*time.Millisecond, maxWait, notifier)

	start := time.Now()
	stop := time.After(300 * time.Millisecond)
	ticker := time.NewTicker(30 * time.Millisecond)
	defer ticker.Stop()

loop:
	for {
		select {
		case <-ticker.C:
			b.Add(notifybatch.Item{Kind: "gmail", Sender: "a@b.com", Subject: "trickle", Reason: "r", BodyExcerpt: "e", ThreadID: "t"})
		case <-stop:
			break loop
		}
	}

	msg := waitForSend(t, notifier, time.Second)
	elapsed := time.Since(start)
	if elapsed > 350*time.Millisecond {
		t.Errorf("first flush took %v, want it forced by the %v max-wait cap well before the 300ms trickle stopped", elapsed, maxWait)
	}
	if !strings.Contains(msg, "important item(s):") {
		t.Errorf("message = %q, want the batch header", msg)
	}
}

func TestBatcher_ReportItem_FlushesWithReportFormat(t *testing.T) {
	notifier := newFakeNotifier()
	b := notifybatch.New(40*time.Millisecond, 2*time.Second, notifier)

	b.Add(notifybatch.Item{Kind: "report", Summary: `Disk space C:\ critical: 97% used`, SourcePath: `system-monitor/disk_space/C:\`, BodyExcerpt: `Disk space C:\ critical: 97% used (threshold 95%)`})

	msg := waitForSend(t, notifier, time.Second)
	if !strings.Contains(msg, `[system-monitor/disk_space/C:\]`) {
		t.Errorf("message = %q, want the source_path in brackets", msg)
	}
	if !strings.Contains(msg, `Disk space C:\ critical: 97% used`) {
		t.Errorf("message = %q, want the summary", msg)
	}
}

func TestBatcher_MixedGmailAndReportItems_BothRenderInOneFlush(t *testing.T) {
	notifier := newFakeNotifier()
	b := notifybatch.New(60*time.Millisecond, 2*time.Second, notifier)

	b.Add(notifybatch.Item{Kind: "gmail", Sender: "boss@company.com", Subject: "Server down", Reason: "outage", BodyExcerpt: "excerpt", ThreadID: "t1"})
	time.Sleep(15 * time.Millisecond)
	b.Add(notifybatch.Item{Kind: "report", Summary: "nats consumer start failed", SourcePath: "log-error/action-svc", BodyExcerpt: "2026/07/27 ERROR nats consumer start failed error=..."})

	msg := waitForSend(t, notifier, time.Second)
	if !strings.Contains(msg, "2 important item(s):") {
		t.Errorf("message = %q, want a 2-item header", msg)
	}
	if !strings.Contains(msg, "boss@company.com") {
		t.Errorf("message = %q, want the gmail item rendered", msg)
	}
	if !strings.Contains(msg, "[log-error/action-svc]") {
		t.Errorf("message = %q, want the report item rendered", msg)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./action-svc/notifybatch/... -v`
Expected: PASS (all tests, including the 3 pre-existing ones with their header assertions updated)

- [ ] **Step 5: Commit**

```bash
git add action-svc/notifybatch/batcher.go action-svc/notifybatch/batcher_test.go
git commit -m "action-svc/notifybatch: generalize Item/formatBatch beyond Gmail-only"
```

---

### Task 9: `action-svc/dispatch` wiring

**Files:**
- Modify: `action-svc/dispatch/dispatch.go`
- Modify: `action-svc/dispatch/gmail_triage.go`
- Modify: `action-svc/dispatch/dispatch_test.go`
- Modify: `action-svc/dispatch/gmail_triage_test.go`
- Modify: `action-svc/NOTES.md`
- Modify: `CLAUDE.md`

**Interfaces:**
- Consumes: `notifybatch.Item{Kind, Summary, SourcePath, BodyExcerpt, ...}` (Task 8); existing `ReportEntryParams` (`action-svc/dispatch/report_entry.go`, same package); existing `Batcher` interface's `Add(item notifybatch.Item)` (`action-svc/dispatch/dispatch.go`)
- Produces: final feature behavior — `dispatchAppendDailyReportEntry` now batches a real-time Discord notification for any important report entry (system-monitor critical/ok, folder-watcher errors, log-error, or any future mechanical rule that sets `Important: true`), not just Gmail triage

- [ ] **Step 1: Write the failing test**

```go
func TestDispatch_AppendDailyReportEntry_Important_AddsToBatcher(t *testing.T) {
	orig := dispatch.AppendReportEntry
	dispatch.AppendReportEntry = func(root string, params json.RawMessage) (string, error) {
		return "fake/path.txt", nil
	}
	defer func() { dispatch.AppendReportEntry = orig }()

	pub := &fakePublisher{}
	batcher := &fakeBatcher{}
	d := dispatch.New(t.TempDir(), pub, batcher, nil)

	params, _ := json.Marshal(map[string]any{
		"summary":     `Disk space C:\ critical: 97% used (threshold 95%)`,
		"raw_content": `Disk space C:\ critical: 97% used (threshold 95%)`,
		"source_path": `system-monitor/disk_space/C:\`,
		"occurred_at": "2026-07-27T10:05:00-06:00",
		"important":   true,
	})
	req := common.ActionRequest{CorrelationID: "r1", ActionHint: "append_daily_report_entry", Parameters: params}
	b, _ := json.Marshal(req)
	d.Handle(b)

	items := batcher.added()
	if len(items) != 1 {
		t.Fatalf("batcher.Add called %d times, want 1", len(items))
	}
	if items[0].Kind != "report" {
		t.Errorf("Kind = %q, want report", items[0].Kind)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./action-svc/dispatch/... -run TestDispatch_AppendDailyReportEntry_Important_AddsToBatcher -v`
Expected: FAIL — `batcher.added()` returns 0 items (dispatchAppendDailyReportEntry doesn't call `batcher.Add` yet)

- [ ] **Step 3: Write minimal implementation**

In `action-svc/dispatch/dispatch.go`, replace `dispatchAppendDailyReportEntry` with:

```go
func (d *Dispatcher) dispatchAppendDailyReportEntry(req common.ActionRequest) {
	var p ReportEntryParams
	// Best-effort: if this fails, AppendReportEntry's own unmarshal below
	// fails identically and status becomes "failed"; p stays zero-value, so
	// no batcher.Add fires for an unparseable request.
	_ = json.Unmarshal(req.Parameters, &p)

	_, err := AppendReportEntry(d.root, req.Parameters)
	if err != nil {
		slog.Warn("dispatch: append_daily_report_entry failed, retrying once", "correlation_id", req.CorrelationID, "error", err)
		_, err = AppendReportEntry(d.root, req.Parameters)
	}

	status := "success"
	if err != nil {
		status = "failed"
		slog.Error("dispatch: append_daily_report_entry failed after retry, giving up", "correlation_id", req.CorrelationID, "error", err)
	}

	if p.Important && d.batcher != nil {
		d.batcher.Add(notifybatch.Item{
			Kind:        "report",
			Summary:     p.Summary,
			SourcePath:  p.SourcePath,
			BodyExcerpt: p.RawContent,
		})
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
		slog.Error("dispatch: outcome publish failed", "correlation_id", req.CorrelationID, "error", pubErr)
	}
}
```

(`notifybatch` and `encoding/json` are already imported in `dispatch.go`; no import changes needed.)

In `action-svc/dispatch/gmail_triage.go`, add the `Kind` field to the existing `batcher.Add` call:

```go
	if p.Important && d.batcher != nil {
		d.batcher.Add(notifybatch.Item{
			Kind:        "gmail",
			Sender:      p.Sender,
			Subject:     p.Subject,
			Reason:      p.Reason,
			BodyExcerpt: p.BodyExcerpt,
			ThreadID:    p.ThreadID,
		})
	}
```

In `action-svc/dispatch/gmail_triage_test.go`, extend `TestDispatch_GmailTriage_Important_AddsToBatcher`'s assertions:

```go
	if items[0].Kind != "gmail" {
		t.Errorf("Kind = %q, want gmail", items[0].Kind)
	}
	if items[0].Sender != "boss@company.com" || items[0].Subject != "Server down" {
		t.Errorf("batched item = %+v, want sender/subject to match", items[0])
	}
```

(replacing the existing two-line `if items[0].Sender != ... || items[0].Subject != ...` assertion block with this three-line version that adds the `Kind` check first.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./action-svc/dispatch/... -v`
Expected: PASS (all tests, including every pre-existing `dispatch`/`gmail_triage` test)

- [ ] **Step 5: Commit**

```bash
git add action-svc/dispatch/dispatch.go action-svc/dispatch/gmail_triage.go action-svc/dispatch/dispatch_test.go action-svc/dispatch/gmail_triage_test.go
git commit -m "action-svc/dispatch: notify Discord in real time for any important report entry, not just Gmail"
```

Then add the negative-case test (same task, before moving to docs):

```go
func TestDispatch_AppendDailyReportEntry_NotImportant_SkipsBatcher(t *testing.T) {
	orig := dispatch.AppendReportEntry
	dispatch.AppendReportEntry = func(root string, params json.RawMessage) (string, error) {
		return "fake/path.txt", nil
	}
	defer func() { dispatch.AppendReportEntry = orig }()

	pub := &fakePublisher{}
	batcher := &fakeBatcher{}
	d := dispatch.New(t.TempDir(), pub, batcher, nil)

	params, _ := json.Marshal(map[string]any{
		"summary":     "routine note",
		"raw_content": "",
		"source_path": `C:\errors\file.txt`,
		"occurred_at": "2026-07-27T10:05:00-06:00",
		"important":   false,
	})
	req := common.ActionRequest{CorrelationID: "r2", ActionHint: "append_daily_report_entry", Parameters: params}
	b, _ := json.Marshal(req)
	d.Handle(b)

	if items := batcher.added(); len(items) != 0 {
		t.Errorf("batcher.Add called %d times, want 0 for a not-important entry", len(items))
	}
}
```

Run: `go test ./action-svc/dispatch/... -v`
Expected: PASS

```bash
git add action-svc/dispatch/dispatch_test.go
git commit -m "action-svc/dispatch: cover not-important report entries skip the batcher"
```

Then update docs (same task):

In `action-svc/NOTES.md`, add a new section (after "Important/not-important report split (added 2026-07-20)"):

```markdown
## Real-time Discord notification now applies to every important report entry (added 2026-07-27)

Previously, `Important: true` on an `append_daily_report_entry` action only changed which section of the daily report file the entry landed in — only `triage_gmail_email` triggered a real-time Discord notification. `system-monitor`'s critical/recovery alerts and any `folder-watcher` (`ErrorReportRule` always sets `Important: true`) or `log-error` entry now also queue on the same `notifybatch.Batcher` used by Gmail triage. This is an intentional behavior change flagged in `docs/superpowers/specs/2026-07-27-log-error-perception-design.md` — it closes the gap `2026-07-18-system-monitor-channel-design.md` explicitly called out of scope ("an immediate Discord ping on critical, the way Gmail triage does").

`notifybatch.Item` gained a `Kind` field (`"gmail"` | `"report"`) so `formatBatch` can render each shape correctly in a single flushed message, including a batch that mixes both kinds. `feign_mode` (already `true` in both `config/dev.json` and `config/prod.json`) governs these sends exactly like it already governs Gmail's — no separate gate was needed.
```

In `CLAUDE.md`, update the action-svc bullet (item 4 under "Services") from:

```markdown
4. **`action-svc`** — dispatches `soulman.thinking.request` actions via a durable JetStream consumer: `append_daily_report_entry` (writes to `$SOULMAN_ROOT/reports/`) and `triage_gmail_email` (report entry + debounced batched Discord notify if important). Independently runs a 10:00 AM cron sending the previous day's report via a pluggable `Notifier` (Discord). `DISCORD_BOT_TOKEN`/`DISCORD_CHANNEL_ID` are non-fatal if blank (Send fails, retried/logged like any other notifier failure) — configured in dev and prod as of 2026-07-18 (a dedicated "Soulman Reports" bot). As of 2026-07-19, `feign_mode` is `true` in both `config/dev.json` and `config/prod.json`, so outbound sends are currently recorded to `logs/feigned-actions.jsonl` instead of actually happening — see `action-svc/NOTES.md`.
   - Specs: `2026-07-17-action-svc-design.md`, `2026-07-17-daily-report-delivery-design.md`, `2026-07-17-error-report-action-design.md`, `2026-07-18-gmail-triage-action-design.md`, `2026-07-18-pipeline-debugging-tools-design.md`, `2026-07-19-action-svc-feign-mode-design.md`, `2026-07-20-daily-report-importance-split-design.md`
   - Notes: `action-svc/NOTES.md` — the incident that motivated durable queues, the notification-batching design, a known deferred bug (dev/prod share one Discord bot), feign mode
```

to:

```markdown
4. **`action-svc`** — dispatches `soulman.thinking.request` actions via a durable JetStream consumer: `append_daily_report_entry` (writes to `$SOULMAN_ROOT/reports/`, and — as of 2026-07-27 — batches a real-time Discord notify for any entry marked important, not just Gmail's) and `triage_gmail_email` (report entry + debounced batched Discord notify if important). Independently runs a 10:00 AM cron sending the previous day's report via a pluggable `Notifier` (Discord). `DISCORD_BOT_TOKEN`/`DISCORD_CHANNEL_ID` are non-fatal if blank (Send fails, retried/logged like any other notifier failure) — configured in dev and prod as of 2026-07-18 (a dedicated "Soulman Reports" bot). As of 2026-07-19, `feign_mode` is `true` in both `config/dev.json` and `config/prod.json`, so outbound sends are currently recorded to `logs/feigned-actions.jsonl` instead of actually happening — see `action-svc/NOTES.md`.
   - Specs: `2026-07-17-action-svc-design.md`, `2026-07-17-daily-report-delivery-design.md`, `2026-07-17-error-report-action-design.md`, `2026-07-18-gmail-triage-action-design.md`, `2026-07-18-pipeline-debugging-tools-design.md`, `2026-07-19-action-svc-feign-mode-design.md`, `2026-07-20-daily-report-importance-split-design.md`, `2026-07-27-log-error-perception-design.md`
   - Notes: `action-svc/NOTES.md` — the incident that motivated durable queues, the notification-batching design, a known deferred bug (dev/prod share one Discord bot), feign mode, the generalization beyond Gmail-only real-time notifications
```

```bash
git add action-svc/NOTES.md CLAUDE.md
git commit -m "action-svc/docs: document the real-time-notification generalization"
```

---

## Self-Review

**1. Spec coverage** — every section of `docs/superpowers/specs/2026-07-27-log-error-perception-design.md` maps to a task:
- Package `perception-svc/logmonitor` (Watcher, Publisher interface, file discovery, detection) → Tasks 1-4, 6
- Tailing and checkpoint (offset persistence, first-run-at-EOF, truncation) → Task 3
- Line parsing (slog default format, ERROR-only) → Task 1
- Dedup (in-memory, per-process, not persisted) → Task 2
- Stimulus construction table → Task 4 (`buildStimulus`)
- Config (`LogMonitorConfig`, fatal-fast validation) → Task 5
- Thinking Rule (`LogErrorRule`, always important, registration) → Task 7
- `notifybatch` generalization (`Item`/`formatBatch`) → Task 8
- Dispatch wiring (`dispatchAppendDailyReportEntry` batches, `feign_mode` unchanged) → Task 9
- Error Handling table → covered across Tasks 3-4 (unreadable file skipped-and-logged, truncation reset, malformed line silently skipped, publish failure leaves dedup unmarked, corrupt checkpoint starts empty) and Task 9 (existing retry-once-then-give-up and batcher failure handling untouched)
- Testing section's five `logmonitor_test.go` assertions → all five present in Task 4's test list; `log_error_test.go`'s always-important assertion → Task 7; `batcher_test.go`'s `Kind: "report"` and mixed-batch assertions → Task 8; `dispatch_test.go`'s `batcher.Add` important/not-important assertions → Task 9
- Out of Scope items (WARN monitoring, recovery notification, cross-env correlation, persisted dedup, log rotation) → deliberately not implemented anywhere in this plan; no task contradicts them

No gaps found.

**2. Placeholder scan** — every code step above contains complete, real Go (or JSON/Markdown) content; no "TBD"/"similar to Task N"/"add appropriate error handling" text appears in any step. The two full-file rewrites (`config_test.go`, `batcher_test.go`) were written out completely rather than described, since both files have enough existing call sites that a partial diff would have been ambiguous about which lines to touch.

**3. Type/signature consistency** — traced across tasks:
- `parseLine(line string) (ParsedLine, bool)` (Task 1) matches its one call site in Task 4's `handleLine`.
- `newDedupState()`, `(*dedupState).seenBefore(service, msg string) bool`, `(*dedupState).markSeen(service, msg string)` (Task 2) match Task 4's `Watcher.dedup` usage exactly.
- `loadCheckpoint(path string) *checkpoint`, `(*checkpoint).resolveStart(filename string, currentSize int64) int64`, `(*checkpoint).mark(filename string, offset int64) error` (Task 3) match Task 4's `Watcher.checkpoint` usage exactly.
- `logmonitor.New(logDir string, publisher Publisher, checkpointPath string, reconcileInterval time.Duration) (*Watcher, error)` (Task 4) matches Task 6's `main.go` call exactly, including argument order.
- `config.Config.LogDir`, `LogMonitorCheckpointPath`, `LogMonitorReconcileIntervalSeconds` (Task 5) match the three field names Task 6 reads off `cfg` exactly.
- `errorReportParams` (pre-existing, `error_report.go`) is reused unmodified by Task 7 — no shadow/rename.
- `notifybatch.Item{Kind, Sender, Subject, ThreadID, Summary, SourcePath, Reason, BodyExcerpt}` (Task 8) matches every field Task 9 sets in both `dispatch.go` (`Kind`, `Summary`, `SourcePath`, `BodyExcerpt`) and `gmail_triage.go` (`Kind`, `Sender`, `Subject`, `Reason`, `BodyExcerpt`, `ThreadID`).
- `ReportEntryParams` (pre-existing, `report_entry.go`) is reused unmodified by Task 9 — no shadow/rename.

No inconsistencies found.

**Judgment calls made (flagging for reviewer attention):**

1. **`LOG_DIR` deployment gap.** The spec states `$LOG_DIR` is "already an environment variable every service (including perception-svc) receives from its run-`<svc>`.ps1 launcher." Checked the live scripts (`C:\Users\Lenovo\soulman-dev\run-perception-svc.ps1` and the `soulman-prod` equivalent) while researching this plan: that's only true for `memory-svc`. `perception-svc`'s own launcher sets `HTTP_PORT`/`CHECKPOINT_PATH`/`CONFIG_PATH` but never `LOG_DIR`. Since those launcher scripts live outside this git repo (`~/soulman-dev/`, `~/soulman-prod/` are runtime environments only, per root `CLAUDE.md`), this plan cannot edit them. Resolved by defaulting `LOG_DIR` via the same `env(key, def)` fallback pattern every other var in `perception-svc/config/config.go` already uses (`env("LOG_DIR", "./logs")`), and explicitly documenting in Task 6's `perception-svc/NOTES.md` addition that a one-line manual edit (mirroring `memory-svc`'s existing `$env:LOG_DIR` line) is needed in both environments' launcher scripts before this channel finds real sibling logs. Flagging this explicitly because it's the one piece of this feature that cannot be verified working end-to-end by `go test`/`go build` alone — it needs that manual out-of-repo step plus a real restart to confirm.
2. **`logmonitor-checkpoint.json`'s path is derived, not independently configured.** Rather than introduce a second env var (`LOG_MONITOR_CHECKPOINT_PATH` or similar) that would have the same out-of-repo-launcher-script problem as `LOG_DIR`, Task 5 derives it from `CHECKPOINT_PATH`'s own directory (`filepath.Join(filepath.Dir(checkpointPath), "logmonitor-checkpoint.json")`) — `CHECKPOINT_PATH` is already correctly set per-environment by the existing launchers, so this needs no new deployment step at all.
3. **`formatBatch`'s count header text changed** from `"N important email(s):"` to `"N important item(s):"`. The spec says formatBatch should "preserve the existing Gmail format exactly" — read as applying to the per-item Gmail block (which Task 8 does preserve byte-for-byte), not the outer count header, since a header claiming "email(s)" would be factually wrong once a batch can legitimately contain zero emails (an all-`"report"` flush, e.g. a lone `system-monitor` critical alert). This is a visible behavior change to existing Gmail notifications' header line — flagging in case the reviewer wants the old wording kept for gmail-only batches specifically.
4. **`dispatchAppendDailyReportEntry` ignores its own `json.Unmarshal` error** for the new `ReportEntryParams` read (Task 9), rather than early-returning on parse failure the way `dispatchGmailTriage` does for its own params. This was deliberate: `AppendReportEntry`'s internal unmarshal (unchanged, still the sole source of truth for the "failed" outcome status) fails identically on the same malformed input, so an early return here would only change *whether a retry is attempted*, not the final status — and the spec didn't ask for that behavior change. Net effect: a malformed request still gets the existing retry-then-fail treatment; it just also silently skips the batcher (zero-value `Important: false`), which is the conservative choice.
5. **Doc updates folded into their nearest functional task** (Tasks 6, 7, 9) rather than a dedicated final task, per the plan's own Task Right-Sizing guidance — each service's `NOTES.md`/`CLAUDE.md` bullet is updated in the same task that makes the corresponding behavior real, so a reviewer approving e.g. Task 7 sees the rule *and* its doc footprint together.
