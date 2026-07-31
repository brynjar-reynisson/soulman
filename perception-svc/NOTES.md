# perception-svc — Operational Notes

Incidents, gotchas, and decisions learned running this service — not captured in the design specs themselves (see `CLAUDE.md`'s Services section for spec links). Read this before touching the Gmail channel or the debugging-tools endpoint.

## Folder-watcher channel

- Watched paths come from the shared config file's `watch_paths` (not `WATCH_PATHS` env var — that was the pre-shared-config convention). A missing config file, malformed JSON, or an empty `watch_paths` list is a **fatal startup error**, not a warning.
- Dev's `config/dev.json` points at `soulman-dev/test-errors/` specifically so manual/test file drops don't mix with real DigitalMe-generated error files; prod's `config/prod.json` points at the real `C:\Users\Lenovo\DigitalMe\errors`.

## Gmail channel (`gmailwatcher` package)

Both `soulman-dev` and `soulman-prod` poll the **same real Gmail inbox**, sharing one OAuth client/refresh token — each environment dedups via its own Gmail label (`soulman/seen-dev` / `soulman/seen`) rather than separate credentials. A message both environments see over time ends up carrying both labels; this is expected, not a bug.

OAuth uses a long-lived offline refresh token. The fix for "why do I have to re-approve this constantly" is **Google Cloud OAuth client Publishing status = Production** — apps left in Testing status get refresh tokens that expire after 7 days. Production status removes that expiry entirely; browser automation was considered and rejected as more fragile than this.

### Real incidents (all fixed)

1. **Padded base64 body decoding.** Assumed Gmail returns unpadded base64url for message body parts (per a literal reading of the docs); it actually returns padded base64 in practice. Every message failed to decode ("illegal base64 data at input byte N") until `decodeBody` was fixed to strip trailing `=` before `base64.RawURLEncoding.DecodeString`.
2. **Blocking startup poll.** `Start()` originally ran the first poll synchronously before returning. Against a real multi-hundred-message backlog, this meant the HTTP server (and its startup log line) never appeared until the whole backlog finished processing. Fixed by moving the immediate poll inside the background poll-loop goroutine, so `Start()` always returns immediately.
3. **The backlog incident.** The Gmail query originally had no date bound. Combined with fix #2 above, a restart triggered the async poll to silently process ~2 months of backlog unattended — hundreds of DeepSeek classifications, and (for the ~10% judged important) many duplicate Discord notifications, doubled since dev and prod share one Discord bot/channel (see `action-svc/NOTES.md`'s known-deferred-bug note). Fixed by adding `after:2026/07/17` to both dev's and prod's Gmail queries (`config/dev.json` / `config/prod.json`) — the project's working rule going forward: **don't let a poll-based channel silently reach back further than a bounded, explicit floor.**

## System Monitor channel (`sysmonitor` package)

Uses `golang.org/x/sys/windows` syscalls directly (`GetDiskFreeSpaceEx`, `GlobalMemoryStatusEx`, `GetSystemTimes`) rather than shelling out to PowerShell or pulling in a cross-platform library like gopsutil — the syscalls are only a few lines each and the dependency was already indirect via `oauth2`/`nats.go`. CPU usage is computed by diffing cumulative idle/total time against the *previous poll's* snapshot rather than sampling twice per poll — natural since the poll interval (300s) is already long enough to average over.

Severity state (`ok`/`warning`/`critical` per check) is **in-memory only**, not persisted like `watcher`'s checkpoint file — a restart resets every check to `ok`, so a still-bad condition re-fires one redundant alert on the next poll. Accepted tradeoff: restarts are rare, and a spurious duplicate alert is far cheaper than the persistence code a checkpoint file would need.

Dev and prod both poll the same physical machine's disk/memory/CPU and will each independently detect and alert on the same real condition — the same accepted duplication the Gmail channel already has for the shared inbox.

`service_health` (added 2026-07-19, see `docs/superpowers/specs/2026-07-19-system-monitor-service-health-design.md`) is a fourth check type, binary (`ok`/`critical`, no `warning` tier) rather than threshold-derived — it probes an external target instead of a local syscall via a separate `healthChecker` seam (`servicehealth.go`), not `statsProvider`. `target` is polymorphic: `http://`/`https://` → GET, any 2xx is healthy; bare `host:port` → TCP dial. Both share the same 300s poll interval and edge-triggered state machine as disk/memory/CPU; the dial/GET timeout is a fixed 5s constant, not configurable per check.

`GET /api/system-monitor/status` (added 2026-07-20, see `docs/superpowers/specs/2026-07-20-system-monitor-dashboard-panel-design.md`) exposes each check's most recent poll result — a separate, mutex-guarded `status` map on `Watcher`, distinct from the `state` map that only tracks severity for publish-gating. `status` updates on every poll regardless of whether severity changed, so it always reflects "what did the last poll see," not "did anything change." Unauthenticated, like every other `perception-svc` route — `web-svc` is where the dashboard's auth gate lives.

## Log Error channel (`logmonitor` package, added 2026-07-27)

Tails every sibling service's `*-startup-err.log` file in `LOG_DIR` for new `slog` `ERROR`-level lines (fsnotify + a 30s reconciliation poll, mirroring `watcher`'s dual detection mechanism), publishing a `Stimulus` the first time a given `(service, msg)` pair is seen this process lifetime — dedup is in-memory only, not persisted, same accepted tradeoff as `sysmonitor`'s severity state. See `docs/superpowers/specs/2026-07-27-log-error-perception-design.md`.

**Deployment gap found while building this feature:** the design spec assumed `LOG_DIR` was already set for every service via its `run-<svc>.ps1` launcher. That's only true for `memory-svc` (which sets it for its own unrelated file-log purpose) — `perception-svc`'s own launcher in both `soulman-dev` and `soulman-prod` does not set `LOG_DIR` today. `perception-svc/config.Load()` defaults it to `./logs` (matching every other env var's local-dev-friendly relative-default pattern in this file), but that only resolves correctly if the process's working directory happens to be the environment root when launched. **Before this channel will find real sibling logs in either environment, add `$env:LOG_DIR = Join-Path $PSScriptRoot "logs"` to `perception-svc`'s `run-perception-svc.ps1` in both `soulman-dev\` and `soulman-prod\`** (mirroring the line `memory-svc`'s launcher already has) — those files live outside this git repo, so this plan cannot make that edit itself.

The checkpoint file (`logmonitor-checkpoint.json`) is derived from `CHECKPOINT_PATH`'s directory rather than needing its own env var — it lives alongside `watcher`'s own `perception-svc-checkpoints.json` in the `state\` folder.

## Pipeline debugging tools (`POST /api/perceive/raw`)

The generic Stimulus-injection endpoint defaults `stimulus_id`, `schema_version`, `received_at`, and `occurred_at` when omitted — but for a while it did **not** default `occurred_at`, which silently broke `cli-note`/`error-report` rule handling downstream (they pass `occurred_at` straight into a `time.Parse` in `action-svc`, and an empty value fails that parse, so the request looked like it succeeded — 202 Accepted — but the report entry was never written, retried once, then silently given up). Fixed: `occurred_at` now defaults to `received_at` when nil, matching what `buildCLIStimulus` already does for `/api/perceive/cli`. If you build another injection helper on top of this endpoint, always populate `occurred_at` explicitly rather than relying on it being optional in spirit.

## Leveled logging (log/slog, added 2026-07-27)

All `log.Printf`/`log.Fatalf` call sites across `main.go`, `watcher`, `gmailwatcher`, and `sysmonitor` replaced with stdlib `log/slog` (`slog.Error`/`slog.Warn`/`slog.Info`) — no new dependency. Genuine failures (fsnotify errors, publish/NATS failures, Gmail API list/get/label-resolution failures) are now `slog.Error`; self-healing/expected-fallback conditions (missing watch path retried by reconciliation, a full event queue falling back to reconciliation, a checkpoint that starts empty, Gmail disabled because credentials aren't configured) are `slog.Warn`; routine lifecycle messages (started, listening, skipping a temp file) are `slog.Info`. Startup `log.Fatalf` calls became `slog.Error(...)` followed by an explicit `os.Exit(1)`.

## Known deferred issue

Dev and prod share one Discord bot/channel/token for the "Soulman Reports" notifications — a real bug (every Gmail-triage Discord notification is sent twice, once per environment), deliberately not fixed yet. See `action-svc/NOTES.md`.

## `internal_health` check type (added 2026-07-27)

A fifth `system_monitor` check type, alongside `disk_space`/`memory`/`cpu`/`service_health`: polls a *soulman* service's own `GET /health` (currently only `memory-svc`, at `http://localhost:9002/health` prod / `:9012` dev) and parses its `dependencies` map (see `docs/superpowers/specs/2026-07-27-dependency-health-design.md`). Two failure modes are kept as independent transition keys so they're never conflated: the endpoint being unreachable at all (`internal_health:<name>`, reported exactly like `service_health`) versus a specific dependency inside a reachable service being down (`internal_health:<name>:<dependency>`, its own edge-triggered state). Both reuse the exact same `publishTransition` machinery `disk_space`/`memory`/`cpu`/`service_health` already share — no second dedup mechanism was built for this.

This is the generic first instance of a pattern meant to extend to other dependencies later (action-svc's Discord webhook, perception-svc's own Gmail polling, NATS connectivity for any service) — each addition is: instrument that dependency with a `common/dephealth.Registry` call, surface it in that service's `/health`, and (if not already present) add one `internal_health` config entry for that service — one check polls a whole service's `/health` and gets all of its dependencies at once, so a second dependency on an already-checked service needs no new config entry.
