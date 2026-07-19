# Daily Report Delivery Design (Action `comm-agent` cron)

**Date:** 2026-07-17
**Status:** Approved
**Phase:** Soulman Phase 2 — first scheduled (non-Thinking-triggered) Action task

---

## Summary

A scheduled job inside the Action module's `comm-agent` checks, once a day, whether yesterday's daily report file is non-empty, and if so sends its full contents through a pluggable `Notifier`. This bypasses Perception and Thinking entirely — deterministic, mechanical, no interpretation needed — and is documented here as an explicit, intentional exception to "all action is Thinking-dispatched."

---

## Trigger

- A local cron/timer inside `comm-agent`, firing daily at **10:00 AM** (local time).
- Not modeled as a Stimulus, not routed through Thinking. `comm-agent` runs its own self-contained scheduling loop for this one job — the same centralized-scheduler pattern Perception's Poll Scheduler uses, just living in Action instead.

---

## Behaviour

1. At 10:00 AM, compute yesterday's date and resolve `$SOULMAN_ROOT/reports/daily-report-<YYYY-MM-DD>.txt`.
2. If the file doesn't exist, or is empty / whitespace-only → do nothing.
3. If non-empty → read the full contents, send via the configured `Notifier`, and log the send outcome (success/failure) to episodic memory via the existing fire-and-forget Memory write path.
4. The file is **never modified or deleted** by this job — dated filenames are their own archive. Meanwhile, today's file is independently accumulating tomorrow's report via the `fs-agent` action described in the error-report-action spec.

---

## Notifier Interface

```go
type Notifier interface {
    Send(message string) error
}
```

Selected via config (`REPORT_NOTIFIER=discord`), extensible to `sms`, `email`, etc. later — adding a notifier means implementing the interface and registering it in a small factory switch, no changes to the scheduling or file-reading logic.

### `DiscordNotifier` (first implementation)

- Sends the report as a message via Discord's Bot REST API (`POST /channels/{channel_id}/messages`), using the bot token already configured for this environment's Discord integration.
- Config: `DISCORD_BOT_TOKEN`, `DISCORD_CHANNEL_ID` (a DM channel with the user, or a dedicated private channel — resolved once at setup time, not looked up dynamically).
- If the message exceeds Discord's 2000-character limit, split into multiple sequential messages at blank-line boundaries (never mid-entry).

---

## Configuration (env vars)

| Variable | Default | Notes |
|---|---|---|
| `SOULMAN_ROOT` | `C:\Users\Lenovo\soulman-dev` | Same variable and resolution as the error-report-action spec — must match so both jobs agree on where `reports/` lives |
| `REPORT_SEND_TIME` | `10:00` | 24h local time |
| `REPORT_NOTIFIER` | `discord` | |
| `DISCORD_BOT_TOKEN` | — | Required if `REPORT_NOTIFIER=discord` |
| `DISCORD_CHANNEL_ID` | — | Required if `REPORT_NOTIFIER=discord` |

---

## Error Handling

| Failure | Behaviour |
|---|---|
| Report file unreadable | Log error, skip this send, try again tomorrow — the file isn't lost, just not sent; can be checked manually in `reports/` |
| Notifier send fails (e.g. Discord API down/rate-limited) | Retry with backoff (3 attempts, exponential); if all fail, log failure to episodic memory — no further escalation this iteration, a missed daily digest isn't safety-critical |
| Both dev and prod environments running the cron | Each environment's `comm-agent` sends its own copy independently based on its own `reports/` dir — acceptable duplication during development; disable the dev-side cron once prod is authoritative (an operational/deployment note, not a design requirement) |

---

## Out of Scope (this iteration)

- Notifiers other than Discord (SMS, email) — the interface supports them, but implementations don't exist yet
- Report content beyond error-folder entries (e.g. mixing in other daily digest content)
- Configurable send time via anything other than an env var + restart (no live-reload)
- Delivery acknowledgement/read-receipt tracking

---

## Related

- `docs/superpowers/specs/2026-07-17-error-report-action-design.md` — writes the file this job reads
- [[Action module]] — `comm-agent` role and general Action module design
