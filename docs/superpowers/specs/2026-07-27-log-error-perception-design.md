# Log Error Perception Channel Design

**Date:** 2026-07-27
**Status:** Approved
**Phase:** Soulman Phase 2 — fourth `perception-svc` pull channel (after folder-watcher, Gmail, system-monitor), paired with a new mechanical Thinking rule and a generalization of `action-svc`'s existing report/notify dispatch.

---

## Summary

Adds a **Log Error** pull channel to `perception-svc`: tails every sibling service's `*-startup-err.log` file (in the same environment) for new `slog` `ERROR`-level lines, and publishes a `Stimulus` the first time a given `(service, message)` pair is seen — silently absorbing repeats of the same error for the life of the process, so an ongoing incident (e.g. the current Postgres connectivity failure) alerts once, not on every retry. Paired with a new `thinking-svc` rule (`channel == "log-error"` → always `Important: true`) and a generalization of `action-svc`'s dispatch so `Important: true` `append_daily_report_entry` entries also trigger a real-time Discord notification via the existing batcher — today only Gmail triage does this; `system-monitor`'s critical alerts silently wait for the next day's report (called out as explicit out-of-scope in `2026-07-18-system-monitor-channel-design.md`). This spec closes that gap for both channels at once.

This became necessary after `2026-07-27`'s logging conversion (`log.Printf` → leveled `log/slog`, see root `CLAUDE.md`'s "Logging" section) made `ERROR` lines reliably greppable — this feature is what actually watches for them.

---

## Package: `perception-svc/logmonitor`

Mirrors the shape of `perception-svc/watcher` (folder-watcher) and `perception-svc/sysmonitor`, the two closest existing precedents — tailing-with-checkpoint from the former, edge-triggered dedup from the latter:

- A `Watcher` struct with `New(...)`, `Start(ctx)`, `Close()`.
- A `Publisher` interface (`Publish(ctx, *common.Stimulus) error`), declared locally — same import-cycle rationale as every other channel package.
- Discovers target files by globbing `$LOG_DIR/*-startup-err.log` — every service in an environment already writes there (set by each `run-<svc>.ps1`), so no per-service list to maintain; a new service's log file is picked up automatically. `service` (used in the dedup key and `Stimulus` metadata) is the filename with the `-startup-err.log` suffix stripped.
- **Detection**: fsnotify watches `$LOG_DIR` for write events on tracked files (instant reaction), plus a periodic reconciliation poll (default 30s) as a safety net for missed events — the same dual mechanism `watcher` already uses for folders, applied to files instead.

### Tailing and checkpoint

Each tracked file has a byte-offset checkpoint, **persisted to disk** as a small JSON file (`logmonitor-checkpoint.json`, alongside `watcher`'s own checkpoint file):

- On new-file-write notification (or reconciliation poll), read from the stored offset to current EOF, split into complete lines (a trailing partial line is left for the next read), advance and persist the offset after each successful read.
- **First run for a given file** (no checkpoint entry yet): start at the file's *current* size, not 0 — deliberately skips replaying old history. An error that's still actively recurring gets picked up within one retry cycle regardless (it'll write a new line soon); one that already stopped in the past doesn't get incorrectly resurrected as a fresh alert on first deploy.
- **Truncation**: if current file size < stored offset (e.g. the file was manually cleared, as `memory-svc-startup-err.log` was on 2026-07-27), reset offset to 0 and continue — not treated as an error.
- Unlike `watcher`'s checkpoint (which exists so file *identity* survives restarts), this one exists so no error line is silently skipped if perception-svc is briefly down while another service keeps writing — a materially different failure mode worth persisting against.

### Line parsing

New lines are matched against `log/slog`'s default (unconfigured) handler format: `<log-package timestamp> <LEVEL> <msg> [key=value ...]`. Only `LEVEL == "ERROR"` lines are considered; everything else (`WARN`, `INFO`, non-matching lines such as stack-trace continuations) is silently skipped — not logged as noise. The `msg` field is slog's static message text; dynamic values are separate `key=value` attributes and are *not* part of the dedup key (see below), matching how every service was converted to call `slog` in the 2026-07-27 logging change.

### Dedup: in-memory, per-process

`map[dedupKey]struct{}` where `dedupKey = service + "\x00" + msg`. On a parsed `ERROR` line:

- Key already seen this process lifetime → absorbed, no `Stimulus`.
- Key not yet seen → mark seen, build and publish a `Stimulus`.

**Not persisted** — a perception-svc restart clears this map, so a still-ongoing error can re-fire once more after a restart. This is the same accepted tradeoff `sysmonitor`'s severity state already makes, and matches the explicit decision that recovery is silent (no "resolved" notification) — each process lifetime is its own incident window, and a restart-triggered repeat is a rare, harmless cost next to the alternative of persisting and reasoning about a "same incident" definition across restarts.

### Stimulus construction

| Field | Value |
|---|---|
| `channel` | `"log-error"` |
| `source` | `{identity: "log-error", authenticated: true, auth_method: "system"}` — local/OS-trust, same as `system-monitor` |
| `content.raw_text` | The full matched line verbatim (timestamp, level, msg, and attrs) — gives the human the complete original context, not just the bare message |
| `content.content_type` | `"text"` |
| `content.raw_payload` | `{}` |
| `channel_metadata.message_id` | `sha256(service + msg + occurred_at)` — dedup key for the wire format, mirrors every other channel's `computeMessageID` |
| `channel_metadata.channel_specific` | `{"service": "...", "msg": "..."}` |
| `hints.priority` | Always `"critical"` — scope is Error-level only (Warn is out of scope this iteration; see below), so there is no lower tier to distinguish |
| `hints.tags` | `["system", "log-error", <service>]` |
| `received_at` / `occurred_at` | Both `time.Now()` at detection time (the log line's own embedded timestamp is preserved verbatim in `raw_text`, not reparsed into a second source of truth) |
| `override` | `{is_override: false}` |

---

## Config

No new `sharedconfig` block strictly required — `$LOG_DIR` is already an environment variable every service (including perception-svc) receives from its `run-<svc>.ps1` launcher. One small addition for consistency with every other poll-based channel: `LogMonitorConfig{ ReconciliationIntervalSeconds int }` (default `30`), nested in `sharedconfig.Config` the same way `SystemMonitorConfig` is, with the same fatal-fast-if-absent-or-non-positive validation as `system_monitor.poll_interval_seconds` (this channel has no external credential dependency and no reason to ever be optional).

---

## Thinking Rule: `thinking-svc/rules/log_error.go`

Same shape as `SystemMonitorRule` — mechanical, no LLM call, since `logmonitor` already built a complete `raw_text` and Error-level *is* the importance signal:

```go
var LogErrorRule = Rule{
    Name:  "log-error",
    Match: func(s *common.Stimulus) bool { return s.Channel == "log-error" },
    Handle: handleLogError,
}
```

`handleLogError`:
- `summary` / `raw_content`: both `s.Content.RawText` verbatim
- `source_path`: `"log-error/<service>"`, derived from `channel_metadata.channel_specific.service` — parallels `systemMonitorSourcePath`
- `occurred_at`: passed through verbatim
- `important`: **always `true`** — unlike `system-monitor` (whose `systemMonitorImportant` only flags `critical`/`ok`, not `warning`), Log Error has no non-important tier since only Error-level lines reach this channel at all
- `risk_level: "low"`, `urgency: "normal"`, `action_hint: "append_daily_report_entry"`, same fallback text as the other mechanical rules

Registered in `rule.go`'s `Registry`, appended after `SystemMonitorRule`.

---

## Action Layer: real-time notification for important report entries

Today, `Important: true` on an `append_daily_report_entry` action only changes which section of the daily report file the entry lands in (`report.PathForDate`) — it does not notify. Real-time Discord notification currently only exists for `triage_gmail_email`, via `dispatchGmailTriage`'s call to `d.batcher.Add(notifybatch.Item{...})`. This was `system-monitor`'s spec's own explicit out-of-scope item ("an immediate Discord ping on critical, the way Gmail triage does") — this feature closes it for both channels, since Log Error needs the same real-time behavior and the underlying gap is identical.

### `notifybatch` generalization

`Item` and `formatBatch` are currently Gmail-shaped (`Sender`/`Subject`/`Reason`/`BodyExcerpt`/`ThreadID`, formatted as `"From: ... Subject: ..."` with a `mailto`-style Gmail link). Generalize to a shape that covers both email and generic report entries:

```go
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
```

`formatBatch` branches on `Kind` per item to preserve the existing Gmail format exactly, and adds a plain `"[<source_path>] <summary>\n<body_excerpt>"`-style block for `"report"` items. Grouping in one flushed message when both kinds arrive in the same batch window is fine — the batcher's grace/max-wait debounce logic is unchanged, only the per-item formatting branches.

### Dispatch wiring

`dispatchAppendDailyReportEntry` (used by `system-monitor`, `error-report`/folder-watcher, `cli-note`, and now `log-error`) gains the same shape `dispatchGmailTriage` already has:

```go
if important && d.batcher != nil {
    d.batcher.Add(notifybatch.Item{Kind: "report", Summary: ..., SourcePath: ..., BodyExcerpt: ...})
}
```

`important` is read from the same `errorReportParams.Important` field already marshaled into `req.Parameters` — no new field needed, just a new read site. This is a **behavior change for existing important report entries**: `system-monitor`'s critical/recovery alerts and any `folder-watcher` (`ErrorReportRule` always sets `Important: true`) entries will now also notify Discord in real time, not just Log Error's. This is intentional per your decision, not an accidental side effect.

`feign_mode` (currently `true` in both `config/dev.json` and `config/prod.json`) already gates all `Notifier.Send` calls regardless of caller — no change needed there; report-entry notifications will feign exactly like Gmail's do today until `feign_mode` is turned off.

---

## Error Handling

| Failure | Behaviour |
|---|---|
| A tracked log file becomes unreadable mid-run (permissions, deleted) | Logged and skipped this cycle; other tracked files unaffected — mirrors `watcher`'s per-file isolation |
| File truncated below checkpoint offset | Offset reset to 0, tailing continues — not an error |
| A line doesn't match the expected `slog` format | Silently skipped, no log noise (stack traces, panics, and any other non-`slog` output are expected and common) |
| Publish fails | Logged; dedup key is **not** marked seen, so the same error is retried on the next matching line rather than being permanently swallowed |
| Checkpoint file unreadable/corrupt on startup | Logged, starts empty — same accepted tradeoff as `watcher.LoadCheckpoint` (worst case: one file's history from-0 replay, but combined with the dedup map this just means each distinct historical error type in that file alerts once, not a flood) |
| `action-svc` / `fs-agent` / Discord failure | Already covered by existing retry-once-then-give-up (report) and batcher (Discord) failure handling; no new failure mode |

---

## Testing

- `logmonitor_test.go`: fake filesystem/clock harness feeding scripted line batches through the parser and dedup state machine. Assertions: a repeated identical `(service, msg)` → exactly one `Stimulus`; two distinct messages from the same service → two `Stimuli`; `WARN`/`INFO`/malformed lines → nothing; a simulated publish failure → dedup key not marked, same line re-fires on next read; truncation (shrinking the fake file) → offset resets and tailing continues; first-run-no-checkpoint → starts at EOF, ignores pre-existing content.
- `thinking-svc/rules/log_error_test.go`: mirrors `system_monitor_test.go` — feed a sample `log-error` stimulus, assert `source_path`, `summary`, and `important == true` always.
- `notifybatch/batcher_test.go`: extend for `Kind: "report"` items — assert `formatBatch` output shape and that mixed gmail+report batches in one flush both render correctly.
- `dispatch/dispatch_test.go`: extend `dispatchAppendDailyReportEntry` tests to assert `batcher.Add` is called once when `important == true` and not called when `false` (mirrors the existing `gmail_triage_test.go` assertions).
- End-to-end manual check once deployed: the live, ongoing Postgres-connectivity `ERROR` is a ready-made real test case — should produce exactly one Discord notification, not one per retry.

---

## Out of Scope (this iteration)

- `WARN`-level monitoring (Error only, per explicit decision)
- Any "recovered" / resolution notification (silent recovery, per explicit decision)
- Cross-environment correlation (dev and prod each independently tail their own logs and dedup independently, same accepted duplication as every other channel running in both environments)
- Persisting dedup state across restarts
- Any log-file rotation/archival policy beyond truncation-detection (the underlying files still grow unbounded between manual truncations — out of scope for this feature, which only concerns detection/alerting, not log lifecycle management)

---

## Related

- `docs/superpowers/specs/2026-07-18-system-monitor-channel-design.md` — the closest existing precedent (edge-triggered emission, in-memory state tradeoff), and the origin of the real-time-notification gap this spec closes
- `docs/superpowers/specs/2026-07-17-perception-svc-design.md` — existing perception-svc design (Stimulus construction, adapter isolation)
- `docs/superpowers/specs/2026-07-18-gmail-triage-action-design.md` — original `notifybatch` design (grace/max-wait debounce) being generalized here
- `docs/superpowers/specs/2026-07-17-error-report-action-design.md` — the `append_daily_report_entry` action this rule reuses unmodified
- Root `CLAUDE.md`'s "Logging" section (added 2026-07-27) — the leveled `slog` conversion this feature depends on being able to grep `ERROR` reliably
- `perception-svc/NOTES.md`, `thinking-svc/NOTES.md`, `action-svc/NOTES.md` — operational notes for the services this feature touches
