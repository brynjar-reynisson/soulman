# Gmail Triage & Notify Design (Thinking rule + Action dispatch)

**Date:** 2026-07-18
**Status:** Approved
**Phase:** Soulman Phase 2 — second Thinking rule + second `action-svc` dispatch action type

---

## Summary

Defines how Thinking judges the importance of `gmail`-channel stimuli (produced by `perception-svc`'s Gmail channel, see `docs/superpowers/specs/2026-07-18-gmail-channel-design.md`) using an LLM, and how Action turns that judgment into a permanent daily-report record plus — when judged important — a near-immediate, debounced Discord notification. Unlike the deterministic `folder-watcher` → `append_daily_report_entry` rule, this rule's core decision (important vs. not) is genuine LLM judgment, not a fixed condition.

---

## Why

The user wants Thinking to read unread-inbox email content and decide whether it's important enough to interrupt them, and — if so — have Action message them on Discord soon (not wait for the existing 10:00 AM daily digest). Every email, important or not, should still leave a record in the daily report so nothing is silently dropped and the LLM's judgment can be reviewed/calibrated later. That calibration mechanism itself (e.g. correcting missed/false-positive verdicts) is out of scope here — the user described it as "some new kind of perception we'll design later"; this design only needs to keep the classification prompt easy to find and tweak in the meantime.

---

## Thinking Rule: `GmailTriageRule`

**Trigger condition:** `stimulus.channel == "gmail"`.

**Decision:** Always `INVOKE_ACTION` with `action_hint: "triage_gmail_email"` — verdict-dependent fields vary, but an action is always produced (contrast with `ErrorReportRule`, which is the only other rule and always logs; here, the *notification* half is conditional but the *logging* half never is).

### Extracting fields from the Stimulus

Per `gmail-channel-design.md`'s Stimulus Field Mapping:

| Field needed | Stimulus source |
|---|---|
| Sender | `source.identity` |
| Subject | `channel_metadata.channel_specific.subject` |
| Body | `content.raw_text` |
| Thread ID | `channel_metadata.thread_id` |
| Occurred at | `stimulus.occurred_at` |

### LLM classification call

Body text is truncated to 4000 characters before being sent to the classifier (same cost/latency bound `error_report.go`'s summarizer call already applies to error text). The classifier call is a single non-streaming DeepSeek request via the same `*DeepSeekClient` used for summarization, just a different method/prompt/response shape (see "LLM Client" section below).

### Building the Action Request

```json
{
  "correlation_id": "<uuid>",
  "intent": "Notify me about this important email" | "Log this email to today's daily report",
  "action_hint": "triage_gmail_email",
  "parameters": {
    "sender": "<source.identity>",
    "subject": "<channel_specific.subject>",
    "body_excerpt": "<first ~200 chars of raw_text, '…' appended if truncated>",
    "reason": "<LLM's stated reason, or classifier-failure fallback text>",
    "important": true | false,
    "thread_id": "<channel_metadata.thread_id>",
    "occurred_at": "<stimulus.occurred_at>"
  },
  "risk_level": "low",
  "urgency": "high" | "normal",
  "expected_outcome": "one report entry appended, plus an immediate (debounced) Discord notification if judged important",
  "fallback": "if report append fails, retry once; if it fails again, log to episodic memory with error:execution tag and give up silently. If the Discord notification fails, no retry is attempted — a missed immediate ping is not worth blocking on since the report entry is the permanent record."
}
```

`urgency` is informational only in v1 — `action-svc` does not branch on it (matching how `risk_level` is already informational-only until a Guard Agent exists). This is the **only** rule this spec defines. It does not attempt thread-level deduplication, multi-email correlation, or any decision type beyond `INVOKE_ACTION`.

---

## LLM Client (`thinking-svc/llm`)

`Rule.Handle`'s existing `summarizer llm.Summarizer` parameter is generalized to `client llm.Client`, a composed interface:

```go
type Client interface {
    Summarizer
    Classifier
}

type Classifier interface {
    ClassifyImportance(ctx context.Context, sender, subject, body string) (important bool, reason string, err error)
}
```

`*DeepSeekClient` implements both `Summarizer` and `Classifier` on the same struct/HTTP client — only the system prompt, user message, and response parsing differ per method. `ErrorReportRule`'s `Handle` signature changes only in the parameter's declared type (still calls `.Summarize()` only); its test fake gains a no-op `ClassifyImportance` stub purely to satisfy the interface.

### Classifier prompt/response shape

- System prompt (package-level `const classifierSystemPrompt`, kept as a plain string constant — the single easiest place to tweak this later, matching the existing `systemPrompt` const pattern for summarization): instructs the model to judge importance from sender/subject/body and respond with **strict JSON only**, shape `{"important": true|false, "reason": "<one-sentence reason, under 140 characters>"}`.
- User message: `"From: <sender>\nSubject: <subject>\n\n<body, truncated to 4000 chars>"`.
- Response parsing: the assistant's message content is unmarshaled as JSON into `{Important bool; Reason string}`.
- No seeded importance criteria/examples in v1 — pure LLM judgment on the email content, per the approved decision. Expected to produce noisy false positives/negatives at first; the correction feedback loop that would fix this is explicitly out of scope for this design.

### Fallback on classifier failure

Non-200 response, timeout, or unparseable JSON → `ClassifyImportance` returns `important=false, reason="classification unavailable: <error>"`. This is a **fail-closed** default: an LLM hiccup never triggers a spurious Discord ping, but the report entry (written regardless of verdict) still records that classification failed and why — nothing is silently lost, only the immediate notification is skipped.

---

## Action Dispatch (`action-svc`)

### `triage_gmail_email` handler

New `case "triage_gmail_email"` in `dispatch.go`'s switch, implemented in a new `dispatch/gmail_triage.go`:

1. **Always** append a report entry via the existing `report.Append` (same file/package `append_daily_report_entry` already uses — no format changes to `report/report.go` itself):
   - `SourcePath`: the sender's email address (fills the "[bracketed context]" slot the report format already reserves — analogous to folder-watcher's watched path, this answers "who/where this came from").
   - `Summary`: `"<subject> — deemed {important|not important}"`.
   - `RawContent`: `"Reason: <reason>\n\n<body_excerpt>"`.
   - `OccurredAt`: from `parameters.occurred_at`.

   Only the ~200-char excerpt is stored in the report, not the full email body — unlike error stimuli (typically short, plain-text logs), email bodies can be arbitrarily long with quoted threads/HTML boilerplate, and the excerpt is enough for an audit trail; the full content remains in Gmail itself, reachable via the deep link on important-email Discord notifications.

2. **If `important == true`**, hand the item to the notification batcher (see below) instead of calling `Notifier.Send` directly.

3. Publish the same fire-and-forget outcome record pattern the existing handler already uses.

### Notification batching (`action-svc/notifybatch`)

New package, `notifybatch.Batcher`:

```go
type Item struct {
    Sender, Subject, Reason, BodyExcerpt, ThreadID string
}

func New(grace, maxWait time.Duration, notifier notify.Notifier) *Batcher
func (b *Batcher) Add(item Item)
```

Debounce-with-max-wait semantics, chosen because the user wants batching (not one Discord message per important email) but also doesn't want a single important email sitting unsent for the full window when nothing else arrives:

- The first `Add` on an idle batcher starts two timers: a **grace timer (30s)** and a **max-wait timer (2 minutes)**.
- Each subsequent `Add` while a batch is pending resets the grace timer only — the max-wait timer keeps counting from the first item and is never reset.
- Whichever timer fires first triggers `Flush()`.
- 30s was chosen as a grace period long enough to catch genuinely-related emails landing seconds apart (e.g. a thread with quick back-and-forth) without meaningfully delaying a lone important email past what "as soon as I can" implies; 2 minutes as the hard cap bounds worst-case delay during a steady trickle of important mail (each arrival resetting the 30s clock) from stretching indefinitely.
- `Flush()` formats all queued items into a single message — one blank-line-separated block per item (`From:`/`Subject:`/`Why:`/a quoted excerpt/the Gmail deep link `https://mail.google.com/mail/u/0/#inbox/<thread_id>`), prefixed with a count header (e.g. `"2 important emails:"`) — then calls `Notifier.Send` once and clears the queue. The blank-line separation matches `discord.go`'s existing `splitMessage` chunking convention, so an oversized batch still splits cleanly if it ever exceeds Discord's 2000-character limit.
- Implemented with `time.AfterFunc` + a mutex — no background goroutine loop needed (unlike `scheduler.Scheduler`'s ticking loop), since each `Add` only needs to (re)schedule at most two pending callbacks.
- **Known v1 limitation:** the queue is in-memory only. If `action-svc` restarts while a batch is pending, any queued-but-unflushed important-email notifications are lost — no persistence, no redelivery. This matches the project's existing accepted best-effort/at-least-once tradeoffs elsewhere (e.g. Gmail's own label-based dedup accepting redelivery risk) and is not fixed in this iteration.

### Wiring (`action-svc/main.go`)

`Dispatcher` gains a batcher (or notifier) dependency, constructed in `main.go` alongside the existing `notifier` and passed into `dispatch.New(...)`. Grace (30s) and max-wait (2min) are hardcoded constants in v1, not environment-configurable — promotable to config later if tuning is ever needed.

---

## Configuration (env vars)

No new environment variables. Reuses `thinking-svc`'s existing `DEEPSEEK_*` config (same client, same timeout) and `action-svc`'s existing `DISCORD_BOT_TOKEN`/`DISCORD_CHANNEL_ID`/`REPORT_NOTIFIER` config (same `Notifier`).

---

## Error Handling

| Failure | Behaviour |
|---|---|
| DeepSeek classification call fails/times out/returns unparseable JSON | Fail-closed: `important=false`, reason records the failure; report entry still written, no Discord notification |
| Report append fails | Retry once; if it still fails, log and give up silently (same as `ErrorReportRule`'s existing fallback) |
| Discord send fails (batch flush) | Logged; no retry — the report entry is the permanent record, a missed immediate ping is not worth blocking on |
| `action-svc` restarts with a batch pending | Queued important-email notifications for that batch are lost (in-memory only); the report entries for those emails were already written independently and are unaffected |
| Malformed/unparseable `gmail` stimulus | Log, ACK, skip — same as any other malformed stimulus |

---

## Testing

- `thinking-svc/llm`: new tests for `ClassifyImportance` mirroring `deepseek_test.go`'s existing success/non-200/timeout/malformed-JSON cases.
- `thinking-svc/rules`: new `gmail_triage_test.go` mirroring `error_report_test.go` — `Match`/no-match, `Handle` with a fake `Classifier` covering both `important=true` and `important=false`, asserting `ActionRequest` fields and marshaled `Parameters` in each case.
- `action-svc/dispatch`: new tests for the `triage_gmail_email` handler — report entry is always written; the batcher is only invoked when `important == true`.
- `action-svc/notifybatch`: new package tests — a single item flushes after the grace period; multiple items arriving within the grace period combine into one `Notifier.Send` call; a steady trickle of items each under 30s apart is forced to flush at the 2-minute cap. Uses an injectable clock (matching `scheduler.Scheduler`'s existing `Now func() time.Time` test-friendly pattern) so tests don't wait on real timers.
- No test exercises a live DeepSeek classification call beyond the existing skip-if-no-API-key live test pattern already used for summarization.

---

## Out of Scope (this iteration)

- Seeded importance criteria/examples in the classifier prompt — pure LLM judgment only.
- Any feedback/correction mechanism for miscalibrated verdicts (false positives/negatives) — described by the user as "some new kind of perception we'll design later."
- Environment-configurable grace/max-wait durations — hardcoded constants for now.
- Persistence/redelivery of a pending batch across `action-svc` restarts.
- Thread-level deduplication or multi-email correlation beyond simple time-based batching.
- Any decision type beyond `INVOKE_ACTION` for gmail stimuli.

---

## Related

- `docs/superpowers/specs/2026-07-18-gmail-channel-design.md` — produces the `gmail`-channel stimulus this rule consumes (Perception side, built independently in parallel).
- `docs/superpowers/specs/2026-07-17-error-report-action-design.md` — the only other existing Thinking rule / Action dispatch pair; this design follows its handoff shape and dispatch conventions.
- `docs/superpowers/specs/2026-07-17-thinking-svc-design.md` — `thinking-svc`'s overall runtime this rule plugs into.
- `docs/superpowers/specs/2026-07-17-action-svc-design.md` — `action-svc`'s overall runtime this dispatch handler plugs into.
- [[Thinking module]] / [[Action module]] — the full eventual designs these are first slices of.
