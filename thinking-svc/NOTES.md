# thinking-svc — Operational Notes

Incidents, gotchas, and decisions learned running this service — not captured in the design specs themselves (see `CLAUDE.md`'s Services section for spec links).

## Rule table

Three rules today, matched in `rules.Table` order:

- `folder-watcher` (`ErrorReportRule`) — mechanical, no LLM: raw error text already speaks for itself, so a summarization call would just spend credits for no signal.
- `cli-note` (`CLINoteRule`) — mechanical, no LLM: same `append_daily_report_entry` shape, built directly from CLI-typed text.
- `gmail` (`GmailTriageRule`) — the only rule with genuine LLM judgment: DeepSeek decides `important: true|false`, always produces a report-log action, but only produces a Discord notification when judged important.

## Classifier prompt tuning (real incident)

v1 shipped with **no seeded importance criteria** — pure LLM judgment on sender/subject/body, expected to be noisy at first. In practice it was noisy in one specific direction: routine newsletters (e.g. tldrnewsletter.com) and routine "if you didn't do this, ignore this email" account notifications were frequently judged important, because their *content* discusses security/urgency-flavored topics (breaches, exploits, GDPR) even though the *message itself* isn't urgent to the recipient.

Fixed by rewriting `classifierSystemPrompt` (`thinking-svc/llm/classifier.go`) with explicit criteria:
- Judge from the **recipient's** perspective, not the topic's inherent urgency.
- Newsletters/digests are never important, regardless of how alarming their content sounds.
- Routine account notifications framed as "if you didn't request this, ignore it" are not important.
- Reserve `important: true` for genuine deadline, financial, legal, or suspicious-account-activity cases that actually require the recipient to act.

There is still no correction/feedback loop for miscalibrated verdicts — described in the original design as "some new kind of perception we'll design later," still out of scope.

## Publisher: now JetStream-backed

`natsclient.Publisher` used to publish to `soulman.thinking.request` via plain core-NATS (ephemeral, no persistence). It now ensures a durable `THINKING_REQUEST` JetStream stream exists (`CreateOrUpdateStream`, idempotent) and publishes through it — part of the pipeline-debugging-tools work that fixed a real message-loss incident (see `action-svc/NOTES.md`).

## System Monitor importance: `ok` is always a recovery (added 2026-07-20)

`systemMonitorImportant` (`thinking-svc/rules/system_monitor.go`) treats `severity == "ok"` as important, same as `critical` — this isn't a guess, it follows directly from `perception-svc/sysmonitor`'s edge-triggered publish design: a `Stimulus` is only ever published when severity *changes*, so a published `"ok"` can never represent "still fine" (that state is never published at all) — it always means "just recovered from warning or critical." If `sysmonitor`'s publish semantics ever changed to also publish steady-state pings, this reasoning would break and `systemMonitorImportant` would need revisiting.

## Log Error rule has no non-important tier (added 2026-07-27)

Unlike `SystemMonitorRule` (`warning` is not important) or `GmailTriageRule` (DeepSeek judges), `LogErrorRule` always sets `Important: true` — there's no lower tier to distinguish because `logmonitor` only ever publishes `channel: "log-error"` stimuli for `ERROR`-level lines in the first place (`WARN`/`INFO` never reach thinking-svc at all, filtered at the source in `perception-svc/logmonitor`). See `docs/superpowers/specs/2026-07-27-log-error-perception-design.md`.

## School Email rule: config-gated and date resolution (added 2026-09-03)

`SchoolEmailRule` matches Gmail senders ending in a configured domain (case-insensitive, default `@reykjavik.is`). It's inserted ahead of the generic `GmailTriageRule` in the rule registry so school emails branch off before importance-triage logic fires. **The rule is config-gated**: `main.go` conditionally prepends it to `rules.Registry` only if `school.enabled` is true in the config AND `school.sender_domains` is non-empty — the registry itself has no built-in conditional-registration concept of its own, so this gating happens at registration time in `main.go`.

The `ExtractSchoolEvents` DeepSeek prompt resolves relative dates and times (e.g. "next Tuesday 10am") against the email's own `received_at` / `OccurredAt` timestamp, not against real "now" — **this is critical for correctness when backfilling historical mail** (see `cli/NOTES.md` for the `school-backfill` tool). A stimulus for an email dated August 15th that says "next Tuesday" should resolve to a date relative to August 15th, not relative to today. If this logic ever changes to use `time.Now()` instead, backfill will break silently, producing incorrect future dates for historical events.

## Test suites were polluting live NATS (incident, 2026-09-03)

`natsclient/consumer_test.go` and `natsclient/publisher_test.go` connected to the real shared NATS server by default and published test payloads onto the real `soulman.stimulus.raw`/`soulman.thinking.request` subjects — a routine `go test ./...` produced real Discord noise and log errors. Every live-NATS test in this package now requires `SOULMAN_NATS_INTEGRATION_TESTS=1` to run (skipped otherwise). Full incident writeup and root cause: `perception-svc/NOTES.md`.

## Leveled logging (log/slog, added 2026-07-27)

All `log.Printf`/`log.Fatalf` call sites in `main.go` and `natsclient/consumer.go` replaced with stdlib `log/slog` (`slog.Error`/`slog.Warn`/`slog.Info`) — no new dependency. Unparseable stimuli and rule-handling failures (dropped, no redelivery for `soulman.thinking.request`) are `slog.Error`; a blank `DEEPSEEK_API_KEY` (non-fatal, falls back to deterministic summaries) is `slog.Warn`; started/listening/shutting-down messages are `slog.Info`. Startup `log.Fatalf` calls became `slog.Error(...)` followed by an explicit `os.Exit(1)`.
