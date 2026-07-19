# Pipeline Debugging Tools Design

**Date:** 2026-07-18
**Status:** Approved
**Phase:** Post-incident hardening — durable inter-service queues + manual inspection/injection tooling

---

## Summary

Tonight's Gmail-channel incident (a ~2-month unread backlog silently flooding Discord, and a data-loss gap between `thinking-svc` and `action-svc` that made the true scope hard to diagnose) exposed two structural gaps: the `soulman.thinking.request` and `soulman.memory.write` subjects are ephemeral core NATS with no persistence, and there was no way to feed a single hand-crafted stimulus through the pipeline (or inspect what the Discord bot had actually sent) without touching real external systems. This design closes both gaps:

1. **Durable queues** — `THINKING_REQUEST` and `MEMORY_WRITE` become real JetStream streams (like `STIMULUS` already is), created idempotently by code at startup. `action-svc`'s consumption of `thinking.request` becomes a durable pull consumer instead of an ephemeral core-NATS subscribe — this is the actual fix for tonight's silent-drop bug.
2. **Generic Stimulus injection** — a new `perception-svc` endpoint and `soulman` CLI subcommand that publish a hand-written or captured Stimulus JSON file directly, bypassing any real channel adapter (Gmail, folder-watcher, etc.) entirely.
3. **Discord history reading** — a small Go package wrapping Discord's REST API to fetch a channel's recent messages using the existing bot token, exposed via another CLI subcommand, so either the user or Claude can verify what was actually sent without needing separate Discord access.

---

## Part 1: Durable JetStream Queues

### Why

`thinking-svc`'s publish to `soulman.thinking.request` and `action-svc`'s subscribe to it are both plain core NATS today (`thinking-svc/natsclient/publisher.go`, `action-svc/natsclient/subscriber.go`) — fire-and-forget with no persistence. When `action-svc` isn't actively subscribed at the exact moment a message is published, that message is gone forever. This is what caused roughly half of tonight's 828 Gmail-triage decisions (durably recorded in `memory_dev.raw_inputs` and confirmed fully classified by DeepSeek) to never reach `action-svc`'s report/notify step, making the true scope of the incident hard to reconstruct after the fact. `soulman.memory.write` (`action-svc/natsclient/publisher.go`'s `PublishOutcome`) has the same ephemeral shape, currently with no consumer at all.

### New Streams

Two new JetStream streams, matching `STIMULUS`'s existing shape (`File` storage, `Limits` retention):

```
THINKING_REQUEST
  Subjects: soulman.thinking.request, soulman.dev.thinking.request
  Max Age: 30d (matching STIMULUS's existing retention)

MEMORY_WRITE
  Subjects: soulman.memory.write, soulman.dev.memory.write
  Max Age: 30d
```

Both created idempotently at startup via `jetstream.JetStream.CreateOrUpdateStream(ctx, cfg)` — the calling service doesn't need to know or care whether the stream already exists; the config is just re-asserted every launch, the same "always current" pattern already used for `go build` and the shared config file copy.

`thinking-svc` ensures `THINKING_REQUEST` exists (it's the publisher). `action-svc` ensures `THINKING_REQUEST` exists too, defensively, before creating its consumer (it might start before `thinking-svc` does) — `CreateOrUpdateStream` is idempotent so both services doing this is harmless. `action-svc` also ensures `MEMORY_WRITE` exists, since it's `MEMORY_WRITE`'s only publisher today.

### `thinking-svc`'s publisher (`natsclient/publisher.go`)

Changes from a plain `nc.Publish` to a JetStream publish, mirroring `perception-svc/natspublish.Publisher`'s exact shape (`jetstream.New(nc)`, ensure stream, `js.Publish(ctx, subject, bytes)` — waits for the JetStream ack, giving the publisher a real signal that the message was durably stored, unlike today's fire-and-forget).

### `action-svc`'s consumption of `thinking.request`

Changes from `natsclient.Subscribe` (ephemeral core NATS) to a durable pull consumer, mirroring `thinking-svc/natsclient/consumer.go`'s exact shape against the `STIMULUS` stream: `stream.CreateOrUpdateConsumer` with a `Durable` name, `AckExplicitPolicy`, `FilterSubject`. New per-environment consumer names — `action-svc` (prod) / `action-svc-dev` (dev) — added to `common/sharedconfig.ConsumerNames` as a new `ActionSvc` field, following the exact precedent `MemorySvc`/`ThinkingSvc` already set.

Ack policy: same as `thinking-svc`'s existing consumer — ACK unconditionally after calling the handler, regardless of the handler's returned error. A failed dispatch already has its own retry-once-then-give-up behavior inside `dispatchAppendDailyReportEntry`/`dispatchGmailTriage`; NAKing at the NATS level would only cause the whole message to redeliver and double-process, not recover anything additional.

### `action-svc`'s outcome publisher (`PublishOutcome`)

Changes from `nc.Publish` to a JetStream publish against the new `MEMORY_WRITE` stream, same shape as `thinking-svc`'s publisher change. No consumer is added for `MEMORY_WRITE` — nothing reads it today either (the existing doc comment already says so: "forward-compatible logging... not a hard dependency"). Durability alone satisfies "a queue to inspect": `nats stream view MEMORY_WRITE` (or a similar CLI incantation) lets either of us browse outcome records directly, without needing to write a consumer for a subject nothing currently needs to react to.

### Config additions

`common/sharedconfig.ConsumerNames` gains `ActionSvc string` (json `action_svc`). `config/dev.json`'s `consumer_names` gains `"action_svc": "action-svc-dev"`; `config/prod.json`'s gains `"action_svc": "action-svc"`.

### Out of scope for this part

- Redelivery/backoff tuning beyond what `thinking-svc`'s existing consumer already does (ACK-always, no retry at the NATS level) — this design only extends the existing pattern to a second consumer, not redesigning it.
- A consumer for `MEMORY_WRITE` — durability without consumption is the explicit scope for now.
- Migrating `STIMULUS`'s own manual setup to code-managed — out of scope; only the two new streams get the code-managed treatment, established as the new convention going forward.

---

## Part 2: Generic Stimulus Injection

### Why

Testing the pipeline today means triggering a real external event (an actual unread email, a real file drop) — slow, imprecise, and (as tonight demonstrated) risky to iterate on against production data. A generic injection path lets either of us feed an exact, hand-controlled Stimulus into the pipeline — including replaying a real captured Stimulus for a regression test, or crafting a synthetic edge case no real external event would conveniently produce.

### `perception-svc` endpoint: `POST /api/perceive/raw`

Mirrors `POST /api/perceive/cli`'s existing shape (`perception-svc/httpserver/cli.go`) closely enough to be a familiar sibling, not a new pattern:

- Request body: a JSON object that may be a complete `common.Stimulus` (every field specified — `channel`, `content`, `source`, `channel_metadata`, etc.) or a partial one supplying only some fields.
- Handler fills in any *required* field that's missing or zero-valued with the same defaults `buildCLIStimulus` already uses: `stimulus_id` (new UUIDv7 if blank), `received_at` (now, UTC, if zero), `schema_version` (1, if zero), `occurred_at` (defaults to `received_at` if nil — rule handlers pass it straight into a downstream `time.Parse`, so leaving it unset would silently fail dispatch rather than fail loudly). Every other field — critically `channel` — is taken verbatim from the request body if present; `channel` has no default and is a required field (a 400 if missing, since "which channel is this pretending to be" is the one thing the caller must always specify).
- No validation beyond "is this valid JSON matching `common.Stimulus`'s shape" — deliberately permissive, since the entire point is precise low-level control for testing, including intentionally malformed edge cases if the caller wants to construct one.
- Publishes via the same `s.publisher.Publish(r.Context(), stimulus)` already wired into `Server`.
- Response: `202 Accepted` with `{"stimulus_id": "..."}`, same shape as `/api/perceive/cli`.

### `soulman` CLI: `soulman inject <file>`

New subcommand on the existing `cli` module (`cli/args.go`, `cli/main.go`, `cli/client/client.go`) — not a separate binary:

- `soulman inject path/to/stimulus.json [--dev]` reads the file, POSTs its raw bytes to `/api/perceive/raw`, prints the resulting `stimulus_id` (or the server's error message) — the same success/failure reporting shape `soulman note`/`soulman "<text>"` already use.
- No client-side JSON validation or schema-building — the file's bytes are sent through as-is (this endpoint's whole point is precise control; validating client-side would just be a second, potentially-inconsistent copy of the same logic already living server-side).

### Example usage

```bash
# Replay a captured real Gmail stimulus for regression testing
soulman inject captured-gmail-stimulus.json --dev

# Quick synthetic test: only the essentials, defaults fill the rest
echo '{"channel": "gmail", "content": {"raw_text": "test body", "content_type": "text"}}' > /tmp/test.json
soulman inject /tmp/test.json --dev
```

### Out of scope for this part

- Any authentication/authorization on `/api/perceive/raw` beyond what `/api/perceive/cli` already has (none — both are local-machine-only endpoints, same trust model as the rest of this single-user personal project).
- A "replay from memory" convenience (e.g., `soulman replay <stimulus_id>` pulling a past Stimulus out of `memory_dev.raw_inputs` and re-injecting it) — a nice future addition, not built now.

---

## Part 3: Discord History Reading

### Why

Tonight, verifying what the "Soulman Reports" bot had actually sent to Discord required guessing from indirect evidence (report files, JetStream consumer state) because Claude's own Discord MCP connection uses a *different* bot application and has no access to DM history between the user and a different bot. A small piece of code using the Soulman Reports bot's own credentials (already configured as `DISCORD_BOT_TOKEN`/`DISCORD_CHANNEL_ID`) closes that gap directly.

### Placement

A new package, not under `action-svc/notify` (which is send-only and stays that way) — placed to anticipate a future "Discord as a perception channel" direction the user flagged (reading Discord messages as *input*, not just inspecting sent history), without building that channel now. Package: `perception-svc/discordread`. This keeps `action-svc/notify` conceptually pure ("things action-svc can do") while putting read-capable Discord code where a future Discord perception channel would naturally live.

### `discordread` package

```go
package discordread

// Message is the minimal shape needed for debugging — not a full Discord
// API model.
type Message struct {
    ID        string
    Author    string
    Content   string
    Timestamp time.Time
}

// FetchHistory fetches up to limit most-recent messages from channelID
// using botToken, via Discord's GET /channels/{id}/messages REST endpoint.
// Read-only — this package never sends anything.
func FetchHistory(ctx context.Context, botToken, channelID string, limit int) ([]Message, error)
```

Implementation: a single HTTP GET to `https://discord.com/api/v10/channels/{channelID}/messages?limit={limit}` with an `Authorization: Bot {token}` header — no SDK dependency needed for a single read-only endpoint, consistent with this project's existing preference for hand-rolled HTTP clients over pulling in a full third-party SDK (`thinking-svc/llm/deepseek.go` already does the same for DeepSeek's API).

### `soulman` CLI: `soulman discord-history [--limit N] [--dev]`

Another new subcommand, reading `DISCORD_BOT_TOKEN`/`DISCORD_CHANNEL_ID` from the environment (same vars `action-svc` already uses — the CLI runs on the same machine and can read the same `.env`-sourced environment, no new credential plumbing) and printing each message's timestamp, author, and content to stdout, newest-last (chronological, easiest to read top-to-bottom in a terminal).

`--dev`/prod distinction here is about which `.env` to read credentials from, not a different Discord channel — as tonight's incident found, dev and prod currently point at the *same* Discord bot/channel. This design does not change that (out of scope — see below); the flag exists for consistency with `soulman`'s other subcommands and so the tool works correctly on whichever machine/environment layout it's run from.

### Out of scope for this part

- Separating dev's and prod's Discord notifications into different channels/bots (the double-notification issue tonight's incident also surfaced) — a real, valid fix, but a separate concern from "read message history"; call it out as a follow-up, not bundled into this design.
- Any Discord *write* capability beyond what `action-svc/notify/discord.go` already has.
- Building the actual "Discord as a perception channel" this package's placement anticipates — noted as a future direction, not started now.

---

## Testing

- **Durable queues:** `thinking-svc`'s publisher and `action-svc`'s consumer/outcome-publisher get unit tests mirroring the existing JetStream test patterns already used for `perception-svc/natspublish` and `thinking-svc/natsclient/consumer_test.go` (real local NATS, unique per-test stream/consumer names to avoid cross-test collision — the same pattern already established, not a new one). Manual verification: restart `action-svc` mid-stream and confirm queued `thinking.request` messages are still delivered on reconnect, directly exercising the fix for tonight's bug.
- **Stimulus injection:** `perception-svc/httpserver`'s new handler gets unit tests mirroring `cli_test.go`'s existing shape (valid full Stimulus, valid partial Stimulus with defaults filled, missing `channel` → 400). The CLI subcommand gets a test mirroring `args_test.go`'s existing coverage for `note`/plain-text parsing.
- **Discord history:** `discordread.FetchHistory`'s HTTP-calling logic is thin enough (one GET, one auth header) that per the same precedent `gmailwatcher/client.go` established (real Gmail API calls have no automated test, verified manually), this is verified manually against the real bot/channel rather than built out with HTTP-mocking infrastructure.

---

## Related

- `docs/superpowers/specs/2026-07-18-gmail-channel-design.md`, `docs/superpowers/specs/2026-07-18-gmail-triage-action-design.md` — the features whose incident tonight motivated this design.
- `docs/superpowers/specs/2026-07-18-soulman-cli-design.md` — the existing CLI module and `/api/perceive/cli` endpoint this design's new endpoint/subcommands sit alongside.
- [[Perception module]] — general Perception module design; `discordread`'s placement anticipates this doc's "Discord as a perception channel" future direction.
