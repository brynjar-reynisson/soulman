# Perception Module

The entry point for all external stimuli. Perception receives raw input from multiple channels, normalizes everything into a canonical format, and routes it to the [[Thinking module]] (normal flow) or directly to the Guard Agent (override flow). It is the **ears and eyes** of the system — it senses, but does not interpret.

---

## Core Principle

> **One canonical format. Channel adapters at the edge. No intelligence in Perception.**

Perception does three things and three things only:

1. **Receive** — accept input from push and pull channels
2. **Normalize** — convert channel-specific formats into the canonical `Stimulus`
3. **Route** — send to Thinking (normal) or Guard Agent (override)

Perception does NOT classify, prioritize, interpret, or decide. That belongs to [[Thinking module]]. The separation is intentional: if a channel adapter guesses wrong about intent, Thinking can correct it. If Perception filters too aggressively, the system goes deaf.

---

## Architecture

```
                    ┌──────────────────────────┐
                    │      External World       │
                    │  ┌──────┐ ┌────┐ ┌────┐  │
                    │  │Email │ │Chat│ │Web │  │
                    │  └──┬───┘ └──┬─┘ └──┬─┘  │
                    └─────┼───────┼──────┼──────┘
                          │       │      │
              ┌───────────┼───────┼──────┼───────────┐
              │           ▼       ▼      ▼           │
              │  ┌──────────────────────────────┐    │
              │  │       Channel Adapters        │    │
              │  │  (one per input source)       │    │
              │  └──────────────┬───────────────┘    │
              │                 │                    │
              │   Normalize to Stimulus              │
              │                 │                    │
              │         ┌───────▼───────┐            │
              │         │ Override?      │            │
              │         │ PAUSE / STOP / │            │
              │         │ RESUME / STATUS│            │
              │         └───┬───────┬───┘            │
              │             │YES    │NO              │
              │             ▼       ▼                │
              │     ┌──────────┐ ┌──────────────┐    │
              │     │  Guard   │ │   Thinking    │    │
              │     │  Agent   │ │   Module      │    │
              │     └──────────┘ └──────────────┘    │
              │                        │             │
              │                        ▼             │
              │              ┌──────────────────┐    │
              │              │  Memory Module   │    │
              │              │  (raw input log) │    │
              │              └──────────────────┘    │
              │           Perception Module          │
              └─────────────────────────────────────┘
```

---

## The Canonical Stimulus

Every input, from every channel, becomes a `Stimulus`. This is the **only** data format that crosses the Perception → Thinking boundary.

### Schema

```json
{
  "stimulus_id": "uuid v7 (time-sortable)",
  "schema_version": "integer — starts at 1, Perception controls the version",
  "received_at": "ISO8601 timestamp — when Perception received it",
  "occurred_at": "ISO8601 timestamp — when the event actually happened",
  "channel": "string — registered channel name (see Channel Registry)",

  "source": {
    "identity": "string — who or what produced this",
    "authenticated": "boolean — did we verify the source's identity?",
    "auth_method": "none | api_key | jwt | oauth | mutual_tls | system"
  },

  "content": {
    "raw_text": "string — the unprocessed input as plain text",
    "raw_payload": "object — the original channel-specific payload, preserved verbatim",
    "content_type": "text | json | html | markdown | image | audio | video | mixed",
    "attachments": [
      {
        "filename": "string",
        "mime_type": "string",
        "size_bytes": "number",
        "uri": "string — temporary storage location or inline base64 data URI"
      }
    ]
  },

  "channel_metadata": {
    "message_id": "string — external ID for deduplication",
    "thread_id": "string — conversation thread reference",
    "reply_to": "string — where to send a response",
    "channel_specific": "object — anything the adapter wants to pass through"
  },

  "hints": {
    "intent": "string | null — channel-level guess (advisory only)",
    "priority": "low | normal | high | critical",
    "tags": ["string"]
  },

  "override": {
    "is_override": "boolean",
    "command": "pause | stop | resume | status | budget_increase | null",
    "params": "object"
  }
}
```

### Design Decisions

| Decision | Rationale |
|----------|-----------|
| **UUID v7 (time-sortable)** | Sortable by time without a separate index. No coordination needed between channels. |
| **`schema_version` from day one** | Thinking checks the version and can handle migration. Perception is the producer and controls the version. |
| **Both `received_at` and `occurred_at`** | A stuck poller might deliver an email from 2 hours ago. Thinking needs to know both when it happened and when we learned about it. |
| **`raw_payload` preserved verbatim** | Debugging. If normalization loses something, the original is there. Also feeds Memory's raw input log. |
| **`hints` are advisory only** | Channel adapters can guess, but Thinking has final say. Prevents smart adapters from hiding information through misclassification. |
| **`override` is a flag, not a separate path** | Override detection happens in Perception, but the Stimulus still flows through the same pipe. Thinking can see override events too — useful for context. |
| **`channel_metadata` carries reply info** | If a stimulus triggers an action that needs a response, the return address is right there. No guessing. |

---

## Non-Text Input

Perception must handle images, audio, and video — not just text. These are forwarded to Thinking and Memory using the same attachment mechanism as any other file. Perception does not transcribe, OCR, or classify media; that's Thinking's job.

### Principle

> **Media is just another attachment. Perception ships the bytes; Thinking decides what they mean.**

### Media Types Supported

| Type | Common MIME types | Example sources |
|------|-------------------|-----------------|
| **Image** | `image/png`, `image/jpeg`, `image/gif`, `image/webp` | Screenshots, photos sent via chat, browser extension captures |
| **Audio** | `audio/mp3`, `audio/wav`, `audio/ogg`, `audio/webm` | Voice messages from chat platforms, voice notes |
| **Video** | `video/mp4`, `video/webm`, `video/quicktime` | Screen recordings, video messages |

### How It Works

1. Channel adapter receives media input (e.g., a Slack message with an image, a voice note from Telegram)
2. Adapter extracts the media and stores it as a temporary file or gets a URI from the source platform
3. Adapter populates `content.attachments[]` — same field as any other attachment
4. Adapter sets `content.content_type` to `"image"`, `"audio"`, `"video"`, or `"mixed"` (if text + media together)
5. Adapter sets `content.raw_text` to any accompanying caption or description, or `""` if none
6. Stimulus is routed normally — Thinking receives it and decides what to do

### Attachment Strategy (applies to all attachments, media or otherwise)

| Size | Method | Notes |
|------|--------|-------|
| **< 1 MB** | Inline as base64 data URI in `attachment.uri` | Self-contained, no external fetch needed. Thinking has immediate access. |
| **≥ 1 MB** | URI reference to temporary store | Points to Supabase Storage, S3, or local disk. Thinking fetches if it needs to process the media. |

The same mechanism applies regardless of whether the attachment is an image, audio file, video, PDF, or any other binary. Perception does not special-case media — it's all just attachments.

### What Perception Does NOT Do

- Transcribe audio to text
- OCR images
- Generate thumbnails or previews
- Extract metadata beyond MIME type and file size
- Compress or transcode media
- Filter media based on content

All of that belongs to Thinking (or specialized sub-agents Thinking may invoke).



---

## Channel Taxonomy

### Push Channels (external → us)

External parties initiate. We expose an endpoint or listen for incoming data.

| Channel | Trigger | Transport | Auth | Notes |
|---------|---------|-----------|------|-------|
| **Webhook** | External service POSTs | HTTP endpoint | API key / JWT | Generic catch-all. GitHub, Stripe, custom services, IFTTT, Zapier. |
| **Message** | Human sends a message | Chat platform API | Platform OAuth | Slack, Telegram, WhatsApp, Signal. The human talks to the agent directly. |
| **Browser Extension** | Human clicks "send to Soulman" | HTTP from extension | JWT (user session) | Sends current page context, selected text, or a quick note. |
| **CLI** | Human runs `soulman ...` | stdin / local socket | System (local user) | Direct typed commands from the terminal. |

### Pull Channels (we → external)

We initiate. Perception polls external services on a schedule.

| Channel | Trigger | Transport | Auth | Notes |
|---------|---------|-----------|------|-------|
| **Email Watcher** | New unread emails | IMAP poll | OAuth / app password | Checks inbox periodically. Only scans, doesn't delete. |
| **System Monitor** | Threshold crossed | Local agent poll | System (local) | Disk space, CPU, memory, service health, SSL cert expiry. |
| **Note Watcher** | File changed in vault | File system watch | System (local) | Watches specific Obsidian notes for changes. Primary channel for PAUSE/STOP overrides. |
| **Calendar Watcher** | Upcoming event | CalDAV / API poll | OAuth | Checks next N hours for events. Feeds goal tracking. |
| **TODO Watcher** | Overdue or new task | File / API poll | Varies | Watches task lists, issue trackers, project boards. |

### Why Both?

| Pattern | Best for |
|---------|----------|
| **Push** | Real-time interactions. Human messages, webhook events. Low latency. |
| **Pull** | Periodic checks. Email, system health, calendar. No inbound port needed. |

Push gives immediacy. Pull gives resilience (we control the schedule, retry on failure, survive network changes). A complete perception system needs both.

---

## Channel Adapters

### Adapter Contract

Every channel adapter implements this interface (logical, not necessarily OOP):

```
adapter = {
  name: string,              // matches Stimulus.channel
  type: "push" | "pull",
  
  // Push channels
  receive(raw_request) → Stimulus | Error,
  
  // Pull channels
  poll(cursor) → { stimuli: Stimulus[], new_cursor: any } | Error,
  get_interval() → seconds,
  
  // Both
  detect_override(stimulus) → Override | null,
  validate(raw) → boolean,
  authenticate(credentials) → identity | null
}
```

### Adapter Responsibilities

1. **Receive raw input** in channel-native format (HTTP request, IMAP message, file change event)
2. **Authenticate** the source (API key check, JWT verify, OAuth token, system user)
3. **Validate** that the input is well-formed (not garbage, not an attack)
4. **Normalize** to the canonical Stimulus format
5. **Detect overrides** — scan for PAUSE, STOP, RESUME, STATUS signals
6. **Return** the Stimulus (or error) to the Perception router

### What Adapters Must NOT Do

- **Classify intent** beyond a rough hint — that's Thinking's job
- **Filter or suppress** input because it "looks unimportant" — send everything
- **Mutate state** beyond their own checkpoint cursor — they're input only
- **Retry on normalization failure** — log the raw payload and move on

### Adapter Isolation

Each adapter runs independently:
- A crash in Email Watcher doesn't affect Webhook reception
- A slow Calendar poll doesn't delay Message processing
- Authentication failure in one channel doesn't lock others

Implementation: each pull adapter runs in its own async task/timer. Push adapters handle requests independently.



---

## Push Channel Design

### Webhook Endpoint

```
POST /api/perceive/webhook
Authorization: Bearer <api_key>
Content-Type: application/json

{
  "source": "github",
  "event_type": "push",
  "payload": { ... }
}
```

**Authentication**: API key in `Authorization` header. Keys are stored in a config file or environment variable. One key for all webhooks initially; per-service keys later if needed.

**Response**: `202 Accepted` immediately. Processing is async. Returns `stimulus_id` for tracking.

**Rate Limiting**: Token bucket — 60 requests/minute burstable to 120. Above that: `429 Too Many Requests`. Configurable.

### Message Channel (Chat)

Messages arrive via platform-specific mechanisms:
- **Slack**: Socket Mode or Events API — the agent appears as a bot user
- **Telegram**: Long-polling or webhook — the agent is a bot
- **WhatsApp**: Business API webhook

Each is its own adapter. All produce the same Stimulus format.

**Authentication**: Platform OAuth. The agent has its own bot token per platform. The human is identified by their platform user ID.

**Deduplication**: Platform message IDs are stored in the `channel_metadata.message_id` field. The Message adapter checks against recently seen IDs to prevent double-processing from retries.

### Browser Extension

The Chrome extension sends context to a dedicated endpoint:

```
POST /api/perceive/browser
Authorization: Bearer <user_jwt>
Content-Type: application/json

{
  "context_type": "current_page | selection | quick_note",
  "url": "https://...",
  "title": "...",
  "selected_text": "...",
  "note": "..."
}
```

**Authentication**: JWT tied to the user's session. Generated when the user logs into the companion web app.

### CLI

Direct input from the terminal:

```
$ soulman "remind me to check server logs at 5pm"
$ soulman --priority high "production deploy started"
$ soulman pause --reason "focus time" --duration 60
```

**Authentication**: System user — the CLI runs as the local OS user. Trusted by default.

**Implementation**: CLI writes to a local socket or appends to a watched file. Or calls the webhook endpoint on localhost.



---

## Pull Channel Design

### Polling Infrastructure

```
┌─────────────────────────────────────────┐
│              Poll Scheduler              │
│                                          │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ │
│  │  Email   │ │ System   │ │ Calendar │ │
│  │  Watcher │ │ Monitor  │ │ Watcher  │ │
│  │ (30s)    │ │ (300s)   │ │ (600s)   │ │
│  └────┬─────┘ └────┬─────┘ └────┬─────┘ │
│       │             │             │      │
│       ▼             ▼             ▼      │
│  ┌──────────────────────────────────┐   │
│  │        Checkpoint Store          │   │
│  │  (last polled timestamp / UID)   │   │
│  └──────────────────────────────────┘   │
└─────────────────────────────────────────┘
```

### Checkpoint Pattern

Each pull adapter maintains a cursor — the last item it successfully processed:

| Adapter | Cursor Type | Example |
|---------|-------------|---------|
| Email Watcher | IMAP UID | `last_uid: 48291` |
| System Monitor | Timestamp | `last_check: 2025-01-15T14:30:00Z` |
| Note Watcher | File hash + mtime | `checksum: abc123, mtime: 1705334400` |
| Calendar Watcher | Timestamp | `last_window_end: 2025-01-15T18:00:00Z` |
| TODO Watcher | File hash / issue updated_at | `last_updated: 2025-01-15T12:00:00Z` |

Checkpoints are stored in the database via DB Agent (persistent across restarts). If the DB is unavailable, adapters fall back to in-memory checkpoints and re-poll from a safe overlap on restart.

### Poll Interval Configuration

| Adapter | Default Interval | Rationale |
|---------|-----------------|-----------|
| Email Watcher | 30 seconds | Email is time-sensitive but not real-time. IMAP polling is cheap. |
| System Monitor | 5 minutes | System metrics change slowly. 5min is enough for most alerts. |
| Note Watcher | 10 seconds (fs events) | Notes changes should be picked up quickly, especially PAUSE/STOP. |
| Calendar Watcher | 10 minutes | Calendar doesn't change rapidly. Check for upcoming events. |
| TODO Watcher | 15 minutes | Task completion is slow-moving. |

All intervals are configurable per channel in the channel registry.

### Error Handling & Backoff

```
Normal:    poll → success → wait(interval) → poll → success → ...
Error:     poll → failure → wait(min(interval × 2^n, max_backoff)) → poll → ...
           where n = consecutive failures

Default max_backoff = 1 hour
```

After 10 consecutive failures: alert via System Monitor channel (which feeds back into Perception — the agent notices a channel is down).

### Stale Channel Detection

If a pull adapter hasn't successfully polled in `stale_threshold` (default: 3× its interval), the scheduler emits a synthetic `Stimulus`:

```json
{
  "channel": "system",
  "content": { "raw_text": "CHANNEL STALE: Email Watcher — no successful poll in 90s" },
  "hints": { "priority": "high", "tags": ["system", "channel-health"] }
}
```

This lets the agent self-diagnose perception problems.



---

## Override Detection

Override commands are emergency signals from the human that bypass the normal Perception → Think → Act loop. They go directly to the Guard Agent or Routing Agent.

### Recognized Commands

| Command | Params | Effect |
|---------|--------|--------|
| `PAUSE` | `duration_minutes?`, `reason?` | Pause all actions. Guard Agent blocks all delegations. If duration given, auto-resume after. |
| `STOP` | `reason?` | Hard stop. All actions halted until human explicitly RESUMEs. |
| `RESUME` | — | Resume normal operation. |
| `STATUS` | — | Generate a status report: what's running, budgets, recent actions. |
| `BUDGET INCREASE` | `amount`, `scope` | Temporarily increase a budget cap. Still goes through Guard for approval. |

### Detection Points

Override detection happens in the adapter, before normalization to Stimulus:

```
Raw Input → Adapter.receive()
                │
                ├─ Scan for override keywords
                │  (case-insensitive, word-boundary match)
                │
                ├─ Override found?
                │  ├─ YES → Set stimulus.override = { ... }
                │  │        Route to Guard Agent (primary)
                │  │        Also send copy to Thinking (informational)
                │  │
                │  └─ NO  → Normal route to Thinking
```

### Detection Rules

- **Keyword matching**: case-insensitive, word-boundary. `"pause"` matches but `"pauseless"` doesn't.
- **Command position**: override command must be at the **start** of the message or on its own line, OR the entire message content. Prevents false triggers from normal conversation.
- **Explicit prefix**: `soulman pause` is unambiguous. `pause` alone is checked but with lower confidence.
- **Note Watcher special case**: any change to a designated "control note" (e.g., `Soulman Control.md`) is treated as a potential override. The full content is scanned.

### Override Routing

```
Perception
    │
    ├─ stimulus.override.is_override == true
    │       │
    │       ▼
    │   ┌──────────────┐
    │   │ Guard Agent   │  ← Primary: execute or reject the override
    │   └──────┬───────┘
    │          │
    │          ▼
    │   ┌──────────────┐
    │   │ Routing Agent │  ← If guard approves: apply the override
    │   └──────────────┘
    │
    └─ stimulus.override.is_override == false
            │
            ▼
        ┌──────────────┐
        │   Thinking    │  ← Normal flow
        │   Module      │
        └──────────────┘
```

Note: even override stimuli are logged to Memory (raw input log + episodic memory) so the system remembers that a pause happened.

---

## Connection to Thinking Module

Perception produces. Thinking consumes. Inter-module delivery happens over the [[Messaging Bus]] — Perception publishes the normalized Stimulus to a topic and moves on. Thinking subscribes and wakes on arrival.

### What Thinking Receives

A single `Stimulus` object (see schema above). Every time. Every channel. No exceptions.

### What Thinking Returns (to Perception)

Nothing. Perception is fire-and-forget. Once the Stimulus is published to the bus, Perception's job is done. Thinking may later produce an Action, which may send a response — but that flows through the Action Module, not back through Perception.

### Ordering Guarantees

- **Within a channel**: stimuli are delivered in the order they were received (FIFO).
- **Across channels**: no ordering guarantees. A webhook event and an email that arrive at the same moment may be processed in any order. Thinking must handle this.

### Backpressure

If Thinking is overwhelmed (too many stimuli in flight):
- Perception buffers up to `max_buffer_size` (default: 100) stimuli in memory.
- Beyond that: new stimuli are written directly to `memory.raw_inputs` table and flagged as `buffered: true`.
- When Thinking catches up, it pulls from the buffer first, then the DB backlog.
- If the buffer stays >80% full for >60 seconds, a synthetic `SYSTEM OVERLOAD` stimulus is generated.

---

## Connection to Memory Module

Every stimulus is logged. No exceptions.

### Raw Input Log

```
memory.raw_inputs table:
  stimulus_id      UUID (PK)
  received_at      TIMESTAMPTZ
  channel          TEXT
  source_identity  TEXT
  raw_payload      JSONB
  normalized_text  TEXT
  is_override      BOOLEAN
```

This is the **immutable audit log**. Never modified after insert. If the agent later realizes it misinterpreted something, the correction goes into episodic memory — the raw log stays untouched.

### When Logging Fails

If the DB Agent is unavailable at stimulus time:
1. Write stimulus to a local append-only log file (JSON lines)
2. On next successful DB connection: replay the local log into `memory.raw_inputs`
3. If local log exceeds 10MB: alert via System Monitor channel

---

## Authentication Model

### Design Principle

> **Default closed. Every channel must prove identity.**

No anonymous input. Even pull channels authenticate to the services they poll.

### Auth Methods by Channel

| Channel | Method | Credential Storage |
|---------|--------|-------------------|
| Webhook | API key (Bearer token) | Environment variable / config file |
| Message (Slack) | Slack Bot Token (OAuth) | Environment variable |
| Message (Telegram) | Telegram Bot Token | Environment variable |
| Browser Extension | User JWT (from companion app auth) | Generated at login, verified via Supabase Auth |
| CLI | System user (local) | OS-level trust |
| Email Watcher | IMAP credentials (OAuth or app password) | Environment variable / secrets manager |
| System Monitor | Local system (runs as OS user) | OS-level trust |
| Note Watcher | Local filesystem | OS-level trust |
| Calendar Watcher | CalDAV credentials / API key | Environment variable |
| TODO Watcher | Varies by backend | Environment variable |

### Auth Failure Response

| Channel Type | Failure Behavior |
|-------------|-----------------|
| Push | Return `401 Unauthorized`. Log attempt. If >5 failures from same IP in 1 minute, temporarily block IP. |
| Pull | Log error. Exponential backoff. After 3 consecutive auth failures, emit a `CHANNEL AUTH FAILURE` stimulus so the agent knows. |

---

## Deduplication Strategy

The same external event might arrive through multiple paths:
- An email arrives via IMAP pull AND a push notification from the email provider
- A GitHub webhook fires twice due to a network retry

### Dedup Mechanism

1. **Natural keys**: `channel + channel_metadata.message_id` is the primary dedup key.
2. **Content hash fallback**: if no message_id, hash `channel + source_identity + raw_text` as a secondary key.
3. **Time window**: dedup window is 1 hour. After that, same message_id is treated as a new event (could be a legitimate repeat).

### Implementation

On receiving a stimulus, before routing:
1. Compute dedup key
2. Check against in-memory LRU cache (last 1000 stimuli, ~1 hour TTL)
3. If duplicate → log as `duplicate` and drop (don't route to Thinking)
4. If new → add to cache, route normally

The cache is in-memory only. On restart, the first hour may see some duplicates — acceptable for MVP.

---

## Error Handling Summary

| Failure | Handling | Recovery |
|---------|----------|----------|
| Adapter crash (pull) | Isolated. Other adapters unaffected. | Scheduler restarts adapter on next tick. Backoff applies. |
| Adapter crash (push) | HTTP 500 returned to caller. | Next request creates new adapter instance. |
| Auth failure (push) | 401 returned. IP throttled after repeated failures. | Automatic (throttle expires). |
| Auth failure (pull) | Exponential backoff. Alert stimulus after 3 failures. | Manual credential fix + restart. |
| Malformed input | Log raw payload. Return 202 (push) or skip (pull). Don't crash. | None needed — malformed input is dropped. |
| Normalization error | Log raw payload + error. Emit stimulus with `content.raw_text = "[NORMALIZATION ERROR]"`. | Thinking sees the error and can alert. |
| DB unavailable (logging) | Fall back to local append-only log file. | Replay on reconnect. |
| Buffer overflow | Spill to DB. Alert if sustained. | Thinking catches up → buffer drains. |
| Duplicate stimulus | Detected by dedup cache. Dropped. | Cache expires after 1 hour. |



---

## Configuration

### Channel Registry

Channels are defined in a registry (stored as a config file or DB facts):

```yaml
channels:
  webhook:
    enabled: true
    type: push
    endpoint: /api/perceive/webhook
    auth: api_key
    rate_limit:
      requests_per_minute: 60
      burst: 120

  email-watcher:
    enabled: true
    type: pull
    interval_seconds: 30
    max_backoff_seconds: 3600
    stale_threshold_multiplier: 3
    auth: oauth
    config:
      imap_server: imap.gmail.com
      port: 993

  note-watcher:
    enabled: true
    type: pull
    interval_seconds: 10
    config:
      watch_paths:
        - "Soulman Control.md"
        - "Goals.md"
        - "Inbox.md"

  system-monitor:
    enabled: true
    type: pull
    interval_seconds: 300
    config:
      checks:
        - type: disk_space
          path: /
          warning_threshold_percent: 80
          critical_threshold_percent: 95
        - type: memory
          warning_threshold_percent: 85
        - type: cpu
          warning_threshold_percent: 90

  calendar-watcher:
    enabled: false   # not yet configured
    type: pull
    interval_seconds: 600

  # ... more channels
```

### Perception Config

```yaml
perception:
  port: 9001
  max_buffer_size: 100
  buffer_overflow_alert_threshold_percent: 80
  buffer_overflow_alert_duration_seconds: 60
  dedup_cache_size: 1000
  dedup_window_seconds: 3600
  local_log_fallback_path: /var/log/soulman/perception-fallback.log
  local_log_max_size_bytes: 10485760  # 10MB
  attachment_max_inline_bytes: 1048576  # 1MB — above this, use URI reference

  watchdog:
    enabled: true   # set false for development
    health_check_interval_seconds: 5
    health_check_failures_before_restart: 3
    cooldown_seconds: 10
    max_restarts: 5
    restart_window_seconds: 60

  override:
    keywords:
      - PAUSE
      - STOP
      - RESUME
      - STATUS
      - "BUDGET INCREASE"
    control_note: "Soulman Control.md"
```

---

## Runtime

Perception runs as a **dedicated server process** within the Soulman project. It is not an edge function, not a sidecar, and not embedded in another module — it gets its own process and its own port.

### Process Model

```
┌────────────────────────────────────────────┐
│           Perception Server                 │
│                                              │
│  ┌──────────────────────────────────────┐   │
│  │          Webservice Port             │   │
│  │  (configurable, e.g. :9001)          │   │
│  │                                      │   │
│  │  POST /api/perceive/webhook          │   │
│  │  POST /api/perceive/browser          │   │
│  │  POST /api/perceive/message          │   │
│  │  GET  /health                        │   │
│  │  (CLI writes to localhost here too)  │   │
│  └──────────────────────────────────────┘   │
│                                              │
│  ┌──────────────────────────────────────┐   │
│  │          Poll Scheduler              │   │
│  │  (runs in-process, async loop)       │   │
│  │                                      │   │
│  │  Email Watcher     ── 30s ── IMAP   │   │
│  │  System Monitor    ── 5m  ── local   │   │
│  │  Note Watcher      ── fs events      │   │
│  │  Calendar Watcher  ── 10m ── CalDAV  │   │
│  │  TODO Watcher      ── 15m ── varies  │   │
│  └──────────────────────────────────────┘   │
│                                              │
│  ┌──────────────────────────────────────┐   │
│  │       Stimulus Router                │   │
│  │  (dedup → override check → route)   │   │
│  └──────────────────────────────────────┘   │
└────────────────────────────────────────────┘
         │                    │
         ▼                    ▼
   ┌──────────┐      ┌──────────────┐
   │ Thinking │      │ Memory (DB)  │
   │ Module   │      │ raw input log│
   └──────────┘      └──────────────┘
```

### Implementation Language

Python or TypeScript — whichever fits the rest of the Soulman stack best. The code lives in the Soulman project repository (e.g. `soulman/perception/`). It should be simple: a web framework (FastAPI/Express), an async loop for polling, and adapter modules for each channel.

### Port

The webservice listens on a dedicated port (e.g. `:9001`). This port is:
- Exposed to localhost for the CLI adapter
- Exposed to the internet (or a reverse proxy) for webhooks and browser extension
- Not shared with any other Soulman service

### Watchdog

A watchdog process monitors the Perception server and restarts it if it crashes or becomes unresponsive:

```
watchdog ── health check ──► Perception Server
   │                            │
   │     no response after      │
   │     N consecutive fails    │
   │◄───────────────────────────│
   │
   ▼
restart Perception Server
```

| Constraint | Why |
|------------|-----|
| **Trivial to disable** | During development, restarts mask bugs and create confusion. Single switch: `WATCHDOG_ENABLED=false` env var or `--no-watchdog` CLI flag. |
| **Health check endpoint** | Perception exposes `GET /health` that the watchdog polls. Returns 200 + `{status: "ok", uptime: ..., channels_active: [...]}`. |
| **Cooldown period** | After a restart, the watchdog waits `cooldown_seconds` (default: 10) before monitoring resumes. Prevents restart loops. |
| **Max restarts per window** | If restarted more than `max_restarts` (default: 5) within `restart_window_seconds` (default: 60), the watchdog gives up and alerts. |
| **Logs on restart** | Every restart logged with reason and last N lines of stdout/stderr from the dead process. |

### Development Mode

When `WATCHDOG_ENABLED=false` (or `--dev` flag):
- Watchdog does not start. Crashes are normal and expected.
- The perception server can be run directly with hot-reload.
- No health check polling overhead.
- Port and all channel configs are overrideable via environment.

---

## Resolved Questions

| # | Question | Status | Notes |
|---|----------|--------|-------|
| 1 | **Where does the Perception runtime live?** | ✅ Resolved | Dedicated server process with its own webservice port. Push channels come in via the webservice port. Pull channels run inside the same process on a schedule. Implementation: Python or TypeScript, lives in the Soulman project repo. Watchdog restarts the server if it goes down — trivial to disable during development. |
| 2 | **How are channel credentials managed?** | ✅ Resolved | Environment variables for MVP. Eventually: a secrets manager (Supabase Vault, AWS Secrets Manager, or 1Password CLI). The agent should NOT have access to rotate its own credentials. |
| 3 | **Multi-device human identity** | ✅ Resolved | Channel + platform user ID is identity enough for Perception. Cross-device identity consolidation is a Memory concern, not Perception. |
| 4 | **Should pull adapters run on a cron or a persistent loop?** | ✅ Resolved (by #1) | Persistent async loop, in-process in the Perception server. The Poll Scheduler is part of the server, not a separate cron. |
| 5 | **What happens when the system is fully down?** | ✅ Resolved | Push events: lost (caller gets 503). Pull events: gap when restarted — adapters use checkpoints to catch up. For critical push events, the external service's own retry logic is the safety net. |
| 6 | **Channel addition workflow** | ✅ Resolved | MVP: edit config file + restart. Future: agent can suggest adding a channel but the human does the credential setup. Can evolve to other mechanisms later if needed. |
| 7 | **Stimulus schema versioning** | ✅ Resolved | Add a `schema_version` field to Stimulus from day one. Thinking checks the version and can handle migration. Perception is the producer and controls the version. |
| 8 | **Attachment handling for large files** | ✅ Resolved | Inline base64 for small files (<1MB), URI reference for larger ones. The URI points to a temporary store (Supabase Storage, S3, local disk). Attachments are passed through to Thinking which decides whether to fetch and process them. |

---

## Implementation Sequence

1. **Scaffold the Perception server process** — web framework, port binding, health check endpoint, watchdog toggle
2. **Define the Stimulus schema** (as a TypeScript type or JSON Schema)
3. **Build the Webhook adapter** — first push channel. Simplest auth (API key).
4. **Build the Note Watcher adapter** — first pull channel. Critical for overrides.
5. **Build the CLI adapter** — enables direct human input during development.
6. **Implement dedup cache + raw input logging**
7. **Build the Poll Scheduler** — generic pull loop infrastructure
8. **Add Email Watcher, System Monitor, Calendar Watcher** — remaining pull channels
9. **Add Message adapters** — Slack, Telegram (push, but more complex auth)
10. **Add Browser Extension adapter** — requires companion app auth

---

## Design Decisions Log

| Decision | Rationale |
|----------|-----------|
| **Perception is stateless (except checkpoints)** | No session state in Perception. Each stimulus is self-contained. Checkpoints are the only persistent state. Keeps the module simple and restartable. |
| **No filtering by priority** | Even low-priority stimuli are delivered to Thinking. Thinking decides what to defer. Perception doesn't have the context to judge. |
| **Override goes to Guard, not Thinking** | Overrides are operational commands, not conversational input. Guard Agent is the operational gate. Thinking observes but doesn't control overrides. |
| **Adapters are dumb pipes** | No intent classification in adapters. No summarization. No enrichment. Any intelligence at the edge creates inconsistency and hides information from Thinking. |
| **Pull scheduling is centralized** | One scheduler, not per-adapter timers. Easier to monitor, pause, and debug. Individual adapters declare their interval; the scheduler enforces it. |
| **Dedup is in-memory with DB fallback** | In-memory is fast for the common case. The DB-backed raw input log is the source of truth for dedup on restart. A small window of duplicates on restart is acceptable. |
| **Local log fallback for DB outages** | Perception should not lose input because the DB is down. A local append-only file is simple, reliable, and easy to replay. |
| **Media is just another attachment** | Images, audio, and video use the same attachment mechanism as any file. No special-casing. Perception ships bytes; Thinking interprets them. |

---

## Related Modules

- [[Thinking module]] — consumes Stimulus, classifies, decides
- [[Memory module]] — stores raw input log, episodic memory
- [[Action module]] — may send responses via channels (reply to email, post to chat)
- [[Project Soulman]] — overview of the full Perceive → Think → Consult Memory → Act loop