# web-svc — Operational Notes

Incidents, gotchas, and decisions learned running this service — not captured in the design specs themselves (see `CLAUDE.md`'s Services section for spec links).

## SUPABASE_URL / SUPABASE_JWT_SECRET are not in this repo

Both are required environment variables (fatal startup error if either is blank), set via `.env` in each of `soulman-dev\` and `soulman-prod\` (loaded by `load-env.ps1`, same as `action-svc`'s Discord token). They must be filled in by hand before `web-svc` will start — the JWT secret is the same Supabase project secret `agent-suite`'s backend already uses (`supabase.jwt-secret` in its `application.yml`), since both apps verify tokens from the same hosted Supabase project.

## Owner-email check, not a roles table

Unlike `agent-suite`'s `UserResolverFilter` (DB-backed `suite_user`/`user_role` lookup), `web-svc`'s `auth.Verifier` does no database lookup at all — it just compares the JWT's `email` claim against `web.owner_email` in `config/dev.json`/`prod.json`. This is deliberate: Soulman has exactly one real user. If this ever needs multiple authorized users, that's a real design change, not a config tweak.

## `/api/status` never fails even when a downstream service is down

Each of the four downstream `/health` checks in `apiStatus` has its own 5s timeout and failures are captured per-service (`"down"` in the response map) rather than failing the whole request — a single service being down (e.g. during a rebuild) shouldn't take down the dashboard's status panel along with it.

## `/api/system-monitor` is a plain proxy, not a status aggregator

Unlike `/api/status` (which independently probes 4 services' `/health` endpoints and reports up/down), `/api/system-monitor` (added 2026-07-20) is a thin passthrough of `perception-svc`'s `GET /api/system-monitor/status` via the existing `proxyGet` helper — same pattern as `/api/episodes`/`/api/raw-inputs/recent`. The data's freshness is entirely `perception-svc`'s: whatever its `sysmonitor.Watcher` last polled (up to 5 minutes stale), not re-checked by `web-svc` on each dashboard load.

## `SUPABASE_JWT_SECRET` is optional, not required

`web-svc/config/config.go`'s `Load()` used to treat a blank `SUPABASE_JWT_SECRET` as a fatal startup error, alongside `SUPABASE_URL`. That was wrong and has been fixed: this deployment's hosted Supabase project signs auth tokens with ES256 (asymmetric, verified via its JWKS endpoint — see `web-svc/auth/verifier.go`), not the legacy HS256 shared-secret scheme, a conclusion reinforced by `agent-suite` having run against this same Supabase project for a while without ever setting `SUPABASE_JWT_SECRET`/`SUPABASE_PROD_JWT_SECRET` anywhere. `Load()` now defaults `SUPABASE_JWT_SECRET` to an empty string via `env("SUPABASE_JWT_SECRET", "")` rather than erroring.

A blank value does not mean "HS256 tokens simply fail verification against a mismatched secret" — that phrasing (from an earlier version of this note) was wrong and described a real auth-bypass vulnerability rather than a benign failure mode: HMAC-SHA256 with a zero-length key is a fully well-defined, computable operation, so a forged HS256 token signed with an empty key would have verified successfully against an empty `v.jwtSecret`, letting an attacker self-sign arbitrary claims (including the `email` claim the owner check relies on) with no knowledge of any real secret. The actual, correct behavior (fixed in `web-svc/auth/verifier.go`'s `Verify` keyfunc) is that an empty/unconfigured `jwtSecret` makes the HS256 branch refuse to verify *any* HS256 token outright, before ever attempting HMAC verification — it returns an error from the keyfunc rather than a key, which `ParseWithClaims` maps to `Unauthorized`. So an unset secret narrows accepted tokens to ES256/JWKS only; it does not "fail closed on a per-token basis," it excludes the entire algorithm. `SUPABASE_URL` remains fatal-if-blank, since ES256 verification depends on it to build both the JWKS URL and the expected issuer.

## `auth.Verifier`'s JWKS cache never refreshes or refetches on an unknown `kid`

`getOrFetchPublicKey` (`web-svc/auth/verifier.go`) caches EC public keys by `kid` after fetching Supabase's JWKS endpoint, but if a token arrives with a `kid` the cache doesn't recognize, it silently falls back to the first-seen key instead of refetching the JWKS document. This was flagged as a residual gap in Task 2's code review and deliberately left unfixed there as out of scope. In practice this means: if Supabase ever rotates its signing key, tokens signed with the new key will carry a new `kid`, the cache (populated before rotation) won't have it, and verification will keep retrying the stale pre-rotation key rather than fetching the new one — so newly issued tokens would fail verification until the process is restarted (which repopulates the cache from a fresh JWKS fetch). A restart is currently the only way to pick up a rotated key; there is no TTL or unknown-`kid` refetch trigger.

## `reports.Read` now combines two files, not one (added 2026-07-20)

See `docs/superpowers/specs/2026-07-20-daily-report-importance-split-design.md`. `reports.PathForDate` gained an `important bool` parameter and `reports.Read`'s return value changed from one file's raw bytes to a combined `## Important` / `## Not Important` string — `web-svc/httpserver/reports_handler.go` needed no changes at all, since it only calls `reports.Read` and forwards `content` through unchanged.

## Dashboard is tunneled to `soulman.breynisson.org` (prod only, added 2026-07-21)

`cloudflared` maps `https://soulman.breynisson.org` to prod's Vite preview server on `localhost:5191`. Two things had to change for this to work, both prod-only (dev is never tunneled):

1. `web/vite.config.ts`'s `preview.allowedHosts` must list `soulman.breynisson.org` — Vite's preview server rejects requests whose `Host` header isn't localhost or in this allowlist (`Blocked request. This host is not allowed.`). This file lives in the vault and is mirrored by `run-web.ps1`'s robocopy into both `soulman-dev\web\` and `soulman-prod\web\`, so the allowlist applies to both environments' preview servers even though only prod is actually tunneled.
2. `soulman-prod\web\.env`'s `VITE_AUTH_REDIRECT_URL` must be `https://soulman.breynisson.org` (was `http://localhost:5191`), and that same URL must be added to the Supabase project's Auth > URL Configuration > Redirect URLs allowlist — otherwise Google OAuth completes but Supabase refuses to redirect back to the tunneled origin. `soulman-prod\web\.env` is excluded from the robocopy mirror (see `run-web.ps1`'s `/XF .env*`), so it's edited directly in `soulman-prod\` rather than in the vault. Since `VITE_*` vars are inlined at build time, a rebuild (`npm run build`) of prod's `web` is required after changing it — restarting the service via `run-web.ps1` (or the next `start-everything.ps1` login run) picks it up.

A third change was needed and easy to miss because it fails silently: `config/prod.json`'s `web.cors_allowed_origin` (`web-svc/httpserver/server.go`'s `cors.Handler`) was still the `http://localhost:5191` placeholder noted as such in `docs/superpowers/specs/2026-07-19-soulman-web-dashboard-design.md`. Once the dashboard is loaded from `https://soulman.breynisson.org`, every browser fetch to `web-svc` (still `http://localhost:9005` — same machine, so it reaches web-svc fine) carries that as its `Origin`, which the stale allowlist rejects; the browser then blocks the response with no CORS header rather than surfacing a clean error. `web/src/App.tsx`'s effect only special-cases a 403 `ApiError` (→ "restricted" screen) — any other failure, including this one, falls through to `setView('login')`, so the symptom looks like sign-in silently failing and looping back to the Google button, with no visible error at all. Fixed by setting `cors_allowed_origin` to `https://soulman.breynisson.org`; `web-svc` reads it from `config.json` at startup (copied from `config/prod.json` by `run-web-svc.ps1`), so a `web-svc` restart is required after changing it.

Separately, loading the dashboard from a public origin while `web-svc`/other services still listen on `localhost` now also triggers Chrome/Edge's Local Network Access prompt ("this site wants to access other apps and services on this device") on first load — that's an unrelated browser permission gate (public page → local-network target) introduced in Chrome 141+, not a Soulman bug; clicking Allow is the correct and sufficient response to it.
