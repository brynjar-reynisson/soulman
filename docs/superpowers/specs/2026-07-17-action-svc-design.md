# Action Service Design (`action-svc`)

**Date:** 2026-07-17
**Status:** Approved
**Language:** Go
**Phase:** Soulman Phase 2 — first Action module runtime (single action type + one scheduled job)

---

## Summary

`action-svc` is the first runtime implementation of the Action module. v1 **consolidates** the Routing Agent, `fs-agent`, and `comm-agent` roles into a single Go binary — justified because only one action type (`append_daily_report_entry`) and one scheduled job (daily report delivery) exist so far. Splitting into separate LLM-driven sub-agent processes, as `Action module.md` envisions long-term, has no payoff until there's actual routing to do between multiple agents. This is a deliberate v1 simplification, not a rejection of the sub-agent architecture — the dispatch layer is structured so new action types (and eventually separate agent processes) can be added without reworking this service.

---

## Architecture

```
NATS core subscribe: soulman.thinking.request
        │
        ▼
  Action dispatcher (switch on action_hint)
        │
        ├─ "append_daily_report_entry" → report writer
        │        (per error-report-action-design.md)
        │              │
        │              ▼
        │        soulman.memory.write
        │        (fire-and-forget outcome log)
        │
        └─ (future action types go here)

Independent goroutine: daily cron, 10:00 AM
        │
        ▼
  Report reader + Notifier.Send()
  (per daily-report-delivery-design.md)

HTTP server (port 9004)
  GET /health
```

The NATS-driven dispatch path and the scheduled cron are independent — a `thinking-svc` or NATS outage doesn't prevent yesterday's report from being sent, and a stalled cron doesn't block new error entries from being logged.

---

## Action Dispatch

- Core NATS subscribe (not JetStream) — `soulman.thinking.request` is ephemeral per `Messaging Bus.md`; `action-svc` must be running to receive requests, the same accepted trade-off as `thinking-svc`'s publish side.
- Dispatches on `action_hint` (the field name `thinking-svc` actually publishes — see the corrected wire format in `error-report-action-design.md`'s Handoff section) via a simple map/switch. v1 has exactly one handler: `append_daily_report_entry`, implementing the behaviour from `docs/superpowers/specs/2026-07-17-error-report-action-design.md` verbatim (resolve `$SOULMAN_ROOT/reports/daily-report-<date>.txt`, create if missing, append the formatted entry).
- Unknown `action_hint`: log and drop (nothing else could send one, since `thinking-svc` only emits this one type in v1).
- After a successful append: publish a small outcome record to `soulman.memory.write` (fire-and-forget, per the existing Action → Memory write pattern) — `{"type": "action_log", "action_type": "append_daily_report_entry", "status": "success", "task_id": "..."}`. Nothing currently subscribes to `soulman.memory.write` (out of scope for `memory-svc` today) — this is forward-compatible logging, not a hard dependency.
- On failure: retry once immediately (per the Thinking rule's documented `fallback`), then publish the same outcome record with `"status": "failed"` and give up — no escalation back to Thinking in v1, matching the "a missed report entry isn't worth interrupting the human" decision already made in the error-report-action spec.

---

## Scheduled Job: Daily Report Delivery

Implements `docs/superpowers/specs/2026-07-17-daily-report-delivery-design.md` directly: a goroutine with a daily timer firing at `REPORT_SEND_TIME` (default `10:00`), checking yesterday's report file, sending its contents via the configured `Notifier` (Discord first) if non-empty.

---

## Project Layout

```
soulman-dev/action-svc/
├── main.go
├── go.mod                  # module: soulman/action-svc
├── config/
│   └── config.go           # NATS_URL, HTTP_PORT, SOULMAN_ROOT, REPORT_SEND_TIME, REPORT_NOTIFIER, DISCORD_*
├── dispatch/
│   ├── dispatch.go          # action_hint → handler map
│   └── report_entry.go      # append_daily_report_entry handler
├── report/
│   └── report.go            # shared report file path/format logic (used by dispatch and the cron)
├── notify/
│   ├── notifier.go          # Notifier interface
│   └── discord.go           # DiscordNotifier
├── scheduler/
│   └── daily.go             # 10:00 AM timer loop
├── natsclient/
│   ├── subscriber.go        # core NATS subscribe on soulman.thinking.request
│   └── publisher.go         # core NATS publish to soulman.memory.write
└── httpserver/
    └── server.go             # GET /health only
```

`report/report.go` is shared between the dispatch handler (writes) and the scheduler (reads) so the filename/date/path convention can't drift between the two.

---

## Configuration (env vars)

| Variable | Default | Notes |
|---|---|---|
| `NATS_URL` | `nats://localhost:4222` | |
| `HTTP_PORT` | `9004` | Next available port |
| `SOULMAN_ROOT` | `C:\Users\Lenovo\soulman-dev` | Shared convention with `thinking-svc`'s consumers and the error-report-action spec |
| `REPORT_SEND_TIME` | `10:00` | 24h local time |
| `REPORT_NOTIFIER` | `discord` | |
| `DISCORD_BOT_TOKEN` | — | Required if `REPORT_NOTIFIER=discord` |
| `DISCORD_CHANNEL_ID` | — | Required if `REPORT_NOTIFIER=discord` |

---

## Error Handling

| Failure | Behaviour |
|---|---|
| NATS unavailable at startup | Log warning; HTTP server and the daily cron still start (the cron doesn't depend on NATS) — only the dispatch side is degraded until reconnect |
| Report-writing failures (disk full, concurrent writes, etc.) | See `error-report-action-design.md` — this service implements those behaviours as specified, no runtime-level changes |
| Notifier send failures (Discord API down, rate-limited, etc.) | See `daily-report-delivery-design.md` — same, implemented as specified |

---

## Out of Scope (this iteration)

- Separate Routing Agent / `fs-agent` / `comm-agent` processes — consolidated into one binary (see Summary)
- Any action type other than `append_daily_report_entry`
- `soulman.action.<agent>` sub-routing subjects — not needed until there's more than one handler
- Guard Agent risk-gating — v1's only action is `risk_level: "low"`, no gate needed yet

---

## Related

- `docs/superpowers/specs/2026-07-17-error-report-action-design.md` — logical design for the dispatch handler
- `docs/superpowers/specs/2026-07-17-daily-report-delivery-design.md` — logical design for the scheduled job
- `docs/superpowers/specs/2026-07-17-thinking-svc-design.md` — publishes the requests this service consumes
- [[Action module]] — the full eventual sub-agent architecture this is a first slice of
