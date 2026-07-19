# Thinking Service Design (`thinking-svc`)

**Date:** 2026-07-17
**Status:** Approved
**Language:** Go
**Phase:** Soulman Phase 2 — first Thinking module runtime (single rule: error → daily report)

---

## Summary

`thinking-svc` is the first runtime implementation of the Thinking module — a long-running Go binary that consumes Stimulus events from NATS, matches them against a small rule table, and for matches, calls an LLM (DeepSeek) where judgment is needed before publishing an Action Request. This iteration implements exactly one rule (`folder-watcher` → `append_daily_report_entry`, per `docs/superpowers/specs/2026-07-17-error-report-action-design.md`). It is deliberately **not** the full reasoning loop described in `Thinking module.md` — no Memory RETRIEVE queries, no goal tracking, no multi-step reasoning. Those arrive when a rule actually needs them.

---

## Architecture

```
NATS STIMULUS stream (soulman.stimulus.raw)
        │  JetStream consumer "thinking-svc"
        ▼
   Rule Matcher
        │
        ├─ match  → DeepSeek call (if the rule needs one)
        │              │
        │              ▼
        │        Action Request builder
        │              │
        │              ▼
        │        NATS publish: soulman.thinking.request
        │        (core NATS, ephemeral — see Result Handling)
        │
        └─ no match → ACK, no-op

HTTP server (port 9003)
  GET /health
```

---

## Rule Table

v1 ships a single entry, expressed as an ordered list so more rules can be appended later without restructuring:

```go
type Rule struct {
    Match  func(*model.Stimulus) bool
    Handle func(ctx context.Context, s *model.Stimulus, llm *DeepSeekClient) (*ActionRequest, error)
}
```

**Rule 1 — Error report:**
- `Match`: `s.Channel == "folder-watcher"`
- `Handle`: calls DeepSeek for a one-line summary, builds the `append_daily_report_entry` Action Request exactly as specified in `error-report-action-design.md`

A stimulus matching no rule is ACKed and dropped — no-op, no episodic log write this iteration (raw log durability is already handled by `memory-svc`; a "nothing happened" event isn't worth a write yet).

---

## DeepSeek Call

- Endpoint: `https://api.deepseek.com/chat/completions` (OpenAI-compatible Chat Completions API)
- Model: `deepseek-chat`
- Single non-streaming call per matched stimulus. System prompt: "Summarize this error in one line, under 120 characters, plain text, no markdown." User message: `stimulus.content.raw_text`, truncated to 4000 characters before sending (bounds cost/latency — the full raw content still travels through untouched in the Action Request's `raw_content` parameter, only the summarization input is truncated).
- Timeout: 15s. On timeout or API error: fall back to a deterministic summary — `"<filename> (summary unavailable: <error>)"` — rather than failing the whole action. A missing summary shouldn't block logging the raw error.

---

## Action Request Publish

- Subject: `soulman.thinking.request` (per `Messaging Bus.md`) — core NATS publish, not JetStream. This subject is ephemeral by design in the existing architecture; `action-svc` must be running to receive it.
- `correlation_id`: generated (UUID) and included per the standard handoff shape, even though v1 doesn't use it for resumption (see below) — keeps the message shape forward-compatible with the full protocol in `Action module.md`.

---

## Result Handling (v1 simplification)

Per `inter-module-communication-design.md`, action results normally return to Thinking asynchronously via `soulman.action.result.<correlation_id>` so a suspended reasoning thread can resume. This rule doesn't suspend anything — Thinking's job is done once the Action Request is published — so `thinking-svc` does **not** subscribe to any result subject in v1. `action-svc` logs its own outcome directly to Memory via the existing `soulman.memory.write` fire-and-forget pattern instead. This is an explicit v1 exception to the documented architecture; revisit once a rule exists that actually needs to resume reasoning based on an action's result.

---

## Project Layout

**Update (2026-07-17, post-implementation):** the `model/` package and the locally-defined `ActionRequest` struct (originally in `rules/rule.go`) were both superseded by a shared `soulman/common` module (`common.Stimulus`, `common.ActionRequest`) after a naming mismatch between this service's and `action-svc`'s independently-copied Action Request structs caused `action-svc` to silently drop every request — see `error-report-action-design.md`'s Handoff correction. `thinking-svc` now imports both types via a `replace soulman/common => ../common` directive in its `go.mod`.

```
soulman-dev/thinking-svc/
├── main.go
├── go.mod                 # module: soulman/thinking-svc
├── config/
│   └── config.go          # NATS_URL, HTTP_PORT, DEEPSEEK_API_KEY, DEEPSEEK_MODEL, DEEPSEEK_BASE_URL, DEEPSEEK_TIMEOUT_SECONDS
├── rules/
│   ├── rule.go             # Rule type + registry
│   └── error_report.go     # Rule 1 implementation
├── llm/
│   └── deepseek.go         # DeepSeekClient: Summarize(ctx, text) (string, error)
├── natsclient/
│   ├── consumer.go         # JetStream consumer on STIMULUS stream
│   └── publisher.go        # core NATS publish to soulman.thinking.request
└── httpserver/
    └── server.go            # GET /health only
```

---

## Configuration (env vars)

| Variable | Default | Notes |
|---|---|---|
| `NATS_URL` | `nats://localhost:4222` | |
| `HTTP_PORT` | `9003` | Next available port after memory-svc (9002) and perception-svc (9001) |
| `DEEPSEEK_API_KEY` | — | Required |
| `DEEPSEEK_MODEL` | `deepseek-chat` | |
| `DEEPSEEK_BASE_URL` | `https://api.deepseek.com` | |
| `DEEPSEEK_TIMEOUT_SECONDS` | `15` | |

---

## Error Handling

| Failure | Behaviour |
|---|---|
| DeepSeek API unreachable/timeout | Fall back to deterministic summary text (see above); action still proceeds |
| DeepSeek returns empty/garbage response | Same fallback |
| NATS publish of Action Request fails | Log error; the stimulus is still ACKed. `soulman.thinking.request` is ephemeral/fire-and-forget in the existing architecture — no redelivery mechanism exists for it in v1, so a failed publish here silently drops the action. Acceptable for v1: the human-visible impact is "one error missing from tomorrow's report," not silent data loss — the raw stimulus is still durably logged by `memory-svc` regardless |
| Malformed/unparseable stimulus from NATS | Log, ACK, skip |

---

## Out of Scope (this iteration)

- Memory RETRIEVE queries (semantic search, episodes, procedures, goals) — no rule needs them yet
- Multiple concurrent rules / rule priority conflicts
- The full decision-type vocabulary from `Thinking module.md` (`NO_ACTION`, `ACKNOWLEDGE`, `ASK_USER`, `UPDATE_GOAL`, `LEARN`, `REFUSE`) — only `INVOKE_ACTION` is implemented
- Result round-trip / reasoning resumption
- Override handling (PAUSE/STOP/RESUME) — no override-capable channel exists yet

---

## Related

- `docs/superpowers/specs/2026-07-17-error-report-action-design.md` — the logical rule this service implements
- `docs/superpowers/specs/2026-07-17-perception-svc-design.md` — produces the stimulus consumed here
- `docs/superpowers/specs/2026-07-17-action-svc-design.md` — consumes the Action Request published here
- [[Thinking module]] — the full eventual design this is a first slice of
- [[Messaging Bus]] — subject names used here
