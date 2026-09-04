# Projects Tool — Design

**Status:** Approved
**Date:** 2026-09-04

## Problem

The user wants a lightweight project-management tool for tracking development tasks across their projects, and — crucially — for automatically kicking off a background Claude Code session per task, walking it through spec → implementation plan → execution without manual intervention to start each one. It should live in the Soulman web dashboard (next to the Obsidian/Claude/Files links) but must not share code with the rest of Soulman beyond visual styling, so it stays easy to spin off into its own standalone application later if needed.

## Goals

- A "Projects" link in the web dashboard, next to Obsidian/Claude/Files/Search.
- Manage `project` rows (name + filesystem path) — add, edit, delete.
- Manage `prompt` rows (one task per row: project, task name, prompt text, state) — add, edit (including a manual state override), list.
- Creating a `prompt` automatically launches a detached, remote-control-enabled Claude Code session rooted at the project's path, running the prompt text plus a fixed appended directive (spec → `IMPLEMENTING` notify → implementation plan/execution → `DONE` notify).
- At most one task in `CREATING_SPEC` at a time, globally — a second prompt created while one is already creating its spec waits, queued, until the first reaches `IMPLEMENTING`. Any number of tasks may be `IMPLEMENTING` concurrently.
- A localhost-only HTTP endpoint the spawned sessions call back on to report `IMPLEMENTING`/`DONE` transitions.
- Own Postgres schema, own backend service, own frontend page — reuses only the dashboard's existing Tailwind setup (visual style) and its existing Supabase-JWT login gate (via a proxy through `web-svc`, not by importing any of its code).

## Non-Goals

- No automatic detection or recovery of a stuck/crashed spec-creation session (no timeout, no health check). A stuck task is unblocked manually, by editing its `state` back to `NOT_STARTED` in the UI.
- No cascading delete — deleting a project with existing prompts is rejected, not silently cascaded.
- No session tracking/stopping/log capture after launch, matching the existing Claude-remote-sessions feature's posture — once started, a task's Claude session is independent of Soulman.
- No NATS involvement, no interaction with the Stimulus/Thinking/Action pipeline. This tool is fully self-contained.
- No live-updating UI (polling/websockets) in v1 — state changes are seen on manual refresh.
- No per-task customization of the appended directive text, no support for tasks that don't go through the spec → implement → done lifecycle.

## Architecture

```
Browser
   │  GET/POST/PUT/DELETE  /api/projects/**   (project + prompt CRUD)
   ▼
web-svc (httpserver, JWT + owner-email authed route group)
   │  thin proxy over localhost
   ▼
projects-svc  ── main port (9006 prod / 9016 dev, plain HTTP, no own auth —
   │              same trust model as memory-svc/perception-svc: reachable
   │              only server-to-server)
   │
   ├─ Postgres (schema projects_dev / projects_prod, same instance as
   │   memory-svc; hand-written DDL under docs/superpowers/specs/sql/)
   │
   ├─ dispatch: sync.Mutex-guarded tryDispatchNext() — launches the oldest
   │   NOT_STARTED prompt when no prompt is CREATING_SPEC
   │
   └─ Launch(project, prompt) → exec.Command("claude", "--remote-control",
        "--bg", "--name", "<project> <task>", fullPromptText)
          Dir: project.path
          Windows: CREATE_NEW_PROCESS_GROUP, cmd.Process.Release()

projects-svc ── notify port (9007 prod / 9017 dev, bound to 127.0.0.1 only)
   ▲
   │  POST /notify {prompt_id, state: "IMPLEMENTING"|"DONE"}
   │
   spawned Claude session (running locally, calls back via curl)
```

Two Go modules touched: a brand-new `projects-svc` (own `go.mod`, `replace soulman/common => ../common`, own Postgres schema, own binary/ports, own `run-projects-svc.ps1` in both environments) and a small addition to the existing `web-svc` (a proxying route group — no business logic). The frontend is a new page inside the existing `web/` app, styled with the same global Tailwind setup every other page already uses, sharing no components or logic with them.

## Data Model

New schema `projects_dev`/`projects_prod`, DDL in `docs/superpowers/specs/sql/2026-09-04-projects-tables.sql`, applied by hand via `psql` like `memory-svc`'s tables:

```sql
create table project (
  name text primary key,
  path text not null
);

create table prompt (
  id bigserial primary key,
  project_name text not null references project(name),
  task_name text not null,
  prompt_text text not null,
  state text not null default 'NOT_STARTED'
    check (state in ('NOT_STARTED', 'CREATING_SPEC', 'IMPLEMENTING', 'DONE')),
  last_launch_error text,
  created_at timestamptz not null default now()
);
```

`last_launch_error` backs the "surface why a task hasn't started" behavior described under `dispatch.TryDispatchNext` below — omitted from an earlier draft of this table, added here for consistency with the rest of this spec.

No `ON DELETE CASCADE` on the `project_name` foreign key — deleting a `project` referenced by any `prompt` fails on the FK constraint; the API surfaces this as a clear `409`-style error rather than silently removing prompt history.

## Components

### Config

New `ProjectsConfig` section on `common/sharedconfig.Config`, following the `SchoolConfig`/`WebConfig` nesting pattern — holds nothing shared cross-service; `projects-svc` reads its own `DATABASE_URL`/`SCHEMA`/`HTTP_PORT`/`NOTIFY_PORT` from plain env vars (mirroring `memory-svc/config/config.go`), not from shared config.

`sharedconfig.WebConfig` (`common/sharedconfig/config.go`) gains one field, alongside its existing `PerceptionSvcURL`/`MemorySvcURL`/`ActionSvcURL`:

```go
ProjectsSvcURL string `json:"projects_svc_url"`
```

Set in `config/dev.json` (`"projects_svc_url": "http://localhost:9016"`) and `config/prod.json` (`"http://localhost:9006"`) under `"web"`. `web-svc/config/config.go`'s `Load()` treats it as fatal-if-empty, same as the other three service URLs.

### `projects-svc` (new module)

```
projects-svc/
  go.mod                       module soulman/projects-svc; replace soulman/common => ../common
  main.go                      wires config, DB, dispatcher, both HTTP servers
  config/config.go             DATABASE_URL, SCHEMA, HTTP_PORT (default 9006), NOTIFY_PORT (default 9007)
  store/store.go                Postgres access: CRUD for project/prompt, atomic state transitions
  launcher/launcher.go          Launch(project, prompt) — the exec.Command wrapper
  dispatch/dispatch.go          tryDispatchNext(), sync.Mutex-guarded
  httpserver/server.go          CRUD routes (main port) + /notify route (notify port, separate listener)
```

- `store.Store` methods: `ListProjects`, `CreateProject`, `UpdateProject`, `DeleteProject` (returns a sentinel `ErrHasPrompts` on FK violation), `ListPrompts`, `CreatePrompt` (inserts `NOT_STARTED`, then triggers `dispatch.TryDispatchNext`), `UpdatePromptState(id, state)` (state-only — task_name/prompt_text/project_name are immutable after creation; this single method is used both by manual UI overrides and by the notify handler, with no other way to mutate a prompt after it's created).
- `launcher.Launch(project store.Project, prompt store.Prompt) error`:
  ```go
  fullPrompt := prompt.PromptText + "\n\n" + directiveFor(prompt.ID, notifyPort)
  cmd := exec.Command("claude", "--remote-control", "--bg", "--name",
      project.Name+" "+prompt.TaskName, fullPrompt)
  cmd.Dir = project.Path
  detach(cmd) // same CREATE_NEW_PROCESS_GROUP + Release() pattern as web-svc/claudesession
  ```
  Validates `project.Path` exists and is a directory before spawning (`ErrNotFound` otherwise) — same guard style as `claudesession.resolveDir`, just without the curated-roots indirection since `path` is a free-form column here.
  `directiveFor` renders the fixed instructional suffix with the real `prompt.ID` and `notifyPort` substituted into the `curl` commands the session is told to run (see Data Flow below for the literal text).
- `dispatch.TryDispatchNext(store, launcher)`: mutex-guarded. If any prompt is `CREATING_SPEC`, no-op. Otherwise picks the oldest `NOT_STARTED` prompt (`order by id limit 1`); on successful `Launch`, updates it to `CREATING_SPEC`; on launch failure, leaves it `NOT_STARTED` and records the error (a `last_launch_error text` column, nullable, cleared on next successful launch attempt) so the UI can surface why a task hasn't started. Called: after `CreatePrompt`, after a `/notify` with `state=IMPLEMENTING`, and once at `projects-svc` startup.
- `/notify` handler: rejects any request whose `RemoteAddr` isn't a loopback address (defense in depth on top of the listener already being bound to `127.0.0.1`). Validates `prompt_id` exists and `state` is `IMPLEMENTING` or `DONE`; updates the row; if `IMPLEMENTING`, calls `dispatch.TryDispatchNext` afterward to free the slot for the next queued task.

### `web-svc` additions

New authed route group, thin proxy — no business logic, just forwards the request body/response to `cfg.ProjectsSvcURL` with the same method/path suffix:

| Method | Path | Forwards to |
|---|---|---|
| GET | `/api/projects/projects` | `GET {ProjectsSvcURL}/projects` |
| POST | `/api/projects/projects` | `POST {ProjectsSvcURL}/projects` |
| PUT | `/api/projects/projects/{name}` | `PUT {ProjectsSvcURL}/projects/{name}` |
| DELETE | `/api/projects/projects/{name}` | `DELETE {ProjectsSvcURL}/projects/{name}` |
| GET | `/api/projects/prompts` | `GET {ProjectsSvcURL}/prompts` |
| POST | `/api/projects/prompts` | `POST {ProjectsSvcURL}/prompts` |
| PUT | `/api/projects/prompts/{id}` | `PUT {ProjectsSvcURL}/prompts/{id}` — body `{state}` only |

The `/notify` endpoint is deliberately **not** proxied through `web-svc` at all — it's reached directly at `http://localhost:{NOTIFY_PORT}/notify` by the spawned session, bypassing the browser-facing auth path entirely, per the "only accessible from localhost" requirement.

### Frontend (`web/src/`)

- `Dashboard.tsx`: new `<button onClick={onOpenProjects}>Projects</button>`, same styling as the existing nav buttons.
- `App.tsx`: `ViewState` gains `'projects'`; `onOpenProjects`/`onBack` wiring identical in shape to the existing pages.
- `ProjectsPage.tsx`: same header pattern as `ClaudePage.tsx`/`ObsidianPage.tsx` (`min-h-screen bg-gray-50 p-6`, back button). Two panels:
  - **Projects**: table (name, path), inline add form, edit-in-place, delete button (shows the server's FK-block error inline on failure).
  - **Prompts**: table (project, task name, state badge, created_at, last_launch_error if present), add-prompt form (project select, task name text input, prompt textarea), and a state dropdown per row for manual override. A manual refresh button re-fetches both lists.
- `web/src/api.ts`: typed functions for the seven proxy routes above, following the existing relative-path (`/api/...`) convention.

## Data Flow

1. User opens the dashboard, clicks "Projects", adds a project (`name: "digital-me"`, `path: "C:\Users\Lenovo\IdeaProjects\digital-me"`).
2. User adds a prompt (`project: "digital-me"`, `task_name: "add dark mode"`, `prompt_text: "Add a dark mode toggle to settings"`).
3. `POST /api/projects/prompts` → `web-svc` proxies → `projects-svc` inserts the row as `NOT_STARTED`, calls `dispatch.TryDispatchNext`.
4. No other prompt is `CREATING_SPEC`, so this one is picked: `launcher.Launch` validates the path, builds the full prompt:
   > Add a dark mode toggle to settings
   >
   > Use Superpowers to figure out via questioning what is being requested, and to write the feature spec. Once the feature spec has been accepted, run `curl -s -X POST http://localhost:9017/notify -H "Content-Type: application/json" -d '{"prompt_id": 7, "state": "IMPLEMENTING"}'`, then proceed to creating an implementation plan and executing it as usual. Once that's complete, run `curl -s -X POST http://localhost:9017/notify -H "Content-Type: application/json" -d '{"prompt_id": 7, "state": "DONE"}'`.

   and starts `claude --remote-control --bg --name "digital-me add dark mode" "<the text above>"` with `Dir` set to the project's path. On successful `Start()`, the row becomes `CREATING_SPEC`.
5. The session runs, brainstorms the spec with the user (remote-control — the user answers from wherever `claude.ai/code` is open, same as any other remote session), and once the spec is accepted, runs the `IMPLEMENTING` curl. `projects-svc`'s notify listener updates the row and calls `TryDispatchNext` — if a second prompt was queued `NOT_STARTED`, it launches now.
6. The session proceeds through planning/implementation as usual, and on completion runs the `DONE` curl, setting the row to `DONE`.

## Error Handling & Edge Cases

- **Project path missing/not a directory** at launch time: `Launch` returns an error before spawning; the prompt stays `NOT_STARTED` with `last_launch_error` set, visible in the UI, and remains queued for retry (e.g. after the user fixes the path or remounts a drive).
- **`claude` not on `PATH`, or `cmd.Start()` fails for any other reason**: same as above — no state change, error recorded, no queue slot consumed.
- **Stuck/crashed `CREATING_SPEC` session** (never calls `/notify IMPLEMENTING`): not auto-detected. The user edits the prompt's state back to `NOT_STARTED` from the UI, which frees the slot on the next dispatch trigger.
- **`projects-svc` restarts while a prompt is `CREATING_SPEC`**: left as-is — the detached Claude process is unaffected by a `projects-svc` restart and will still call `/notify` when ready. `TryDispatchNext` only acts when no prompt is `CREATING_SPEC`, so no double-launch. It's also called once at startup, to resume dispatching a queue that built up while the service was down.
- **Delete a project with existing prompts**: rejected by the FK constraint; `store.DeleteProject` maps that to `ErrHasPrompts`, surfaced as a 409 with a clear message.
- **Concurrent notify calls / concurrent prompt creation**: `TryDispatchNext` is guarded by a single in-process `sync.Mutex` — safe because exactly one `projects-svc` process runs per environment.
- **`/notify` reached from a non-local address**: rejected regardless of body contents — enforced both by the listener binding to `127.0.0.1` and a `RemoteAddr` check in the handler.
- **Duplicate session names**: not deduplicated — same posture as the existing Claude-remote-sessions feature.

## Testing

- `projects-svc/store`: unit tests against a real local Postgres (or the same test-DB convention `memory-svc` uses, if one exists) for CRUD and the `ErrHasPrompts` FK-violation mapping.
- `projects-svc/dispatch`: table-driven tests for `TryDispatchNext` against a fake `launcher`-shaped interface (never a real `exec.Command("claude", ...)` in tests, matching the existing rule for `claudesession` — no test may reach a real process spawn) — covers: no-op when a `CREATING_SPEC` row exists, picks oldest `NOT_STARTED` by id, launch failure leaves state untouched and records the error, successful launch transitions to `CREATING_SPEC`.
- `projects-svc/launcher`: unit tests for the path-validation guard only (missing dir, path is a file not a directory), same posture as `claudesession`'s tests — the actual `exec.Command` call is not exercised.
- `projects-svc/httpserver`: table-driven tests for the CRUD routes, and a dedicated test asserting the `/notify` handler rejects a non-loopback `RemoteAddr`.
- `web-svc`: tests for the new proxy route group asserting correct forwarding and status-code passthrough.
- Frontend: no automated tests, consistent with the other dashboard pages.
- End-to-end verification (manual): with dev `projects-svc`/`web-svc` running, create a project pointed at a real folder, add a prompt, confirm a `claude --remote-control --bg` process starts with the right working directory and that the prompt row moves to `CREATING_SPEC`; manually curl the notify endpoint to confirm the state transitions and that a second queued prompt then launches.
