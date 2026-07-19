# common — Operational Notes

## Why this module exists (real incident)

`thinking-svc` and `action-svc` originally hand-copied the `ActionRequest` struct independently rather than sharing one definition. Their copies used different field names for the same concepts (`action_hint`/`correlation_id` in one, `action_type`/`task_id` in the other). Because JSON unmarshaling silently zero-values fields it doesn't recognize, `action-svc` accepted every request without error but silently dropped all of them (`action_hint` was always empty, so the dispatch switch always hit its "unknown action_hint" branch) — this was only caught during live end-to-end testing, not by any test suite, since each service's own tests were self-consistent with its own (wrong) copy of the struct.

`common` exists specifically to make this class of bug a compile error instead of a silent runtime no-op: every service imports `common.Stimulus`/`common.ActionRequest` via a local `replace soulman/common => ../common` directive (no registry/versioning needed, everything lives in one repo). When adding a field to either type, change it once here — the compiler catches every call site that needs updating.

## `sharedconfig` schema evolution

Added incrementally, each behind its own spec/plan:

1. `watch_paths` — perception-svc's folder-watcher paths (first field, replacing a `WATCH_PATHS` env var).
2. `nats_url`, `stimulus_subject`, `thinking_request_subject`, `memory_write_subject`, `consumer_names` (`memory_svc`, `thinking_svc`, later `action_svc`) — moved dev/prod NATS wiring out of per-service env vars into one git-tracked place.
3. `gmail` (`query`, `seen_label`, `poll_interval_seconds`) — perception-svc's Gmail channel.

Pattern for adding a new field: extend the `Config`/nested struct here, add fatal-fast validation matching the existing style (empty string / non-positive int → startup error), add it to both `config/dev.json` and `config/prod.json`, and extend `common/sharedconfig`'s tests for both the populated and zero-value cases.
