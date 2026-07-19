# action-svc — Operational Notes

Incidents, gotchas, and decisions learned running this service — not captured in the design specs themselves (see `CLAUDE.md`'s Services section for spec links).

## Dispatch handlers

- `append_daily_report_entry` — writes a `report.Entry` to `$SOULMAN_ROOT/reports/`. Entries are filed under the entry's **`OccurredAt` date, not the processing date** — if you're auditing "what happened today," search every report file the event could plausibly belong to, not just today's, or you'll conclude data is missing when it isn't (this happened once: apparent "missing" Discord-reported emails turned out to be filed correctly under their original date).
- `triage_gmail_email` — always writes a report entry (only a ~200-char excerpt, not the full email body — full content stays in Gmail, reachable via the deep link on Discord notifications); additionally hands the item to the notification batcher when Thinking judged it important.

## Notification batching (`notifybatch.Batcher`)

Debounce-with-max-wait: 30s grace timer (resets on each new item) + 2-minute hard cap (never resets, counts from the first item). Chosen so a lone important email doesn't sit unsent for a long window, but a burst of related emails still batches into one Discord message instead of one-per-email.

**Known v1 limitation:** the batch queue is in-memory only. If `action-svc` restarts while a batch is pending, those queued notifications are lost — no persistence, no redelivery. (The report entries for those emails are unaffected; they're written independently and immediately, before batching.) Not fixed — an accepted tradeoff, consistent with other at-least-once/best-effort behavior in this project.

## The incident that motivated durable queues

`action-svc`'s consumer for `soulman.thinking.request` was originally a plain core-NATS `Subscribe` — ephemeral, no persistence. Any message published while `action-svc` wasn't running was **silently dropped**, with no error, no log, nothing to indicate a message ever existed. This is the incident that motivated the entire "pipeline debugging tools" project (`docs/superpowers/specs/2026-07-18-pipeline-debugging-tools-design.md`).

Fixed: `action-svc/natsclient/consumer.go` now uses a durable JetStream consumer (`AckExplicitPolicy`, a `Durable` name, `FilterSubject` scoped to the environment's subject) against a durable `THINKING_REQUEST` stream. Verified for real: publish a message while `action-svc` is down, then start it — the message is delivered once it comes back up (see `TestConsumer_SurvivesRestartAfterDowntime`, and confirmed manually against live dev infrastructure).

One subtlety worth remembering if you touch this wiring again: JetStream identifies a durable consumer by `(stream, name)`, and `DeliverPolicy` is **only consulted at a consumer's very first creation** — later `CreateOrUpdateConsumer` calls preserve the existing ack-floor/position regardless of what `DeliverPolicy` you pass again. An earlier draft of this fix accidentally added `DeliverPolicy: DeliverNewPolicy` to work around a flaky test — that would have reintroduced a narrow first-creation cold-start data-loss window, the exact bug class this fix exists to close. It was caught in review and reverted; the actual fix for the flaky test was to make the test count occurrences of its own specific payload instead of a global counter (the underlying NATS subject is shared and 30-day-retained, so a global counter is inflated by unrelated test runs).

Also: `action-svc/main.go`'s NATS wiring must set up this consumer **independently** of the (separate, currently-inert) `MEMORY_WRITE` outcome-publisher — an earlier version nested the consumer's setup inside the publisher's success branch, which meant a `MEMORY_WRITE` provisioning hiccup would have silently disabled the actual incident fix. Fixed by decoupling them; if you touch `main.go`'s NATS block again, keep them independent.

## Known deferred issue

Dev and prod share one Discord bot/channel/token for "Soulman Reports" — every Gmail-triage Discord notification is sent twice (once per environment) since both watch the same real inbox. Real bug, not yet fixed; deliberately deferred rather than addressed as part of the debugging-tools or triage work.

## Feign mode

The `feign_mode` field in `config/dev.json`/`config/prod.json` (currently `true` in both environments — not an environment variable, unlike `REPORT_NOTIFIER`/`DISCORD_BOT_TOKEN`) makes `action-svc` record outbound side effects instead of performing them — see `docs/superpowers/specs/2026-07-19-action-svc-feign-mode-design.md`. Concretely: the shared `notify.Notifier` (used by both the 10:00 AM daily-report cron and the gmail-triage batcher) is wrapped with `feign.Gate` in `main.go`; when the gate is enabled, `Send` appends a JSON line to `$SOULMAN_ROOT/logs/feigned-actions.jsonl` instead of hitting Discord's API. `episodes` rows stay honest about it too — `dispatchGmailTriage`'s `Decision` reads `"feigned notify via Discord"` instead of `"notified via Discord"`, and the daily cron's `Summary` reads `"Daily report delivery feigned"` instead of `"Daily report delivered"`, whenever the gate is on.

**If you're wondering why no Discord messages are arriving:** check `feign_mode` in the running environment's config first, before assuming something's broken. It was turned on deliberately in both dev and prod as of 2026-07-19 — turn it back off (`feign_mode: false` in `config/dev.json`/`config/prod.json`, then restart `action-svc`) when you want real sends again.
