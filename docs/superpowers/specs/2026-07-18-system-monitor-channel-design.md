# System Monitor Perception Channel Design

**Date:** 2026-07-18
**Status:** Approved
**Phase:** Soulman Phase 2 — third `perception-svc` pull channel (after folder-watcher, Gmail), paired with a new mechanical Thinking rule reusing the existing `append_daily_report_entry` action.

---

## Summary

Adds a **System Monitor** pull channel to `perception-svc`, per `Perception module.md`'s System Monitor row: polls local disk space, memory, and CPU usage on a fixed interval (default 300s) and emits a `Stimulus` when a check's severity crosses a threshold (`ok → warning → critical`, and back down). Paired with a new `thinking-svc` rule matching `channel == "system-monitor"` that mechanically reuses the existing `append_daily_report_entry` action — **no new `action-svc` code**. This is a genuine quick win: one new perception-svc package, one new sharedconfig block, one new thinking-svc rule file.

---

## Package: `perception-svc/sysmonitor`

Mirrors the shape of `perception-svc/watcher` (folder-watcher) and `perception-svc/gmailwatcher`, the two existing pull-channel precedents:

- A `Watcher` struct with `New(...)`, `Start(ctx)`, `Close()`.
- A `Publisher` interface (`Publish(ctx, *common.Stimulus) error`), declared locally to avoid an import cycle — same rationale as both existing packages.
- An internal poll loop: one immediate poll on `Start` (so a bad state is caught right away, not after a full interval), then a `time.Ticker`-driven loop at the configured interval — same pattern as `gmailwatcher.pollLoop`.

### Stats source (Windows)

A small `statsProvider` interface is the seam between the poll loop and the actual OS calls, so tests can inject a fake instead of hitting real hardware (mirrors `gmailwatcher`'s fake-`gmailClient` seam):

```go
type statsProvider interface {
    DiskUsagePercent(path string) (float64, error)
    MemoryUsagePercent() (float64, error)
    CPUUsagePercent() (float64, error)
}
```

The real implementation (`checks_windows.go`) uses `golang.org/x/sys/windows` — already an indirect dependency of this module (via `oauth2`/`nats.go`), so this adds no new third-party dependency tree:

- **Disk**: `windows.GetDiskFreeSpaceEx(path, &free, &total, &totalFree)` → `percentUsed = 100 * (1 - free/total)`.
- **Memory**: `windows.GlobalMemoryStatusEx(&mem)` → `mem.MemoryLoad` is already a 0–100 percentage.
- **CPU**: `windows.GetSystemTimes(&idle, &kernel, &user)` returns cumulative time since boot. `winStats` keeps the previous poll's cumulative snapshot internally and computes `1 - (idleDelta / totalDelta)` between the current and previous call. Since the poll interval is already 300s, this diff naturally yields a usage percentage averaged over the interval — no artificial busy-wait sampling needed. **The first CPU reading after process startup has no prior snapshot to diff against and is silently skipped that one cycle** (not an error — no stimulus, no log noise).

No cross-platform build tags: this module already only builds on Windows once it imports `golang.org/x/sys/windows`, consistent with the rest of the Soulman deployment (Windows-only, per `start-everything.ps1`).

### Emission semantics: edge-triggered

The `Watcher` holds in-memory state: `map[checkKey]severity` where `severity` is `ok | warning | critical`. `checkKey` is the check `type` alone for `memory`/`cpu`, or `type+path` for `disk_space` (future-proofs multi-drive configs even though v1 only lists one path).

Each poll, every configured check computes its current percentage and derived severity, then compares to the stored previous severity for that key:

- **No change → no stimulus.** A disk stuck at 96% full for hours produces one stimulus, not one every 5 minutes.
- **Change → publish a Stimulus**, covering escalation (`warning→critical`), first crossing (`ok→warning` or, if a poll interval is missed, straight `ok→critical`), and recovery (`warning→ok`, `critical→ok`).
- In-memory state is a **plain Go map, not persisted** across restarts (unlike `watcher`'s checkpoint file). On restart, all keys reset to `ok`. Worst case: a condition that was already critical before a restart re-fires one redundant-but-harmless alert on the next poll after restart. Restarts here are rare (deploy/reboot) and the cost of a spurious duplicate is far lower than folder-watcher's checkpoint (which exists because *file identity* must survive restarts to avoid re-publishing old files) — so no checkpoint file is warranted for v1.
- **State advances only after a successful publish** — same WAL-style pattern as `watcher.Mark`-after-publish-success and `gmailwatcher`'s label-after-publish-success. A publish failure leaves the old severity in place, so the transition is retried on the next poll instead of being silently swallowed.

### Severity derivation

Per check, given `warning_threshold_percent` and an optional `critical_threshold_percent`:

- `value < warning` → `ok`
- `warning <= value < critical` (or `critical` unset) → `warning`
- `value >= critical` (only if set) → `critical`

`critical_threshold_percent` is optional per check (see Config below) — a check with it unset only ever reports `ok`/`warning`, never `critical`.

### Stimulus construction

| Field | Value |
|---|---|
| `channel` | `"system-monitor"` |
| `source` | `{identity: "system-monitor", authenticated: true, auth_method: "system"}` — local/OS-trust, same as folder-watcher |
| `content.raw_text` | Pre-formatted human-readable message, e.g. `"Disk space critical: C:\ at 97% used (threshold 95%)"` or `"Memory usage recovered to normal: 62% used"`. Built entirely in `sysmonitor` so `thinking-svc`'s rule stays mechanical — same reasoning as `cli_note.go` passing CLI-typed text straight through. |
| `content.content_type` | `"text"` |
| `content.raw_payload` | `{}` — no external payload to preserve |
| `channel_metadata.message_id` | `sha256(check_type + path + severity + occurred_at)` — dedup key, mirrors `folder-watcher`'s `computeMessageID` |
| `channel_metadata.channel_specific` | `{"check_type": "...", "path": "..." (omitempty), "severity": "...", "value_percent": ..., "threshold_percent": ...}` |
| `hints.priority` | `"critical"` on a transition into `critical`; `"high"` on a transition into `warning`; `"normal"` on a transition back to `ok` (a recovery notice, not an alarm) |
| `hints.tags` | `["system", "system-monitor", <check_type>]` |
| `received_at` / `occurred_at` | Both `time.Now()` — measurement and detection are simultaneous, unlike email's poll lag |
| `override` | `{is_override: false}` |

---

## Config: `common/sharedconfig`

New nested config, following the `GmailConfig` precedent (nested struct, JSON-tagged, loaded once by `sharedconfig.Load` and validated by each service's own `config.Load`):

```go
type SystemMonitorConfig struct {
    PollIntervalSeconds int           `json:"poll_interval_seconds"`
    Checks              []CheckConfig `json:"checks"`
}

type CheckConfig struct {
    Type                     string  `json:"type"` // "disk_space" | "memory" | "cpu"
    Path                     string  `json:"path,omitempty"` // disk_space only
    WarningThresholdPercent  float64 `json:"warning_threshold_percent"`
    CriticalThresholdPercent float64 `json:"critical_threshold_percent,omitempty"` // 0/absent = no critical tier
}
```

Added to `sharedconfig.Config` as a new field: `SystemMonitor SystemMonitorConfig` (JSON tag `system_monitor`).

### Fatal-fast validation (`perception-svc/config`)

Unlike the Gmail channel — optional because its OAuth credentials might not be bootstrapped yet — System Monitor has **no external credential dependency**; it is as fundamentally "always on" as `watch_paths`. `perception-svc/config.Load` therefore treats a missing/empty `system_monitor.checks` list or a non-positive `poll_interval_seconds` as a **fatal startup error**, matching `watch_paths`'s existing fatal-fast validation rather than Gmail's skip-if-unconfigured pattern.

### `config/dev.json` and `config/prod.json`

Both get a `system_monitor` block added in this feature (matching `Perception module.md`'s example config, translated to this machine's actual drive letter):

```json
"system_monitor": {
  "poll_interval_seconds": 300,
  "checks": [
    { "type": "disk_space", "path": "C:\\", "warning_threshold_percent": 80, "critical_threshold_percent": 95 },
    { "type": "memory", "warning_threshold_percent": 85 },
    { "type": "cpu", "warning_threshold_percent": 90 }
  ]
}
```

Memory and CPU deliberately omit `critical_threshold_percent`, matching `Perception module.md`'s own example exactly (only `disk_space` has both thresholds there) rather than inventing unspecified critical values.

Dev and prod run on the same physical Windows machine and will each independently poll and alert on the same real disk/memory/CPU condition — the same accepted duplication the Gmail channel already has for the shared inbox (each environment dedups/tracks state independently; a real event is visible to both).

---

## Thinking Rule: `thinking-svc/rules/system_monitor.go`

Same shape as `CLINoteRule` — mechanical, no LLM call, since the message is already a complete, human-readable one-liner built by `sysmonitor`:

```go
var SystemMonitorRule = Rule{
    Name:  "system-monitor",
    Match: func(s *common.Stimulus) bool { return s.Channel == "system-monitor" },
    Handle: handleSystemMonitor,
}
```

`handleSystemMonitor`:
- `summary` / `raw_content`: both `s.Content.RawText` verbatim
- `source_path`: derived from `channel_metadata.channel_specific` as `"system-monitor/<check_type>"`, or `"system-monitor/<check_type>/<path>"` when `path` is present — parallels `error_report.go`'s `watchedPath()` extraction helper
- `occurred_at`: passed through verbatim
- `risk_level: "low"`, `urgency: "normal"`, `action_hint: "append_daily_report_entry"`, same fallback text as `ErrorReportRule`/`CLINoteRule`

Registered in `rule.go`'s `Registry`, appended after `CLINoteRule` (match order is irrelevant here since `channel` values are disjoint across all three rules; appending keeps registration in chronological-add order).

**No `action-svc` changes.** `append_daily_report_entry` already writes `{summary, raw_content, source_path, occurred_at}` to the daily report file. A resulting entry reads like:

```
2026-07-18 15:42  [system-monitor/disk_space/C:\]  Disk space critical: C:\ at 97% used (threshold 95%)
```

---

## Error Handling

| Failure | Behaviour |
|---|---|
| One check's syscall fails (e.g. configured disk path doesn't exist) | Logged and skipped; other checks in the same poll tick still run — mirrors `watcher`'s per-file isolation |
| Publish fails | Logged; in-memory severity state for that check is **not** advanced, so the transition is retried on the next poll instead of silently dropped |
| CPU check's first poll after startup (no prior sample) | Silently skipped — not an error, not logged |
| `action-svc` / `fs-agent` failure | Already covered by `append_daily_report_entry`'s existing retry-once-then-give-up behavior; no new failure mode |

---

## Testing

- `sysmonitor_test.go`: fake `statsProvider` returning a scripted sequence of percentages across successive poll calls. Assertions: steady `ok` → no stimulus; `ok→warning`, `warning→critical`, `critical→ok` → exactly one stimulus each, with correct `severity`/`hints.priority`; repeated same-state polls → nothing further; a simulated publish failure → severity not advanced, same transition re-fires next poll.
- `checks_windows.go`: a thin smoke test asserting each real stat function returns a value in `[0, 100]` with no error. No build tags needed — the module already only builds on Windows via `golang.org/x/sys/windows`.
- `thinking-svc/rules/system_monitor_test.go`: mirrors `cli_note_test.go` — feed a sample `system-monitor` stimulus, assert the resulting `ActionRequest`'s `source_path`, `summary`, and `occurred_at`.

---

## Out of Scope (this iteration)

- Service health checks and SSL certificate expiry (listed in `Perception module.md`'s System Monitor row alongside disk/memory/CPU, but not requested for this iteration)
- Multiple disk paths in the default config (the `CheckConfig` schema supports it; only `C:\` is configured today)
- Any severity-aware routing in `action-svc` beyond the existing mechanical report-entry write (e.g. an immediate Discord ping on `critical`, the way Gmail triage does) — a natural future extension once this pipeline is proven, not required for v1
- Persisted check-state checkpoint across restarts

---

## Related

- `Perception module.md` — System Monitor channel row, poll interval table, channel registry example config
- `docs/superpowers/specs/2026-07-17-perception-svc-design.md` — existing perception-svc design (Stimulus construction, adapter isolation)
- `docs/superpowers/specs/2026-07-18-gmail-channel-design.md` — the other existing pull-channel precedent, source of the optional-vs-fatal config validation distinction
- `docs/superpowers/specs/2026-07-17-error-report-action-design.md` — the `append_daily_report_entry` action this rule reuses unmodified
- `perception-svc/NOTES.md`, `thinking-svc/NOTES.md` — operational notes for the services this feature touches
