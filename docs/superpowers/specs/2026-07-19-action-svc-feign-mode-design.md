# action-svc Feign Mode Design

**Date:** 2026-07-19
**Status:** Approved
**Phase:** action-svc — a configurable dry-run mode for outbound side effects

---

## Summary

`action-svc` currently has one class of outbound, external side effect: sending a Discord message via `notify.Notifier` (used by both the 10:00 AM daily-report cron and the gmail-triage important-email batcher, which already share one `Notifier` instance constructed once in `main.go`). This design adds a **feign mode**: when enabled, any component that would send a real notification instead records what it *would* have sent — to an append-only file and to the existing `episodes` Postgres pipeline (via `common.OutcomeRecord`, already built) — without making the real call.

The mechanism is a small, reusable `Gate` type (new package `action-svc/feign`), not a one-off Discord-specific flag: today's only integration point (`notify.Notifier`) wraps itself with the Gate in one line; any future outbound integration (email, webhook, SMS, ...) adopts the same one-line convention when it's built, rather than needing new bespoke suppression logic each time.

Both `soulman-dev` and `soulman-prod` are turned on (`feign_mode: true`) as part of this change, per explicit request — no real Discord messages should go out from either environment until this is turned back off.

---

## Part 1: The `Gate`

New package `action-svc/feign`:

```go
// Gate decides whether an outbound side effect actually happens, or is
// only recorded. Any component with an external side effect to gate wraps
// itself around a shared *Gate (see WrapNotifier below) — this is the one
// reusable mechanism new integrations adopt, rather than each inventing
// its own suppression flag.
type Gate struct {
    enabled bool
    logPath string
    mu      sync.Mutex
}

func New(enabled bool, logPath string) *Gate

// Enabled reports whether feign mode is on. Components that need to phrase
// an outcome record differently depending on mode (see Part 2) call this
// directly, since a feigned action and a real one both "succeed" from the
// caller's point of view and can't be told apart by return value alone.
func (g *Gate) Enabled() bool

// Record appends one feigned-action entry to logPath as a single JSON
// line (mirroring memory-svc's raw_inputs.jsonl convention): {"timestamp":
// "...", "kind": "notify", "detail": "<message that would have been
// sent>"}. Safe for concurrent use.
func (g *Gate) Record(kind, detail string) error
```

Default log path: `$SOULMAN_ROOT/logs/feigned-actions.jsonl`, append-only, created if missing.

### `Notifier` wrapping

```go
// WrapNotifier returns a notify.Notifier that delegates to real when the
// gate is disabled, and records (instead of sending) when enabled. The
// wrapped Notifier is indistinguishable from a real one at the call site —
// notifybatch.Batcher and scheduler.Scheduler need zero code changes for
// this half of the feature.
func WrapNotifier(gate *Gate, real notify.Notifier) notify.Notifier
```

`main.go` always wraps: `notifier = feign.WrapNotifier(gate, notify.NewDiscordNotifier(...))`. When the gate is disabled this is a transparent passthrough (real send happens exactly as today); when enabled, `Send` calls `gate.Record("notify", message)` instead of making the Discord HTTP call, and returns whatever `Record` returns (nil on success).

---

## Part 2: Keeping `episodes` honest

`dispatchGmailTriage` (`action-svc/dispatch/gmail_triage.go`) and `scheduler.RunOnce` (`action-svc/scheduler/daily.go`) both build a `common.OutcomeRecord` whose `Decision`/`Summary` text currently *asserts* a real send happened ("notified via Discord", "Daily report delivered") whenever the underlying `Notifier.Send` call returns `nil` — which it now will for a feigned send too. Both need the same `*feign.Gate` passed in so they can ask `gate.Enabled()` when composing that text:

- `dispatch.New(root, publisher, batcher, gate)` — `dispatchGmailTriage`'s `Decision` becomes `"feigned notify via Discord"` (was `"notified via Discord"`) when `p.Important && gate.Enabled()`. Unaffected: the not-important case (`"logged only"`), and `dispatchAppendDailyReportEntry` (no Discord side effect exists there to feign).
- `scheduler.New(root, sendTime, notifier, publisher, gate)` — `RunOnce`'s success-path `Summary` becomes `"Daily report delivery feigned"` (was `"Daily report delivered"`) when `gate.Enabled()`. The failure-path `Summary` (`"Daily report delivery failed: %v"`) is unaffected — feign mode only changes what happens on a would-be-successful send, not failure handling.

This way `GET /memory/episodes` (built earlier) already shows, per row, whether an outcome was real or feigned — no schema change, just accurate text through the pipeline that already exists.

---

## Part 3: Config

`common/sharedconfig.Config` gains a flat top-level field (non-secret, environment-specific — exactly what this file is for): `FeignMode bool \`json:"feign_mode"\``. Defaults to `false` when absent, matching every other optional field in this schema.

`action-svc/config.Config` gains `FeignMode bool`, sourced directly from `shared.FeignMode` — no environment variable, no default-override helper. (Per explicit direction: `.env` stays reserved for secrets; behavior toggles belong in the versioned per-environment JSON config, `config/dev.json` / `config/prod.json`.)

`config/dev.json` and `config/prod.json` both get `"feign_mode": true` added as part of this change.

### `main.go` wiring

```go
gate := feign.New(cfg.FeignMode, filepath.Join(cfg.SoulmanRoot, "logs", "feigned-actions.jsonl"))

var notifier notify.Notifier = notify.NewDiscordNotifier(cfg.DiscordBotToken, cfg.DiscordChannelID)
notifier = feign.WrapNotifier(gate, notifier)

batcher := notifybatch.New(notifybatch.DefaultGrace, notifybatch.DefaultMaxWait, notifier) // unchanged
disp := dispatch.New(cfg.SoulmanRoot, dispatchPublisher, batcher, gate)                    // +gate
sched := scheduler.New(cfg.SoulmanRoot, cfg.ReportSendTime, notifier, schedPublisher, gate) // +gate
```

The startup log line gains a `feign_mode=%v` field alongside the existing `notifier=%s` field, so which mode a running instance is in is visible in its own log at a glance.

---

## Part 4: Testing

- `action-svc/feign` (new): `Gate.Enabled()` reflects the constructor argument; `Gate.Record` appends a well-formed JSON line to a real temp-dir file (not mocked); `WrapNotifier` — when the gate is enabled, the wrapped real `Notifier`'s `Send` is never called and `Record` is; when disabled, the real `Send` is called and `Record` is not.
- `action-svc/dispatch`: extend the gmail-triage tests to cover both `Decision` phrasings, using a real `*feign.Gate` constructed with `t.TempDir()` (matching this test file's existing use of real `report.Append`, not mocks).
- `action-svc/scheduler`: same idea for `RunOnce`'s `Summary`, gate enabled vs. disabled.
- `common/sharedconfig`: `Load()` test confirming `feign_mode` parses `true`, parses `false`, and defaults to `false` when the key is absent.
- `action-svc/config`: `Load()` test confirming `cfg.FeignMode` is sourced correctly from the shared config.

---

## Out of scope (explicitly deferred)

- Per-integration granularity — one global on/off flag for now, since there's exactly one shared `Notifier` instance today; per-integration control is future work once a second integration exists.
- Runtime toggle (HTTP endpoint, hot-reload, SIGHUP) — config-file only, takes effect on service restart.
- Retroactive relabeling of `episodes` rows written before this change.
- Any change to `report.Append`/report-file writing — those remain always-on; feign mode only gates the `Notifier`-level external send.
- A reader/viewer tool for `feigned-actions.jsonl` — inspect manually for now (`Get-Content` / `nats`-style manual inspection, matching the existing accepted precedent for `MEMORY_WRITE`).
- Actually turning services back on / restarting them — this design only changes code and config; no process management is performed as part of this work.
