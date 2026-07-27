# Dependency Health Design

**Status:** Approved
**Date:** 2026-07-27

## Problem

A soulman service can be "up" (process alive, `/health` returns 200) while
being completely non-functional because a dependency it needs is
unreachable. The concrete incident that motivated this: memory-svc's
connection to Postgres failed, and nothing in the system noticed —
`/health` reported `db: "unavailable"` but nothing polled that field
meaningfully, nothing retried the connection, and no notification ever
fired. The gap wasn't detection difficulty, it was that "process health"
and "functional health" were never distinguished anywhere in the system.

Postgres-for-memory-svc is the first instance encountered, not the only
one expected. The goal is a **generic** dependency-health concept any
service can plug a named dependency into, not a one-off Postgres fix.

## Goals

- A generic, reusable mechanism for a service to track the live status of
  a named external dependency (up/down, since when, last error).
- Visibility: a service's `/health` endpoint reports per-dependency status,
  not just process liveness.
- Detection: perception-svc polls this and raises a stimulus when a
  dependency's status changes.
- Notification: Discord gets one notification on the transition to down,
  and one on the transition back to up — no repeat spam while steady.
- A concrete first implementation: memory-svc's Postgres connection,
  including the currently-missing reconnect/retry logic (today, a failed
  startup connection never retries — the process must be restarted
  manually to recover).

## Non-Goals (this iteration)

- Instrumenting any dependency other than memory-svc's Postgres (NATS,
  Discord webhook, Gmail API, etc. are natural follow-ups, each a small
  addition once this pattern exists, not part of this plan).
- A periodic "still down" reminder notification — transition-only for now.
- Any change to `system_monitor`'s existing `service_health` check type
  (external targets like `agent-suite-backend`) — this is additive, a new
  check type alongside it.

## Architecture

```
memory-svc                          perception-svc (sysmonitor)
┌─────────────────────────┐         ┌──────────────────────────┐
│ dephealth.Registry       │  poll   │ internal_health check     │
│  postgres: {ok|down,     │◄────────│  GET /health every        │
│    since, detail}        │ /health │  poll_interval_seconds    │
│                           │         │                           │
│ writer.go: Record() on   │         │  parses dependencies map, │
│  every real DB call      │         │  runs each dependency     │
│                           │         │  through existing         │
│ reconnector: 30s ticker, │         │  publishTransition state  │
│  reconnect-when-down /   │         │  machine (edge-triggered) │
│  ping-when-up            │         │           │                │
└─────────────────────────┘         └───────────┼────────────────┘
                                                  │ Stimulus (on transition only)
                                                  ▼
                                          thinking-svc → action-svc → Discord
```

## Components

### `common/dephealth` (new package)

```go
package dephealth

type Status struct {
    State   string    // "ok" or "down"
    Since   time.Time // when this state was entered
    Detail  string    // last error message, empty when ok
}

type Registry struct {
    mu    sync.Mutex
    items map[string]Status
}

func NewRegistry() *Registry
func (r *Registry) Record(name string, err error)
func (r *Registry) Snapshot() map[string]Status
```

`Record` is idempotent-safe to call on every operation, not just on
change: it only updates `Since` when the state actually flips (ok→down or
down→ok), so repeated calls with the same outcome don't reset the timer.

### memory-svc changes

**`storage/postgres.go`**: `NewDB()` behavior unchanged (attempt connect +
ping, return pool or error). The pool held by the service becomes
swappable — wrapped in a small holder type guarded by a mutex:

```go
type DBHolder struct {
    mu sync.RWMutex
    db *pgxpool.Pool
}

func (h *DBHolder) Get() *pgxpool.Pool   // read, RLock
func (h *DBHolder) Set(db *pgxpool.Pool) // write, Lock
```

`writer.go`'s call sites read through `DBHolder.Get()` instead of a bare
field, and call `registry.Record("postgres", err)` after every real
insert attempt (success or failure) — this is the fast path, detecting a
failure at the moment real traffic hits it.

**New file `reconnect.go`**: a background loop started from `main.go`,
given the `DBHolder`, the `Registry`, and the DB connection string. Every
30s:
- If `registry` shows `postgres` as down (or `DBHolder.Get()` is nil):
  attempt `NewDB()`. On success, `DBHolder.Set(pool)` and
  `registry.Record("postgres", nil)`.
- If `postgres` is currently ok: call `pool.Ping(ctx)` and
  `registry.Record("postgres", result)` — this is what catches a mid-run
  failure during an idle period with no write traffic to trigger
  detection otherwise.

This loop is what turns "down" into a state that can actually recover
without a manual process restart — today, a failed startup connection
leaves `db` `nil` forever.

**`httpserver/server.go`**: `/health` handler changes from a flat
`{"status": "ok", "db": "connected"}` shape to:

```json
{
  "status": "ok",
  "dependencies": {
    "postgres": { "status": "ok" }
  }
}
```

or, when down:

```json
{
  "status": "degraded",
  "dependencies": {
    "postgres": {
      "status": "down",
      "since": "2026-07-27T14:32:00Z",
      "detail": "dial tcp 127.0.0.1:54322: connect: connection refused"
    }
  }
}
```

Top-level `status` is `"degraded"` if any dependency is down, `"ok"`
otherwise. This replaces the old `db` field; nothing else currently
depends on the old shape (confirmed: web-svc's status proxy passes
`/health` bodies through opaquely, doesn't parse the `db` field itself).

### perception-svc changes

**`sysmonitor/config.go`** (or wherever `CheckConfig` lives): new check
type `internal_health`, config shape:

```json
{ "type": "internal_health", "name": "memory-svc", "target": "http://localhost:9002/health" }
```

**`sysmonitor/sysmonitor.go`**: `runCheck` gets a new branch for
`internal_health`, parallel to the existing `service_health` branch:

1. GET `c.Target` with the existing `serviceHealthTimeout`.
2. If the request itself fails (network error, non-2xx, unparseable body):
   treat exactly like `service_health` today — one status under key
   `internal_health:<name>`, severity critical, message
   `"<name> unreachable: <detail>"`.
3. If the request succeeds and parses: for each entry in the response
   body's `dependencies` map, compute severity (`ok` → `severityOK`,
   `down` → `severityCritical`) and call `recordStatus` +
   `publishTransition` with key `internal_health:<name>:<dependency>` —
   each dependency's transition state is independent, reusing the exact
   same edge-triggered machinery `disk_space`/`memory`/`cpu`/
   `service_health` already share. No new dedup logic needed.

Message format, mirroring `formatServiceHealthMessage`:
- Down: `"<name>: <dependency> unavailable: <detail, truncated to 200 chars>"`
- Recovered: `"<name>: <dependency> recovered (was down since <since>)"`

### Config

`config/dev.json` and `config/prod.json`'s `system_monitor.checks` array
gets one new entry:

```json
{ "type": "internal_health", "name": "memory-svc", "target": "http://localhost:9002/health" }
```

(dev's entry targets `http://localhost:9012/health` — memory-svc's dev
port, following the documented prod-port-plus-10 convention: prod `9001`-
`9004` for perception/memory/thinking/action, dev `9011`-`9014`.)

## Data Flow (end-to-end example)

1. Postgres goes unreachable. memory-svc's next write attempt (or, if
   idle, the next 30s ticker `Ping`) fails; `registry.Record("postgres",
   err)` flips state to down.
2. perception-svc's next `internal_health` poll (interval from
   `log_monitor`-style config, reusing `system_monitor.poll_interval_seconds`)
   fetches `/health`, sees `dependencies.postgres.status == "down"`,
   computes `severityCritical` for key `internal_health:memory-svc:postgres`.
3. `publishTransition` sees this key's previous state was `ok` (or unseen)
   → publishes a `Stimulus`.
4. thinking-svc's `system-monitor` rule fires (existing rule, `critical`
   severity already sets `Important: true`) → action-svc appends a report
   entry and sends a real-time (DND-aware) Discord notification.
5. Postgres comes back. memory-svc's reconnector loop reconnects within
   30s, `registry.Record("postgres", nil)` flips state to ok.
6. Next poll: `internal_health:memory-svc:postgres` transitions
   critical→ok → publishes a "recovered" stimulus → another Discord
   notification.

## Error Handling & Edge Cases

- **`memory_prod`'s Postgres schema doesn't exist yet** (documented gap in
  this repo's `CLAUDE.md`): prod's memory-svc inserts fail continuously
  today, silently. Once this ships, the first `internal_health` poll in
  prod will correctly fire one "postgres unavailable" critical
  notification — this is accurate, not a false positive, and is expected
  the first time this deploys to prod.
- **perception-svc can't reach thinking-svc when trying to publish a
  transition**: already handled by the existing `publishTransition`
  behavior — state is left unadvanced so the same transition is retried
  next poll, rather than being silently lost.
- **Reconnector/writer race**: `DBHolder`'s `RWMutex` covers concurrent
  `Get()` (writer path) against `Set()` (reconnector path) — same pattern
  as the `pendingFileMu` mutex added for the do-not-disturb feature.
- **memory-svc process itself down** (not just its DB) vs. **memory-svc up
  but Postgres down**: kept as two distinct signals — the former surfaces
  as `internal_health:memory-svc` (unreachable), the latter as
  `internal_health:memory-svc:postgres` (degraded). Never conflated.

## Testing

- `common/dephealth`: unit tests for `Record`/`Snapshot`, including that
  `Since` doesn't reset on a repeated same-state `Record` call, and
  concurrent-access safety.
- memory-svc: `reconnect.go` tested with an injectable `NewDB` function
  seam (fake success/failure sequences); `/health` handler tested for
  both shapes (all-ok, one-down).
- perception-svc: `internal_health` check tested against a fake HTTP
  server returning both the unreachable case and both healthy/degraded
  body shapes; transition behavior itself is already covered by existing
  `sysmonitor` tests since it reuses `publishTransition` unchanged.
- Manual end-to-end in dev: stop the local Postgres container, confirm
  `/health` flips to degraded, confirm a Discord notification arrives;
  restart Postgres, confirm the reconnector recovers within ~30s and a
  recovery notification arrives.

## Future Extensions (not this iteration)

Once this pattern exists, adding a new dependency to any service is:
instantiate a `dephealth.Registry`, call `Record` at the relevant call
sites, wire it into that service's `/health`, add one `internal_health`
config entry per service (not per dependency — one check polls a whole
service's `/health` and gets all its dependencies at once). Candidates:
action-svc's Discord webhook, perception-svc's Gmail API polling, NATS
connectivity for every service.
