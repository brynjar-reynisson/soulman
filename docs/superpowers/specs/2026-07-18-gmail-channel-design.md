# Gmail Channel (Perception) Design

**Date:** 2026-07-18
**Status:** Approved
**Phase:** Perception module — first pull-channel adapter beyond folder-watcher

---

## Summary

Adds a Gmail channel adapter to `perception-svc`: a new `gmailwatcher` package, structurally parallel to the existing `watcher` (folder-watcher) package, that polls a Gmail inbox via the Gmail REST API and publishes matching messages as `Stimulus` events on the same NATS subject the folder watcher already uses. This is the "Email Watcher" pull channel [[Perception module]] already anticipated, now concretely specified for Gmail (REST API, not IMAP).

Authentication uses a single, long-lived OAuth 2.0 offline refresh token — obtained once via a manual consent flow, kept valid indefinitely by putting the Google Cloud OAuth client in "Production" publishing status (avoiding the 7-day refresh-token expiry that applies to apps left in "Testing" status). No browser automation, no repeated interactive consent.

Both `soulman-dev` and `soulman-prod` read the same real Gmail inbox, sharing one OAuth client and refresh token. Each environment marks the messages it processes with its own Gmail label, so the two never re-process (or interfere with) each other's work despite watching the same mailbox.

---

## Why

`perception-svc` currently has one channel (folder-watcher). Gmail is the first channel that needs external OAuth and network polling rather than a local filesystem watch, so it's a natural next channel to prove out the Perception module's pull-channel pattern end-to-end. The user's own inbox is a genuinely useful "unread mail" input source for Thinking, and building this on the Gmail REST API with an offline refresh token avoids both the operational fragility of browser-automation scraping and the repeated-consent annoyance of a naively-configured OAuth app (see the "OAuth Setup" section below for why Production status is what actually fixes this).

---

## Architecture

```
        Google Cloud OAuth (offline refresh token, Production status)
                              │
                              ▼
              ┌───────────────────────────────┐
              │      gmailwatcher.Watcher      │
              │  ticker (poll_interval_seconds) │
              │  ──────────────────────────────│
              │  1. users.messages.list(query)  │
              │  2. users.messages.get(id) each │
              │  3. build Stimulus               │
              │  4. publish → NATS               │
              │  5. users.messages.modify        │
              │     (add seen_label)             │
              └───────────────┬─────────────────┘
                               │ common.Stimulus
                               ▼
                    natspublish.Publisher
                   (same publisher folder-watcher
                          already uses)
                               │
                               ▼
                   soulman(.dev).stimulus.raw
```

No local checkpoint file is needed (unlike folder-watcher's hash-based checkpoint) — Gmail's own labels are the checkpoint. The configured `query` always excludes the environment's own `seen_label`, so a message drops out of future poll results the moment it's been processed.

---

## OAuth Setup (one-time, manual, outside this repo)

1. Create a Google Cloud project (or reuse an existing personal one).
2. Enable the Gmail API for that project.
3. Configure the OAuth consent screen:
   - Scopes: `https://www.googleapis.com/auth/gmail.readonly` and `https://www.googleapis.com/auth/gmail.modify` (modify is required to add the seen-label after processing; readonly alone can't).
   - Add yourself as a user.
   - Set **Publishing status: Production** — this is the actual fix for "repeated approvals": apps left in Testing status get refresh tokens that expire after 7 days, forcing re-consent on that cadence. Production status removes that expiry. For a single personal user this doesn't require Google's full verification review (that's only triggered past ~100 users); you'll see a one-time "Google hasn't verified this app" click-through during the very first consent, never again after.
4. Create an OAuth 2.0 Client ID (type: Desktop app, or Web app with a `localhost` redirect URI — either works for a manual flow).
5. Run through the consent flow once (any OAuth playground or a manual browser + curl exchange works — no code in this repo is needed for this, per the earlier decision to handle bootstrapping manually) with `access_type=offline&prompt=consent`, so Google actually issues a refresh token (omitting either param on a repeat consent can silently skip issuing one).
6. Paste the resulting `client_id`, `client_secret`, and `refresh_token` into `.env` (see below) in both `soulman-dev` and `soulman-prod`.

This flow is only re-run if the refresh token is ever revoked or the scopes change — not on any regular cadence.

---

## Credentials — `.env` (secret, shared by dev and prod)

```
GMAIL_CLIENT_ID=...
GMAIL_CLIENT_SECRET=...
GMAIL_REFRESH_TOKEN=...
```

Same three values in both `soulman-dev\.env` and `soulman-prod\.env` — one Google Cloud OAuth client and one refresh token cover both environments, since it's genuinely the same Gmail account either way (per the approved design discussion: separate credentials per environment would double the one-time setup for no real isolation benefit on a single personal account).

`perception-svc/config/config.go` gains:

```go
GmailClientID     string
GmailClientSecret string
GmailRefreshToken string
```

populated via `env("GMAIL_CLIENT_ID", "")` etc. — env-var-driven like the other secrets in this codebase (`DISCORD_BOT_TOKEN`, `DEEPSEEK_API_KEY`), not shared-config-driven, consistent with the project's existing secret/non-secret split.

---

## Non-secret settings — `common/sharedconfig` + `config/dev.json` / `config/prod.json`

`common/sharedconfig.Config` gains a nested `Gmail` field:

```go
type Config struct {
	// ... existing fields (WatchPaths, NATSURL, etc.) ...
	Gmail GmailConfig `json:"gmail"`
}

type GmailConfig struct {
	Query               string `json:"query"`
	SeenLabel           string `json:"seen_label"`
	PollIntervalSeconds int    `json:"poll_interval_seconds"`
}
```

`config/dev.json` adds:

```json
"gmail": {
  "query": "in:inbox is:unread -label:soulman/seen-dev",
  "seen_label": "soulman/seen-dev",
  "poll_interval_seconds": 60
}
```

`config/prod.json` adds:

```json
"gmail": {
  "query": "in:inbox is:unread -label:soulman/seen",
  "seen_label": "soulman/seen",
  "poll_interval_seconds": 60
}
```

Both environments watch the real inbox (per the approved revision — dev no longer needs a manually-applied test label) and each marks what it processes with its own `seen_label`, so a message that both environments see ends up carrying both `soulman/seen-dev` and `soulman/seen` labels over time; this is expected and harmless, not a bug to guard against.

`perception-svc/config/config.go`'s `Load()` reads these off `shared.Gmail.*` the same way it already reads `shared.WatchPaths`. Fatal-fast validation (matching the existing pattern for `watch_paths`/`nats_url`/etc.): `shared.Gmail.Query == ""`, `shared.Gmail.SeenLabel == ""`, or `shared.Gmail.PollIntervalSeconds <= 0` each return a startup error.

---

## `gmailwatcher` Package

New package `perception-svc/gmailwatcher`, mirroring `perception-svc/watcher`'s shape:

```go
package gmailwatcher

type Publisher interface {
	Publish(ctx context.Context, s *common.Stimulus) error
}

func New(cfg Config, publisher Publisher) (*Watcher, error)
func (w *Watcher) Start(ctx context.Context)
func (w *Watcher) Close() error
```

Where `Config` holds `ClientID`, `ClientSecret`, `RefreshToken`, `Query`, `SeenLabel`, `PollInterval time.Duration` — passed in from `perception-svc/config.Config` at construction, same wiring shape `watcher.New` already receives its arguments in `main.go`.

### Startup

1. Build an `oauth2.Config` (`golang.org/x/oauth2/google`) from `ClientID`/`ClientSecret` with the `gmail.readonly` + `gmail.modify` scopes, and a `TokenSource` seeded from `RefreshToken` — the `x/oauth2` package handles silent access-token refresh from here on; no manual refresh logic needed.
2. Construct a `gmail.Service` (`google.golang.org/api/gmail/v1`) using that token source's HTTP client.
3. Resolve `SeenLabel` (e.g. `"soulman/seen-dev"`) to a Gmail label ID via `users.labels.list`; if not found, create it via `users.labels.create`. Cache the resolved ID for the `modify` calls below.

### Poll loop

A single `time.Ticker` at `PollInterval`, mirroring folder-watcher's `reconcileLoop` shape:

```
tick →
  users.messages.list(q: Query) → message IDs
  for each ID:
    users.messages.get(id, format: "full")
    build Stimulus (see field mapping below)
    publisher.Publish(ctx, stimulus)
    if publish succeeded:
      users.messages.modify(id, addLabelIds: [seenLabelID])
```

One immediate poll runs before the ticker starts (matching folder-watcher's `Start` running one reconciliation pass up front), so messages already unread at startup aren't stuck waiting a full interval.

### Stimulus Field Mapping

| Stimulus field | Value |
|---|---|
| `stimulus_id` | new UUID v7 (same `google/uuid` pattern as folder-watcher) |
| `schema_version` | `1` |
| `received_at` | now, UTC |
| `occurred_at` | the message's `internalDate` (Gmail's own received timestamp) |
| `channel` | `"gmail"` |
| `source.identity` | sender's email address, parsed from the `From` header |
| `source.authenticated` | `false` |
| `source.auth_method` | `"none"` — we authenticated *our own* access to Gmail via OAuth, but have no cryptographic proof of the sender's identity (unlike folder-watcher's trusted-local-file case); SPF/DKIM verification is out of scope |
| `content.raw_text` | the `text/plain` MIME part if present; else the raw `text/html` part |
| `content.content_type` | `"text"` or `"html"` matching whichever part was used |
| `content.raw_payload` | the raw Gmail API message JSON, verbatim (same "preserve the original" pattern folder-watcher follows, though folder-watcher currently just sets `{}` since it has no structured source payload) |
| `content.attachments` | one entry per MIME part with a filename: `filename`, `mime_type`, `size_bytes` from the part metadata, and `uri` set to a synthetic `gmail://<message_id>/attachments/<attachment_id>` reference — bytes are never fetched (per the approved decision), this just records enough for a future consumer to fetch them via the Gmail API if it ever needs to |
| `channel_metadata.message_id` | Gmail's message ID |
| `channel_metadata.thread_id` | Gmail's thread ID |
| `channel_metadata.reply_to` | sender's email address (same value as `source.identity`) |
| `channel_metadata.channel_specific` | `{"subject": "...", "label_ids": [...]}` — the message's subject line and current Gmail label IDs at fetch time |
| `hints.priority` | `"normal"` — no priority signal exists at the adapter level; Thinking assigns real priority |
| `hints.tags` | `["email", "gmail"]` |
| `override` | `{is_override: false, params: {}}` — same as folder-watcher; override detection for email isn't in scope here |

### Error Handling

| Failure | Behavior |
|---|---|
| OAuth token refresh fails (revoked/expired) | Log and retry next tick with exponential backoff (matches [[Perception module]]'s existing backoff table: `min(interval × 2^n, 1 hour)`) |
| `users.messages.list`/`get` network/API error | Same backoff as above; a failure on one poll doesn't crash the watcher, just skips to the next tick |
| `Publish` fails for a given message | Log and skip the `modify` (seen-label) call for that message — it stays unlabeled and gets re-fetched and re-published next poll, same at-least-once tradeoff folder-watcher already accepts when its own `Publish` fails |
| `modify` (seen-label) fails after a successful `Publish` | Log; the message re-appears in the next poll's query results and gets re-published — an accepted at-least-once duplication risk, not treated as fatal |
| Seen-label doesn't exist at startup | Created automatically via `users.labels.create`; not a startup error |
| Missing/empty `GMAIL_CLIENT_ID`/`SECRET`/`REFRESH_TOKEN` | `perception-svc` still starts — the Gmail channel is optional at this stage (see Out of Scope); logs a warning and the `gmailwatcher` is simply not constructed. (This differs from `shared.Gmail.Query`/`SeenLabel`/`PollIntervalSeconds`, which — once the `gmail` config block is present at all — are validated fatal-fast like every other shared-config field.) |

### `main.go` wiring

`perception-svc/main.go` constructs `gmailwatcher.New(...)` alongside the existing `watcher.New(...)`, using the same `pub` (`natspublish.Publisher`) instance, and calls `.Start(ctx)` the same way. If the Gmail credentials are blank, the watcher is skipped entirely (see Error Handling above) rather than failing startup — this keeps the folder-watcher channel independent and always-on regardless of Gmail configuration state, per [[Perception module]]'s "Adapter Isolation" principle (a crash/misconfiguration in one adapter must not affect another).

---

## Testing

- `gmailwatcher`: unit tests around the Stimulus field-mapping logic (given a fixture Gmail API message JSON, assert the resulting `common.Stimulus` fields) — the actual Gmail API calls are behind the same kind of interface seam `watcher.Publisher` already demonstrates, so the mapping logic is testable without a live Gmail account or HTTP mocking framework beyond what's already used elsewhere in this codebase.
- `common/sharedconfig`: extend existing tests to cover the new `Gmail` field (populated and zero-value cases, matching the existing `TestLoad_AllFields` / `TestLoad_MissingNATSFields_ZeroValues` pattern).
- `perception-svc/config`: extend existing tests to cover `Gmail.Query`/`SeenLabel`/`PollIntervalSeconds` fatal-fast validation and the `GMAIL_CLIENT_ID` etc. env vars.
- No automated test exercises a live Gmail account — verified manually instead (apply a test label or send yourself mail, confirm the Stimulus appears on the NATS subject and the seen-label gets applied), the same approach this repo already takes for anything requiring a live external dependency (Discord delivery, DeepSeek calls).

---

## Out of Scope (this iteration)

- Building a CLI tool for the OAuth bootstrap flow — handled manually per the approved decision.
- Downloading attachment bytes — metadata + synthetic URI only.
- SPF/DKIM sender verification.
- Any Gmail action beyond read + label (no send, no delete, no archive).
- Override command detection (PAUSE/STOP/etc.) via email — folder-watcher's Note Watcher already serves that role; email overrides aren't needed yet.
- A `gmail_status` field on `perception-svc`'s `/health` endpoint — could be added later the same way `nats` status already is, but isn't required for this iteration.
- Live-reload of `gmail` config without a restart — same limitation already accepted for every other shared-config field.

---

## Related

- [[Perception module]] — general Perception module design; this spec fulfills the "Email Watcher" pull channel it already anticipated (updating the transport from the doc's original IMAP sketch to the Gmail REST API, per the approved decision).
- `docs/superpowers/specs/2026-07-17-perception-svc-design.md` — original perception-svc / folder-watcher design this channel runs alongside.
- `docs/superpowers/specs/2026-07-18-shared-config-nats-design.md` — the most recent precedent for adding a new field group to `common/sharedconfig`.
