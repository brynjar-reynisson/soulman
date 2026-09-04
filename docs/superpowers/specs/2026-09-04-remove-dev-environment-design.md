# Remove Dev Environment — Design

**Status:** Approved
**Date:** 2026-09-04

## Problem

Soulman has run two parallel environments — `dev` and `prod` — since early in the project, each with its own config file, NATS subjects, JetStream consumer names, Postgres schema, ports, and runtime directory (`C:\Users\Lenovo\soulman-dev\` / `C:\Users\Lenovo\soulman-prod\`). This system is personal, single-user software with no separate testing audience and no deploy pipeline that benefits from a staging tier — the dev/prod split is pure overhead: double the build/restart work in `start-everything.ps1`, double the firewall rules, a real bug already on record where an unfixed default (`SOULMAN_ROOT`, `SCHEMA`) silently pointed prod at dev's tree unless explicitly overridden, and a duplicate-notification bug (dev and prod share one Discord bot/channel, so every Gmail-triage alert sends twice) that has sat deliberately unfixed because dev was never going to be the target of a real fix. Removing dev entirely — from code, build/deploy scripts, the `deploy-soulman-services` skill, documentation, and finally the runtime directory and its Postgres schemas — eliminates all of this at once.

## Goals

- Delete `config/dev.json`; `config/prod.json` is the sole config file going forward, unchanged in content and filename.
- Fix the three service-config defaults that currently point at dev (`memory-svc` `SCHEMA`, `projects-svc` `SCHEMA`, `action-svc` `SOULMAN_ROOT`) so they default correctly with no override needed.
- Remove the `soulman.dev.*` subject entries from every JetStream stream's `Subjects` list (Go code and the manually-provisioned `STIMULUS` stream).
- Update `start-everything.ps1` and `setup-firewall-rules.ps1` to operate on `soulman-prod` only.
- Simplify `soulman-prod`'s own `run-*.ps1` launcher scripts now that the dev-default overrides they carried are no longer needed.
- Rewrite the global `deploy-soulman-services` skill (`~/.claude/skills/deploy-soulman-services/SKILL.md`) to describe a single-environment workflow.
- Remove dev/prod dual-environment framing from living documentation: root `CLAUDE.md`, every service's `NOTES.md`, and the root module design docs (`Action module.md`, `Perception module.md`, `Memory module.md`, `Thinking module.md`, `Project Soulman.md`).
- Delete `C:\Users\Lenovo\soulman-dev\` and drop the `memory_dev`/`projects_dev` Postgres schemas.

## Non-Goals

- No renaming of anything "prod" — `soulman-prod\`, `memory_prod`, `projects_prod`, unprefixed NATS subjects, ports 9001–9007, and every existing "prod" identifier stay exactly as they are today. This is a subtraction, not a rename.
- No edits to `docs/superpowers/specs/*.md` or `docs/superpowers/plans/*.md` — this repo's own convention treats those as the historical record of already-approved, already-shipped decisions, not edited after the fact. Several of them describe dev as part of what was actually built at the time; that history stays intact.
- No fix for pre-existing gaps unrelated to dev/prod duality noticed along the way — e.g. `start-everything.ps1` never launches `projects-svc` at login, and `setup-firewall-rules.ps1` never provisioned a rule for it either. Both are left alone; out of scope for this change.
- No change to how the single surviving environment is deployed (still hand-built via `run-<svc>.ps1`, still no CI/CD).

## Scope of Changes

### 1. Config

Delete `config/dev.json`. No changes to `config/prod.json`.

### 2. Code

**Dev-defaulting configs** (currently wrong-by-default, masked only by an explicit override elsewhere — see Data Flow below for where those overrides live today):

| File | Change |
|---|---|
| `memory-svc/config/config.go` | `env("SCHEMA", "memory_dev")` → `env("SCHEMA", "memory_prod")` |
| `projects-svc/config/config.go` | `env("SCHEMA", "projects_dev")` → `env("SCHEMA", "projects_prod")` |
| `action-svc/config/config.go` | `env("SOULMAN_ROOT", `C:\Users\Lenovo\soulman-dev`)` → `env("SOULMAN_ROOT", `C:\Users\Lenovo\soulman-prod`)` |

Update the corresponding `config_test.go` assertions in each package where they hardcode the old default (default-value tests only — tests that merely use a `-dev`-suffixed string as arbitrary example fixture data for parsing, unrelated to which default the code falls back to, are left alone).

**JetStream subject lists** — remove the `"soulman.dev.*"` entry from each `Subjects` slice:

| File | Stream |
|---|---|
| `action-svc/natsclient/consumer.go` | `THINKING_REQUEST` |
| `action-svc/natsclient/publisher.go` | `MEMORY_WRITE` |
| `thinking-svc/natsclient/publisher.go` | `THINKING_REQUEST` |
| `memory-svc/natsconsumer/memory_write_consumer.go` | `MEMORY_WRITE` |

The `STIMULUS` stream is provisioned by hand via the `nats` CLI, not in Go code — its subject list gets the same cleanup via `nats stream edit STIMULUS --subjects soulman.stimulus.raw` (or equivalent), run once against the shared local NATS server.

**Doc-comment cleanup in code** — `common/sharedconfig/config.go`'s package doc and the `GmailConfig`/`SchoolConfig` comments describing "both dev and prod populate this," and `perception-svc/config/config.go`'s comment referencing `soulman-dev`/`soulman-prod` launchers, get reworded to describe the single environment.

### 3. Build/deploy pipeline

- **`start-everything.ps1`**: remove the `soulman-dev` root from the `foreach ($svc in @(...))` loop over service names — only `soulman-prod` remains.
- **`setup-firewall-rules.ps1`**: remove the `dev` entry from `$envs`, leaving only `@{ Label = "prod"; Root = "C:\Users\Lenovo\soulman-prod" }`. Firewall rule display names (`"Soulman $svc (prod)"`) are left as-is — re-running the script is idempotent and harmless even though "(prod)" is now the only label that will ever appear.
- **`soulman-prod\run-memory-svc.ps1`**: remove `$env:SCHEMA = "memory_prod"` and its preceding `Write-Warning` about the schema not existing yet (it exists — see Section 6) — the fixed code default now handles it.
- **`soulman-prod\run-projects-svc.ps1`**: remove `$env:SCHEMA = "projects_prod"` and its explanatory comment, same reasoning.
- **`soulman-prod\run-action-svc.ps1`**: remove `$env:SOULMAN_ROOT = $PSScriptRoot` and its comment about the dev-default footgun, same reasoning.
- Each `run-<svc>.ps1`/`run-web.ps1` header comment's "(dev)"/"(prod) environment" qualifier is simplified to drop the now-meaningless environment label (e.g. "Builds memory-svc from the vault source and runs it." — no parenthetical).

### 4. `deploy-soulman-services` skill

Global skill at `~/.claude/skills/deploy-soulman-services/SKILL.md` (outside this vault, not tracked by this repo's git history). Rewritten to:

- Drop the "soulman-dev or soulman-prod" framing from the frontmatter `description` and body — a single root, `C:\Users\Lenovo\soulman-prod\`.
- Keep the core safety rule unchanged: never hand-roll a robocopy/copy/delete/`Remove-Item` command against the runtime directory; always go through `run-<svc>.ps1`. The underlying reason (`soulman-prod` isn't a git repo, `.env` has no backup) is exactly as true with one environment as it was with two.
- Drop the "Both environments" example block from the Go-service restart section (no second root to restart in parallel).
- Update the `web` frontend section's example the same way.

### 5. Documentation

**Root `CLAUDE.md`:**
- Delete the entire "Running dev and prod simultaneously" section, including the dual-port/dual-subject/dual-consumer-name explanation and the `sync-soulman-dev.cmd`/`sync-soulman-prod.cmd` references (the latter already point at files that don't exist in the repo — stale regardless of this change).
- Each service's numbered entry (`memory-svc`, `perception-svc`, `thinking-svc`, `action-svc`, `web-svc`, `projects-svc`) loses its dev-specific asides (e.g. memory-svc's `internal_health` URL mentioning both `config/dev.json`/`config/prod.json`, action-svc's dev/prod-shared-Discord-bot mention, web-svc's dual-port table).
- The `## Two Environments` table (`Dev` / `Prod` rows pointing at `~/soulman-dev/memory/` and `~/soulman-prod/memory/`) becomes a single-line statement of the one path, since the OpenCode memory-agent workflow (a separate concern from the Go services' dev/prod split) also loses its dev half.
- `### Startup` section: `start-everything.ps1` description updated to reflect it now builds one environment, not two; `setup-firewall-rules.ps1` description updated similarly.
- `### Logging` and other cross-cutting sections that incidentally mention "both dev and prod" get trimmed to the single case.

**Per-service `NOTES.md`:** remove dev-only operational details (dev ports, dev config-file mentions, "verified against live dev infrastructure" asides). Where a note documents a real incident that becomes structurally impossible once dev is gone, mark it resolved rather than deleting the history:
- `action-svc/NOTES.md`: the "dev and prod share one Discord bot, every notification sends twice" bug is annotated as resolved by dev's removal (no second environment left to double-send from), not deleted.
- `memory-svc/NOTES.md`: the `memory_prod`-schema-created-late incident write-up is about prod and stays; only its dev-specific asides (e.g. the dual `internal_health` URL) are trimmed.
- `perception-svc/NOTES.md`, `thinking-svc/NOTES.md`, `common/NOTES.md`, `cli/NOTES.md`, `projects-svc/NOTES.md`, `web-svc/NOTES.md`: dev-specific mentions trimmed; anything describing prod-only behavior or history is untouched.

**Root module design docs** (`Action module.md`, `Perception module.md`, `Memory module.md`, `Thinking module.md`, `Project Soulman.md`): dev/prod dual-environment mentions removed; these describe module architecture, not environment topology, so changes here should be small.

**Out of scope (explicitly preserved):** `docs/superpowers/specs/*.md`, `docs/superpowers/plans/*.md`, `memory/Implementation Plan.md` if it turns out to contain historical dev references rather than forward-looking ones (checked case-by-case during implementation, but the default is: don't touch it unless it's actively describing dev as something still to be maintained).

### 6. Environment teardown (destructive, run last)

Only after every other change above is made and the surviving `soulman-prod` environment is confirmed still working end-to-end (see Verification):

1. Delete `C:\Users\Lenovo\soulman-dev\` (built exes, logs, `.env`, `run-*.ps1`, the OpenCode memory-agent setup under `memory\.opencode\`) via its own `run-<svc>.ps1`-independent removal — there is no launcher-script protection to route through here since the whole directory is going away, not a subset of files inside it.
2. Drop the dev Postgres schemas against the shared local Supabase instance (`supabase_db_agent-suite`, port 54322):
   ```
   docker exec supabase_db_agent-suite psql -U postgres -c "DROP SCHEMA memory_dev CASCADE; DROP SCHEMA projects_dev CASCADE;"
   ```
   `memory_prod`, `projects_prod`, and the unrelated `projects_test` schema (used only by `projects-svc/store/store_test.go`) are untouched.

## Data Flow (today, before removal)

Understanding why the code defaults are currently dev-shaped:

- `memory-svc`'s `run-memory-svc.ps1` (dev) leaves `SCHEMA` at its code default (`memory_dev`) and copies `config/dev.json`. `soulman-prod\run-memory-svc.ps1` explicitly sets `$env:SCHEMA = "memory_prod"` and copies `config/prod.json` — the override exists precisely because the code default is wrong for prod.
- `projects-svc`'s `run-projects-svc.ps1` (prod) explicitly sets `$env:SCHEMA = "projects_prod"` for the same reason; its code default is `projects_dev`.
- `action-svc`'s `soulman-prod\run-action-svc.ps1` explicitly sets `$env:SOULMAN_ROOT = $PSScriptRoot`, with a comment reading "config.go's SOULMAN_ROOT default points at soulman-dev — must override explicitly here or prod would write reports into the dev tree." That comment is the clearest existing evidence that this default was always backwards for a prod-only reality.

After this change, each of those three defaults is corrected at the source, and the launcher-script overrides that existed only to compensate are removed as dead weight.

## Verification

1. **Build check**: `go build ./...` succeeds in each of `memory-svc`, `perception-svc`, `thinking-svc`, `action-svc`, `projects-svc`, `common`, `cli` after the code changes.
2. **Unit tests**: existing test suites pass, including the updated default-value assertions in `memory-svc/config`, `projects-svc/config`, and `action-svc/config`.
3. **Config load**: `soulman-prod`'s `run-<svc>.ps1` scripts still start every service successfully with no `SCHEMA`/`SOULMAN_ROOT` override present — confirms the new defaults are correct on their own.
4. **NATS**: confirm via `nats stream info STIMULUS` / `THINKING_REQUEST` / `MEMORY_WRITE` that no `soulman.dev.*` subject remains in any stream's subject list, and that prod traffic still flows end-to-end (a real stimulus reaches memory-svc, thinking-svc, and action-svc as before).
5. **`start-everything.ps1`**: dry-read confirms it now launches only `soulman-prod`'s six services (`memory-svc`, `perception-svc`, `thinking-svc`, `action-svc`, `web-svc`, `web`) with no `soulman-dev` counterpart.
6. **Firewall script**: `setup-firewall-rules.ps1` (read-through, not necessarily re-run since it requires elevation) only lists `prod` in `$envs`.
7. **Skill**: `~/.claude/skills/deploy-soulman-services/SKILL.md` no longer mentions `soulman-dev` anywhere.
8. **Docs**: `grep -ri dev` across `CLAUDE.md`, every `NOTES.md`, and the root module docs turns up nothing except incidental English words (e.g. "development," if any) — no remaining references to the dev environment, dev config, dev ports, or dev schemas.
9. **Teardown (last)**: `C:\Users\Lenovo\soulman-dev\` no longer exists; `docker exec supabase_db_agent-suite psql -U postgres -c "\dn"` no longer lists `memory_dev` or `projects_dev`, and still lists `memory_prod`, `projects_prod`, `projects_test`.
