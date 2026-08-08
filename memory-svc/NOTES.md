# memory-svc — Operational Notes

Incidents, gotchas, and decisions learned running this service — not captured in the design specs themselves (see `CLAUDE.md`'s Services section for spec links).

## Episodes consumer has no file-log/replay layer

Unlike the STIMULUS consumer (`natsconsumer.Consumer`), `MemoryWriteConsumer` doesn't write to a local file log before acking — on a DB write failure it NAKs and relies purely on JetStream's own 30-day `MEMORY_WRITE` retention for redelivery. This was a deliberate first-cut simplification (see `docs/superpowers/specs/2026-07-18-memory-episodes-design.md`), not an oversight: episodes aren't the sacred immutable audit log `raw_inputs` is, so skipping the extra local-durability layer was an acceptable tradeoff against duplicating the STIMULUS consumer's more complex replay machinery for a second stream.

## Episode dedup uses JetStream stream sequence, not task_id

`action-svc`'s `OutcomeRecord.TaskID` is sometimes empty (the daily-report cron has no per-message correlation ID), so it can't be a unique dedup key. `episodes.stream_seq` (the MEMORY_WRITE message's JetStream stream sequence number, from `msg.Metadata().Sequence.Stream`) is used instead — `ON CONFLICT (stream_seq) DO NOTHING` on insert.

## The episodes table isn't created by memory-svc

Same as `raw_inputs`: `memory-svc` never runs its own DDL. Both tables are applied by hand once per environment — `episodes` via `docs/superpowers/specs/sql/2026-07-18-episodes-table.sql`, `raw_inputs` via `docs/superpowers/specs/sql/2026-08-08-raw-inputs-table.sql` (added retroactively, see the incident below — no DDL for `raw_inputs` had ever been committed before this, since `memory_dev`'s copy was originally created live by the `soulman-db-builder` OpenCode agent rather than from a checked-in file).

## Leveled logging (log/slog, added 2026-07-27)

All `log.Printf`/`log.Fatalf` call sites replaced with stdlib `log/slog` (`slog.Error`/`slog.Warn`/`slog.Info`) — no new dependency, Go 1.25 already ships it. Prompted by a 2.75GB `soulman-prod/logs/memory-svc-startup-err.log` that had silently accumulated undifferentiated retry noise from a genuine, ongoing Postgres outage with no way to grep signal from noise. `storage/writer.go`'s DB-insert failures and `natsconsumer/memory_write_consumer.go`'s episode-write failures — both symptoms of that outage — are now `slog.Error`; the "DB unavailable, written to file only" fallback path stays `slog.Warn`. Startup `log.Fatalf` calls became `slog.Error(...)` followed by an explicit `os.Exit(1)`, since slog has no Fatal-and-exit helper.

## Dependency health tracking (added 2026-07-27)

Postgres connectivity is tracked via a `common/dephealth.Registry`, wrapped by `storage.DBHolder` — every real Postgres call (insert, query, episode write) records its outcome, and `GET /health` now reports `dependencies.postgres` (`ok`/`down`, plus `since`/`detail` when down) instead of the old flat `db: "connected"/"unavailable"` field. A background `storage.Reconnector` ticks every 30s: while disconnected it retries `NewDB`; while connected it pings the existing pool. This is what makes a Postgres outage self-healing — before this, a failed startup connect left the DB `nil` for the process's entire lifetime, requiring a manual restart to recover even after Postgres came back. See `docs/superpowers/specs/2026-07-27-dependency-health-design.md`.

`perception-svc`'s `system_monitor` polls this service's `/health` via a new `internal_health` check (`config/dev.json`/`config/prod.json`) and notifies Discord on any dependency's `ok`↔`down` transition — not on every poll while steady, matching the Log Error channel's dedup philosophy.

## Known gap: buffered writes don't auto-replay after a mid-run reconnect (as of 2026-07-27)

`storage.Writer.ReplayPending` only runs once, at startup (`main.go`). The `Reconnector` (added alongside `DBHolder`, same date) makes a Postgres outage self-healing without a process restart — but nothing currently triggers a replay when that reconnect succeeds mid-run. Net effect: after a self-healed outage, new writes resume flowing to Postgres immediately, but any entries buffered to `raw_inputs.jsonl` *during* that outage stay unsynced until the next process restart (not data loss — the file log is durable and `start-everything.ps1` restarts every login — just delayed). Wiring a reconnect-triggered replay needs its own small design (avoiding a race with writes that are in flight when the replay starts) — deferred to a follow-up task rather than rushed into this branch.

## Incident: memory_prod schema still missing, 11M-line/2.46GB error log (2026-08-08)

The gap noted above ("`memory_prod`'s schema doesn't exist yet at all") went unaddressed for over a week and caused a second log-bloat incident, distinct in root cause from the 2026-07-27 one: every insert failed with `relation "memory_prod.raw_inputs"` (later `memory_prod.episodes`) `does not exist (SQLSTATE 42P01)`, and every failure NAKed its NATS message for 5s-delay redelivery — an infinite retry loop that produced `memory-svc-startup-err.log` at 11M+ lines / 2.46GB over roughly 9 days before being caught (via a disk-usage scan, not via any alert).

**Why the 2026-07-27 dependency-health work didn't catch this:** `dephealth`/`DBHolder`/`Reconnector` all track *connectivity* (can we dial and ping Postgres) — that was fine the whole time. A missing table is a query-level failure on an otherwise-healthy connection, so `GET /health`'s `dependencies.postgres` reported `"ok"` throughout the entire 9-day outage. This class of failure — schema/DDL drift on an environment that's otherwise reachable — is invisible to the current health check. No fix attempted here; flagging it as a real blind spot for whoever next touches dependency health.

**Fix applied:** created `memory_prod` schema + `raw_inputs` + `episodes` tables by hand (via `docker exec -i supabase_db_agent-suite psql`, both tables verified column-for-column identical to the live `memory_dev` copies — see `\d` output captured during the fix), restarted prod's `memory-svc`. Confirmed clean: 189 file-buffered `raw_inputs` rows and 146 NATS-redelivered `episodes` rows landed with zero new error lines afterward. The 2.46GB log was archived then deleted (its 11M lines were 100% the same repeated error, fully summarized in `soulman-prod/MEMORY_SVC_LOG_FINDINGS.md` from the initial investigation) — not merged into the fresh log.

**Process gap, not just a data gap:** this was caught by manually noticing disk usage, not by any monitoring. `perception-svc`'s `internal_health` check would have caught a connectivity-down transition immediately (see above) but is structurally blind to this failure mode. There's currently no alert on log file size or growth rate either. Both are real gaps; neither fixed here.
