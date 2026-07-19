-- Episodes table for memory-svc.
-- Apply by hand against each environment's Postgres (memory_dev / memory_prod
-- schema) -- memory-svc itself never creates its own tables, matching how
-- raw_inputs was originally provisioned. See
-- docs/superpowers/specs/2026-07-18-memory-episodes-design.md for context.
--
-- Usage:
--   psql "$DATABASE_URL" -v schema=memory_dev -f 2026-07-18-episodes-table.sql

CREATE TABLE IF NOT EXISTS :schema.episodes (
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

CREATE INDEX IF NOT EXISTS episodes_received_at_idx ON :schema.episodes (received_at DESC);
