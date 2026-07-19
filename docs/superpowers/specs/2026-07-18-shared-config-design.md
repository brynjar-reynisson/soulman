# Shared JSON Config Files Design

**Date:** 2026-07-18
**Status:** Approved
**Phase:** Soulman config cleanup — first non-secret settings file, replacing an env-var override

---

## Summary

Introduces one shared, git-tracked JSON config file per environment (`config/dev.json`, `config/prod.json` in the vault), holding non-secret settings common to the four services. The first field is `watch_paths` — the list of folders `perception-svc` watches — moved out of the `WATCH_PATHS` env var and the per-environment PowerShell startup scripts. A new `common/sharedconfig` package (in the existing shared `common` Go module) defines the schema and loads it. This is deliberately scoped to `perception-svc` only; the other three services gain the file-copy plumbing but no code changes, since they have nothing to read from it yet.

---

## Why a file instead of more env vars

Secrets (`DISCORD_BOT_TOKEN`, `DEEPSEEK_API_KEY`, etc.) stay in `.env`, which deliberately lives outside the git-tracked vault. `watch_paths` isn't a secret — it's a structural setting, and the project already anticipates more than one watched folder per environment. A comma-separated env var doesn't scale cleanly to a growing, possibly-nested list; a JSON file that's part of the vault's own version history does, and it puts the setting in the same "design lives in the vault" place as everything else about this system.

---

## Schema (v1)

```json
{
  "watch_paths": [
    "C:\\Users\\Lenovo\\soulman-dev\\test-errors"
  ]
}
```

`config/prod.json` is identical in shape, with `watch_paths` pointing at `C:\Users\Lenovo\DigitalMe\errors` instead. `watch_paths` is a list from the start, even though each environment has exactly one entry today — adding a second watched folder later is a one-line JSON edit, no schema or code change.

Future fields for other services get added to this same schema as they're needed (e.g. something `thinking-svc` or `action-svc` wants to configure without it being a secret) — this doc only specifies `watch_paths`; extending the schema later is a small follow-up, not a redesign.

---

## Components

### `common/sharedconfig` (new package)

```go
package sharedconfig

type Config struct {
    WatchPaths []string `json:"watch_paths"`
}

func Load(path string) (*Config, error) {
    // os.ReadFile + json.Unmarshal; returns an error (not a fatal log) —
    // the caller decides how to fail, consistent with how errors already
    // propagate up through natspublish.New / watcher.New in main.go today.
}
```

Lives in `common` because that's the module every service already imports via a local `replace` directive — no new `go.mod` entry needed. Only `perception-svc` calls it today; the other services are free to import it whenever they have a field to read.

### `perception-svc/config/config.go`

- `WatchPaths` is populated by calling `sharedconfig.Load(env("CONFIG_PATH", "./config.json"))` instead of parsing `WATCH_PATHS`.
- The `WATCH_PATHS` env var and the now-unused `splitPaths` helper are removed entirely — the file is the only source, no override.
- All other fields (`NATSURL`, `HTTPPort`, `CheckpointPath`, `ReconcileInterval`, `StimulusSubject`) are unchanged — still env-var driven, still with their existing defaults.

### `perception-svc/main.go`

- If `sharedconfig.Load` returns an error, or the loaded `WatchPaths` list is empty, `log.Fatalf` immediately at startup — matches the existing fail-fast pattern already used for `natspublish.New` and `watcher.New` errors on the very next lines. A misconfigured or missing watch list is a real operational problem, not something to silently run degraded on (unlike the Discord/DeepSeek credentials, which are genuinely optional).

### `config/dev.json`, `config/prod.json` (new, vault root)

Git-tracked, source of truth. No secrets ever go here — same boundary the project already draws around `.env`.

### PowerShell startup scripts

All 8 `run-<svc>.ps1` scripts (4 services × dev/prod) gain a copy step, placed alongside the existing `go build` step:

```powershell
$configSrc = "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\config\dev.json"   # or prod.json
$configDst = Join-Path $PSScriptRoot "config.json"
Copy-Item $configSrc $configDst -Force
$env:CONFIG_PATH = $configDst
```

This mirrors the existing `CHECKPOINT_PATH` pattern — an explicit, pinned path set by the script rather than relying on whatever directory the process happens to launch from — and the existing "fresh from vault on every launch" pattern already used for `go build`. Only `perception-svc`'s binary actually reads `CONFIG_PATH` right now; the other three scripts copy the file and set the var anyway, so the plumbing is already in place the day another service needs it.

`run-perception-svc.ps1` (dev) additionally loses its `WATCH_PATHS` override block (the comment about not mixing manual test drops with real `DigitalMe` files moves to a comment on `config/dev.json`'s `watch_paths` entry instead). `run-perception-svc.ps1` (prod) had no `WATCH_PATHS` override to remove; it now gets an explicit `config/prod.json` entry instead of relying on the Go-level hardcoded default.

---

## Data Flow

```
config/dev.json  (vault, git-tracked)
      │  copied by every run-<svc>.ps1 launch
      ▼
soulman-dev\config.json   (runtime, gitignored — same status as .env)
      │  CONFIG_PATH env var points here
      ▼
sharedconfig.Load()  →  perception-svc's WatchPaths
```

Prod follows the identical path via `config/prod.json` → `soulman-prod\config.json`.

---

## Error Handling

| Failure | Behaviour |
|---|---|
| `config.json` missing at the copied runtime path | `sharedconfig.Load` returns an error → `perception-svc` logs it and exits (`log.Fatalf`) |
| `config.json` present but malformed JSON | Same — `json.Unmarshal` error surfaces through `Load`, fatal at startup |
| `config.json` valid but `watch_paths` is an empty list | Treated the same as missing/malformed — fatal at startup, since perception-svc has nothing meaningful to do with zero watched folders |
| Vault's `config/<env>.json` edited but a service not restarted | No live-reload (same limitation the daily-report design already accepts for `REPORT_SEND_TIME`) — takes effect on next launch, since each `run-<svc>.ps1` copies fresh only when it runs |

---

## Testing

- `common/sharedconfig`: new unit tests — valid file loads correctly, missing file returns an error, malformed JSON returns an error, empty `watch_paths` loads successfully as an empty slice (validation that empty-is-fatal belongs to the *caller*, not the loader).
- `perception-svc/config/config_test.go`: existing tests that set/unset `WATCH_PATHS` are replaced with tests that point `CONFIG_PATH` at a temp file (valid-file case) and at a nonexistent path (error case), following the same `os.Setenv`/`defer os.Unsetenv` pattern already used for the other env vars in this file.
- `main.go`'s fatal-exit path itself has no automated test (there's no `main_test.go` today, and `main` isn't currently under test elsewhere in this service) — verified manually instead, the same way the perception-svc implementation plan's smoke test already runs the built binary and checks its logged output.

---

## Out of Scope (this iteration)

- Any config field beyond `watch_paths` — no other service reads the file yet.
- Live-reload / hot-reconfiguration without a restart.
- Validating that watched paths actually exist on disk at config-load time (perception-svc's existing watcher startup already surfaces that failure separately).
- Moving any currently-env-var-driven setting (ports, NATS subjects, etc.) into this file — only `WATCH_PATHS` is migrated.

---

## Related

- `docs/superpowers/specs/2026-07-17-perception-svc-design.md` — original `WATCH_PATHS` env var this supersedes
- `docs/superpowers/specs/2026-07-17-common-module-design.md` — the `common` module this new package joins
- [[Perception module]] — general Perception module design
