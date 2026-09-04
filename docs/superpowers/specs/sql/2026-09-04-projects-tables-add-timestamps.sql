-- Adds diagnostic-only timestamp columns to the prompt table added in
-- 2026-09-04-projects-tables.sql: when a prompt started IMPLEMENTING and
-- when it reached DONE. Not surfaced in the web UI (see
-- docs/superpowers/specs/2026-09-04-projects-tool-design.md) but returned
-- by the API for diagnostics.
--
-- Usage:
--   psql "$DATABASE_URL" -v schema=projects_dev -f 2026-09-04-projects-tables-add-timestamps.sql
--   psql "$DATABASE_URL" -v schema=projects_test -f 2026-09-04-projects-tables-add-timestamps.sql
--   psql "$DATABASE_URL" -v schema=projects_prod -f 2026-09-04-projects-tables-add-timestamps.sql

ALTER TABLE :schema.prompt ADD COLUMN IF NOT EXISTS implementation_started_at timestamptz;
ALTER TABLE :schema.prompt ADD COLUMN IF NOT EXISTS done_at timestamptz;
