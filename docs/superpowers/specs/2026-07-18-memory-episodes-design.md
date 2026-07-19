# Memory Episodes Design

**Date:** 2026-07-18
**Status:** Approved
**Phase:** Memory module — first real backing for episodic memory (`/memory/episodes`)

---

## Summary

`memory-svc` currently only implements one of the Memory module's four responsibilities: the immutable raw input log (`raw_inputs`). `/memory/search`, `/memory/episodes`, `/memory/procedures`, and `/memory/goals` are all literal `501` stubs with zero backing (`memory-svc/httpserver/server.go`).

Meanwhile, `action-svc` has been durably publishing `OutcomeRecord`s to the `MEMORY_WRITE` JetStream stream (`soulman.memory.write` / `soulman.dev.memory.write`, 30-day retention) since the pipeline-debugging-tools work — nothing has ever consumed it (`action-svc/natsclient/publisher.go`).

This design closes the first gap using the second: a real `episodes` table, a durable `memory-svc` consumer of `MEMORY_WRITE`, and a working `GET /memory/episodes` read endpoint. Schema starting point per `Memory module.md`'s Episodic Memory section: `id`, `timestamp`, `source`, `summary`, `decision`, `outcome`, `tags`.

Explicitly **not** in scope: `/memory/search`, `/memory/procedures`, `/memory/goals` (stay stubs), semantic/procedural memory, `pgvector`/embeddings, a `forgotten_at` writer, or thinking-svc's future RETRIEVE-step consumption of this endpoint.

---

## Part 1: Wire contract — `common.OutcomeRecord`

`OutcomeRecord` currently lives in `action-svc/natsclient` and is thin: `{type, action_type, status, task_id}`. Per this repo's existing convention — `common` holds every cross-service wire type (`Stimulus` for Perception→Thinking/Memory, `ActionRequest` for Thinking→Action) — it's promoted to `common.OutcomeRecord` and enriched so episodes carry real content instead of being reconstructed purely from `action_type`/`status`:

```go
// common/outcome.go
type OutcomeRecord struct {
    Type       string    `json:"type"`        // "action_log" — discriminator, for forward compat
    ActionType string    `json:"action_type"`
    Status     string    `json:"status"`       // "success" | "failed"
    TaskID     string    `json:"task_id"`      // may be empty (e.g. daily_report_delivery)
    OccurredAt time.Time `json:"occurred_at"`  // when the action was taken, set by action-svc
    Summary    string    `json:"summary"`      // one-line human summary
    Decision   string    `json:"decision"`     // what was decided/done
    Tags       []string  `json:"tags"`
}
```

`action-svc/natsclient/publisher.go`'s `Publisher.PublishOutcome` signature changes from `(actionType, status, taskID string) error` to `(rec common.OutcomeRecord) error`. The `Publisher` interface defined in `dispatch/dispatch.go` and `scheduler/daily.go` (and their test fakes) change to match.

Call sites populate the new fields:

| Call site | `Summary` | `Decision` | `Tags` |
|---|---|---|---|
| `dispatch.go`'s `dispatchAppendDailyReportEntry` | `"Daily report entry appended"` | `"append_daily_report_entry"` | `["report"]` |
| `gmail_triage.go`'s `dispatchGmailTriage` | `fmt.Sprintf("%s — %s", p.Subject, verdict)` | `"notified via Discord"` if `p.Important` else `"logged only"` | `["gmail", "triage"]` |
| `scheduler/daily.go`'s `RunOnce` | `"Daily report delivered"` (or a failure-derived string) | `"daily_report_delivery"` | `["report", "cron"]` |

`OccurredAt` is set by each call site (`time.Now()` for the cron/daily path; `p.OccurredAt` parsed value for the Gmail path, matching what `AppendGmailReportEntry` already does).

---

## Part 2: Postgres schema — `episodes` table

Mirrors `raw_inputs`'s conventions: fully-qualified `schema.table` (no `search_path`), a `forgotten_at` soft-delete column present but unwritten (same state `raw_inputs` itself is in for anything beyond the one documented forget-command path).

```sql
CREATE TABLE IF NOT EXISTS <schema>.episodes (
    id           bigserial PRIMARY KEY,
    stream_seq   bigint NOT NULL UNIQUE,   -- JetStream MEMORY_WRITE stream sequence; dedup key on redelivery
    occurred_at  timestamptz NOT NULL,
    received_at  timestamptz NOT NULL DEFAULT now(),
    source       text NOT NULL,            -- "action-svc" for this first cut
    action_type  text NOT NULL,
    status       text NOT NULL,
    task_id      text,
    summary      text NOT NULL,
    decision     text NOT NULL,
    outcome      text NOT NULL,            -- = status for now; free-text room for future detail
    tags         text[] NOT NULL DEFAULT '{}',
    forgotten_at timestamptz
);

CREATE INDEX IF NOT EXISTS episodes_received_at_idx ON <schema>.episodes (received_at DESC);
```

`stream_seq` — not `task_id` — is the dedup key, because `task_id` is sometimes empty (the daily-report cron publishes it as `""`).

`<schema>` is `memory_dev` / `memory_prod`, same as `raw_inputs`. This DDL ships as a plain `.sql` file (`docs/superpowers/specs/sql/2026-07-18-episodes-table.sql`) applied once by hand against each environment's Postgres — `memory-svc` itself never creates its own tables, matching how `raw_inputs` was originally provisioned by the `soulman-db-builder` agent before any Go code touched it. Dev's local Supabase (port 54322) needs to be running for this and for the Postgres-backed tests below; at design time it wasn't (a different local Supabase project — `agent-suite` — was occupying that port instead).

---

## Part 3: `memory-svc` — consumer, storage, HTTP, config

### Consumer

`memory-svc/natsconsumer` gets a second, sibling type — not a generic refactor of the existing `Consumer` — because their Ack/Nak semantics genuinely differ:

```go
// EpisodeWriter is satisfied by *storage.EpisodeStore.
type EpisodeWriter interface {
    WriteEpisode(ctx context.Context, streamSeq uint64, rec *common.OutcomeRecord) error
}

type MemoryWriteConsumer struct { /* same shape as Consumer: nc, js, writer, consumerName, subject, cc, wg */ }

func NewMemoryWriteConsumer(natsURL, consumerName, subject string, w EpisodeWriter) (*MemoryWriteConsumer, error)
func (c *MemoryWriteConsumer) Start(ctx context.Context) error // consumes the MEMORY_WRITE stream
func (c *MemoryWriteConsumer) Close()
```

`Start`'s `Consume` callback: unmarshal into `common.OutcomeRecord`; `Ack` and skip (log) if `Type != "action_log"` (forward-compat discriminator); otherwise call `writer.WriteEpisode(ctx, msg.Metadata().Sequence.Stream, &rec)`. Unlike the STIMULUS consumer, there is **no local file-log/replay layer** for episodes — on a `WriteEpisode` error the message is `Nak`'d and JetStream's own 30-day-retained redelivery is the durability backstop (episodes aren't the sacred immutable audit log `raw_inputs` is, so this simpler failure path is an intentional difference, not an oversight).

### Storage

New file `memory-svc/storage/episodes.go` (kept separate from `postgres.go` rather than growing that file further — same `*storage.DB`/pool, no new package):

- `Episode` struct mirroring the table columns.
- `(db *DB) InsertEpisode(ctx, streamSeq uint64, rec *common.OutcomeRecord) error` — `INSERT ... ON CONFLICT (stream_seq) DO NOTHING`.
- `(db *DB) GetRecentEpisodes(ctx, limit int) ([]Episode, error)` — same shape as the existing `GetRecent`, `ORDER BY received_at DESC`, filters `WHERE forgotten_at IS NULL`.

### HTTP

`GET /memory/episodes` in `httpserver/server.go` mirrors `rawInputsRecent` exactly: `limit` query param (default 20, capped at 100, matching the existing handler), JSON array, `503` if DB is unavailable. No time-range or tag filtering — nothing consumes this endpoint yet (thinking-svc's future RETRIEVE step is out of scope here), so building filters with no real caller is speculative. `/memory/search`, `/memory/procedures`, `/memory/goals` remain untouched `501` stubs.

### Config

`common/sharedconfig.ConsumerNames` gains `MemorySvcEpisodes string` (json `memory_svc_episodes`). `config/dev.json`'s `consumer_names` gains `"memory_svc_episodes": "memory-svc-episodes-dev"`; `config/prod.json`'s gains `"memory-svc-episodes"`.

`memory-svc/config/config.go`'s `Config` gains `MemoryWriteSubject` (already present in `sharedconfig.Config` but currently unused by `memory-svc`) and `EpisodesConsumerName`, both validated non-empty in `Load()` the same way `StimulusSubject`/`ConsumerNames.MemorySvc` already are.

### Wiring

`main.go` constructs and starts the `MemoryWriteConsumer` alongside the existing `Consumer`, independently (mirroring the "don't nest one consumer's setup inside another's success branch" lesson already documented in `action-svc/NOTES.md` for exactly this kind of dual-consumer wiring).

---

## Part 4: Testing

- `action-svc`: update the three existing test files' fake publishers (`dispatch_test.go`, `gmail_triage.go`'s test, `daily_test.go`) to the new `PublishOutcome(rec)` signature; extend assertions to cover `Summary`/`Decision`/`Tags` being populated as specified in Part 1's table.
- `memory-svc/natsconsumer`: new tests for `MemoryWriteConsumer` mirroring `consumer_test.go` — unparseable-message handling (Ack-and-skip), non-`action_log` type (Ack-and-skip), `Nak` on writer error, `Ack` on success, and a redelivery-after-restart test if `consumer_test.go` already has an equivalent for the STIMULUS consumer.
- `memory-svc/storage/episodes.go`: DB-backed tests mirroring `postgres_test.go`'s `TestDB_GetRecent`/insert pattern, plus a dedup test (same `stream_seq` inserted twice → one row, `ON CONFLICT DO NOTHING` verified).
- `memory-svc/httpserver`: handler test for `/memory/episodes` mirroring the existing raw-inputs handler test.
- All Postgres-backed tests require dev's Supabase running on port 54322 — they skip when the DB is unreachable, matching existing test behavior.

---

## Out of scope (explicitly deferred)

- `/memory/search`, `/memory/procedures`, `/memory/goals` — stay `501` stubs.
- Semantic memory (`memory.facts`, `pgvector`/embeddings) and procedural memory (`memory.procedures`).
- A `forgotten_at` writer for episodes (column exists per convention; nothing sets it yet, same as `raw_inputs` today).
- Time-range/tag filtering on `GET /memory/episodes`.
- `thinking-svc` consuming `/memory/episodes` during its RETRIEVE step — that's future work once episodes exist to retrieve.
- Applying the `episodes` table DDL to `memory_prod` — prod's Postgres schema doesn't exist yet at all (per this repo's `CLAUDE.md`), consistent with `raw_inputs`'s current prod state.
