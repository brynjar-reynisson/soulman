# memory-svc — Operational Notes

Incidents, gotchas, and decisions learned running this service — not captured in the design specs themselves (see `CLAUDE.md`'s Services section for spec links).

## Episodes consumer has no file-log/replay layer

Unlike the STIMULUS consumer (`natsconsumer.Consumer`), `MemoryWriteConsumer` doesn't write to a local file log before acking — on a DB write failure it NAKs and relies purely on JetStream's own 30-day `MEMORY_WRITE` retention for redelivery. This was a deliberate first-cut simplification (see `docs/superpowers/specs/2026-07-18-memory-episodes-design.md`), not an oversight: episodes aren't the sacred immutable audit log `raw_inputs` is, so skipping the extra local-durability layer was an acceptable tradeoff against duplicating the STIMULUS consumer's more complex replay machinery for a second stream.

## Episode dedup uses JetStream stream sequence, not task_id

`action-svc`'s `OutcomeRecord.TaskID` is sometimes empty (the daily-report cron has no per-message correlation ID), so it can't be a unique dedup key. `episodes.stream_seq` (the MEMORY_WRITE message's JetStream stream sequence number, from `msg.Metadata().Sequence.Stream`) is used instead — `ON CONFLICT (stream_seq) DO NOTHING` on insert.

## The episodes table isn't created by memory-svc

Same as `raw_inputs`: `memory-svc` never runs its own DDL. The `episodes` table is applied by hand once per environment via `docs/superpowers/specs/sql/2026-07-18-episodes-table.sql`. As of this writing it's applied to `memory_dev` only — `memory_prod`'s schema doesn't exist yet at all (see root `CLAUDE.md`).
