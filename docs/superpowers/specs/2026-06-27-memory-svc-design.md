# Memory Service Design (`memory-svc`)

**Date:** 2026-06-27
**Status:** Approved
**Language:** Go
**Phase:** Soulman Phase 1 — Memory module, perception-to-storage pipeline

---

## Summary

`memory-svc` is a single long-running Go binary that bridges the NATS message bus and the Supabase Postgres database for the Memory module. It has two concurrent responsibilities:

1. **NATS consumer** — subscribes to the STIMULUS JetStream stream, parses incoming Stimulus JSON, and persists each message through the write pipeline (file log first, then Postgres)
2. **HTTP server** — exposes a health check and a raw inputs retrieval endpoint; all other retrieve endpoints (search, episodes, procedures, goals) are stubbed with `501 Not Implemented` for now, to be filled in as Thinking is built

---

## Architecture

```
NATS (STIMULUS stream)
        │
        ▼
  nats/consumer.go
  Parse JSON → Stimulus
        │
        ▼
  storage/writer.go
  ┌─────────────────────────────┐
  │ 1. Append to raw_inputs.jsonl (_sync: "pending")  │
  │ 2. Insert into memory_dev.raw_inputs              │
  │ 3. Mark file entry _sync: "ok"                    │
  │    (on DB failure: leave "pending", retry later)  │
  └─────────────────────────────┘
        │
        ▼
  ACK NATS message

HTTP server (port 9002)
  GET /health
  GET /raw-inputs/recent
  GET /memory/search          → 501
  GET /memory/episodes        → 501
  GET /memory/procedures      → 501
  GET /memory/goals           → 501
```

---

## Write Pipeline

Every incoming Stimulus goes through this sequence in `storage/writer.go`:

1. **Append `_type:"stimulus"` record to `raw_inputs.jsonl`** — synchronous, blocking. If this fails, return an error; do not ACK the NATS message (it will be redelivered).
2. **Insert into `memory_dev.raw_inputs`** via pgx. If this succeeds, append a `_type:"synced"` record to the file.
3. **If DB insert fails**: no synced record is appended. ACK the NATS message anyway (the stimulus is already durable in the file). The entry will be replayed on the next DB reconnect.

### Startup replay

On startup, before subscribing to NATS, `memory-svc` scans `raw_inputs.jsonl` for any entries with `_sync: "pending"` and attempts to insert them into Postgres. Only after replay completes does it begin consuming new messages.

---

## File Log (`raw_inputs.jsonl`)

- Location: `$LOG_DIR/raw_inputs.jsonl` (default: `./logs/raw_inputs.jsonl`)
- Format: newline-delimited JSON — two record types, both append-only:
  - `{"_type":"stimulus","stimulus_id":"...","received_at":"...", ...full Stimulus fields...}`
  - `{"_type":"synced","stimulus_id":"..."}` — appended when the DB write succeeds
- Rotation: when file size exceeds 10 MB, rename to `raw_inputs.jsonl.1` and start a new file. Only one rotation file is kept (`.1` is overwritten on next rotation).
- The file is strictly append-only — no line is ever modified or deleted. Sync state is derived by scanning for `_type:"synced"` records whose `stimulus_id` matches a `_type:"stimulus"` record.

---

## Project Layout

```
soulman-dev/
├── memory/              ← existing OpenCode agent workspace (unchanged)
└── memory-svc/          ← new
    ├── main.go          # wiring: config → storage → nats consumer + http server
    ├── go.mod           # module: soulman/memory-svc
    ├── config/
    │   └── config.go    # reads env vars, validates, exposes Config struct
    ├── storage/
    │   ├── postgres.go  # pgx pool, InsertRawInput(Stimulus), ReplayPending()
    │   ├── filelog.go   # Append, ScanPending, MarkSynced, Rotate
    │   └── writer.go    # Write(Stimulus) — orchestrates filelog + postgres
    ├── nats/
    │   └── consumer.go  # JetStream push consumer on STIMULUS stream; calls writer.Write
    └── http/
        └── server.go    # chi router; /health, /raw-inputs/recent, 501 stubs
```

---

## Configuration (env vars)

| Variable | Default | Notes |
|----------|---------|-------|
| `NATS_URL` | `nats://localhost:4222` | |
| `DATABASE_URL` | `postgres://postgres:postgres@localhost:54322/postgres` | Supabase local Postgres port |
| `HTTP_PORT` | `9002` | |
| `LOG_DIR` | `./logs` | Directory for raw_inputs.jsonl |
| `SCHEMA` | `memory_dev` | Postgres schema name |

---

## HTTP Endpoints

| Method | Path | This iteration |
|--------|------|----------------|
| `GET` | `/health` | `{"status":"ok","nats":"connected","db":"connected"}` |
| `GET` | `/raw-inputs/recent?limit=N` | Last N rows from `memory_dev.raw_inputs`, ordered by `received_at DESC`. Default limit 20, max 100. |
| `GET` | `/memory/search` | `501 Not Implemented` |
| `GET` | `/memory/episodes` | `501 Not Implemented` |
| `GET` | `/memory/procedures` | `501 Not Implemented` |
| `GET` | `/memory/goals` | `501 Not Implemented` |

---

## Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/nats-io/nats.go` | NATS client + JetStream |
| `github.com/jackc/pgx/v5` | Postgres client (pgx pool) |
| `github.com/go-chi/chi/v5` | HTTP router |

No ORM. Raw SQL against the existing `memory_dev` schema from `plan.md`.

---

## Stimulus → raw_inputs Mapping

The NATS message payload is a JSON-encoded Stimulus (as defined in `Perception module.md`). Fields map to `memory_dev.raw_inputs` as follows:

| Stimulus field | raw_inputs column |
|---------------|-------------------|
| `stimulus_id` | `stimulus_id` (UUID PK) |
| `received_at` | `received_at` |
| `occurred_at` | `occurred_at` |
| `channel` | `channel` |
| `source.identity` | `source_identity` |
| _(full message)_ | `raw_payload` (JSONB) |
| `content.raw_text` | `normalized_text` |
| `override.is_override` | `is_override` |
| `override.command` | `override_cmd` |

---

## Error Handling

| Failure | Behaviour |
|---------|-----------|
| NATS message unparseable | Log error, ACK message (don't block consumer on bad data) |
| File log write fails | Log error, do NOT ACK NATS message (triggers redelivery) |
| Postgres insert fails | Leave file entry as `_sync: "pending"`, ACK NATS message, schedule retry |
| Postgres down at startup | Log warning, proceed — replay will catch up when DB reconnects |
| File log exceeds 10 MB | Rotate, log event |

---

## Out of Scope (this iteration)

- Embedding generation (pgvector search)
- `/memory/episodes`, `/memory/search`, `/memory/procedures`, `/memory/goals` endpoints
- Action log writes
- Windows service registration for `memory-svc`
- Metrics / observability beyond structured log lines
