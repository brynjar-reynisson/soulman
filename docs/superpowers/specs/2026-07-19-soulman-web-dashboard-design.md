# Soulman Web Dashboard Design

**Date:** 2026-07-19
**Status:** Approved
**Phase:** New surface — first web frontend for Soulman, read-only dashboard + Supabase auth. No control/override actions in this iteration.

---

## Summary

Soulman has no web UI today — only four Go services (each reachable by CLI/curl on `localhost`, none with CORS or authentication) plus the `soulman` CLI. This adds:

1. **`web/`** — a React 19 + Vite + TypeScript + Tailwind 4 frontend (matching `agent-suite`'s existing stack), showing recent episodes, recent raw inputs, daily reports, and per-service health.
2. **`web-svc/`** — a new fifth Go service (own module, own module-per-service pattern) that is the *only* Soulman component with CORS enabled and JWT verification. It aggregates read-only data for the frontend from `memory-svc`'s HTTP API and from `action-svc`'s report files on disk. `perception-svc`, `thinking-svc`, and `action-svc` are not modified.

Authentication reuses `agent-suite`'s existing hosted Supabase project (ref `grgspbzqzjblsoxmmojy`) and its already-configured Google OAuth client — no new Supabase project, no new Google Cloud OAuth client. Authorization is a single owner-email check, not a roles table: only `breynisson@gmail.com` gets the dashboard; anyone else (or anyone unauthenticated) is turned away before any data is fetched.

Building the actual PAUSE/STOP/RESUME override dispatch (the "control panel" half of "dashboard-first, control later") is explicitly **out of scope** — `Project Soulman.md`'s Guard Agent doesn't exist in code yet (confirmed: zero references to "Guard" anywhere in the four services' Go source, and `Stimulus.Override.IsOverride` is always hardcoded `false`). That needs its own design once the Guard Agent itself is specified.

---

## Architecture

```
Browser (web/)
  │  1. Supabase JS: signInWithOAuth('google')  → session + JWT
  │  2. Authorization: Bearer <jwt> on every API call
  ▼
web-svc (:9005 prod / :9015 dev)
  │  verifies JWT (HS256 shared secret or ES256 via Supabase JWKS,
  │  same pinning agent-suite's UserResolverFilter uses),
  │  checks email claim == configured owner_email
  ├─→ memory-svc  GET /memory/episodes, GET /raw-inputs/recent   (HTTP, existing endpoints, unmodified)
  ├─→ $SOULMAN_ROOT/reports/daily-report-YYYY-MM-DD.txt          (direct file read, same filesystem)
  └─→ GET /health on perception-svc, thinking-svc, action-svc, memory-svc  (aggregated into /api/status)
```

`web-svc` is the only new backend surface with authn/authz and CORS. The existing four services keep their current open-localhost HTTP surfaces exactly as they are — no CORS, no JWT, no changes — since only `web-svc` ever calls them, and it does so from the same trusted host.

---

## Auth: reusing `agent-suite`'s Supabase project

- **Frontend** (`web/src/auth.ts`): a near-verbatim port of `agent-suite/frontend/src/auth.ts` — same `@supabase/supabase-js` client construction from `VITE_SUPABASE_URL` / `VITE_SUPABASE_ANON_KEY`, same `useAuth()` hook (`user`, `loading`, `signIn`, `signOut`), same `signInWithOAuth({ provider: 'google', options: { redirectTo: VITE_AUTH_REDIRECT_URL } })`. These three env vars point at the same hosted Supabase project agent-suite already uses; `VITE_AUTH_REDIRECT_URL` gets its own value (this app's own URL, not agent-suite's).
- **`web-svc`** ports the JWT verification logic from `UserResolverFilter.java` to Go:
  - Reject anything without a `Bearer` token → HTTP 401.
  - Parse the JWT, pinning to exactly the two algorithms Supabase issues: `HS256` (shared secret, env var `SUPABASE_JWT_SECRET`) or `ES256` (public key fetched once from `<SUPABASE_URL>/auth/v1/.well-known/jwks.json` and cached in memory, mirroring the Java filter's `getOrFetchPublicKey`). Any other algorithm (including `none`) → 401.
  - Require `iss == <SUPABASE_URL>/auth/v1` and `aud == "authenticated"` → mismatch is 401.
  - Extract the `email` claim. If it doesn't equal the configured `owner_email` → HTTP 403.
  - No database lookup, no `findOrCreate`, no roles table — the email check *is* the authorization decision.
- **No shared Go/TS package**: exactly like `agent-suite` itself, this logic is small enough (one file per side) to port directly rather than extract into a shared library across two separate repos/toolchains.

---

## `web-svc` endpoints

All routes use `chi`, matching the existing four services.

| Method | Path | Auth | Behavior |
|---|---|---|---|
| GET | `/health` | none | `{status: "ok"}` — for `start-everything.ps1`/monitoring, not the dashboard |
| GET | `/api/status` | owner | Calls `/health` on perception-svc, memory-svc, thinking-svc, action-svc (short timeout each, in parallel); returns `{service: "up"/"down"}` per service. A downstream timeout/error marks that service `"down"`, doesn't fail the whole response |
| GET | `/api/episodes?limit=` | owner | Proxies `memory-svc`'s `GET /memory/episodes?limit=`, passes the response through |
| GET | `/api/raw-inputs/recent?limit=` | owner | Proxies `memory-svc`'s `GET /raw-inputs/recent?limit=` |
| GET | `/api/reports/latest` | owner | Reads today's report file (falls back to the most recent existing file within the last 7 days if today's doesn't exist yet); 404 if none found |
| GET | `/api/reports?date=YYYY-MM-DD` | owner | Reads that specific date's report file; 404 if it doesn't exist |

`memory-svc` proxy calls and `/api/status` downstream calls use plain `net/http` with a short timeout (e.g. 5s); a downstream 5xx or timeout is surfaced as a 502/503 from `web-svc` with a small JSON error body (`{"error": "memory-svc unavailable"}`), not a crash or a hang.

### Report file reading

`web-svc` gets its own small `reports` package that re-derives the same `daily-report-YYYY-MM-DD.txt` naming (`filepath.Join(root, "reports", "daily-report-"+date+".txt")`) and does a plain `os.ReadFile`, returning `""`/404-equivalent on `os.IsNotExist`. This duplicates roughly ten lines from `action-svc/report/report.go`'s `PathForDate`/`Read` rather than adding a cross-module Go dependency — consistent with this codebase's existing choice to keep each service an independent module (see root `CLAUDE.md`: "one Go module per service").

---

## Config

New nested block in `common/sharedconfig`, following the `GmailConfig`/`SystemMonitorConfig` precedent (nested struct, JSON-tagged):

```go
type WebConfig struct {
    OwnerEmail        string `json:"owner_email"`
    CORSAllowedOrigin string `json:"cors_allowed_origin"`
    PerceptionSvcURL  string `json:"perception_svc_url"`
    MemorySvcURL      string `json:"memory_svc_url"`
    ThinkingSvcURL    string `json:"thinking_svc_url"`
    ActionSvcURL      string `json:"action_svc_url"`
}
```

Added to `sharedconfig.Config` as `Web WebConfig` (JSON tag `web`). `config/dev.json` gets dev ports (9011-9014) and `http://localhost:5178` as the dev frontend origin; `config/prod.json` gets prod ports (9001-9004) and a placeholder origin (`http://localhost:4173`, Vite's preview server) until Cloudflare exposure is set up — see Out of Scope. Both files get the same `owner_email: "breynisson@gmail.com"`.

`SUPABASE_URL` and `SUPABASE_JWT_SECRET` are env vars (secrets), not sharedconfig, matching how `DEEPSEEK_API_KEY`/`DISCORD_BOT_TOKEN` are handled today — both non-fatal-if-blank at startup is *not* appropriate here, though: an empty `SUPABASE_JWT_SECRET` would make JWT verification unverifiable, so `web-svc/config.Load` treats a blank `SUPABASE_URL` or `SUPABASE_JWT_SECRET` as a fatal startup error (this service is useless without them, unlike Gmail/Discord which are optional features of other services).

`web-svc` follows the existing dual dev/prod convention: `HTTP_PORT` env var, defaulting to `9005` (prod) — dev sets `HTTP_PORT=9015` at launch exactly like the other four services (`docs/superpowers/plans/2026-07-18-shared-config-nats.md`'s per-service `HTTP_PORT` export pattern). No JetStream consumer, no NATS subscription at all — `web-svc` only makes outbound HTTP/file calls, so it needs no `consumer_names` entry and no stream subject.

---

## Frontend (`web/`)

Three top-level states, driven entirely by auth state:

1. **Login screen** — no Supabase session. A "Sign in with Google" button and nothing else. Default view for anyone arriving unauthenticated.
2. **Restricted (static info) page** — a Supabase session exists, but the first authenticated call to `web-svc` (`/api/status`) returns 403. A plain "this is a private system" page, no retry-as-different-account prompt beyond a sign-out link.
3. **Dashboard** — `/api/status` succeeds (200). Shows:
   - System status panel: up/down per service, from `/api/status`.
   - Recent episodes list, from `/api/episodes`.
   - Recent raw inputs list, from `/api/raw-inputs/recent`.
   - Daily report viewer: latest report by default (`/api/reports/latest`), with a date picker to browse older reports (`/api/reports?date=`).

Every `web-svc` call attaches `Authorization: Bearer <token>` via the same `getAccessToken()` pattern as `agent-suite/frontend/src/api.ts`. A 401 from any call while already on the dashboard (expired session) drops back to the login screen; a 403 drops to the restricted page.

Each dashboard panel fetches and renders independently — if `/api/episodes` 502s because `memory-svc` is down, that one panel shows an inline error banner ("episodes unavailable") while the rest of the dashboard still renders normally.

---

## Error Handling

| Failure | Behavior |
|---|---|
| No/invalid/expired JWT | `web-svc` → 401 on every owner-gated route; frontend shows login screen |
| Valid JWT, wrong email | `web-svc` → 403; frontend shows static restricted page |
| `memory-svc` down/timeout during `/api/episodes` or `/api/raw-inputs/recent` | `web-svc` → 502 with error body; frontend shows inline panel error, rest of dashboard unaffected |
| Any service down during `/api/status` | That service marked `"down"` in the response; `web-svc` itself still returns 200 |
| Report file missing for requested date | `web-svc` → 404; frontend shows "no report for this date" |
| JWKS fetch fails (ES256 token, network hiccup) | Verification fails closed → 401, same as an invalid signature; cached key (if previously fetched) is reused for subsequent requests without re-fetching |

---

## Testing

- `web-svc`: unit tests for JWT verification — valid HS256, valid ES256 (fake JWKS server), expired, wrong issuer, wrong audience, wrong email, missing header, unsupported algorithm. Unit tests for the `reports` package's date-path derivation and not-found handling. Handler tests for `/api/episodes`/`/api/raw-inputs/recent`/`/api/status` against a fake upstream HTTP server (real network calls not required).
- `web/`: component tests for the three top-level states (login / restricted / dashboard) with a mocked `web-svc` client; a manual smoke test (sign in with the real Google account, confirm the dashboard renders) since full OAuth redirect flows aren't practical to automate here.

---

## Out of Scope (this iteration)

- **Override/control dispatch** (PAUSE/STOP/RESUME actually reaching `perception-svc`/`thinking-svc`/`action-svc`) — blocked on a Guard Agent design that doesn't exist yet. This spec only shapes `web-svc`'s API surface and the frontend's states so control actions can be added later without restructuring auth.
- **Cloudflare tunnel / public exposure** — mentioned as a future step by the user, not designed here. `cors_allowed_origin` in prod config is a localhost placeholder until that work happens.
- **Roles/permissions table** — deliberately not built; single hardcoded `owner_email` check is sufficient for a single real user.
- **`memory-svc` search/procedures/goals endpoints** — still stubs on `memory-svc`'s side; nothing for `web-svc` to proxy until those are implemented.
- **Writing/note-submission from the dashboard** — the CLI (`soulman note`) already covers this; not duplicated in the web UI.

---

## Related

- `CLAUDE.md` — repo structure, per-service module pattern, dev/prod port and consumer-name conventions
- `Project Soulman.md` — Guard Agent / override design (paper-only today; the reason control actions are out of scope)
- `docs/superpowers/specs/2026-07-18-shared-config-design.md`, `2026-07-18-shared-config-nats-design.md` — `sharedconfig` schema and per-environment config precedent this design follows
- `docs/superpowers/specs/2026-07-18-memory-episodes-design.md` — the `/memory/episodes` endpoint this proxies
- `docs/superpowers/specs/2026-07-17-error-report-action-design.md` — the daily report file format (`report.go`) this reads
- `C:\Users\Lenovo\IdeaProjects\agent-suite\frontend\src\auth.ts`, `UserResolverFilter.java` — the auth pattern being ported
- `memory-svc/NOTES.md`, `action-svc/NOTES.md` — operational notes for the two existing services `web-svc` calls into
