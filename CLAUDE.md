# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Repository Is

This is an **Obsidian vault** that serves as the design and planning hub for **Project Soulman** — a personal AI agent that perceives input from multiple channels, thinks about what matters, consults its memory for context, and acts within human-controlled boundaries.

The vault contains design documents, module specs, agent definitions, and sync scripts. It also contains the **source code** for each backend service (`memory-svc/`, `perception-svc/`, `thinking-svc/`, `action-svc/` — one Go module per service, each following the same spec → plan → build process) plus a shared `common/` Go module and a `cli/` module. `~/soulman-dev/` and `~/soulman-prod/` are runtime environments only (config, data, logs) — services are built from this vault and run against one or the other via environment variables, they are not checked out there.

This file is intentionally an index, not an encyclopedia: each service/module below gets a short summary, a list of the specs that fully explain it, and a link to its own `NOTES.md`. **Read a component's `NOTES.md` before making non-trivial changes to it** — specs describe what was designed and approved; notes describe what was actually learned running it (real incidents, gotchas, deferred bugs). Specs are the historical record of a decision and are not edited after the fact; notes accumulate.

## Repository Structure

| Path                            | Purpose                                                                         |
| ------------------------------- | ------------------------------------------------------------------------------- |
| `*.md` (root)                   | Module design docs — one per system module                                      |
| `Project Soulman.md`            | System overview: architecture, data contracts, module interactions              |
| `memory/Implementation Plan.md` | Current implementation plan — the source of truth for what to build next        |
| `memory/.opencode/agent/*.md`   | OpenCode agent definitions for DB operations (builder, evolve, retrieve, store) |
| `memory/opencode.json`          | OpenCode MCP config — points to Supabase at `http://127.0.0.1:54321/mcp`        |
| `.agent-suite-mcp.json`         | Claude Code MCP config — obsidian and filesystem servers for this vault         |
| `sync-soulman-dev.cmd`          | Syncs design artifacts from vault → `~/soulman-dev/memory/`                     |
| `sync-soulman-prod.cmd`         | Syncs design artifacts from vault → `~/soulman-prod/memory/`                    |
| `setup-firewall-rules.ps1`      | One-time (elevated) setup: pre-creates Windows Firewall inbound-allow rules for all 10 service exes (5 services × dev/prod) so rebuild+restart never re-triggers the "app blocked" prompt |
| `docs/specs/`                   | Detailed specs (currently sparse)                                               |
| `docs/superpowers/specs/`       | Approved design specs — source of truth for each feature, one file per feature  |
| `docs/superpowers/plans/`       | Task-by-task implementation plans, one per spec                                 |
| `config/`                       | Per-environment JSON config files (`dev.json`, `prod.json`) — non-secret settings shared across services, copied to `<env-root>\config.json` by each `run-<svc>.ps1` |
| `common/`                       | Go module — shared wire-format types (`Stimulus`, `ActionRequest`) and the `sharedconfig` schema. See `common/NOTES.md`. |
| `cli/`                          | Go module — `soulman` CLI tool. See `cli/NOTES.md`.                            |
| `memory-svc/`                   | Go service — Memory module runtime (`:9002`). See `memory-svc/NOTES.md`.        |
| `perception-svc/`               | Go service — Perception module runtime (`:9001`). See `perception-svc/NOTES.md`. |
| `thinking-svc/`                 | Go service — Thinking module runtime (`:9003`). See `thinking-svc/NOTES.md`.    |
| `action-svc/`                   | Go service — Action module runtime (`:9004`). See `action-svc/NOTES.md`.        |
| `web-svc/`                      | Go service — Web dashboard backend runtime (`:9005`). See `web-svc/NOTES.md`. |
| `web/`                          | React + Vite frontend — Soulman's web dashboard. See `web/README.md` (if present) or `web-svc/NOTES.md` for the auth flow. |

## Services

Four Go services, each an independent module at the vault root, each built via its own spec(s) and implementation plan(s) below. All four require `NATS_URL` (default `nats://localhost:4222`).

1. **`memory-svc`** — consumes `soulman.stimulus.raw`, writes to `raw_inputs.jsonl` then Postgres (`memory_dev`/`memory_prod` schema). Also durably consumes `action-svc`'s `soulman.memory.write` outcome records into an `episodes` table, exposed read-only via `GET /memory/episodes`; `/memory/search`, `/memory/procedures`, `/memory/goals` remain unimplemented stubs.
   - Specs: `2026-06-27-memory-svc-design.md`, `2026-07-18-memory-episodes-design.md`
   - Notes: `memory-svc/NOTES.md`

2. **`perception-svc`** — normalizes external input into `Stimulus` events on `soulman.stimulus.raw`. Three input channels: **folder-watcher** (`fsnotify` on paths from the shared config file's `watch_paths`), **Gmail** (`gmailwatcher` package — polls the inbox via OAuth2 offline refresh token, dedups via a per-environment Gmail label), and **System Monitor** (`sysmonitor` package — polls disk/memory/CPU via `golang.org/x/sys/windows` plus external `service_health` targets via TCP dial/HTTP GET, publishes only on a severity transition). Also serves `POST /api/perceive/cli` (CLI push channel) and `POST /api/perceive/raw` (generic Stimulus injection for debugging).
   - Specs: `2026-07-17-perception-svc-design.md`, `2026-07-18-gmail-channel-design.md`, `2026-07-18-soulman-cli-design.md`, `2026-07-18-pipeline-debugging-tools-design.md`, `2026-07-18-system-monitor-channel-design.md`, `2026-07-19-system-monitor-service-health-design.md`, `2026-07-20-system-monitor-dashboard-panel-design.md`
   - Notes: `perception-svc/NOTES.md` — real incidents (padded Gmail base64 bodies, a blocking-startup-poll bug, the unbounded-backlog incident that motivated the debugging tools)

3. **`thinking-svc`** — matches stimuli against a rule table, publishes an Action Request to `soulman.thinking.request` (durable JetStream stream). Rules today: `folder-watcher`, `cli-note`, and `system-monitor` → mechanical report-entry (no LLM); `gmail` → DeepSeek-judged importance triage, always logs, notifies Discord (batched) only if judged important. `DEEPSEEK_API_KEY` is non-fatal if blank (logs a warning; DeepSeek calls then fail and summarization falls back to deterministic text) but the Gmail triage classifier needs it to actually classify anything.
   - Specs: `2026-07-17-thinking-svc-design.md`, `2026-07-18-gmail-triage-action-design.md`, `2026-07-18-system-monitor-channel-design.md`
   - Notes: `thinking-svc/NOTES.md` — the classifier prompt was rewritten with explicit criteria after real false positives (newsletters flagged important)

4. **`action-svc`** — dispatches `soulman.thinking.request` actions via a durable JetStream consumer: `append_daily_report_entry` (writes to `$SOULMAN_ROOT/reports/`) and `triage_gmail_email` (report entry + debounced batched Discord notify if important). Independently runs a 10:00 AM cron sending the previous day's report via a pluggable `Notifier` (Discord). `DISCORD_BOT_TOKEN`/`DISCORD_CHANNEL_ID` are non-fatal if blank (Send fails, retried/logged like any other notifier failure) — configured in dev and prod as of 2026-07-18 (a dedicated "Soulman Reports" bot). As of 2026-07-19, `feign_mode` is `true` in both `config/dev.json` and `config/prod.json`, so outbound sends are currently recorded to `logs/feigned-actions.jsonl` instead of actually happening — see `action-svc/NOTES.md`.
   - Specs: `2026-07-17-action-svc-design.md`, `2026-07-17-daily-report-delivery-design.md`, `2026-07-17-error-report-action-design.md`, `2026-07-18-gmail-triage-action-design.md`, `2026-07-18-pipeline-debugging-tools-design.md`, `2026-07-19-action-svc-feign-mode-design.md`
   - Notes: `action-svc/NOTES.md` — the incident that motivated durable queues, the notification-batching design, a known deferred bug (dev/prod share one Discord bot), feign mode

5. **`web-svc`** — the only Soulman service reachable from a browser: CORS-enabled, verifies Supabase-issued JWTs (reusing `agent-suite`'s existing hosted Supabase project and Google OAuth client), and authorizes a single configured owner email (`web.owner_email` in shared config) — no roles table. Serves `GET /api/status` (aggregates `/health` from the other four services), `GET /api/episodes` and `GET /api/raw-inputs/recent` (proxy `memory-svc`), `GET /api/system-monitor` (proxies `perception-svc`'s System Monitor check status), and `GET /api/reports/latest` / `GET /api/reports?date=` (reads `$SOULMAN_ROOT/reports/*.txt` directly). Does not touch NATS at all. Override/control dispatch (PAUSE/STOP/RESUME) is explicitly not implemented here — blocked on a Guard Agent design that doesn't exist yet.
   - Specs: `2026-07-19-soulman-web-dashboard-design.md`, `2026-07-20-system-monitor-dashboard-panel-design.md`
   - Notes: `web-svc/NOTES.md`

### Running dev and prod simultaneously

Dev and prod share one local NATS server and one local Postgres instance. Each service's NATS subjects, JetStream durable consumer name, and HTTP port are configurable — subjects and `consumer_names` (`memory_svc`, `thinking_svc`, `action_svc`) come from the shared config file, `HTTP_PORT` from env. Prod keeps unprefixed defaults (`soulman.stimulus.raw`, ports `9001`-`9004`); dev uses `soulman.dev.*` subjects, `*-dev` consumer names, and ports `9011`-`9014`.

**This is essential, not cosmetic**: JetStream identifies a durable consumer by `(stream, name)`, so two environments reusing the same consumer name on the same stream would silently steal each other's messages — every consumer also sets `FilterSubject` so it only ever sees its own environment's traffic. `STIMULUS` (provisioned manually via the `nats` CLI), `THINKING_REQUEST`, and `MEMORY_WRITE` (both idempotently created/updated in code via `CreateOrUpdateStream`) are all durable JetStream streams, each one's subject list covering both the prod and `soulman.dev.*` variants.

`web-svc` follows the same port convention (`9005` prod / `9015` dev) but has no JetStream consumer and no NATS subscription at all — it only makes outbound HTTP calls to the other four services and reads report files directly off disk, so it needs no `consumer_names` entry and isn't part of the STIMULUS/THINKING_REQUEST/MEMORY_WRITE stream discussion above.

`soulman-prod/` mirrors `soulman-dev/`'s layout exactly but was provisioned later and has no credentials filled in yet; `memory_prod`'s Postgres schema doesn't exist yet either, so prod's `memory-svc` logs to file but every DB insert fails until that schema is created.

### Startup

`C:\Users\Lenovo\start-everything.ps1` (via `Start Everything.lnk` in the Windows Startup folder) builds and starts all five services (including `web-svc`) in both `soulman-dev` and `soulman-prod` on every login — a git pull here is picked up on the next login without a separate deploy step. `setup-firewall-rules.ps1` (run once, elevated) pre-creates the Windows Firewall rules each service's `http.ListenAndServe` needs so a rebuild doesn't re-trigger the "app blocked" prompt.

`start-everything.ps1` also runs `web` (the frontend) per environment via the same generic launcher, but its `run-web.ps1` differs from the Go services' pattern: instead of building in place, it `robocopy /MIR`s `web/` from the vault into a private per-environment copy (`<env-root>\web\`, excluding `node_modules`/`dist`) before running `npm ci`, so dev's and prod's installed dependencies and build output never collide — mirroring the isolation each Go service already gets from building into its own `bin/`. Dev then runs `npm run dev` (Vite dev server); prod runs `npm run build && npm run preview`.

### Shared modules

- **`common`** — `Stimulus`, `ActionRequest`, and `OutcomeRecord` wire-format types (imported via `replace soulman/common => ../common` in each service's `go.mod`) plus the `sharedconfig` schema for `config/dev.json`/`config/prod.json`. Specs: `2026-07-17-common-module-design.md`, `2026-07-18-shared-config-design.md`, `2026-07-18-shared-config-nats-design.md`, `2026-07-18-memory-episodes-design.md`. Notes: `common/NOTES.md`.
- **`cli`** — the `soulman` command-line tool: `soulman note "<text>"` / `soulman "<text>"` (the CLI push channel), `soulman inject <file>` and `soulman discord-history --limit N` (debugging tools). Specs: `2026-07-18-soulman-cli-design.md`, `2026-07-18-pipeline-debugging-tools-design.md`. Notes: `cli/NOTES.md`.

## System Architecture (Four Modules)

```
Perception → Thinking → Action
              ↕
            Memory
```

1. **Perception** (`Perception module.md`) — Normalizes all external input into a canonical `Stimulus` JSON. No interpretation. Runs as a dedicated server on `:9001`.
2. **Thinking** (`Thinking module.md`) — The only module with agency. Classifies intent, reasons, queries Memory, and produces action requests.
3. **Memory** (`Memory module.md`) — Supabase (Postgres + pgvector). Self-building schema managed by an LLM DB Agent. Immutable raw input log + episodic/semantic/procedural memory.
4. **Action** (`Action module.md`) — Routing Agent + sub-agents (db-agent, fs-agent, web-agent, comm-agent, code-agent, guard-agent). Each sub-agent scoped to its own MCP tools.

Key boundary: `Stimulus` is the only format that crosses Perception → Thinking. Action Requests are the only format crossing Thinking → Action.

## Design Workflow

1. Design and spec work happens **in this vault** (Obsidian notes).
2. Run `sync-soulman-dev.cmd` to push `Implementation Plan.md`, module design docs, agent definitions, and `opencode.json` to `~/soulman-dev/memory/`.
3. OpenCode (AI code assistant) runs inside `~/soulman-dev/memory/` against a local Supabase instance.
4. Use `sync-soulman-prod.cmd` for the production workspace.

The `memory/CLAUDE.md` is intentionally empty — it exists only to prevent OpenCode from falling back to `~/.claude/CLAUDE.md` (outside the sandbox).

## Two Environments

| Env | Path | Supabase Schema |
|-----|------|-----------------|
| Dev | `~/soulman-dev/memory/` | `memory_dev` |
| Prod | `~/soulman-prod/memory/` | `memory_prod` |

Agent definitions in `memory/.opencode/agent/` resolve the schema based on which directory they're invoked from.

## Key Design Constraints

- **Raw input log is immutable** — corrections are appended, never overwrites.
- **Override commands** (PAUSE/STOP/RESUME) bypass Thinking but still go through the Guard Agent.
- **Default closed** — every channel must authenticate; every action must pass Guard + Budget Tracker.
- **Implementation order**: Memory → Perception → Thinking → Action (dependency order).
