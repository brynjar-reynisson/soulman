# School Email Events — Design

## Summary

Adds a specialized path for school/municipal emails (`@reykjavik.is`) that extracts actionable dates and times (special clothing days, packed-lunch days, meetings, events) and reminds both parents the day before, at a configured time: a Discord message to the owner (reusing the existing bot/channel) and a Google Calendar invite to the other parent (a new integration). Runs prod-only. Includes a one-off backfill for mail already in the inbox since August 2026.

## Why

Today's Gmail pipeline classifies every message as important/not-important and logs it to the daily report — useful, but passive. School emails frequently bury an actionable date inside routine prose ("remember Friday is sweater day"), easy to miss in a report file nobody re-reads days later. The ask is an active reminder, timed to actually be useful (the day before, not the moment the email arrives, since school mail often arrives weeks ahead of the date it mentions), delivered to both parents even though only one of them uses Soulman.

## Architecture

```
Gmail (sender ends in @reykjavik.is)
        │  (existing perception-svc gmail channel — unchanged)
        ▼
thinking-svc: SchoolEmailRule (new — checked before the existing
GmailTriageRule; first-match-wins, so school senders skip generic
importance triage entirely)
   │  DeepSeek: extract 0+ {date, optional time, description} events,
   │  resolved against the *email's own received date* (so a backfilled
   │  "this Friday" resolves against when the email was sent, not today)
   ▼
ActionRequest{action_hint: "process_school_event", ...}
        │
        ▼
action-svc: dispatchSchoolEvent (new)
   │  always writes a report entry (same pattern as gmail-triage)
   │  for each extracted event whose date is still in the future
   │  (checked against real "now" at dispatch time — this is where
   │  past/backfilled events get silently dropped): persists it as one
   │  JSON file in a small local store, both channels "pending"
        │
        ▼
action-svc: SchoolEventScheduler (new — same shape as the existing
daily-report cron, plus a startup catch-up check)
   │  ticks once a day at school.notify_time (default 16:00 local),
   │  and once immediately on every process start
   │  for any pending-channel event whose date is due or overdue
   │  (≤ tomorrow, and not more than 2 days stale):
   │     → Discord message to the owner (existing bot/channel,
   │       NOT feign-gated — this feature ships live from day one)
   │     → Google Calendar invite to the configured recipient(s)
   │       (new Calendar API client; skipped, not failed, while the
   │       recipient list is empty)
   │  each channel's status flips to "sent" independently, so a
   │  partial failure (or an empty recipient list filled in later)
   │  never re-sends a channel that already succeeded
        ▼
   event fully resolved once both channels are non-pending
```

This whole feature is gated by `school.enabled` in shared config — `false` in `dev.json`, `true` in `prod.json`. Both environments poll the same real inbox; without this gate, dev's and prod's action-svc would each independently create the same Calendar invite and send the same Discord message — the same duplicate-notification problem already known and accepted for Discord (see `action-svc/NOTES.md`), but worth avoiding outright for a brand new channel rather than repeating it.

## Why a new rule instead of extending the existing one

`thinking-svc`'s rule dispatch (`thinking-svc/rules/rule.go`) is strictly first-match-wins, one `ActionRequest` per stimulus — there is no fan-out mechanism today. `SchoolEmailRule` is inserted ahead of `GmailTriageRule` in the registry and fully owns `@reykjavik.is` senders; those emails no longer go through the generic importance classifier.

Trade-off, explicitly accepted: a hypothetically-urgent non-school `@reykjavik.is` email (the domain is the whole municipality's, not just the school) gets a report-log entry but not a Discord ping the way generic Gmail triage would give it. The extraction classifier itself decides whether a given email is actually about a school date — if not, it returns zero events and the email is logged same as any not-important email today, just via a different rule.

### Prod-only gating mechanism

`rules.Registry` is a static package-level slice today. Rather than restructure the (well-tested) existing dispatch mechanism, `thinking-svc/main.go` conditionally prepends the new rule at startup:

```go
if cfg.SchoolEnabled && len(cfg.SchoolSenderDomains) > 0 {
    rules.Registry = append([]rules.Rule{rules.NewSchoolEmailRule(cfg.SchoolSenderDomains)}, rules.Registry...)
}
```

`NewSchoolEmailRule` is a constructor (unlike `GmailTriageRule`, which is a package `var`) because the domain allowlist is config-driven and must differ between environments in principle, even though today only prod enables it at all.

## Components

### 1. Config additions (`common/sharedconfig`)

```go
// SchoolConfig holds the school-email-events feature's settings, shared
// between thinking-svc (SenderDomains, Enabled) and action-svc (Enabled,
// NotifyTime, CalendarRecipientEmails). Enabled is false in dev.json and
// true in prod.json — see the design doc's prod-only gating section: dev
// and prod poll the same real inbox, so running this in both would create
// duplicate Calendar invites and duplicate Discord messages.
type SchoolConfig struct {
    Enabled                 bool     `json:"enabled"`
    SenderDomains           []string `json:"sender_domains"`
    NotifyTime              string   `json:"notify_time"`
    CalendarRecipientEmails []string `json:"calendar_recipient_emails"`
}
```

Added to the top-level `Config` as `School SchoolConfig \`json:"school"\``.

`config/prod.json` gains:
```json
"school": {
  "enabled": true,
  "sender_domains": ["@reykjavik.is"],
  "notify_time": "16:00",
  "calendar_recipient_emails": []
}
```
`config/dev.json` gains the same block with `"enabled": false` (and an empty `sender_domains`, as a second belt-and-suspenders guard — `main.go`'s gating checks both).

`calendar_recipient_emails` starts **empty**, per the approved rollout plan: only the Discord message fires until testing looks right, at which point `joninasveins@gmail.com` is added and prod is restarted once. Because the pending-event store tracks each channel's status independently (see Component 3), an event whose Discord message already fired today will still pick up the calendar invite automatically on the very next scheduler run once the recipient is added — no re-processing needed.

### 2. thinking-svc: extraction capability (`thinking-svc/llm`)

New capability added to the existing `Client` interface (same growth pattern the doc comment on `Client` already anticipates: *"a future rule needing a new LLM capability grows this interface instead of Rule.Handle's parameter list"*). Every existing fake `llm.Client` in the rules test suite gains a stub implementation — expected, not accidental, blast radius.

```go
// SchoolEvent is one actionable school date/time extracted from an email.
// Date is always an absolute "YYYY-MM-DD" — the classifier resolves any
// relative phrase ("this Friday") against the referenceDate it's given,
// not against real wall-clock time, so a backfilled email from weeks ago
// resolves correctly.
type SchoolEvent struct {
    Date        string
    HasTime     bool
    Time        string // "HH:MM", only meaningful when HasTime
    Description string
}

// SchoolEventExtractor pulls zero or more actionable dates/times out of a
// school email. Mirrors Classifier's fail-closed convention: production
// *DeepSeekClient never returns a non-nil error — any failure collapses to
// (nil events, a "extraction unavailable: ..." note, nil error) so an LLM
// hiccup never blocks the report entry this rule always writes.
type SchoolEventExtractor interface {
    ExtractSchoolEvents(ctx context.Context, sender, subject, body string, referenceDate time.Time) (events []SchoolEvent, note string, err error)
}
```

Prompt (new constant, same file-level pattern as `classifierSystemPrompt`):

```
You extract actionable school dates/times from an email sent by a school or
municipal school system. The reference date for resolving relative phrases
("this Friday," "next Tuesday," "tomorrow") is {referenceDate:2006-01-02} —
resolve against that date, not any other notion of "today."

Only include events where a child or parent needs to DO something or BE
somewhere on a specific date: a special clothing/theme day, a packed-lunch
day, a field trip, a meeting, an event with a start time. Do not include
general announcements with no specific date, or purely informational
content.

If the email is not actually about a school date (e.g. an unrelated
municipal notice) or contains no such actionable date, return an empty
array.

Respond with strict JSON only, no markdown, exactly this shape:
{"events": [{"date": "YYYY-MM-DD", "has_time": true or false, "time": "HH:MM" or "", "description": "<short phrase>"}]}
```

### 3. thinking-svc: `SchoolEmailRule` (`thinking-svc/rules/school_email.go`)

Matches `channel == "gmail"` and sender address ending in a configured domain (case-insensitive suffix match). Truncates body to 4000 chars before extraction (same `classifyBodyTruncateLen` convention as gmail-triage). Always builds an `ActionRequest` (`action_hint: "process_school_event"`) — a report entry gets written whether or not any event was found, same as gmail-triage's always-log half.

`Urgency` is `"high"` when at least one event was extracted, `"normal"` otherwise — mirroring gmail-triage's important/not-important split, but driven by "did we find a date" instead of an LLM importance verdict.

`Parameters` carries: `sender, subject, body_excerpt (200 chars), note, thread_id, occurred_at, events[]` (each `{date, has_time, time, description}`).

### 4. action-svc: dispatch handler (`action-svc/dispatch/school_event.go`)

`GmailTriageParams`-shaped mirror struct, unmarshaled from `req.Parameters`. `dispatchSchoolEvent`:

1. Writes a report entry via `report.Append` (same `SourcePath: sender + "/" + threadID` synthesis trick gmail-triage already uses), `Important: len(events) > 0`, retry-once-then-give-up on failure — identical shape to the existing gmail-triage dispatch.
2. For each extracted event, parses `Date` and compares against `time.Now()` (local): a date in the past is dropped silently (this is the point where backfilled historical mentions of already-happened events get filtered out — nothing else in the pipeline does this check). A date today-or-later is persisted via `schoolevents.Save` (Component 5).
3. Publishes an `OutcomeRecord` (`ActionType: "process_school_event"`, `Tags: []string{"gmail", "school"}`), `Decision` reflecting how many events were queued (`"logged only"` / `"N event(s) queued"`).

Wired into `dispatch.go`'s existing switch statement: `case "process_school_event": d.dispatchSchoolEvent(req)`.

### 5. action-svc: pending-event store (`action-svc/schoolevents/store.go`)

One JSON file per event, at `$SOULMAN_ROOT/logs/school-events/<id>.json` — same directory tier as the existing `dnd-pending.txt` and `feigned-actions.jsonl` local-state files (not memory-svc/Postgres — this is action-svc's own scheduling state, not the durable audit log).

```go
// Event is one persisted school-date reminder. DiscordStatus and
// CalendarStatus track independently ("pending" | "sent" | "skipped" —
// skipped is set only for Calendar when no recipients are configured at
// send time) so a partial failure, or a recipient list that starts empty
// and gets filled in later, never causes a channel that already succeeded
// to fire twice.
type Event struct {
    ID            string    `json:"id"`
    Date          string    `json:"date"`
    HasTime       bool      `json:"has_time"`
    Time          string    `json:"time"`
    Description   string    `json:"description"`
    Sender        string    `json:"sender"`
    Subject       string    `json:"subject"`
    DiscordStatus string    `json:"discord_status"`
    CalendarStatus string   `json:"calendar_status"`
    CreatedAt     time.Time `json:"created_at"`
}

// ID is deterministic (sha256 of threadID + event index + date, first 16
// hex chars) so re-processing the same email — a deliberate backfill rerun,
// or perception-svc's at-least-once redelivery — overwrites the same file
// instead of creating a duplicate pending reminder. Save is a no-op (not an
// overwrite) when the file already exists with both channels non-pending —
// an already-fully-sent event is never resurrected.
func ID(threadID string, index int, date string) string
func Save(root string, e Event) error
func MarkDiscordSent(root, id string) error
func MarkCalendarStatus(root, id, status string) error

// DueOrOverdue returns every event with at least one pending channel whose
// Date is on or before tomorrow (relative to now) and not more than 2 days
// past its own date — the "better late than never, not forever" cutoff:
// a genuinely stuck event (e.g. a dead Calendar credential) stops being
// retried after that window rather than accumulating forever, but a
// weekend-long outage still catches up cleanly.
func DueOrOverdue(root string, now time.Time) ([]Event, error)
```

### 6. action-svc: Calendar client (`action-svc/calendar/calendar.go`)

Structurally mirrors `perception-svc/gmailwatcher/client.go`'s OAuth2 bootstrap exactly (offline refresh token, `golang.org/x/oauth2/google`, Production app status) but against `google.golang.org/api/calendar/v3` with the `https://www.googleapis.com/auth/calendar.events` scope.

```go
type Invite struct {
    Summary     string
    Description string
    Date        string   // "YYYY-MM-DD"
    HasTime     bool
    Time        string   // "HH:MM" local, only when HasTime
    Attendees   []string
}

type Client struct{ svc *calendar.Service }

func New(ctx context.Context, clientID, clientSecret, refreshToken string) (*Client, error)

// CreateInvite creates a primary-calendar event with sendUpdates="all" so
// every attendee gets a real invite email/notification. Date-only events
// use Start.Date/End.Date (an all-day event, end exclusive = Date+1 day).
// Timed events use Start.DateTime/End.DateTime with a fixed 1-hour
// duration (no end time is ever supplied by the source email).
func (c *Client) CreateInvite(ctx context.Context, inv Invite) error
```

New env vars, action-svc's `.env` only, non-fatal if blank (same posture as `DISCORD_BOT_TOKEN`/`DEEPSEEK_API_KEY` — a blank value just makes every `CreateInvite` call fail, logged and retried by the scheduler's catch-up mechanism, not a startup crash): `CALENDAR_CLIENT_ID`, `CALENDAR_CLIENT_SECRET`, `CALENDAR_REFRESH_TOKEN`.

### 7. action-svc: `SchoolEventScheduler` (`action-svc/scheduler/school.go`)

Same wake-loop shape as the existing `Scheduler` (daily report) and `dnd.Flusher` (`nextRun`/`time.Until`/`time.After`, overridable `Now`/`BackoffBase` for tests), plus a startup catch-up — modeled directly on `dnd.Flusher.Start`, which already launches a non-blocking catch-up goroutine before its wake loop:

```go
func (s *SchoolEventScheduler) Start() {
    go s.RunOnce() // catch-up: anything due or overdue right now, including
                    // a missed 16:00 tick from downtime, fires immediately
    go s.loop()     // then the normal daily-tick loop
}
```

`RunOnce` calls `schoolevents.DueOrOverdue`, and for each returned event attempts only its still-pending channel(s):

- Discord (if `DiscordStatus == "pending"`): sends via the existing `notify.Notifier`/`DiscordNotifier` — a **separate, un-feign-wrapped, un-DND-wrapped** notifier instance (see Design Decision below), retried up to 3 times with backoff (same `sendWithRetry` shape as `Scheduler`/`Flusher`). On success, `MarkDiscordSent`.
- Calendar (if `CalendarStatus == "pending"`): skipped (not attempted, not an error) when `school.calendar_recipient_emails` is empty; otherwise calls `CreateInvite`, retried the same way. On success, `MarkCalendarStatus(..., "sent")`.

An event with both channels resolved (`sent` or `skipped`) is simply never returned by `DueOrOverdue` again — no separate cleanup step.

### 8. Design decision: this feature bypasses `feign.Gate` entirely

Every existing Discord send in `action-svc` (daily digest, gmail-triage batch) is wrapped in `feign.WrapNotifier`, and `feign_mode` is currently `true` in **both** dev and prod — meaning nothing from either of those paths reaches real Discord today, pending the DND-verification gate described in `action-svc/NOTES.md`.

This feature is explicitly *not* wrapped: it constructs its own plain `notify.DiscordNotifier` (same bot token/channel as everything else, but no `feign.Gate` in front of it). This is a deliberate divergence from the established pattern, made because the whole point of today's work is a real, felt test — a feign-wrapped send would silently just write to `feigned-actions.jsonl` and defeat "we'll send a notification to me first." Flagging this explicitly since it's the one place this design departs from an otherwise-uniform convention.

DND wrapping doesn't apply here either — the DND window (`00:00`–`10:00`) can never overlap a `16:00` trigger, so wrapping would be a no-op; not adding it avoids implying a relationship that doesn't exist.

### 9. Backfill script (`cli`, new subcommand)

`soulman school-backfill --since 2026-08-01 [--dry-run]` — a new subcommand alongside the existing `inject`/`discord-history` debugging tools (`cli/` module).

1. Builds a Gmail service using the existing `GMAIL_CLIENT_ID`/`GMAIL_CLIENT_SECRET`/`GMAIL_REFRESH_TOKEN` env vars (reading historical mail needs no new scope — the existing `gmail.readonly` token already covers it; only *sending calendar invites* needed the new Calendar scope). The OAuth bootstrap is a small local duplicate of `gmailwatcher/client.go`'s constructor — `cli` is a separate Go module, so it can't import `perception-svc` directly, consistent with this repo's "small independent duplication over cross-module imports" precedent (see `action-svc/NOTES.md`).
2. Queries `from:*@reykjavik.is after:<since>` — deliberately **not** filtered by read/unread or any seen-label, since this is a one-time historical scan independent of the live poller's checkpoint (and running it twice by mistake is harmless — see Component 5's idempotent `ID`).
3. For each matching message, builds a `common.Stimulus` (same field-mapping `gmailwatcher/stimulus.go` already does, duplicated locally for the same cross-module reason as above) — critically setting `OccurredAt` from the message's real `InternalDate`, not "now," so the extraction prompt's relative-date resolution is correct.
4. `POST`s each Stimulus to **prod's** `perception-svc` `/api/perceive/raw` (the existing debug-injection endpoint) — never dev's, since dev has `school.enabled: false` and the message would instead fall through to dev's generic `GmailTriageRule`, doing a wasted (feigned) importance classification for no benefit.
5. Prints a summary: messages found, injected, any per-message errors. `--dry-run` builds and logs the Stimuli without POSTing.

This flows through the exact same live pipeline as a real-time email — no special-case backfill logic anywhere else in the system.

## Notification timing semantics

"16:00 the day before" is computed as one absolute instant from the event's own date (and time, if present): for a date-only event, midnight is the anchor (matching how Google Calendar itself treats all-day events), so "day before 16:00" is 8 hours before that midnight — the same arithmetic whether or not the event has an explicit time. `SchoolEventScheduler` doesn't need to compute this offset explicitly, though: it simply asks "is this event's date ≤ tomorrow" once a day at `notify_time`, which produces the same practical outcome without needing per-event reminder math.

## Error handling & edge cases

| Case | Behavior |
|---|---|
| Extraction call fails (network/malformed JSON) | Fails closed to zero events (same convention as `ClassifyImportance`) — report entry still gets written, `note` field records why, nothing gets queued |
| Extracted date is unparseable | Dropped, logged, not queued — never blocks the report entry |
| Extracted date is already in the past (real "now" at dispatch time) | Dropped silently — this is the mechanism that makes backfill safe for old mail |
| `action-svc` down at `16:00` | Caught by the startup catch-up check next time it starts — "due or overdue" is the same condition either way |
| Discord send fails, Calendar succeeds (or vice versa) | Only the failed channel stays `pending` and retries on the next tick/startup; the channel that already succeeded is never re-sent |
| Calendar recipient list is empty | `CalendarStatus` stays `pending` indefinitely (not `skipped`) until a recipient is configured — so adding her email later today automatically catches up the already-queued sweater-day event on the next scheduler run, no manual re-trigger |
| An event stays fully unresolved for a very long time (e.g. dead Calendar credential) | `DueOrOverdue`'s 2-day-past-the-event cutoff stops retrying it — matches the existing "give up silently" convention used elsewhere in this pipeline (a missed reminder for a date that's now well past isn't worth chasing) |
| Same real Gmail message reprocessed (redelivery, or backfill run twice) | `schoolevents.Save`'s deterministic ID makes this a no-op once both channels are resolved; while still pending, it's just an idempotent overwrite |
| Non-school `@reykjavik.is` email | Extraction returns zero events; report entry only, `Important: false` — same as any not-important email today |

## OAuth Setup (one-time, manual — same pattern as the original Gmail bootstrap)

1. Reuse the existing Google Cloud project (the one already hosting the Gmail OAuth client) and enable the Calendar API on it.
2. On the OAuth consent screen, this can either extend the *same* OAuth client with an additional scope, or (recommended, to avoid touching the working Gmail token) create a **second** OAuth 2.0 Client ID for Calendar specifically — either way, run the consent flow once with `access_type=offline&prompt=consent` and scope `https://www.googleapis.com/auth/calendar.events`.
3. Paste the resulting `client_id`/`client_secret`/`refresh_token` into prod's `.env` as `CALENDAR_CLIENT_ID`/`CALENDAR_CLIENT_SECRET`/`CALENDAR_REFRESH_TOKEN`.
4. Confirm the OAuth client's publishing status is **Production** (same reason as the Gmail setup: avoids the 7-day refresh-token expiry that applies to apps left in Testing status).

This is a manual, outside-the-repo step — no code in this repo performs the bootstrap, consistent with the Gmail channel's original decision.

## Testing

Today's rollout, once implementation is done:
1. Temporarily set `school.notify_time` in prod's `config.json` a couple of minutes ahead of the current time (leaving `calendar_recipient_emails` empty), restart `action-svc` in prod.
2. Confirm the sweater-day email produces a report entry and a real Discord message.
3. Set `school.notify_time` back to `16:00`, add `joninasveins@gmail.com` to `calendar_recipient_emails`, restart once more — confirm the same (still-pending-on-Calendar) event now also produces a real Calendar invite to her, without needing to touch the pending-event file directly.
4. Run the backfill script against prod for `--since 2026-08-01` and confirm no already-past dates get queued.

Unit tests (per component, TDD as usual): `SchoolEmailRule` matching + parameter building against a fake `llm.Client`; `dispatchSchoolEvent`'s past-date filtering and report-entry writing; `schoolevents` store's idempotent `Save`/`ID`/`DueOrOverdue` windowing; `SchoolEventScheduler`'s catch-up-on-start and independent per-channel retry behavior (fake `notify.Notifier` + fake `EventInviter`, overridden `Now`); `calendar.Client`'s all-day vs. timed event construction (unit-testable against a recorded request, no live Google account needed, mirroring however `gmailwatcher`'s existing tests avoid a live account).

## Out of Scope (this iteration)

- Extracting *which child* an event is about — descriptions are freeform; the reader is expected to infer this from context the same way they would reading the email itself.
- A correction/feedback loop for extraction accuracy (same deferred status as the existing importance classifier).
- Any manual "run now" HTTP/CLI trigger — today's testing reuses the `notify_time` config knob directly instead (see Testing).
- Deleting or updating a Calendar invite if a school later reschedules/cancels — each email is processed independently; a follow-up email about the same date produces a second, separate invite.
- Any notification channel for the extracted-events feature beyond Discord (owner) and Calendar (other parent) — no email, SMS, etc.
- Fan-out at the `thinking-svc` rule level (a stimulus producing more than one `ActionRequest`) — not needed here, and not touched.
