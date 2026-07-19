# Perception Service Design (`perception-svc`)

**Date:** 2026-07-17
**Status:** Approved
**Language:** Go
**Phase:** Soulman Phase 2 — Perception module, first channel adapter (Folder Watcher)

---

## Summary

`perception-svc` is a long-running Go binary implementing the Perception module's runtime, following the same pattern as `memory-svc`. This iteration ships exactly one channel adapter — the **Folder Watcher** — which watches a configurable list of local directories for newly-created files and publishes each as a canonical `Stimulus` to the NATS message bus. `memory-svc` already subscribes to that subject and persists every stimulus to `raw_inputs`; Thinking will subscribe once built. `perception-svc` has no direct database dependency — durability comes from NATS JetStream plus a local checkpoint file that prevents re-publishing files the watcher has already seen.

---

## Architecture

```
Folder(s) on disk
      │  fsnotify (Create events)
      ▼
┌──────────────────┐        30s reconciliation scan
│  Folder Watcher   │◄───────(checkpoint diff, catches
└─────────┬─────────┘        missed events + backlog)
          │
          ▼
  Stimulus builder
          │
          ▼
  NATS JetStream publish
  (subject: soulman.stimulus.raw)

HTTP server (port 9001)
  GET /health
```

---

## Folder Watcher

### Detection

- `fsnotify` watch per configured directory (top-level only, not recursive).
- Trigger: file-create events. Modify/remove events are ignored this iteration.
- Backed by a periodic reconciliation scan (default 30s) that lists each directory and diffs against the checkpoint store — catches files created while the service was down, and files `fsnotify` misses (a known OS-level gap, e.g. on some network drives).

### Checkpoint Store

- Local JSON file at `$CHECKPOINT_PATH` (default `./checkpoints.json`).
- Shape: `{ "<folder_path>": { "<filename>": { "hash": "sha256:...", "mtime": "RFC3339", "published_at": "RFC3339" } } }`.
- A file is "new" if its filename is absent from the checkpoint, or present but its content hash differs (handles a file being replaced with new content under the same name).
- A checkpoint entry is written only **after** a successful NATS publish ack — a crash between publish and checkpoint write results in a harmless duplicate stimulus on restart (acceptable, matches the "some duplicates on restart" precedent already accepted elsewhere in `Perception module.md`).
- Files are **never** moved, renamed, or deleted by the watcher.

### Stimulus Construction

| Field | Value |
|---|---|
| `stimulus_id` | UUID v7, generated fresh per file |
| `schema_version` | `1` |
| `received_at` | now (UTC) |
| `occurred_at` | file's mtime |
| `channel` | `"folder-watcher"` |
| `source.identity` | `"folder-watcher"` |
| `source.authenticated` | `true` |
| `source.auth_method` | `"system"` |
| `content.raw_text` | file content, if valid UTF-8 text and < 1 MB; else `""` |
| `content.content_type` | `"text"` if inlined; `"binary"` otherwise |
| `content.attachments` | empty if inlined; else one entry with `filename`, best-effort `mime_type`, `size_bytes`, and `uri` = local file path |
| `channel_metadata.message_id` | `sha256(watched_path + filename + mtime)` — stable id for downstream dedup |
| `channel_metadata.channel_specific` | `{"watched_path": "<folder>"}` |
| `hints.tags` | `["error", "folder-watcher"]` |
| `hints.priority` | `"high"` |
| `hints.intent` | `null` |
| `override.is_override` | `false` |

`hints.tags` is hardcoded to `["error", "folder-watcher"]` because the only configured use case today is error folders. Per-folder configurable tags are out of scope for this iteration (see below) — if a non-error folder is ever added to `WATCH_PATHS`, this needs revisiting.

---

## Project Layout

**Update (2026-07-17, post-implementation):** the `model/` package described below was superseded by a shared `soulman/common` module (`common/stimulus.go`) once a schema mismatch between two services' independently-copied structs caused a real bug — see `error-report-action-design.md`'s Handoff correction. `perception-svc` now imports `common.Stimulus` via a `replace soulman/common => ../common` directive in its `go.mod` instead of defining its own copy.

```
soulman-dev/perception-svc/
├── main.go              # wiring: config → checkpoint store → watcher → nats publisher → http
├── go.mod               # module: soulman/perception-svc
├── config/
│   └── config.go        # Load() → Config from env vars
├── watcher/
│   ├── checkpoint.go     # checkpoint file read/write/diff
│   └── folderwatcher.go  # fsnotify subscription + reconciliation loop
├── natspublish/
│   └── publisher.go      # JetStream publish to soulman.stimulus.raw
└── httpserver/
    └── server.go         # chi router; /health only, this iteration
```

**Dependency flow** (no cycles): `model` ← `watcher`, `natspublish` ← `main`; `httpserver` ← `main`

---

## Configuration (env vars)

| Variable | Default | Notes |
|---|---|---|
| `NATS_URL` | `nats://localhost:4222` | |
| `HTTP_PORT` | `9001` | Matches the port `Perception module.md` reserves for the Perception server |
| `WATCH_PATHS` | `C:\Users\Lenovo\DigitalMe\errors` | Comma-separated list of absolute directory paths |
| `CHECKPOINT_PATH` | `./checkpoints.json` | |
| `RECONCILE_INTERVAL_SECONDS` | `30` | |

---

## NATS Publish

- Subject: `soulman.stimulus.raw` — the same subject `memory-svc` already consumes. No new stream or subject setup needed; the existing `STIMULUS` JetStream stream covers it.
- JetStream synchronous publish (`js.Publish`, waits for ack).
- On publish error: log, do not write a checkpoint entry — the next reconciliation scan (or a future fsnotify event on that file) retries.

---

## HTTP Endpoints

| Method | Path | This iteration |
|---|---|---|
| `GET` | `/health` | `{"status":"ok","nats":"connected","watched_paths":[...]}` |

---

## Error Handling

| Failure | Behaviour |
|---|---|
| Watched directory doesn't exist at startup | Log error, skip that directory, continue watching the others; retried automatically on the next reconciliation scan (in case it's created later) |
| NATS unavailable at startup | Log warning, HTTP server still starts; fsnotify events queue in-memory (bounded, see below) until reconnect |
| NATS publish fails mid-run | Log, leave checkpoint unset, rely on reconciliation retry |
| File deleted between fsnotify event and read | Log, skip (nothing to publish) |
| File actively being written (partial read) | Reconciliation scan re-checks the hash each tick; a file whose content is still changing is treated as "new" again until it stabilizes — settles within one interval for typical log-sized files |
| `checkpoints.json` unreadable/corrupt at startup | Log error, start with an empty checkpoint (re-publishes everything currently present once — acceptable, same precedent as above) |

In-memory fsnotify event queue is bounded to 100 pending events (mirrors `Perception module.md`'s `max_buffer_size` default); if exceeded, the reconciliation scan is relied upon instead of the dropped events.

---

## Out of Scope (this iteration)

- Any channel other than Folder Watcher (Webhook, CLI, Note Watcher, etc.)
- Per-folder configurable tags/priority (hardcoded to error semantics)
- Recursive directory watching
- Uploading large attachments to durable storage (Supabase Storage/S3) — attachment `uri` is a local file path only, valid as long as the file isn't moved
- A dedup cache — `channel_metadata.message_id` is set for downstream consumers to dedup on if needed, but `perception-svc` itself doesn't dedup beyond its own checkpoint
- Windows service registration

---

## Related

- [[Perception module]] — overall Perception design this service implements
- [[Messaging Bus]] — transport this service publishes onto
- `docs/superpowers/specs/2026-06-27-memory-svc-design.md` — the consumer of this service's output, and the structural pattern this spec follows
- `docs/superpowers/specs/2026-07-17-error-report-action-design.md` — what happens downstream to a `folder-watcher` stimulus
