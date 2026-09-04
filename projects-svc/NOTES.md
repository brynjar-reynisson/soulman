# projects-svc — Operational Notes

Incidents, gotchas, and decisions learned building/reviewing this service — not captured in the design spec itself (see `CLAUDE.md`'s Services section for the spec link).

## Two HTTP listeners, both loopback-only

`HTTP_PORT` (default `9006`) is the main CRUD API — `web-svc` is its only client, always on the same host in this repo's current single-machine deployment model (confirmed via `common/sharedconfig`'s `WebConfig.ProjectsSvcURL`, `http://localhost:...` in `config/prod.json`). `NOTIFY_PORT` (default `9007`) is the callback listener spawned Claude sessions curl into.

Both bind to `127.0.0.1` only, not `0.0.0.0` (`projects-svc/main.go`). The notify listener always did; the main listener was fixed to match during final review — unlike the other Soulman services, this one accepts a filesystem path plus free-form prompt text that gets handed straight to `exec.Command` (`launcher.Launch`), making it a much higher-value target if it were ever reachable off-host. A bind failure on the main listener is fatal (`slog.Error` + `os.Exit(1)`) since nothing else can serve the API without it; the notify listener's bind failure is logged but non-fatal, since the main CRUD API still works without it (a session just can't report `IMPLEMENTING`/`DONE` until the process is restarted with a free port).

## `SCHEMA` must be set explicitly per environment

`config.Load()` defaults `SCHEMA` to `projects_prod` (mirrors `memory-svc`'s convention, and matches this repo's single-environment deployment — no override is needed in `run-projects-svc.ps1`).

## Store tests use a separate `projects_test` schema, not `projects_prod`

`projects-svc/store/store_test.go`'s `testStore(t)` connects to a dedicated `projects_test` schema (same `DATABASE_URL` default as `projects_prod`, same Postgres instance). This was a final-review fix: several tests assert *global* conditions (no other `CREATING_SPEC` prompt exists anywhere, the prompt just created is the oldest `NOT_STARTED` row anywhere, no `NOT_STARTED` row exists anywhere) that would start failing the moment real prod data exists, if they ran against `projects_prod`.

Before running `go test ./store/...` locally, apply the DDL to `projects_test` too — it isn't created automatically:

```
psql "postgres://postgres:postgres@localhost:54322/postgres" -v schema=projects_test -f docs/superpowers/specs/sql/2026-09-04-projects-tables.sql
```

(On a machine without `psql` on `PATH`, the local Supabase Postgres container can run it directly, e.g. `docker exec -i <postgres-container> psql -U postgres -v schema=projects_test -f - < docs/superpowers/specs/sql/2026-09-04-projects-tables.sql`.) Missing Postgres entirely is fine too — `testStore` calls `t.Skipf` on a connection failure, same as every other DB-backed test in this repo.

## `/notify` is deliberately unauthenticated

Its only protection is the loopback bind on `NOTIFY_PORT` plus a `RemoteAddr` check inside `notifyHandler` (`httpserver/notify.go`'s `isLoopback`) — defense in depth, not two independent layers of real auth. If either listener's binding ever changes (e.g. someone "temporarily" binds `0.0.0.0` to debug from another machine), the `RemoteAddr` check stops meaning anything, since `RemoteAddr` reflects whatever peer actually connected. Keep both in sync.

## Deployment is out of scope for this repo

`run-projects-svc.ps1` (in `soulman-prod\`), applying the DDL against the real `projects_prod` schema, and wiring `start-everything.ps1`/`setup-firewall-rules.ps1` for the two new ports are **not** part of this git repo and were explicitly out of scope for `docs/superpowers/plans/2026-09-04-projects-tool.md` — that work happens by hand, after this branch merges, the same way every other service's initial deployment was bootstrapped.

## Known follow-up: no frontend test coverage

This branch adds `web/src/api.ts`'s seven projects/prompts functions and three new components (`ProjectsPanel.tsx`, plus the page/nav wiring) with zero automated tests — unlike the rest of `web/`, which has an established test convention for its other panels. Deferred rather than fixed as part of the final review pass; worth picking up if `ProjectsPanel.tsx` grows more interactive logic (e.g. the edit-in-place control added in the same review pass).
