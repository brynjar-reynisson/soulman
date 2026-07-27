# Discord Do-Not-Disturb Window Design

**Date:** 2026-07-27
**Status:** Approved
**Phase:** Soulman Phase 2 — gates `action-svc`'s real-time Discord notification path (added 2026-07-27 by the Log Error perception feature) ahead of turning `feign_mode` off in prod.

---

## Summary

Adds a configurable **do-not-disturb (DND) window** (default `00:00`–`10:00`, local time) that suppresses real-time Discord notifications overnight without losing them: during the window, `action-svc/notifybatch.Batcher`'s notifications are appended to a pending file instead of sent; a background loop wakes exactly at the window's end time each day, sends the accumulated content as one message, and clears the file. This is the prerequisite for setting `feign_mode: false` in `config/prod.json` — until now, every real-time Discord ping (Gmail triage, system-monitor, folder-watcher, log-error, cli-note) has only ever been feigned, so nothing has actually tested what firing at 3am would feel like. DND makes that safe.

Scope is deliberately narrow: only the `Batcher`'s notifier is gated. The once-daily digest cron (`action-svc/scheduler.Scheduler`, default send time `10:00`) is untouched — it's already a single deliberate delivery, not an interruption, and its own `sendTime` is independently configurable if it ever needs to move.

---

## Package: `action-svc/dnd`

Mirrors `action-svc/feign`'s shape (a `Gate`-like construct wrapping `notify.Notifier`) and reuses `action-svc/scheduler`'s wake-loop mechanics (`nextRun`/`time.Until`/`time.After`) — no new architectural pattern, two existing ones combined.

### Window

```go
type Window struct {
    Start string // "HH:MM", local time
    End   string // "HH:MM", local time
}
```

`Active(t time.Time) bool` parses `Start`/`End` the same way `scheduler.parseSendTime` does (silent fallback, not fatal — see Config below) and reports whether `t`'s local wall-clock time falls inside `[Start, End)`. Handles both the non-wrapping case (`Start < End`, e.g. `00:00`–`10:00`) and the midnight-wrapping case (`Start > End`, e.g. `22:00`–`06:00`) — the window is configurable, and wrapping is a realistic real-world DND configuration even though it's not today's default.

### WrapNotifier

```go
func WrapNotifier(window Window, pendingFilePath string, real notify.Notifier) notify.Notifier
```

Mirrors `feign.WrapNotifier(gate, real)` exactly in shape. `Send(message string) error`:
- **Inside the window**: append `message` to `pendingFilePath` (creating it and its parent directory if needed, one entry per append with a separator between accumulated entries — mirrors `feign.Gate.Record`'s append-one-line pattern, but plain text rather than JSON since the content is later sent verbatim). Returns `nil` on successful append.
- **Append fails** (disk error, permissions): fail open — call `real.Send(message)` immediately instead of silently losing the notification. Logged at `slog.Warn`. Losing a genuine alert is worse than one disturbed night.
- **Outside the window**: delegates straight to `real.Send(message)`, no different from today.

### Background flush loop

Started alongside the notifier construction in `main.go`, shaped like `Scheduler.loop`:
- Computes the next window-end time the same way `Scheduler.nextRun` computes the next send time, sleeps until then, flushes, repeats.
- **On `Start()`**, before entering the wait loop: if the current time is currently outside the window AND the pending file is non-empty, flush immediately. Covers the case where a restart happens after window-end has already passed but before this loop had a chance to run today (e.g. a rebuild at 11am with leftover content from an earlier partial day).
- **Flush**: read the pending file. If empty, do nothing. If non-empty, format as `"<N> notification(s) from overnight:\n\n<content>"` (mirrors the existing `"<N> important item(s):"` batch-header convention) and send it through the *same notifier chain* the `Batcher` uses (i.e. still passes through `feign.WrapNotifier`, so `feign_mode` continues to govern real-vs-recorded) — retried up to 3 times with the same backoff `Scheduler.sendWithRetry` already uses. Whether the send ultimately succeeds or exhausts retries, the pending file is cleared after the attempt (no cross-day retry chain — matches the digest cron's own "log the outcome and move on" philosophy, and avoids the unbounded-growth failure mode a still-broken Discord API for several days would otherwise cause).

---

## Config: `common/sharedconfig`

```go
type DNDConfig struct {
    Enabled bool   `json:"enabled"`
    Start   string `json:"start"` // "HH:MM", local time
    End     string `json:"end"`   // "HH:MM", local time
}
```

Added to `sharedconfig.Config` as `DoNotDisturb DNDConfig` (JSON tag `do_not_disturb`). `Enabled` lets the window be turned off without discarding the configured times — matches `feign_mode`'s existing explicit-boolean convention rather than an implicit "empty times = disabled" signal.

### `config/dev.json` and `config/prod.json`

Both get a `do_not_disturb` block:
```json
"do_not_disturb": { "enabled": true, "start": "00:00", "end": "10:00" }
```
Dev gets this too (not just prod) — dev's Discord sends are already feigned regardless, so there's no behavioral risk, and it keeps the two environments' config shape consistent and gives the feature a low-stakes place to be exercised before prod's `feign_mode` flips.

### Validation

Not fatal-fast — matches `scheduler.parseSendTime`'s existing loose convention (a malformed `"HH:MM"` silently falls back to a sensible default, `10:00`) rather than the fatal-fast pattern `system_monitor`/`log_monitor` use. `action-svc/config.Load` reads `shared.DoNotDisturb` directly into the service config with no additional validation step, consistent with how `ReportSendTime` is handled today.

---

## Wiring: `action-svc/main.go`

Today (existing code):
```go
notifier = feign.WrapNotifier(gate, notifier)
batcher := notifybatch.New(notifybatch.DefaultGrace, notifybatch.DefaultMaxWait, notifier)
sched := scheduler.New(cfg.SoulmanRoot, cfg.ReportSendTime, notifier, schedPublisher, gate)
```

After this change:
```go
notifier = feign.WrapNotifier(gate, notifier)

dndWindow := dnd.Window{Start: cfg.DNDStart, End: cfg.DNDEnd}
pendingPath := filepath.Join(cfg.SoulmanRoot, "logs", "dnd-pending.txt")
batcherNotifier := notifier
if cfg.DNDEnabled {
    batcherNotifier = dnd.WrapNotifier(dndWindow, pendingPath, notifier)
    dndFlusher := dnd.NewFlusher(dndWindow, pendingPath, notifier) // starts its own background loop
    dndFlusher.Start()
}
batcher := notifybatch.New(notifybatch.DefaultGrace, notifybatch.DefaultMaxWait, batcherNotifier)

sched := scheduler.New(cfg.SoulmanRoot, cfg.ReportSendTime, notifier, schedPublisher, gate)
```

`sched` keeps using the plain feign-wrapped `notifier`, unaffected by DND — matches the scope decision above. If `cfg.DNDEnabled` is false, `batcher` gets the same plain `notifier` the scheduler uses today, i.e. behavior is identical to pre-DND when the feature is off.

---

## Error Handling

| Failure | Behaviour |
|---|---|
| Pending-file append fails during the window | Fail open: send for real immediately, logged at `Warn`. Losing an alert is worse than one disturbed night. |
| Flush-time send fails (Discord API down at window-end) | Retried 3x (same backoff as `Scheduler.sendWithRetry`); pending file cleared regardless of outcome — no cross-day retry chain, avoids unbounded growth if Discord stays down for days. |
| Process restarts during the window | Pending file is on disk, survives naturally — no special handling needed. |
| Process restarts after window-end already passed, with stale pending content | Flushed immediately on `Start()` rather than waiting for tomorrow's window-end. |
| Malformed `start`/`end` config | Silent fallback to `10:00` (matches `scheduler.parseSendTime`'s existing loose convention) — not fatal. |
| `feign_mode` still `true` (e.g. in dev) | The flush send still passes through `feign.WrapNotifier`, so it's recorded to `feigned-actions.jsonl` like any other send — DND is fully exercisable in dev before prod's `feign_mode` ever flips. |

---

## Testing

- `dnd/window_test.go`: `Window.Active` across before/at-start/inside/at-end/after cases for both a non-wrapping window (`00:00`–`10:00`) and a wrapping one (`22:00`–`06:00`), via an injectable clock (mirrors `Scheduler.Now`'s override-for-tests pattern).
- `dnd/notifier_test.go`: `WrapNotifier`'s `Send` — inside the window, appends to the pending file and returns `nil` without calling the real notifier; outside the window, delegates straight through; a simulated file-append failure falls back to calling the real notifier.
- `dnd/flusher_test.go`: empty pending file → no send; non-empty → sends formatted content and clears the file; send failure → retries 3x then still clears; a flusher started while already outside the window with pending content → flushes immediately before entering the wait loop.
- `action-svc/config`: extend existing config tests for the new `do_not_disturb` block, including a malformed-time-falls-back-to-default case.
- End-to-end manual verification once deployed (per your request): inject a synthetic important stimulus (via the existing `POST /api/perceive/raw` debug endpoint or `soulman inject`) during the DND window and confirm it lands in the pending file instead of `feigned-actions.jsonl`, then confirm it flushes correctly once the window ends (or once manually triggered for testing — see Out of Scope).

---

## Out of Scope (this iteration)

- Actually flipping `feign_mode` to `false` in `config/prod.json` — a deployment decision made once this feature is built, tested, and verified working in dev, not part of this spec's implementation.
- A manual/admin-triggered "flush now" endpoint for testing without waiting for the real window-end time — testing instead relies on configuring a short window during manual verification (e.g. temporarily setting `end` a few minutes in the future) rather than adding new production surface area for a one-time testing convenience.
- Per-severity DND overrides (e.g. a "critical enough to interrupt anyway" tier) — not requested; DND applies uniformly to everything the `Batcher` handles.
- Gating the daily digest cron — explicit scope decision, see Summary.
- Any change to `notifybatch.Batcher` itself — it's unaware DND exists; from its point of view it's just calling `Send` on whatever notifier it was constructed with, same as always.

---

## Related

- `docs/superpowers/specs/2026-07-19-action-svc-feign-mode-design.md` — the `feign.Gate`/`WrapNotifier` pattern this design mirrors, and the flag this feature is a prerequisite for turning off in prod.
- `docs/superpowers/specs/2026-07-17-daily-report-delivery-design.md` — the `Scheduler` wake-loop and retry mechanics this design reuses.
- `docs/superpowers/specs/2026-07-27-log-error-perception-design.md` — the feature that made `action-svc`'s real-time Discord path apply broadly (not just Gmail triage), which is what makes DND worth having before `feign_mode` flips.
- `action-svc/NOTES.md` — feign mode, the notification-batching design, the real-time-notification generalization.
