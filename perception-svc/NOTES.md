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

## Pipeline debugging tools (`POST /api/perceive/raw`)

The generic Stimulus-injection endpoint defaults `stimulus_id`, `schema_version`, `received_at`, and `occurred_at` when omitted — but for a while it did **not** default `occurred_at`, which silently broke `cli-note`/`error-report` rule handling downstream (they pass `occurred_at` straight into a `time.Parse` in `action-svc`, and an empty value fails that parse, so the request looked like it succeeded — 202 Accepted — but the report entry was never written, retried once, then silently given up). Fixed: `occurred_at` now defaults to `received_at` when nil, matching what `buildCLIStimulus` already does for `/api/perceive/cli`. If you build another injection helper on top of this endpoint, always populate `occurred_at` explicitly rather than relying on it being optional in spirit.

## Known deferred issue

Dev and prod share one Discord bot/channel/token for the "Soulman Reports" notifications — a real bug (every Gmail-triage Discord notification is sent twice, once per environment), deliberately not fixed yet. See `action-svc/NOTES.md`.
