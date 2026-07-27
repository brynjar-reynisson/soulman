# Discord Do-Not-Disturb Window Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a configurable do-not-disturb window that suppresses `action-svc`'s real-time Discord notifications overnight by accumulating them to a pending file and flushing as one message when the window ends, as the prerequisite for turning `feign_mode` off in prod.

**Architecture:** A new `action-svc/dnd` package mirrors `action-svc/feign`'s `Gate`-wrapping-`Notifier` shape for `WrapNotifier`, and reuses `action-svc/scheduler`'s wake-loop mechanics (`nextRun`/`time.Until`/`time.After`, a `Now`-field test-injection seam) for a background `Flusher`. `common/sharedconfig` and `action-svc/config` gain a `DoNotDisturb`/`DND*` block with the same loose (non-fatal) time-parsing convention `ReportSendTime` already uses. `main.go` wires `notifybatch.Batcher` through the DND-wrapped notifier while the daily digest cron (`scheduler.Scheduler`) keeps using the plain feign-wrapped one, unaffected.

**Tech Stack:** Go 1.25, stdlib `time`/`os`/`strings`/`strconv`, the existing `notify.Notifier` interface (no new dependency).

## Global Constraints

- Only `notifybatch.Batcher`'s real-time notification path is gated; the once-daily digest cron (`scheduler.Scheduler`, default `10:00`) is untouched.
- `Window.Active(t time.Time) bool` reports whether `t`'s local wall-clock time falls inside `[Start, End)`, handling both the non-wrapping case (`Start < End`) and the midnight-wrapping case (`Start > End`).
- Malformed `start`/`end` config is not fatal — silent fallback to `10:00`, matching `scheduler.parseSendTime`'s existing loose convention, not the fatal-fast pattern `system_monitor`/`log_monitor` use.
- Pending-file append failure fails open: sends for real immediately instead of silently losing the notification, logged at `slog.Warn`.
- Flush-time send failure is retried up to 3x (same backoff as `Scheduler.sendWithRetry`); the pending file is cleared regardless of outcome — no cross-day retry chain, avoiding unbounded growth if Discord stays down for days.
- On `Start()`, if currently outside the window and the pending file is non-empty, flush immediately — covers a restart after window-end already passed but before today's loop iteration ran.
- The flush send passes through the same notifier chain the `Batcher` uses (still feign-wrapped), so `feign_mode` continues to govern real-vs-recorded — DND is fully exercisable in dev before prod's `feign_mode` ever flips.
- `do_not_disturb.enabled` lets the window be turned off without discarding configured times, matching `feign_mode`'s explicit-boolean convention rather than an implicit "empty times = disabled" signal.
- Both `config/dev.json` and `config/prod.json` get `do_not_disturb: { "enabled": true, "start": "00:00", "end": "10:00" }` — dev too, since dev's sends are already feigned regardless.
- Out of scope this iteration: flipping `feign_mode` to `false` in prod; a manual/admin "flush now" endpoint; per-severity DND overrides; gating the daily digest cron; any change to `notifybatch.Batcher` itself (it stays unaware DND exists).

---

## File Structure

| Path | Action | Responsibility |
|---|---|---|
| `action-svc/dnd/window.go` | Create | `Window` struct + `Active(t time.Time) bool`; `parseTime` (HH:MM parsing, loose fallback) |
| `action-svc/dnd/window_test.go` | Create | Tests for `Window.Active` (non-wrapping, wrapping, boundaries) |
| `action-svc/dnd/notifier.go` | Create | `dndNotifier`/`WrapNotifier` — the `Send` gating logic (append during window, fail-open, delegate outside window) |
| `action-svc/dnd/notifier_test.go` | Create | Tests for `WrapNotifier`'s `Send` |
| `action-svc/dnd/flusher.go` | Create | `Flusher` — wake-at-window-end loop, read/format/send/clear, retry, catch-up-on-Start |
| `action-svc/dnd/flusher_test.go` | Create | Tests for `Flusher` |
| `common/sharedconfig/config.go` | Modify | Add `DNDConfig` type and `Config.DoNotDisturb` field |
| `action-svc/config/config.go` | Modify | Add `DNDEnabled`/`DNDStart`/`DNDEnd`, sourced from `shared.DoNotDisturb`, no additional validation |
| `action-svc/config/config_test.go` | Modify | Extend for the new `do_not_disturb` block, including the absent-defaults and malformed-time-passthrough cases |
| `config/dev.json` | Modify | Add `do_not_disturb` block |
| `config/prod.json` | Modify | Add `do_not_disturb` block |
| `action-svc/main.go` | Modify | Construct `batcherNotifier`/`dndFlusher` per the spec's Wiring section; `sched` stays on the plain notifier |
| `action-svc/NOTES.md` | Modify | Document the DND window |
| `CLAUDE.md` | Modify | Update action-svc's Services bullet |

---

### Task 1: `action-svc/dnd` Window

**Files:**
- Create: `action-svc/dnd/window.go`
- Test: `action-svc/dnd/window_test.go`

**Interfaces:**
- Consumes: nothing (first task)
- Produces: `type Window struct { Start, End string }`, `func (w Window) Active(t time.Time) bool`, `func parseTime(s string) (int, int)` (unexported) — used by Task 3's `Flusher`

- [ ] **Step 1: Write the failing test**

```go
package dnd

import (
	"testing"
	"time"
)

func TestWindow_Active_NonWrapping_InsideWindow(t *testing.T) {
	w := Window{Start: "00:00", End: "10:00"}
	got := w.Active(time.Date(2026, 7, 27, 5, 0, 0, 0, time.Local))
	if !got {
		t.Error("Active(05:00) = false, want true for a 00:00-10:00 window")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./action-svc/dnd/... -run TestWindow_Active_NonWrapping_InsideWindow -v`
Expected: FAIL — `Window`/`Active` are undefined (the package doesn't exist yet, build fails)

- [ ] **Step 3: Write minimal implementation**

```go
// Package dnd implements action-svc's do-not-disturb window: a configurable
// time-of-day range during which real-time Discord notifications are
// accumulated to a pending file instead of sent immediately, flushed as one
// message once the window ends. Mirrors action-svc/feign's Gate-wrapping-
// Notifier shape and reuses action-svc/scheduler's wake-loop mechanics. See
// docs/superpowers/specs/2026-07-27-discord-do-not-disturb-design.md.
package dnd

import (
	"strconv"
	"strings"
	"time"
)

// Window is a do-not-disturb time-of-day range, local time, "HH:MM"
// boundaries.
type Window struct {
	Start string // "HH:MM", local time
	End   string // "HH:MM", local time
}

// Active reports whether t's local wall-clock time falls inside
// [Start, End). Handles both the non-wrapping case (Start < End, e.g.
// 00:00-10:00) and the midnight-wrapping case (Start > End, e.g.
// 22:00-06:00) — the window is configurable, and wrapping is a realistic
// real-world DND configuration even though it's not today's default. A
// zero-width window (Start == End) is never active — there's no duration
// to suppress.
func (w Window) Active(t time.Time) bool {
	startH, startM := parseTime(w.Start)
	endH, endM := parseTime(w.End)

	startMinutes := startH*60 + startM
	endMinutes := endH*60 + endM
	nowMinutes := t.Hour()*60 + t.Minute()

	if startMinutes == endMinutes {
		return false
	}
	if startMinutes < endMinutes {
		return nowMinutes >= startMinutes && nowMinutes < endMinutes
	}
	return nowMinutes >= startMinutes || nowMinutes < endMinutes
}

// parseTime parses "HH:MM" the same way scheduler.parseSendTime does:
// silent fallback to 10:00 on any malformed input, not fatal — matches
// action-svc/config's loose treatment of do_not_disturb.start/.end (no
// validation at the config-loading layer; this is where the fallback
// actually happens).
func parseTime(s string) (int, int) {
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./action-svc/dnd/... -run TestWindow_Active_NonWrapping_InsideWindow -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add action-svc/dnd/window.go action-svc/dnd/window_test.go
git commit -m "action-svc/dnd: add Window with non-wrapping and midnight-wrapping Active logic"
```

Then add the remaining boundary and malformed-input tests (same task, before moving to Task 2):

```go
func TestWindow_Active_NonWrapping(t *testing.T) {
	w := Window{Start: "00:00", End: "10:00"}
	cases := []struct {
		name string
		t    time.Time
		want bool
	}{
		{"before start", time.Date(2026, 7, 27, 23, 59, 0, 0, time.Local), false},
		{"at start", time.Date(2026, 7, 27, 0, 0, 0, 0, time.Local), true},
		{"inside", time.Date(2026, 7, 27, 5, 0, 0, 0, time.Local), true},
		{"at end", time.Date(2026, 7, 27, 10, 0, 0, 0, time.Local), false},
		{"after end", time.Date(2026, 7, 27, 15, 0, 0, 0, time.Local), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := w.Active(tc.t); got != tc.want {
				t.Errorf("Active(%v) = %v, want %v", tc.t, got, tc.want)
			}
		})
	}
}

func TestWindow_Active_Wrapping(t *testing.T) {
	w := Window{Start: "22:00", End: "06:00"}
	cases := []struct {
		name string
		t    time.Time
		want bool
	}{
		{"before start", time.Date(2026, 7, 27, 21, 0, 0, 0, time.Local), false},
		{"at start", time.Date(2026, 7, 27, 22, 0, 0, 0, time.Local), true},
		{"inside (late night)", time.Date(2026, 7, 27, 23, 30, 0, 0, time.Local), true},
		{"inside (early morning)", time.Date(2026, 7, 27, 3, 0, 0, 0, time.Local), true},
		{"at end", time.Date(2026, 7, 27, 6, 0, 0, 0, time.Local), false},
		{"after end", time.Date(2026, 7, 27, 12, 0, 0, 0, time.Local), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := w.Active(tc.t); got != tc.want {
				t.Errorf("Active(%v) = %v, want %v", tc.t, got, tc.want)
			}
		})
	}
}

func TestWindow_Active_MalformedTimes_FallBackToTenAM(t *testing.T) {
	// Both Start and End fall back to 10:00 on malformed input, producing a
	// zero-width window (never active) — exercises parseTime's fallback
	// without needing to export it.
	w := Window{Start: "garbage", End: "also-garbage"}
	if w.Active(time.Date(2026, 7, 27, 10, 0, 0, 0, time.Local)) {
		t.Error("Active(10:00) = true, want false: both malformed times fall back to the same 10:00, a zero-width window")
	}

	w2 := Window{Start: "garbage", End: "12:00"}
	if !w2.Active(time.Date(2026, 7, 27, 11, 0, 0, 0, time.Local)) {
		t.Error("Active(11:00) = false, want true: malformed Start falls back to 10:00, so 10:00-12:00 should be active at 11:00")
	}
}
```

Run: `go test ./action-svc/dnd/... -run TestWindow_Active -v`
Expected: PASS (all `Window.Active` tests)

```bash
git add action-svc/dnd/window_test.go
git commit -m "action-svc/dnd: cover Window.Active boundary and malformed-input cases"
```

---

### Task 2: `action-svc/dnd` WrapNotifier

**Files:**
- Create: `action-svc/dnd/notifier.go`
- Test: `action-svc/dnd/notifier_test.go`

**Interfaces:**
- Consumes: `Window.Active(t time.Time) bool` (Task 1)
- Produces: `func WrapNotifier(window Window, pendingFilePath string, real notify.Notifier) notify.Notifier`, `func newNotifier(window Window, path string, real notify.Notifier, now func() time.Time) *dndNotifier` (test seam), `const entrySeparator = "\n\n---\n\n"` — used by Task 3's `Flusher` and Task 5's `main.go`

- [ ] **Step 1: Write the failing test**

```go
package dnd

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeNotifier struct {
	sent []string
}

func (f *fakeNotifier) Send(message string) error {
	f.sent = append(f.sent, message)
	return nil
}

func fixedInsideWindow() time.Time {
	return time.Date(2026, 7, 27, 5, 0, 0, 0, time.Local) // 05:00, inside 00:00-10:00
}

func fixedOutsideWindow() time.Time {
	return time.Date(2026, 7, 27, 14, 0, 0, 0, time.Local) // 14:00, outside 00:00-10:00
}

func TestNotifier_InsideWindow_AppendsToFile_DoesNotCallReal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dnd-pending.txt")
	real := &fakeNotifier{}
	w := Window{Start: "00:00", End: "10:00"}
	n := newNotifier(w, path, real, fixedInsideWindow)

	if err := n.Send("hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if len(real.sent) != 0 {
		t.Errorf("real.Send called %d times, want 0 (inside window)", len(real.sent))
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(b) != "hello" {
		t.Errorf("pending file content = %q, want %q", string(b), "hello")
	}
}
```

(`fixedOutsideWindow` is unused by this single test but is added now since every test in this file needs one or the other — Go will flag it as an unused declaration only if nothing in the package ever calls it; the remaining tests added after Step 5 use it, so leave it in place.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./action-svc/dnd/... -run TestNotifier_InsideWindow_AppendsToFile_DoesNotCallReal -v`
Expected: FAIL — `newNotifier` is undefined

- [ ] **Step 3: Write minimal implementation**

```go
package dnd

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"soulman/action-svc/notify"
)

// entrySeparator delimits distinct Send-level entries accumulated in the
// pending file — deliberately not a bare "\n\n", since a single Send call's
// message (e.g. a multi-item notifybatch.formatBatch flush) can itself
// already contain internal "\n\n" separators between its own blocks; using
// a distinct separator here lets Flusher.FlushIfPending count entries
// accurately by splitting on it.
const entrySeparator = "\n\n---\n\n"

// dndNotifier wraps a real notify.Notifier so Send appends to a pending
// file instead of sending while window is active, and delegates straight
// through otherwise. Mirrors feign.gatedNotifier's shape exactly.
type dndNotifier struct {
	window Window
	path   string
	real   notify.Notifier
	now    func() time.Time
	mu     sync.Mutex
}

// WrapNotifier returns a notify.Notifier that appends to pendingFilePath
// instead of sending while window is active (local time), and delegates to
// real otherwise. The wrapped Notifier is indistinguishable from a real one
// at the call site — notifybatch.Batcher needs no code changes to benefit
// from this, the same property feign.WrapNotifier already has.
func WrapNotifier(window Window, pendingFilePath string, real notify.Notifier) notify.Notifier {
	return newNotifier(window, pendingFilePath, real, time.Now)
}

// newNotifier is the test seam: WrapNotifier calls it with the real clock;
// tests call it directly with an injectable now so Send's window-active
// decision is deterministic instead of depending on when the test happens
// to run — mirrors perception-svc/sysmonitor's New/newWatcher split.
func newNotifier(window Window, path string, real notify.Notifier, now func() time.Time) *dndNotifier {
	return &dndNotifier{window: window, path: path, real: real, now: now}
}

func (n *dndNotifier) Send(message string) error {
	if !n.window.Active(n.now()) {
		return n.real.Send(message)
	}

	if err := n.append(message); err != nil {
		slog.Warn("dnd: pending file append failed, sending immediately instead", "path", n.path, "error", err)
		return n.real.Send(message)
	}
	return nil
}

// append writes message to the pending file, separating it from any
// already-accumulated content with entrySeparator.
func (n *dndNotifier) append(message string) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(n.path), 0o755); err != nil {
		return fmt.Errorf("dnd: mkdir for %s: %w", n.path, err)
	}

	info, statErr := os.Stat(n.path)
	sep := ""
	if statErr == nil && info.Size() > 0 {
		sep = entrySeparator
	}

	f, err := os.OpenFile(n.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("dnd: open %s: %w", n.path, err)
	}
	defer f.Close()

	if _, err := f.WriteString(sep + message); err != nil {
		return fmt.Errorf("dnd: write to %s: %w", n.path, err)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./action-svc/dnd/... -run TestNotifier_InsideWindow_AppendsToFile_DoesNotCallReal -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add action-svc/dnd/notifier.go action-svc/dnd/notifier_test.go
git commit -m "action-svc/dnd: add WrapNotifier, append-during-window gating"
```

Then add the remaining notifier tests (same task, before moving to Task 3):

```go
func TestNotifier_InsideWindow_MultipleAppends_Separated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dnd-pending.txt")
	real := &fakeNotifier{}
	w := Window{Start: "00:00", End: "10:00"}
	n := newNotifier(w, path, real, fixedInsideWindow)

	if err := n.Send("first"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := n.Send("second"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	want := "first" + entrySeparator + "second"
	if string(b) != want {
		t.Errorf("pending file content = %q, want %q", string(b), want)
	}
}

func TestNotifier_OutsideWindow_DelegatesStraightThrough(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dnd-pending.txt")
	real := &fakeNotifier{}
	w := Window{Start: "00:00", End: "10:00"}
	n := newNotifier(w, path, real, fixedOutsideWindow)

	if err := n.Send("hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if len(real.sent) != 1 || real.sent[0] != "hello" {
		t.Errorf("real.sent = %v, want [hello]", real.sent)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("pending file should not be created when outside the window")
	}
}

func TestNotifier_AppendFails_FailsOpenToReal(t *testing.T) {
	// A path whose parent "directory" is actually a plain file forces
	// os.MkdirAll to fail deterministically.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	path := filepath.Join(blocker, "dnd-pending.txt")

	real := &fakeNotifier{}
	w := Window{Start: "00:00", End: "10:00"}
	n := newNotifier(w, path, real, fixedInsideWindow)

	if err := n.Send("hello"); err != nil {
		t.Fatalf("Send: %v (should fail open, not return an error)", err)
	}

	if len(real.sent) != 1 || real.sent[0] != "hello" {
		t.Errorf("real.sent = %v, want [hello] (fail-open to real notifier)", real.sent)
	}
}
```

Run: `go test ./action-svc/dnd/... -run TestNotifier -v`
Expected: PASS (all 4 notifier tests)

```bash
git add action-svc/dnd/notifier_test.go
git commit -m "action-svc/dnd: cover multi-append separation, outside-window passthrough, fail-open"
```

---

### Task 3: `action-svc/dnd` Flusher

**Files:**
- Create: `action-svc/dnd/flusher.go`
- Test: `action-svc/dnd/flusher_test.go`

**Interfaces:**
- Consumes: `Window.Active(t time.Time) bool`, `parseTime(s string) (int, int)` (Task 1); `entrySeparator` (Task 2, for counting/joining pending entries)
- Produces: `type Flusher struct{...}` with exported `Now func() time.Time` / `BackoffBase time.Duration` fields, `func NewFlusher(window Window, pendingFilePath string, notifier notify.Notifier) *Flusher`, `func (f *Flusher) Start()`, `func (f *Flusher) Stop()`, `func (f *Flusher) FlushIfPending()` — used by Task 5's `main.go`

- [ ] **Step 1: Write the failing test**

```go
package dnd

import (
	"os"
	"path/filepath"
	"testing"
)

type fakeFlushNotifier struct {
	sent []string
}

func (f *fakeFlushNotifier) Send(message string) error {
	f.sent = append(f.sent, message)
	return nil
}

func TestFlushIfPending_EmptyFile_NoSend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dnd-pending.txt")
	notifier := &fakeFlushNotifier{}
	fl := NewFlusher(Window{Start: "00:00", End: "10:00"}, path, notifier)

	fl.FlushIfPending()

	if len(notifier.sent) != 0 {
		t.Errorf("Send called %d times, want 0 for a missing pending file", len(notifier.sent))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./action-svc/dnd/... -run TestFlushIfPending_EmptyFile_NoSend -v`
Expected: FAIL — `Flusher`/`NewFlusher` are undefined

- [ ] **Step 3: Write minimal implementation**

```go
package dnd

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"soulman/action-svc/notify"
)

// Flusher wakes at the do-not-disturb window's end time each day, sends
// the pending file's accumulated content as one message through notifier,
// and clears the file. Mirrors scheduler.Scheduler's wake-loop shape
// (nextRun/time.Until/time.After) applied to a window's End time instead
// of a single daily send time.
type Flusher struct {
	window   Window
	path     string
	notifier notify.Notifier
	stop     chan struct{}

	// Overridable for tests: Now controls "current time" (avoids waiting for
	// a real clock), BackoffBase controls the retry delay (avoids a slow
	// test) — same pattern as scheduler.Scheduler.
	Now         func() time.Time
	BackoffBase time.Duration
}

// NewFlusher builds a Flusher. notifier is the same notifier chain the
// Batcher sends through (still feign-wrapped) — DND only changes *when*
// pending content is sent, not how feign_mode governs the send itself.
func NewFlusher(window Window, pendingFilePath string, notifier notify.Notifier) *Flusher {
	return &Flusher{
		window:      window,
		path:        pendingFilePath,
		notifier:    notifier,
		stop:        make(chan struct{}),
		Now:         time.Now,
		BackoffBase: 1 * time.Second,
	}
}

// Start performs the catch-up check (flush immediately if currently
// outside the window and there's stale pending content — see
// flushIfOutsideWindow), then launches the wake-at-window-end loop in a
// background goroutine.
func (f *Flusher) Start() {
	f.flushIfOutsideWindow()
	go f.loop()
}

func (f *Flusher) Stop() {
	close(f.stop)
}

// flushIfOutsideWindow is Start's synchronous catch-up check, factored out
// so tests can exercise it directly without also spawning the background
// wake-loop goroutine (which would otherwise race against a test's
// assertions, since its wait duration is computed from the real clock via
// time.Until while Now may be a fixed test value).
func (f *Flusher) flushIfOutsideWindow() {
	if !f.window.Active(f.Now()) {
		f.FlushIfPending()
	}
}

func (f *Flusher) loop() {
	for {
		wait := time.Until(f.nextWindowEnd(f.Now()))
		select {
		case <-time.After(wait):
			f.FlushIfPending()
		case <-f.stop:
			return
		}
	}
}

// nextWindowEnd computes the next occurrence of window.End the same way
// Scheduler.nextRun computes the next send time.
func (f *Flusher) nextWindowEnd(from time.Time) time.Time {
	hh, mm := parseTime(f.window.End)
	next := time.Date(from.Year(), from.Month(), from.Day(), hh, mm, 0, 0, from.Location())
	if !next.After(from) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// FlushIfPending reads the pending file; if empty or missing, does
// nothing. If non-empty, formats and sends the accumulated content
// (retried up to 3 times), then clears the file regardless of whether the
// send ultimately succeeded — no cross-day retry chain.
func (f *Flusher) FlushIfPending() {
	content, err := readPending(f.path)
	if err != nil {
		slog.Error("dnd: read pending file failed", "path", f.path, "error", err)
		return
	}
	if strings.TrimSpace(content) == "" {
		return
	}

	entries := strings.Split(content, entrySeparator)
	message := fmt.Sprintf("%d notification(s) from overnight:\n\n%s", len(entries), content)

	if err := f.sendWithRetry(message); err != nil {
		slog.Error("dnd: flush send failed after 3 attempts", "error", err)
	}

	if err := os.WriteFile(f.path, nil, 0o644); err != nil {
		slog.Error("dnd: clear pending file failed", "path", f.path, "error", err)
	}
}

func (f *Flusher) sendWithRetry(message string) error {
	var err error
	backoff := f.BackoffBase
	for attempt := 1; attempt <= 3; attempt++ {
		err = f.notifier.Send(message)
		if err == nil {
			return nil
		}
		slog.Warn("dnd: flush send attempt failed", "attempt", attempt, "max_attempts", 3, "error", err)
		if attempt < 3 {
			time.Sleep(backoff)
			backoff *= 2
		}
	}
	return err
}

// readPending returns the pending file's content, or "" if it doesn't
// exist yet (never written to during this window).
func readPending(path string) (string, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("dnd: read %s: %w", path, err)
	}
	return string(b), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./action-svc/dnd/... -run TestFlushIfPending_EmptyFile_NoSend -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add action-svc/dnd/flusher.go action-svc/dnd/flusher_test.go
git commit -m "action-svc/dnd: add Flusher wake-loop, read-format-send-clear"
```

Then add the remaining flusher tests (same task, before moving to Task 4):

```go
func TestFlushIfPending_NonEmptyFile_SendsFormattedContentAndClears(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dnd-pending.txt")
	if err := os.WriteFile(path, []byte("first"+entrySeparator+"second"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	notifier := &fakeFlushNotifier{}
	fl := NewFlusher(Window{Start: "00:00", End: "10:00"}, path, notifier)

	fl.FlushIfPending()

	if len(notifier.sent) != 1 {
		t.Fatalf("Send called %d times, want 1", len(notifier.sent))
	}
	msg := notifier.sent[0]
	if !strings.Contains(msg, "2 notification(s) from overnight:") {
		t.Errorf("message = %q, want a 2-notification header", msg)
	}
	if !strings.Contains(msg, "first") || !strings.Contains(msg, "second") {
		t.Errorf("message = %q, want both entries present", msg)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after flush: %v", err)
	}
	if len(b) != 0 {
		t.Errorf("pending file not cleared, content = %q", string(b))
	}
}

func TestFlushIfPending_SendFailsAllThreeAttempts_StillClearsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dnd-pending.txt")
	if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	notifier := &failNTimesNotifier{failN: 3}
	fl := NewFlusher(Window{Start: "00:00", End: "10:00"}, path, notifier)
	fl.BackoffBase = time.Millisecond

	fl.FlushIfPending()

	if notifier.calls != 3 {
		t.Errorf("Send called %d times, want 3 (retried, then given up)", notifier.calls)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after flush: %v", err)
	}
	if len(b) != 0 {
		t.Errorf("pending file should still be cleared after exhausted retries, content = %q", string(b))
	}
}

func TestFlushIfOutsideWindow_OutsideWindowWithPendingContent_FlushesImmediately(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dnd-pending.txt")
	if err := os.WriteFile(path, []byte("stale content"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	notifier := &fakeFlushNotifier{}
	fl := NewFlusher(Window{Start: "00:00", End: "10:00"}, path, notifier)
	fl.Now = fixedOutsideWindow // 14:00, outside 00:00-10:00

	fl.flushIfOutsideWindow()

	if len(notifier.sent) != 1 {
		t.Errorf("Send called %d times, want 1 (immediate catch-up flush)", len(notifier.sent))
	}
}

func TestFlushIfOutsideWindow_InsideWindow_DoesNotFlush(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dnd-pending.txt")
	if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	notifier := &fakeFlushNotifier{}
	fl := NewFlusher(Window{Start: "00:00", End: "10:00"}, path, notifier)
	fl.Now = fixedInsideWindow // 05:00, inside 00:00-10:00

	fl.flushIfOutsideWindow()

	if len(notifier.sent) != 0 {
		t.Errorf("Send called %d times, want 0 (still inside window, wait for window-end)", len(notifier.sent))
	}
}
```

Add a small retry-counting fake alongside `fakeFlushNotifier` at the top of the file:

```go
type failNTimesNotifier struct {
	failN int
	calls int
}

func (f *failNTimesNotifier) Send(message string) error {
	f.calls++
	if f.failN > 0 {
		f.failN--
		return errors.New("simulated send failure")
	}
	return nil
}
```

Add `"errors"`, `"strings"`, and `"time"` to the test file's imports.

Run: `go test ./action-svc/dnd/... -v`
Expected: PASS (all tests in the package — window, notifier, and flusher)

```bash
git add action-svc/dnd/flusher_test.go
git commit -m "action-svc/dnd: cover flush retry-then-clear and Start's catch-up-flush behavior"
```

---

### Task 4: `sharedconfig` + `action-svc/config` wiring

**Files:**
- Modify: `common/sharedconfig/config.go`
- Modify: `action-svc/config/config.go`
- Modify: `action-svc/config/config_test.go`
- Modify: `config/dev.json`
- Modify: `config/prod.json`

**Interfaces:**
- Consumes: nothing code-level (data/config layer only)
- Produces: `sharedconfig.DNDConfig{Enabled bool, Start, End string}`, `sharedconfig.Config.DoNotDisturb`, `action-svc/config.Config.DNDEnabled bool`, `.DNDStart string`, `.DNDEnd string` — used by Task 5's `main.go`

- [ ] **Step 1: Write the failing test**

```go
func TestLoad_DoNotDisturb_AllFieldsSet(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"nats_url": "nats://localhost:4222",
		"thinking_request_subject": "soulman.thinking.request",
		"memory_write_subject": "soulman.memory.write",
		"consumer_names": {"action_svc": "action-svc"},
		"do_not_disturb": {"enabled": true, "start": "00:00", "end": "10:00"}
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
	if !cfg.DNDEnabled {
		t.Error("DNDEnabled = false, want true")
	}
	if cfg.DNDStart != "00:00" {
		t.Errorf("DNDStart = %q, want 00:00", cfg.DNDStart)
	}
	if cfg.DNDEnd != "10:00" {
		t.Errorf("DNDEnd = %q, want 10:00", cfg.DNDEnd)
	}
}
```

(This references `cfg.DNDEnabled`/`.DNDStart`/`.DNDEnd`, which don't exist yet — Step 2 confirms the resulting compile failure.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./action-svc/config/... -run TestLoad_DoNotDisturb_AllFieldsSet -v`
Expected: FAIL — build fails: `cfg.DNDEnabled undefined (type *config.Config has no field or method DNDEnabled)`

- [ ] **Step 3: Write minimal implementation**

First, `common/sharedconfig/config.go` — add the new type and field (insert `DoNotDisturb` after `LogMonitor` in `Config`, and the `DNDConfig` type after `LogMonitorConfig`'s block, before `WebConfig`):

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
	DoNotDisturb  DNDConfig           `json:"do_not_disturb"`
	Web           WebConfig           `json:"web"`
}
```

```go
// DNDConfig holds action-svc's do-not-disturb window settings for its
// real-time Discord notification path (the notifybatch.Batcher chain) —
// see docs/superpowers/specs/2026-07-27-discord-do-not-disturb-design.md.
// Enabled lets the window be turned off without discarding the configured
// times, matching FeignMode's explicit-boolean convention rather than an
// implicit "empty times = disabled" signal. Start/End are not fatally
// validated here — a malformed "HH:MM" silently falls back to 10:00 inside
// action-svc/dnd.Window.Active, matching scheduler.parseSendTime's existing
// loose convention for ReportSendTime.
type DNDConfig struct {
	Enabled bool   `json:"enabled"`
	Start   string `json:"start"`
	End     string `json:"end"`
}
```

Next, `action-svc/config/config.go` — full new file:

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
	FeignMode              bool
	DNDEnabled             bool
	DNDStart               string
	DNDEnd                 string
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
		FeignMode:              shared.FeignMode,
		DNDEnabled:             shared.DoNotDisturb.Enabled,
		DNDStart:               shared.DoNotDisturb.Start,
		DNDEnd:                 shared.DoNotDisturb.End,
	}, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./action-svc/config/... ./common/sharedconfig/... -v`
Expected: PASS (`TestLoad_DoNotDisturb_AllFieldsSet` and every pre-existing test)

- [ ] **Step 5: Commit**

```bash
git add common/sharedconfig/config.go action-svc/config/config.go action-svc/config/config_test.go
git commit -m "sharedconfig/action-svc: add do_not_disturb config block, no fatal validation"
```

Then add the remaining config tests (same task, before moving to Task 5):

```go
func TestLoad_DoNotDisturb_Absent_DefaultsDisabledAndEmpty(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://localhost:4222", "soulman.thinking.request", "soulman.memory.write", "action-svc")
	os.Setenv("CONFIG_PATH", configPath)
	defer os.Unsetenv("CONFIG_PATH")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DNDEnabled {
		t.Error("DNDEnabled = true, want false when do_not_disturb absent from JSON")
	}
	if cfg.DNDStart != "" {
		t.Errorf("DNDStart = %q, want empty when do_not_disturb absent", cfg.DNDStart)
	}
	if cfg.DNDEnd != "" {
		t.Errorf("DNDEnd = %q, want empty when do_not_disturb absent", cfg.DNDEnd)
	}
}

func TestLoad_DoNotDisturb_MalformedTime_PassesThroughWithoutError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"nats_url": "nats://localhost:4222",
		"thinking_request_subject": "soulman.thinking.request",
		"memory_write_subject": "soulman.memory.write",
		"consumer_names": {"action_svc": "action-svc"},
		"do_not_disturb": {"enabled": true, "start": "not-a-time", "end": "10:00"}
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	os.Setenv("CONFIG_PATH", path)
	defer os.Unsetenv("CONFIG_PATH")

	// action-svc/config.Load performs no time-format validation of its own
	// (matching how ReportSendTime is handled) — a malformed value is
	// passed through unchanged; the actual "HH:MM" parsing and fallback to
	// 10:00 happens downstream in dnd.Window.Active, covered by
	// action-svc/dnd/window_test.go.
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: want no error for a malformed do_not_disturb.start, got %v", err)
	}
	if cfg.DNDStart != "not-a-time" {
		t.Errorf("DNDStart = %q, want the malformed value passed through unchanged", cfg.DNDStart)
	}
}
```

Then add the `do_not_disturb` block to `config/dev.json` (insert after `log_monitor`, before `web`):

```json
  "do_not_disturb": { "enabled": true, "start": "00:00", "end": "10:00" },
```

and the identical block to `config/prod.json` in the same position.

Run: `go test ./action-svc/config/... -v`
Expected: PASS (all tests, including the two new ones)

```bash
git add action-svc/config/config_test.go config/dev.json config/prod.json
git commit -m "sharedconfig/action-svc: cover do_not_disturb absent-defaults and malformed-time passthrough"
```

---

### Task 5: `action-svc/main.go` wiring

**Files:**
- Modify: `action-svc/main.go`
- Modify: `action-svc/NOTES.md`
- Modify: `CLAUDE.md`

**Interfaces:**
- Consumes: `dnd.Window{Start, End string}`, `dnd.WrapNotifier(window Window, pendingFilePath string, real notify.Notifier) notify.Notifier`, `dnd.NewFlusher(window Window, pendingFilePath string, notifier notify.Notifier) *Flusher`, `(*Flusher).Start()`, `(*Flusher).Stop()` (Tasks 1-3); `cfg.DNDEnabled`, `cfg.DNDStart`, `cfg.DNDEnd` (Task 4); the existing `feign.WrapNotifier`, `notifybatch.New`, `scheduler.New` (unchanged)
- Produces: final feature behavior — `action-svc`'s real-time Discord notification path (the `Batcher`) respects the configured do-not-disturb window; the daily digest cron is unaffected

- [ ] **Step 1: Write the failing test**

`action-svc/main.go` has no existing test file (it's an entrypoint, exercised via the package-level tests of the packages it wires together, all already covered by Tasks 1-4). This task's verification step is a full-service build, not a unit test — matching how the Log Error channel's wiring into `perception-svc/main.go` was verified.

```bash
go build ./action-svc/...
```

Expected: this currently succeeds (nothing to fail yet — recorded here only so Step 4's post-edit build is a meaningful before/after comparison, per this task's nature as a wiring task rather than a new-behavior unit). Proceed to Step 3.

- [ ] **Step 2: Run test to verify it fails**

Run: `go build ./action-svc/...`
Expected: PASS (baseline, pre-edit)

- [ ] **Step 3: Write minimal implementation**

Add `"soulman/action-svc/dnd"` to `action-svc/main.go`'s import block (alphabetically, after `dispatch`, before `feign`):

```go
import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"soulman/action-svc/config"
	"soulman/action-svc/dispatch"
	"soulman/action-svc/dnd"
	"soulman/action-svc/feign"
	"soulman/action-svc/httpserver"
	"soulman/action-svc/natsclient"
	"soulman/action-svc/notifybatch"
	"soulman/action-svc/notify"
	"soulman/action-svc/scheduler"
)
```

Replace the notifier/batcher construction block:

```go
	notifier = feign.WrapNotifier(gate, notifier)

	// Batches important-email Discord notifications from the
	// triage_gmail_email dispatch handler (30s grace / 2min max-wait — see
	// docs/superpowers/specs/2026-07-18-gmail-triage-action-design.md).
	// Reuses the same (feign-wrapped) notifier the daily cron already sends
	// through.
	batcher := notifybatch.New(notifybatch.DefaultGrace, notifybatch.DefaultMaxWait, notifier)
```

with:

```go
	notifier = feign.WrapNotifier(gate, notifier)

	// Do-not-disturb window — see
	// docs/superpowers/specs/2026-07-27-discord-do-not-disturb-design.md.
	// Only the Batcher's real-time notification path is gated; the daily
	// digest cron (sched, below) keeps using the plain feign-wrapped
	// notifier, unaffected by DND. If DNDEnabled is false, batcherNotifier
	// stays the same plain notifier sched uses — behavior identical to
	// pre-DND.
	dndWindow := dnd.Window{Start: cfg.DNDStart, End: cfg.DNDEnd}
	pendingPath := filepath.Join(cfg.SoulmanRoot, "logs", "dnd-pending.txt")
	batcherNotifier := notifier
	if cfg.DNDEnabled {
		batcherNotifier = dnd.WrapNotifier(dndWindow, pendingPath, notifier)
		dndFlusher := dnd.NewFlusher(dndWindow, pendingPath, notifier) // starts its own background loop once Start is called
		dndFlusher.Start()
		defer dndFlusher.Stop()
	}

	// Batches important-email Discord notifications from the
	// triage_gmail_email dispatch handler (30s grace / 2min max-wait — see
	// docs/superpowers/specs/2026-07-18-gmail-triage-action-design.md).
	// Reuses the same (feign-wrapped, and — if DND is enabled — DND-wrapped)
	// notifier the daily cron already sends through.
	batcher := notifybatch.New(notifybatch.DefaultGrace, notifybatch.DefaultMaxWait, batcherNotifier)
```

`sched := scheduler.New(cfg.SoulmanRoot, cfg.ReportSendTime, notifier, schedPublisher, gate)` (further down, unchanged) keeps using the plain `notifier` variable — it was never reassigned to `batcherNotifier`, so no edit is needed there at all.

- [ ] **Step 4: Run test to verify it passes**

Run: `go build ./action-svc/... && go vet ./action-svc/...`
Expected: PASS — build succeeds, `dnd` is now wired into `main.go`

- [ ] **Step 5: Commit**

```bash
git add action-svc/main.go
git commit -m "action-svc: wire the do-not-disturb window into the real-time notification path"
```

Then update docs (same task):

In `action-svc/NOTES.md`, add a new section (after "Leveled logging (log/slog, added 2026-07-27)"):

```markdown
## Do-not-disturb window for real-time Discord notifications (added 2026-07-27)

See `docs/superpowers/specs/2026-07-27-discord-do-not-disturb-design.md`. Only `notifybatch.Batcher`'s real-time notification path is gated — the once-daily digest cron (`scheduler.Scheduler`, default `10:00`) is untouched, still sending through the plain feign-wrapped `notifier`. `action-svc/dnd.WrapNotifier` mirrors `feign.WrapNotifier`'s shape exactly: during the configured window (default `00:00`-`10:00`, local time, `do_not_disturb.enabled`/`.start`/`.end` in `config/dev.json`/`config/prod.json`), `Send` appends to `$SOULMAN_ROOT/logs/dnd-pending.txt` instead of sending; outside the window, it delegates straight through. A background `dnd.Flusher` wakes at the window's end time each day (same wake-loop mechanics as `Scheduler.loop`/`nextRun`), sends the accumulated pending content as one message — still through the feign-wrapped notifier, so `feign_mode` continues to govern real-vs-recorded — and clears the file, with up to 3 retries on send failure (same backoff as `Scheduler.sendWithRetry`) but no cross-day retry chain: the file is cleared after the attempt regardless of outcome, to avoid unbounded growth if Discord stays down for days.

`Flusher.Start()` also flushes immediately if the process comes up outside the window with stale pending content (e.g. a restart at 11am after an earlier partial day's window already ended), rather than waiting for tomorrow's window-end.

This is the prerequisite for turning `feign_mode` off in prod — flipping that flag is a separate, deliberate deployment decision once DND is verified working in dev (both environments have `do_not_disturb.enabled: true` as of this feature, even though dev's sends are still feigned regardless).
```

In `CLAUDE.md`, update the action-svc bullet (item 4 under "Services") from:

```markdown
4. **`action-svc`** — dispatches `soulman.thinking.request` actions via a durable JetStream consumer: `append_daily_report_entry` (writes to `$SOULMAN_ROOT/reports/`, and — as of 2026-07-27 — batches a real-time Discord notify for any entry marked important, not just Gmail's) and `triage_gmail_email` (report entry + debounced batched Discord notify if important). Independently runs a 10:00 AM cron sending the previous day's report via a pluggable `Notifier` (Discord). `DISCORD_BOT_TOKEN`/`DISCORD_CHANNEL_ID` are non-fatal if blank (Send fails, retried/logged like any other notifier failure) — configured in dev and prod as of 2026-07-18 (a dedicated "Soulman Reports" bot). As of 2026-07-19, `feign_mode` is `true` in both `config/dev.json` and `config/prod.json`, so outbound sends are currently recorded to `logs/feigned-actions.jsonl` instead of actually happening — see `action-svc/NOTES.md`.
   - Specs: `2026-07-17-action-svc-design.md`, `2026-07-17-daily-report-delivery-design.md`, `2026-07-17-error-report-action-design.md`, `2026-07-18-gmail-triage-action-design.md`, `2026-07-18-pipeline-debugging-tools-design.md`, `2026-07-19-action-svc-feign-mode-design.md`, `2026-07-20-daily-report-importance-split-design.md`, `2026-07-27-log-error-perception-design.md`
   - Notes: `action-svc/NOTES.md` — the incident that motivated durable queues, the notification-batching design, a known deferred bug (dev/prod share one Discord bot), feign mode, the generalization beyond Gmail-only real-time notifications
```

to:

```markdown
4. **`action-svc`** — dispatches `soulman.thinking.request` actions via a durable JetStream consumer: `append_daily_report_entry` (writes to `$SOULMAN_ROOT/reports/`, and — as of 2026-07-27 — batches a real-time Discord notify for any entry marked important, not just Gmail's) and `triage_gmail_email` (report entry + debounced batched Discord notify if important). Real-time notifications respect a configurable do-not-disturb window (default `00:00`-`10:00`, `do_not_disturb.enabled`/`.start`/`.end`) — suppressed and accumulated to a pending file during the window, flushed as one message at window-end; the once-daily digest cron is unaffected. Independently runs a 10:00 AM cron sending the previous day's report via a pluggable `Notifier` (Discord). `DISCORD_BOT_TOKEN`/`DISCORD_CHANNEL_ID` are non-fatal if blank (Send fails, retried/logged like any other notifier failure) — configured in dev and prod as of 2026-07-18 (a dedicated "Soulman Reports" bot). As of 2026-07-19, `feign_mode` is `true` in both `config/dev.json` and `config/prod.json`, so outbound sends are currently recorded to `logs/feigned-actions.jsonl` instead of actually happening — see `action-svc/NOTES.md`.
   - Specs: `2026-07-17-action-svc-design.md`, `2026-07-17-daily-report-delivery-design.md`, `2026-07-17-error-report-action-design.md`, `2026-07-18-gmail-triage-action-design.md`, `2026-07-18-pipeline-debugging-tools-design.md`, `2026-07-19-action-svc-feign-mode-design.md`, `2026-07-20-daily-report-importance-split-design.md`, `2026-07-27-log-error-perception-design.md`, `2026-07-27-discord-do-not-disturb-design.md`
   - Notes: `action-svc/NOTES.md` — the incident that motivated durable queues, the notification-batching design, a known deferred bug (dev/prod share one Discord bot), feign mode, the generalization beyond Gmail-only real-time notifications, the do-not-disturb window
```

```bash
git add action-svc/NOTES.md CLAUDE.md
git commit -m "action-svc/docs: document the do-not-disturb window"
```

---

## Self-Review

**1. Spec coverage** — every section of `docs/superpowers/specs/2026-07-27-discord-do-not-disturb-design.md` maps to a task:
- Package `action-svc/dnd`, `Window` (parsing, non-wrapping and wrapping) → Task 1
- `WrapNotifier` (append during window, fail-open, delegate outside window) → Task 2
- Background flush loop (wake-at-window-end, catch-up-on-Start, format/send/clear, retry) → Task 3
- Config (`DNDConfig`, no fatal validation, `config/dev.json`/`config/prod.json`) → Task 4
- Wiring (`main.go`'s `batcherNotifier`/`dndFlusher`, `sched` untouched) → Task 5
- Error Handling table → covered across Tasks 2-3 (append failure fails open, flush failure retries-then-clears, restart-during-window needs no special handling since the file is just on disk, restart-after-window-end triggers `flushIfOutsideWindow`, malformed config falls back silently, feign_mode still governs the flush send) and Task 4 (malformed-time passthrough test)
- Testing section's `dnd/window_test.go`, `dnd/notifier_test.go`, `dnd/flusher_test.go` assertions → all present in Tasks 1-3's test lists; `action-svc/config` extension including malformed-time case → Task 4
- Out of Scope items (flipping `feign_mode`, a manual flush endpoint, per-severity overrides, gating the digest cron, changes to `Batcher` itself) → deliberately not implemented anywhere in this plan; no task contradicts them

No gaps found.

**2. Placeholder scan** — every code step contains complete, real Go (or JSON/Markdown) content; no "TBD"/"similar to Task N"/"add appropriate error handling" text appears in any step.

**3. Type/signature consistency** — traced across tasks:
- `Window{Start, End string}`, `(w Window) Active(t time.Time) bool`, `parseTime(s string) (int, int)` (Task 1) match Task 2's `n.window.Active(n.now())` and Task 3's `f.window.Active(f.Now())`/`parseTime(f.window.End)` exactly.
- `WrapNotifier(window Window, pendingFilePath string, real notify.Notifier) notify.Notifier` (Task 2) matches Task 5's `dnd.WrapNotifier(dndWindow, pendingPath, notifier)` call exactly, including argument order.
- `entrySeparator` (Task 2) is referenced, not redefined, by Task 3's `FlushIfPending` (`strings.Split(content, entrySeparator)`) — same package, no import needed.
- `NewFlusher(window Window, pendingFilePath string, notifier notify.Notifier) *Flusher` (Task 3) matches Task 5's `dnd.NewFlusher(dndWindow, pendingPath, notifier)` call exactly.
- `cfg.DNDEnabled`/`.DNDStart`/`.DNDEnd` (Task 4) match the three fields Task 5 reads off `cfg` in `main.go` exactly.
- The coordinator-specified verbatim signatures — `notify.Notifier`, `feign.WrapNotifier`'s shape, `action-svc/config.Config`'s existing fields, `scheduler.parseSendTime`'s fallback, and the exact `main.go` wiring diff — were used as given; `parseTime` (Task 1) is a direct structural mirror of `scheduler.parseSendTime` (same fallback values, same split-on-":" logic), not a copy-paste (it lives in a different package and can't import `scheduler`'s unexported function).

No inconsistencies found.

**Judgment calls made (flagging for reviewer attention):**

1. **`entrySeparator` is `"\n\n---\n\n"`, not a bare `"\n\n"`.** The spec's Window/WrapNotifier section says the pending-file separator "mirrors `feign.Gate.Record`'s append-one-line pattern" without specifying an exact delimiter, and the Flush section's `"<N> notification(s) from overnight"` header implies *counting* accumulated entries. A single `Send` call's message (e.g. a multi-item `notifybatch.formatBatch` flush) can already contain internal `"\n\n"` separators between its own blocks — splitting the whole pending file on a bare `"\n\n"` would badly over-count. Chose a distinct separator so `FlushIfPending`'s entry count is accurate regardless of what's inside any individual queued message.
2. **`Flusher` needs an injectable clock for `WrapNotifier`'s `Send`, but `WrapNotifier` returns the `notify.Notifier` interface, not a concrete type** — so a test can't reach in and override a clock field on the returned value. Resolved with the same `New`/`newWatcher`-style test-seam split `perception-svc/sysmonitor` already uses: the public `WrapNotifier` always uses the real `time.Now`, and an unexported `newNotifier(..., now func() time.Time)` is what tests call directly (white-box `package dnd` tests, not `dnd_test`).
3. **`Flusher.Start()`'s catch-up check is factored into its own method, `flushIfOutsideWindow`, tested directly instead of through `Start()`.** `Start()` launches `go f.loop()`, whose wait duration is computed via `time.Until(...)` against the *real* clock while `f.Now()` may be a fixed test value — calling `Start()` in a test risks the background goroutine racing in an extra (usually harmless, but non-deterministic) flush attempt before assertions run. Testing the synchronous catch-up logic directly avoids that race entirely while still fully covering the spec's "flush immediately if outside window with pending content on Start" requirement.
4. **Added `defer dndFlusher.Stop()` in `main.go`**, which the spec's own wiring snippet doesn't show. Every other background loop in `main.go` (`sched`, `consumer`, `nc`) gets a deferred stop/close for graceful shutdown; omitting one for `dndFlusher` looked like an oversight in the spec's illustrative snippet rather than a deliberate omission, so it's included for consistency. Flagging in case the reviewer intended no shutdown hook here.
5. **Zero-width window (`Start == End`) is defined as never-active**, not always-active — not addressed explicitly by the spec. Chosen because a DND window's entire purpose is to have positive duration; "never active" degrades safely (falls back to "notifications always send," the pre-DND behavior) if someone ever misconfigures `start`/`end` to be identical, rather than silently going the other direction (suppressing everything, always).
