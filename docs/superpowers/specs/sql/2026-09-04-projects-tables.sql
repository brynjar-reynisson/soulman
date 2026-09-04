-- project/prompt tables for projects-svc.
-- Apply by hand against each environment's Postgres (projects_dev /
-- projects_prod schema), matching how memory-svc's tables are provisioned
-- -- projects-svc itself never creates its own tables. See
-- docs/superpowers/specs/2026-09-04-projects-tool-design.md for context.
--
-- Usage:
--   psql "$DATABASE_URL" -v schema=projects_dev -f 2026-09-04-projects-tables.sql
--   psql "$DATABASE_URL" -v schema=projects_prod -f 2026-09-04-projects-tables.sql

CREATE SCHEMA IF NOT EXISTS :schema;

CREATE TABLE IF NOT EXISTS :schema.project (
    name text PRIMARY KEY,
    path text NOT NULL
);

CREATE TABLE IF NOT EXISTS :schema.prompt (
    id                 bigserial PRIMARY KEY,
    project_name       text NOT NULL REFERENCES :schema.project(name),
    task_name          text NOT NULL,
    prompt_text        text NOT NULL,
    state              text NOT NULL DEFAULT 'NOT_STARTED'
                         CHECK (state IN ('NOT_STARTED', 'CREATING_SPEC', 'IMPLEMENTING', 'DONE')),
    last_launch_error  text,
    created_at         timestamptz NOT NULL DEFAULT now()
);
