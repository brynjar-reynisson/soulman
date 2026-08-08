-- raw_inputs table for memory-svc.
-- Apply by hand against each environment's Postgres (memory_dev / memory_prod
-- schema) -- memory-svc itself never creates its own tables, matching how
-- episodes is provisioned (see 2026-07-18-episodes-table.sql). No DDL for
-- this table was ever committed when memory_dev's copy was first created by
-- the soulman-db-builder OpenCode agent; this file reverse-engineers it from
-- memory_dev's live schema (dumped 2026-08-08) to close that gap. See
-- docs/superpowers/specs/2026-06-27-memory-svc-design.md for context.
--
-- Usage:
--   psql "$DATABASE_URL" -v schema=memory_prod -f 2026-08-08-raw-inputs-table.sql

CREATE SCHEMA IF NOT EXISTS :schema;

CREATE TABLE IF NOT EXISTS :schema.raw_inputs (
    stimulus_id     text PRIMARY KEY,
    received_at     timestamptz NOT NULL DEFAULT now(),
    occurred_at     timestamptz,
    channel         text NOT NULL CHECK (channel <> ''),
    source_identity text,
    raw_payload     jsonb NOT NULL,
    normalized_text text,
    is_override     boolean NOT NULL DEFAULT false,
    override_cmd    text,
    forgotten_at    timestamptz
);
