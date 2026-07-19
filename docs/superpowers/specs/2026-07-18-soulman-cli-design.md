# Soulman CLI Design

**Date:** 2026-07-18
**Status:** Approved
**Phase:** Soulman Phase 2 — first CLI push channel (Perception module.md's Channel Taxonomy)

---

## Summary

Adds a `soulman` command-line tool that lets the human feed input into Soulman directly from a terminal, implementing the `CLI` push channel described in `Perception module.md`. It supports two behaviors:

1. **`soulman note "<text>"`** — direct-effect, mechanical. No LLM/Thinking judgment involved: the text is appended to today's daily report exactly like a folder-watcher error is, via the same `append_daily_report_entry` action.
2. **`soulman "<text>"`** — general free-text stimulus. Flows through Perception → Thinking for goal-driven reasoning. Since Thinking's rule table is still v1 (folder-watcher only), no rule matches this yet — the stimulus is logged to Memory's raw input log and no action results, until a future reasoning rule is built.

Override commands (`PAUSE`/`STOP`/`RESUME`/`STATUS`) are explicitly **out of scope** for this iteration — no Guard Agent exists yet in `action-svc` to consume them, and building a command with no consumer isn't worth doing now.

The CLI is a thin HTTP client. It does not talk to NATS or construct `Stimulus` values itself — it POSTs to a new endpoint on `perception-svc`, which remains the sole owner of Stimulus construction and publishing (consistent with the design doc's "one canonical format, adapters at the edge" principle).

---

## Component 1: `perception-svc` — new endpoint

### `POST /api/perceive/cli`

Added to `perception-svc/httpserver`, alongside the existing `/health`.

**Request body:**
```json
{
  "text": "disk cleanup done",
  "mode": "note",
  "priority": "normal"
}
```

| Field | Values | Default | Notes |
|---|---|---|---|
| `text` | non-empty string | — (required) | 400 if blank |
| `mode` | `"note"` \| `"stimulus"` | `"stimulus"` if omitted | 400 if any other value |
| `priority` | `low`\|`normal`\|`high`\|`critical` | `"normal"` if omitted | 400 if any other value |

**Behavior:**

1. Validate `text`/`mode`/`priority` as above.
2. Build a `Stimulus`:
   - `StimulusID`: UUIDv7 (same as `watcher.buildStimulus`, falling back to UUIDv4 on generation failure)
   - `SchemaVersion`: 1
   - `ReceivedAt`: now (UTC)
   - `OccurredAt`: now (UTC) — CLI input has no separate "occurred" time
   - `Channel`: `"cli-note"` if `mode == "note"`, else `"cli"`
   - `Source`: `{identity: "cli", authenticated: true, auth_method: "system"}` — matches the design doc's "CLI runs as the local OS user, trusted by default"; no additional auth check on this endpoint
   - `Content`: `{raw_text: text, content_type: "text", raw_payload: {}, attachments: []}`
   - `ChannelMeta.MessageID`: `sha256(text + received_at)` — no natural external id like folder-watcher's filename+mtime
   - `Hints`: `{priority: <request priority>, tags: []}`
   - `Override`: `{is_override: false, params: {}}`
3. Publish via the existing `Publisher` interface (`natspublish.Publisher`, already used by `watcher`) — no new publish logic.
4. Respond:
   - `202 Accepted`, body `{"stimulus_id": "<id>"}` on success
   - `503 Service Unavailable` if the NATS publish fails — this endpoint has no local-fallback queue (unlike channel adapters that must never lose data, this is an interactive tool; the human re-running the command is the retry mechanism)
   - `400 Bad Request` with an error message for validation failures

**Known duplication:** Stimulus-building logic now exists in both `watcher/folderwatcher.go` and this new handler. Left un-abstracted — the shapes differ enough (file vs. free text, different channel/source fields) that a shared builder would be premature. Worth extracting if a third caller appears.

---

## Component 2: `thinking-svc` — new mechanical rule

New file `thinking-svc/rules/cli_note.go`, registered in `Registry` alongside `ErrorReportRule`:

```go
var CLINoteRule = Rule{
	Name:  "cli-note",
	Match: func(s *common.Stimulus) bool { return s.Channel == "cli-note" },
	Handle: handleCLINote,
}
```

`handleCLINote` builds an `append_daily_report_entry` `ActionRequest` directly from `s.Content.RawText` — no filename/watched-path extraction (that's specific to folder-watcher's file-based stimuli). No LLM call, same reasoning as `ErrorReportRule`: a short human-typed note doesn't need summarization.

```json
{
  "correlation_id": "<uuid>",
  "intent": "Log this note to today's daily report",
  "action_hint": "append_daily_report_entry",
  "parameters": {
    "summary": "<stimulus.content.raw_text, verbatim>",
    "raw_content": "<stimulus.content.raw_text, verbatim>",
    "source_path": "cli/note",
    "occurred_at": "<stimulus.occurred_at>"
  },
  "risk_level": "low",
  "urgency": "normal",
  "expected_outcome": "one entry appended to today's report file",
  "fallback": "if fs-agent fails, retry once; if it fails again, log to episodic memory with error:execution tag and give up silently — a missed report entry is not worth interrupting the human"
}
```

`summary` and `raw_content` are identical — CLI notes are short, human-typed text; there's no meaningful distinction between "summary" and "full content" worth introducing. `source_path` is the fixed string `"cli/note"` (no source file exists) — `action-svc`'s existing `append_daily_report_entry` handler (`report.formatEntry`) bracket-prefixes the report line with `filepath.Dir(source_path)`; a bare `"cli"` would produce `filepath.Dir("cli") == "."`, so `"cli/note"` is used specifically so the report line reads `[cli]`.

This is the same `ActionRequest` shape `ErrorReportRule` produces, so **no changes to `action-svc`** are needed — it already dispatches `append_daily_report_entry` regardless of which rule produced it.

**The general `"cli"` channel gets no new rule.** Stimuli with `channel == "cli"` fall through `Process`'s existing `nil, nil` path (already the behavior for any unmatched stimulus) — logged to Memory's raw input log, no action taken, until a future reasoning-capable rule is added.

---

## Component 3: the `soulman` CLI

New Go module `cli/` at the vault root (module `soulman/cli`, binary name `soulman`), following the existing one-module-per-component convention.

### Commands

```
soulman "remind me to check server logs at 5pm"     # mode=stimulus
soulman note "disk cleanup done"                     # mode=note
```

### Flags (apply to both forms)

| Flag | Values | Default | Effect |
|---|---|---|---|
| `--priority` | `low`\|`normal`\|`high`\|`critical` | `normal` | Passed through as the stimulus's `hints.priority` |
| `--dev` | (boolean) | off | Targets `soulman-dev`'s perception-svc (`http://localhost:9011`) instead of prod's (`http://localhost:9001`) |

No other configuration surface (no env var, no config file) — the flag covers the only two real targets, keeping the CLI's config minimal.

### Structure

- `cli/main.go` — arg parsing (plain `flag` package / manual `os.Args` dispatch; no CLI framework needed for two commands and two flags), builds the request, calls the client, prints result, sets exit code
- `cli/client/client.go` — `Send(baseURL string, req Request) (stimulusID string, err error)`, a thin `net/http` POST wrapper around the `/api/perceive/cli` endpoint

### Output & error handling

| Case | Behavior |
|---|---|
| Success | Print `logged (stimulus_id: <id>)` (note) or `sent (stimulus_id: <id>)` (stimulus); exit 0 |
| Empty text argument | Validated locally before any network call; print usage error to stderr, exit 1 |
| `perception-svc` unreachable (connection refused) | Print error to stderr, exit 1 — no retry, the human re-runs |
| `perception-svc` returns 4xx | Print the server's error message, exit 1 |
| `perception-svc` returns 503 (NATS down) | Print error, exit 1 — no local queueing in the CLI |

### Build

On-demand only (`go build ./cli` or `go run ./cli ...`) — not added to `start-everything.ps1`'s pre-build step. It's an interactive tool invoked occasionally, not a daemon; unlike the four services there's no benefit to having a fresh binary sitting on disk after every login.

---

## Testing

| Component | Approach |
|---|---|
| `cli/client` | `httptest.Server`-backed tests: success, 400, 503, connection-refused paths |
| `cli/main` | Direct tests of arg-parsing logic (note vs. stimulus, `--priority`, `--dev`) |
| `perception-svc/httpserver` | New endpoint tested the same way `/health` already is — `httptest.NewRecorder`/`NewRequest`, asserting status code, published `Stimulus` fields (via a fake `Publisher`), and validation error cases |
| `thinking-svc/rules` | `cli_note_test.go` mirrors `error_report_test.go` — `Match` true only for `channel: "cli-note"`, `Handle` produces the correct `ActionRequest` |

---

## Out of Scope (this iteration)

- Override commands (`PAUSE`/`STOP`/`RESUME`/`STATUS`) — no Guard Agent exists yet to consume them
- A shared `Stimulus`-building abstraction between `watcher` and the new CLI endpoint handler
- Any general-purpose reasoning rule for the plain `"cli"` channel — stimuli on that channel are logged only, per Thinking's current v1 scope
- Env var / config file for the CLI's target URL — the `--dev` flag covers the only two real targets

---

## Related

- `Perception module.md` — CLI push channel definition (Channel Taxonomy, Push Channel Design)
- `docs/superpowers/specs/2026-07-17-error-report-action-design.md` — the `append_daily_report_entry` action and `ErrorReportRule` this design mirrors
- `docs/superpowers/specs/2026-07-17-perception-svc-design.md` — existing Stimulus construction and publish pattern
- `docs/superpowers/specs/2026-07-17-thinking-svc-design.md` — rule table / `Registry` mechanism
