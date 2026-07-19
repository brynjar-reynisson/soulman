# web-svc — Operational Notes

Incidents, gotchas, and decisions learned running this service — not captured in the design specs themselves (see `CLAUDE.md`'s Services section for spec links).

## SUPABASE_URL / SUPABASE_JWT_SECRET are not in this repo

Both are required environment variables (fatal startup error if either is blank), set via `.env` in each of `soulman-dev\` and `soulman-prod\` (loaded by `load-env.ps1`, same as `action-svc`'s Discord token). They must be filled in by hand before `web-svc` will start — the JWT secret is the same Supabase project secret `agent-suite`'s backend already uses (`supabase.jwt-secret` in its `application.yml`), since both apps verify tokens from the same hosted Supabase project.

## Owner-email check, not a roles table

Unlike `agent-suite`'s `UserResolverFilter` (DB-backed `suite_user`/`user_role` lookup), `web-svc`'s `auth.Verifier` does no database lookup at all — it just compares the JWT's `email` claim against `web.owner_email` in `config/dev.json`/`prod.json`. This is deliberate: Soulman has exactly one real user. If this ever needs multiple authorized users, that's a real design change, not a config tweak.

## `/api/status` never fails even when a downstream service is down

Each of the four downstream `/health` checks in `apiStatus` has its own 5s timeout and failures are captured per-service (`"down"` in the response map) rather than failing the whole request — a single service being down (e.g. during a rebuild) shouldn't take down the dashboard's status panel along with it.

## `SUPABASE_JWT_SECRET` is optional, not required

`web-svc/config/config.go`'s `Load()` used to treat a blank `SUPABASE_JWT_SECRET` as a fatal startup error, alongside `SUPABASE_URL`. That was wrong and has been fixed: this deployment's hosted Supabase project signs auth tokens with ES256 (asymmetric, verified via its JWKS endpoint — see `web-svc/auth/verifier.go`), not the legacy HS256 shared-secret scheme, a conclusion reinforced by `agent-suite` having run against this same Supabase project for a while without ever setting `SUPABASE_JWT_SECRET`/`SUPABASE_PROD_JWT_SECRET` anywhere. `Load()` now defaults `SUPABASE_JWT_SECRET` to an empty string via `env("SUPABASE_JWT_SECRET", "")` rather than erroring.

A blank value does not mean "HS256 tokens simply fail verification against a mismatched secret" — that phrasing (from an earlier version of this note) was wrong and described a real auth-bypass vulnerability rather than a benign failure mode: HMAC-SHA256 with a zero-length key is a fully well-defined, computable operation, so a forged HS256 token signed with an empty key would have verified successfully against an empty `v.jwtSecret`, letting an attacker self-sign arbitrary claims (including the `email` claim the owner check relies on) with no knowledge of any real secret. The actual, correct behavior (fixed in `web-svc/auth/verifier.go`'s `Verify` keyfunc) is that an empty/unconfigured `jwtSecret` makes the HS256 branch refuse to verify *any* HS256 token outright, before ever attempting HMAC verification — it returns an error from the keyfunc rather than a key, which `ParseWithClaims` maps to `Unauthorized`. So an unset secret narrows accepted tokens to ES256/JWKS only; it does not "fail closed on a per-token basis," it excludes the entire algorithm. `SUPABASE_URL` remains fatal-if-blank, since ES256 verification depends on it to build both the JWKS URL and the expected issuer.

## `auth.Verifier`'s JWKS cache never refreshes or refetches on an unknown `kid`

`getOrFetchPublicKey` (`web-svc/auth/verifier.go`) caches EC public keys by `kid` after fetching Supabase's JWKS endpoint, but if a token arrives with a `kid` the cache doesn't recognize, it silently falls back to the first-seen key instead of refetching the JWKS document. This was flagged as a residual gap in Task 2's code review and deliberately left unfixed there as out of scope. In practice this means: if Supabase ever rotates its signing key, tokens signed with the new key will carry a new `kid`, the cache (populated before rotation) won't have it, and verification will keep retrying the stale pre-rotation key rather than fetching the new one — so newly issued tokens would fail verification until the process is restarted (which repopulates the cache from a fresh JWKS fetch). A restart is currently the only way to pick up a rotated key; there is no TTL or unknown-`kid` refetch trigger.
