# memory-svc — Operational Notes

Incidents, gotchas, and decisions learned running this service — not captured in the design specs themselves (see `CLAUDE.md`'s Services section for spec links).

## Episodes consumer has no file-log/replay layer

Unlike the STIMULUS consumer (`natsconsumer.Consumer`), `MemoryWriteConsumer` doesn't write to a local file log before acking — on a DB write failure it NAKs and relies purely on JetStream's own 30-day `MEMORY_WRITE` retention for redelivery. This was a deliberate first-cut simplification (see `docs/superpowers/specs/2026-07-18-memory-episodes-design.md`), not an oversight: episodes aren't the sacred immutable audit log `raw_inputs` is, so skipping the extra local-durability layer was an acceptable tradeoff against duplicating the STIMULUS consumer's more complex replay machinery for a second stream.

## Episode dedup uses JetStream stream sequence, not task_id

`action-svc`'s `OutcomeRecord.TaskID` is sometimes empty (the daily-report cron has no per-message correlation ID), so it can't be a unique dedup key. `episodes.stream_seq` (the MEMORY_WRITE message's JetStream stream sequence number, from `msg.Metadata().Sequence.Stream`) is used instead — `ON CONFLICT (stream_seq) DO NOTHING` on insert.

## The episodes table isn't created by memory-svc

Same as `raw_inputs`: `memory-svc` never runs its own DDL. The `episodes` table is applied by hand once per environment via `docs/superpowers/specs/sql/2026-07-18-episodes-table.sql`. As of this writing it's applied to `memory_dev` only — `memory_prod`'s schema doesn't exist yet at all (see root `CLAUDE.md`).

## Leveled logging (log/slog, added 2026-07-27)

All `log.Printf`/`log.Fatalf` call sites replaced with stdlib `log/slog` (`slog.Error`/`slog.Warn`/`slog.Info`) — no new dependency, Go 1.25 already ships it. Prompted by a 2.75GB `soulman-prod/logs/memory-svc-startup-err.log` that had silently accumulated undifferentiated retry noise from a genuine, ongoing Postgres outage with no way to grep signal from noise. `storage/writer.go`'s DB-insert failures and `natsconsumer/memory_write_consumer.go`'s episode-write failures — both symptoms of that outage — are now `slog.Error`; the "DB unavailable, written to file only" fallback path stays `slog.Warn`. Startup `log.Fatalf` calls became `slog.Error(...)` followed by an explicit `os.Exit(1)`, since slog has no Fatal-and-exit helper.

## Dependency health tracking (added 2026-07-27)

Postgres connectivity is tracked via a `common/dephealth.Registry`, wrapped by `storage.DBHolder` — every real Postgres call (insert, query, episode write) records its outcome, and `GET /health` now reports `dependencies.postgres` (`ok`/`down`, plus `since`/`detail` when down) instead of the old flat `db: "connected"/"unavailable"` field. A background `storage.Reconnector` ticks every 30s: while disconnected it retries `NewDB`; while connected it pings the existing pool. This is what makes a Postgres outage self-healing — before this, a failed startup connect left the DB `nil` for the process's entire lifetime, requiring a manual restart to recover even after Postgres came back. See `docs/superpowers/specs/2026-07-27-dependency-health-design.md`.

`perception-svc`'s `system_monitor` polls this service's `/health` via a new `internal_health` check (`config/dev.json`/`config/prod.json`) and notifies Discord on any dependency's `ok`↔`down` transition — not on every poll while steady, matching the Log Error channel's dedup philosophy.
