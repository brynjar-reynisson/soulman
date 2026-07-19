# Common Module Design (`common`)

**Date:** 2026-07-17
**Status:** Approved
**Language:** Go
**Phase:** Soulman Phase 2 — retrofit after a live schema-drift bug

---

## Summary

`common` is a shared Go module at the vault root holding the wire-format types used by more than one service: `Stimulus` (Perception → Thinking/Memory) and `ActionRequest` (Thinking → Action). It replaces the original approach — each service hand-copying these structs into its own `model` package — after that approach caused a real bug: `thinking-svc` and `action-svc` were each built correctly against a different, incompatible JSON shape for the same message (`correlation_id`/`action_hint` vs `task_id`/`action_type`), so `action-svc` silently dropped every real request. The mismatch wasn't caught by any test, since each service's tests only exercised its own copy of the struct — it only surfaced during live end-to-end testing with both services running together.

---

## Why a Local Module, Not a Registry

All four services already live in this one git repository (no separate repos, no remote configured). A local Go module with `replace` directives gets compiler-enforced consistency without any of the version-registry machinery a typical multi-repo microservices setup would need:

```
require soulman/common v0.0.0
replace soulman/common => ../common
```

Each service still builds and tests independently (`go build ./...` inside any service directory works standalone); the `replace` directive just resolves the module from a relative path instead of a registry. There is no cross-service coupling beyond "the same Go source now backs the same struct."

---

## What's In Scope

| Type | Used by | Crosses |
|---|---|---|
| `Stimulus` (+ `Source`, `Content`, `Attachment`, `ChannelMeta`, `Hints`, `Override`) | `memory-svc`, `perception-svc`, `thinking-svc` | Perception → Thinking, Perception → Memory |
| `ActionRequest` | `thinking-svc`, `action-svc` | Thinking → Action (`soulman.thinking.request`) |

`ActionRequest.Parameters` is `json.RawMessage`, not a typed struct — each Thinking rule marshals its own parameter shape into it, and each Action handler unmarshals it into its own params struct. This keeps `common` from needing to know about every action type's parameter schema; only the envelope (`correlation_id`, `action_hint`, etc.) is shared.

## What's Out of Scope

- Anything internal to a single service (e.g. `perception-svc`'s `CheckpointEntry`, `action-svc`'s `ReportEntryParams`) stays local — no benefit to sharing a type only one service uses.
- The `soulman.memory.write` outcome-log record (`action-svc` → Memory) — nothing consumes it yet, so there's no second party to drift out of sync with. Revisit if `memory-svc` grows a handler for it.
- Non-Go consumers — there are none today, but if one appears, it would work against the JSON wire format regardless of `common` existing.

---

## Project Layout

```
common/
├── go.mod            # module: soulman/common
├── stimulus.go        # Stimulus + nested types
├── stimulus_test.go    # JSON round-trip tests (consolidated from the 3 services' former duplicates)
├── action.go           # ActionRequest
└── action_test.go      # JSON round-trip test + an explicit wire-field-name regression test
```

`TestActionRequest_WireFieldNames` in `action_test.go` asserts the exact JSON keys (`correlation_id`, `action_hint`) and the exact absence of the old, incompatible ones (`task_id`, `action_type`) — a direct regression test for the bug that motivated this module.

---

## Migration Notes

Each service's local `model` package (and, for `thinking-svc`, the locally-defined `ActionRequest` in `rules/rule.go`) was deleted and replaced with an import of `common`. `thinking-svc`'s rule construction changed from building `Parameters` as `map[string]any` to marshaling a small typed `errorReportParams` struct into `json.RawMessage` — the wire bytes are unchanged (a Go map and a struct marshal to the same JSON shape), only the in-process representation moved earlier in the pipeline.

---

## Related

- `docs/superpowers/specs/2026-07-17-error-report-action-design.md` — the Handoff section documents the original two-shapes inconsistency this module corrects
- `docs/superpowers/specs/2026-07-17-perception-svc-design.md`, `2026-07-17-thinking-svc-design.md` — both updated with a note pointing here
- `Perception module.md` — Stimulus schema rationale
