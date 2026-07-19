# Shared Config: NATS URL, Subjects, and Consumer Names Design

**Date:** 2026-07-18
**Status:** Approved
**Phase:** Soulman config cleanup — second iteration, extends the shared JSON config file to NATS wiring

---

## Summary

Extends `common/sharedconfig.Config` (introduced in [[2026-07-18-shared-config-design]] for `watch_paths`) to also carry `nats_url`, the three cross-service subject names, and the two JetStream durable consumer names. These currently live as `NATS_URL`, `STIMULUS_SUBJECT`, `CONSUMER_NAME`, `THINKING_REQUEST_SUBJECT`, and `MEMORY_WRITE_SUBJECT` env vars, set per-service in each environment's `run-<svc>.ps1` script. They move into `config/dev.json` and `config/prod.json` in the vault, becoming the sole source of truth — the env vars are removed entirely, following the same precedent `watch_paths` set (no override, file wins). `HTTP_PORT` is explicitly out of scope and stays an env var.

---

## Why

These settings aren't secrets — they're structural wiring between services, and today they're scattered across four `run-<svc>.ps1` scripts that live outside the git-tracked vault (in `soulman-dev`/`soulman-prod`), duplicated with only the `soulman.dev.*` prefix/`-dev` suffix differing between environments. Moving them into the vault's own `config/<env>.json` puts this wiring in version history next to the rest of the system's design, and removing the env var path avoids a second source of truth for values that must stay consistent across services (e.g. `thinking-svc`'s publish subject must exactly match `action-svc`'s subscribe subject).

---

## Schema (v2)

```json
{
  "watch_paths": ["C:\\Users\\Lenovo\\soulman-dev\\test-errors"],
  "nats_url": "nats://localhost:4222",
  "stimulus_subject": "soulman.dev.stimulus.raw",
  "thinking_request_subject": "soulman.dev.thinking.request",
  "memory_write_subject": "soulman.dev.memory.write",
  "consumer_names": {
    "memory_svc": "memory-svc-dev",
    "thinking_svc": "thinking-svc-dev"
  }
}
```

`config/prod.json` is identical in shape, using the unprefixed subjects/consumer names prod uses today (`soulman.stimulus.raw`, `soulman.thinking.request`, `soulman.memory.write`, `memory-svc`, `thinking-svc`) and `watch_paths` pointing at `C:\Users\Lenovo\DigitalMe\errors`.

`consumer_names` is nested rather than flattened because it's inherently per-service (only `memory-svc` and `thinking-svc` have JetStream durable consumers — `perception-svc` only publishes, `action-svc`'s subscribe is ephemeral core NATS with no durable name) — a nested object keeps that grouping explicit rather than implying every field applies to every service.

---

## Field usage per service

| Service | Fields read | NATS role |
|---|---|---|
| `perception-svc` | `nats_url`, `stimulus_subject` | publishes to `stimulus_subject` |
| `memory-svc` | `nats_url`, `stimulus_subject`, `consumer_names.memory_svc` | JetStream durable consumer on `stimulus_subject` |
| `thinking-svc` | `nats_url`, `stimulus_subject`, `consumer_names.thinking_svc`, `thinking_request_subject` | JetStream durable consumer on `stimulus_subject`; publishes to `thinking_request_subject` |
| `action-svc` | `nats_url`, `thinking_request_subject`, `memory_write_subject` | core-NATS subscribe on `thinking_request_subject`; publishes to `memory_write_subject` |

---

## Components

### `common/sharedconfig/config.go`

```go
type Config struct {
    WatchPaths             []string      `json:"watch_paths"`
    NATSURL                string        `json:"nats_url"`
    StimulusSubject        string        `json:"stimulus_subject"`
    ThinkingRequestSubject string        `json:"thinking_request_subject"`
    MemoryWriteSubject     string        `json:"memory_write_subject"`
    ConsumerNames          ConsumerNames `json:"consumer_names"`
}

type ConsumerNames struct {
    MemorySvc   string `json:"memory_svc"`
    ThinkingSvc string `json:"thinking_svc"`
}
```

`Load` itself is unchanged — it only does `os.ReadFile` + `json.Unmarshal` and returns file/parse errors. It still does not validate that any field is non-empty; that stays the caller's responsibility, same as the existing `watch_paths` split.

### Each service's `config/config.go`

All four services now call `sharedconfig.Load(env("CONFIG_PATH", "./config.json"))`. Today only `perception-svc` does this — `memory-svc`, `thinking-svc`, and `action-svc` gain the call. This means all four `Load()` functions now return `(*Config, error)` — `memory-svc`, `thinking-svc`, and `action-svc` currently return `*Config` directly with no error, so this is a signature change for those three (their `main.go` call sites update accordingly, matching the `if err != nil { log.Fatalf(...) }` pattern `perception-svc/main.go` already uses).

The removed env vars and their defaults (`NATS_URL` → `nats://localhost:4222`, `STIMULUS_SUBJECT` → `soulman.stimulus.raw`, `CONSUMER_NAME` → `memory-svc`/`thinking-svc`, `THINKING_REQUEST_SUBJECT` → `soulman.thinking.request`, `MEMORY_WRITE_SUBJECT` → `soulman.memory.write`) are deleted from each service's `config.go` — no fallback, config file is the only source, consistent with how `WATCH_PATHS` was removed rather than kept as an override.

`HTTP_PORT` remains an env var in all four services — out of scope for this change.

### Fatal-fast validation (each service's `Load`)

Same fail-fast posture as the existing `watch_paths` check in `perception-svc`:

| Service | Fatal if empty |
|---|---|
| `perception-svc` | `nats_url`, `stimulus_subject` (plus existing `watch_paths` check) |
| `memory-svc` | `nats_url`, `stimulus_subject`, `consumer_names.memory_svc` |
| `thinking-svc` | `nats_url`, `stimulus_subject`, `consumer_names.thinking_svc`, `thinking_request_subject` |
| `action-svc` | `nats_url`, `thinking_request_subject`, `memory_write_subject` |

Each service's `Load()` returns an error for the first empty required field it finds; `main.go` treats that the same as a `sharedconfig.Load` failure — `log.Fatalf` at startup.

### `config/dev.json`, `config/prod.json`

Gain the five new fields as shown in the Schema section above. Dev keeps its `soulman.dev.*`-prefixed subjects and `*-svc-dev` consumer names (currently set via env vars in `soulman-dev`'s `run-<svc>.ps1` scripts); prod gets the unprefixed defaults it already relies on today.

### `run-<svc>.ps1` scripts (in `soulman-dev`/`soulman-prod`, not vault-tracked)

Each of the 8 scripts (4 services × dev/prod) drops its now-redundant subject/consumer-name/NATS_URL env var lines:

- `run-perception-svc.ps1` (dev): removes `$env:STIMULUS_SUBJECT` line; had no `NATS_URL` override to remove.
- `run-memory-svc.ps1` (dev): removes `$env:STIMULUS_SUBJECT` and `$env:CONSUMER_NAME` lines.
- `run-thinking-svc.ps1` (dev): removes `$env:STIMULUS_SUBJECT`, `$env:CONSUMER_NAME`, `$env:THINKING_REQUEST_SUBJECT` lines.
- `run-action-svc.ps1` (dev): removes `$env:THINKING_REQUEST_SUBJECT` and `$env:MEMORY_WRITE_SUBJECT` lines.
- All 4 prod scripts: none of them currently override these vars (prod already relies on Go-level defaults), so no lines to remove — they pick up the new prod.json fields automatically once `config/prod.json` includes them.
- `$env:HTTP_PORT` lines stay in every script — out of scope.

---

## Data Flow

```
config/dev.json  (vault, git-tracked)
      │  copied by every run-<svc>.ps1 launch
      ▼
soulman-dev\config.json   (runtime, gitignored)
      │  CONFIG_PATH env var points here
      ▼
sharedconfig.Load()  →  each service's NATSURL / StimulusSubject / etc.
```

Prod follows the identical path via `config/prod.json` → `soulman-prod\config.json`.

---

## Error Handling

| Failure | Behaviour |
|---|---|
| `config.json` missing, or malformed JSON | Same as today — `sharedconfig.Load` returns an error, service logs it and exits (`log.Fatalf`) |
| `config.json` valid but a required field (per the per-service table above) is empty | Service's own `Load()` returns an error for that field, fatal at startup — same posture as the existing empty-`watch_paths` check |
| A field a given service doesn't use is missing/empty (e.g. `memory-svc` doesn't care about `thinking_request_subject`) | No error — each service only validates the fields it actually reads |
| Vault's `config/<env>.json` edited but a service not restarted | No live-reload, same limitation already accepted for `watch_paths` — takes effect on next launch |

---

## Testing

- `common/sharedconfig/config_test.go`: extend existing table-style tests to cover the new fields (valid file with all fields populates correctly; fields default to zero values if absent from JSON, since `Load` itself still doesn't validate).
- Each service's `config/config_test.go`: replace the env var set/unset tests for the removed vars (`NATS_URL`, `STIMULUS_SUBJECT`, `CONSUMER_NAME`, `THINKING_REQUEST_SUBJECT`, `MEMORY_WRITE_SUBJECT`) with tests pointing `CONFIG_PATH` at temp JSON fixtures — valid-file case, and a case per required-field-empty validation error — following the pattern `perception-svc/config_test.go` already established for `watch_paths`.
- No `main_test.go` exists for any service today; the fatal-exit paths are verified manually (build + run each service against a deliberately broken `config.json`, confirm it logs the expected error and exits), same approach the original shared-config plan used.

---

## Out of Scope (this iteration)

- `HTTP_PORT` — stays an env var.
- Live-reload / hot-reconfiguration without a restart.
- Any further settings beyond NATS URL, subjects, and consumer names (e.g. DeepSeek/Discord config) — those remain env-var-driven secrets or are simply not addressed here.

---

## Related

- `docs/superpowers/specs/2026-07-18-shared-config-design.md` — the original shared config file design this extends (`watch_paths`); its "Out of Scope" section flagged this exact migration as future work.
- `docs/superpowers/specs/2026-07-17-common-module-design.md` — the `common` module `sharedconfig` lives in.
- [[Project Soulman]] — system overview, inter-module communication via NATS subjects.
