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
