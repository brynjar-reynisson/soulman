# Project Soulman

> **Soulman** is a personal AI agent that runs alongside you. It perceives the world through multiple input channels, thinks about what matters against its goals, consults its self-built memory for context, and acts through a fleet of specialized sub-agents — all within boundaries you control.

---

## Core Loop

```
┌─────────────────────────────────────────────────────────────────┐
│                      THE SOULMAN LOOP                            │
│                                                                  │
│   ┌──────────┐     ┌──────────┐     ┌──────────┐     ┌───────┐  │
│   │ PERCEIVE │ ──▶ │  THINK   │ ──▶ │ CONSULT  │ ──▶ │  ACT  │  │
│   │          │     │          │     │  MEMORY  │     │       │  │
│   └──────────┘     └──────────┘     └──────────┘     └───────┘  │
│        │                                                  │      │
│        │              ┌──────────────┐                    │      │
│        └─────────────▶│  RAW INPUT   │◀───────────────────┘      │
│                       │     LOG      │                           │
│                       └──────────────┘                           │
│                                                                  │
│   ════════════════  OVERRIDE BYPASS  ════════════════            │
│                                                                  │
│   ┌──────────┐                                         ┌───────┐ │
│   │ PERCEIVE │ ──────── PAUSE / STOP / RESUME ───────▶ │ GUARD │ │
│   └──────────┘                                         └───────┘ │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

The agent follows one unbroken chain: **Perceive → Think → Consult Memory → Act**. Every external stimulus enters through Perception, flows through Thinking for goal-driven reasoning and decision, pulls context from Memory, and produces actions delegated to specialized sub-agents. Every stimulus and every action is logged to Memory's immutable raw input log.

A separate **override bypass** allows the human to issue PAUSE, STOP, RESUME, STATUS, or BUDGET INCREASE commands that skip Thinking entirely and go straight to the Guard Agent — the operational gatekeeper that can halt all action.

---

## Module Summaries

### [[Perception module]] — Ears and Eyes

Perception is the **entry point for all external stimuli**. It receives raw input from multiple channels, normalizes everything into a single canonical `Stimulus` format, and routes it onward. It does not interpret, classify, or filter — it senses, and only senses.

**Key responsibilities:**
- Receive input from **push channels** (Webhook, Chat Messages, Browser Extension, CLI) and **pull channels** (Email Watcher, System Monitor, Note Watcher, Calendar Watcher, TODO Watcher)
- Normalize every input into the canonical `Stimulus` schema — a single versioned JSON structure that crosses the Perception → Thinking boundary
- Detect **override commands** (PAUSE, STOP, RESUME, STATUS, BUDGET INCREASE) and route them directly to the Guard Agent
- Log every stimulus to Memory's raw input log (immutable audit trail)
- Deduplicate stimuli that arrive through multiple paths
- Handle media (images, audio, video) as attachments — ships bytes without interpreting them

**What it deliberately does NOT do:** classify intent, prioritize, filter, transcribe media, or make any decisions. All intelligence lives downstream in Thinking.

**Runtime:** Dedicated server process with its own port (e.g. `:9001`), running both a webservice for push channels and an async poll scheduler for pull channels. A watchdog restarts it if it crashes.

---

### [[Thinking module]] — Brain

Thinking is the **goal-driven reasoning core** of Soulman. It consumes normalized `Stimulus` objects from Perception, evaluates them against active goals, retrieves context from Memory, reasons through a structured six-step loop, and produces decisions that may lead to delegated actions. It is the only module with agency — every other module is a pipe or a store.

**Architecture:** A single LLM session governed by a system prompt that serves as its constitution — defining identity, active goals (loaded from DB at session start), constraints, decision rules, and personality. Thinking is goal-driven, not rule-driven: goals provide direction without exhaustively specifying behavior.

**The Reasoning Loop (6 steps):**
1. **PERCEIVE** — Ingest stimulus from Perception, check idempotency
2. **PRIORITIZE** — Filter against active goals. Score 0 → ignored, loop ends
3. **RETRIEVE** — Query Memory for relevant episodes, facts, procedures (budget: 5 queries/cycle)
4. **REASON** — Apply explicit reasoning patterns (chain of thought, pros/cons, pre-mortem, root cause, first principles, precedent comparison) selected by input type and risk
5. **DECIDE** — Choose: NO_ACTION, ACKNOWLEDGE, INVOKE_ACTION, ASK_USER, UPDATE_GOAL, LEARN, or REFUSE. For actions: produce intent + parameters + risk level + urgency
6. **REFLECT** — Log episode, check for errors, extract new facts, update procedure stats, calibrate confidence. Mandatory every cycle

**Goals System:** Active goals live in `memory.goals` (DB source of truth) and are loaded into the system prompt at session start. The human manages goals through natural language via any Perception channel — create, edit, reorder, pause, abandon. The agent can propose changes but only auto-applies factual state changes (e.g., marking a goal `achieved` when criteria are met). Value judgments always go through the human.

**Goal Conflict Resolution:** Priority score first (gap ≥2 auto-resolves), precedent second, human-in-the-loop for value trade-offs. Conflicts are logged, never silently buried.

**Refusal Protocol:** When asked to violate constraints, Thinking refuses gracefully with a 4-part structure: Acknowledge → Explain (which constraint, no jargon) → Offer alternatives → Invite re-direction.

**Error Recovery:** When wrong: Apologize → Log → Adjust confidence on the specific thing → Retry with different reasoning. Systemic error patterns (3+ same error/week, procedure success <50%) surface to the human.

**Session Model:** Configurable modes — Interaction (one exchange), Daily (calendar day), Idle-timeout (N minutes idle). Sleep mode (consolidation windows) handles summarization, deduplication, embedding generation, and proactive observations on a separate budget.

**What it deliberately does NOT do:** execute actions directly, store memory directly, receive raw external input, or modify its own system prompt. It sits at the center of the loop but never touches the edges.

---

### [[Memory module]] — Self-Building Library and Diary

Memory is the **persistent, self-structured store** of everything the agent knows and everything that has happened. It is unique among the modules: the LLM is the memory architect — it decides what to remember, how to structure it, when to retrieve, and when to consolidate. We provide the tools (Supabase + pgvector); the agent designs its own schema.

**Four Memory Types:**

| Type | Purpose | Storage | Lifetime |
|------|---------|---------|----------|
| **Working Memory** | Active context for current task | LLM context window | Single session |
| **Episodic Memory** | Record of interactions, decisions, outcomes | `memory.episodes` | Permanent |
| **Semantic Memory** | Facts, preferences, learned patterns | `memory.facts` + pgvector embeddings | Permanent |
| **Procedural Memory** | Known-good action sequences, reusable strategies | `memory.procedures` | Permanent, evolves |

**Self-Building Pattern:** The first time the LLM needs memory, it bootstraps — checks if schema exists, applies migrations to create base tables + pgvector extension if not, then uses the fresh tables. It can extend the schema at any time (add columns, tables, indexes) as new types of knowledge emerge. No predefined ontology — tags are freeform, the agent organizes as it learns.

**Retrieval:** Multi-strategy — exact SQL match, pgvector semantic similarity (cosine distance on embeddings), time-range queries, tag filtering. The Thinking module queries during its RETRIEVE step with a budget of 5 queries per cycle.

**Consolidation:** Schedule-based (daily/weekly cadence). Summarizes old episodes into facts, merges duplicates, prunes low-confidence or stale information, updates procedure success rates, generates missing embeddings. Runs during sleep/consolidation windows with a separate budget.

**Conflict Resolution:** Majority vote for clear winners; explicit CONFLICTED flag when there's no majority — surfaces ambiguity to Thinking rather than silently picking.

**Forget Mechanism:** Supports hard deletion of derived data (episodes, facts) while annotating the immutable raw input log with `forgotten_at` timestamps. Cascade: forget a fact → forget episodes referencing it → annotate raw inputs that sourced it.

**Infrastructure:** Supabase Postgres + pgvector. One database for relational + vector — no separate vector store. Every fact links back to its source episode for provenance.

**What it deliberately does NOT do:** interpret or modify the raw input log, make decisions, or push data to other modules. Memory is a service queried by Thinking and Action.

---

### [[Action module]] — Multi-Agent Execution Fleet

Action is the **execution layer**. It takes action requests from Thinking and delegates them to a fleet of **specialized sub-agents**, each scoped to a specific domain with its own MCP tools. A Routing Agent dispatches; a Guard Agent gates; a Governance layer enforces budgets and circuit breakers.

**Sub-Agent Fleet:**

| Agent | Domain | Key MCP Tools |
|-------|--------|---------------|
| **DB Agent** | Data persistence, queries, schema | `supabase` (execute_sql, apply_migration, list_tables) |
| **Filesystem Agent** | Notes, file ops, vault management | `obsidian` (create-note, read-note, edit-note, search-vault), local FS |
| **Web Agent** | External APIs, webhooks, search | `webSearch`, `webFetch`, HTTP client |
| **Communication Agent** | Email, messaging, notifications | SMTP/IMAP, platform APIs (Slack, Telegram) |
| **Code Agent** | Software development, refactoring | `agent-lsp` (find_references, apply_edit, run_tests, run_build) |
| **Guard Agent** | Safety gate for high-risk actions | None of its own — intercepts delegations before execution |

Each sub-agent is a separate LLM session with its own context window, tool set, and failure domain. A crash in one doesn't touch the others. Agents can be added, removed, or upgraded independently — they're defined in a registry stored as Memory facts, not hardcoded.

**Routing Agent** — The single entry point from Thinking. Receives action intent, classifies the task, selects the best-fit sub-agent (by capability description, tool availability, and historical success rate), delegates via a structured handoff protocol, and aggregates results if multiple agents are needed. For now: rule-based routing. Procedural memory already captures success/failure patterns for future learning.

**Handoff Protocol:** Structured JSON both ways — task_id, action_type, intent, parameters, context (thinking summary + relevant memory + previous attempts), expected output format. Sub-agent returns status, result, artifacts (URLs/file paths/record IDs), and memory update suggestions.

**Guard Agent** — The operational gatekeeper. Intercepts high-risk actions before execution. Risk classification: 🟢 Low (read-only, idempotent — auto-approved), 🟡 Medium (writes, messages — auto within budget), 🟠 High (destructive, mass actions — Guard review), 🔴 Critical (data deletion, credential changes, financial — always requires human approval).

**Governance Layer** — Constrains volume, velocity, and cost:

| Dimension | Unit | Scope |
|-----------|------|-------|
| Tool Calls | count | Per-task / per-session / per-day |
| Tokens | tokens | Per-task / per-session |
| Wall Time | seconds | Per-task timeout |
| Cost | $ estimated | Per-task / per-session / per-day |

Three enforcement tiers: Normal (proceed), Soft Throttle (warning + linear backoff when ≤30% remaining), Hard Cap (delegation refused when exhausted).

**Circuit Breakers:** Separate from budget. Consecutive errors (3 → suspend agent for session), error rate spike (>50% → pause all, notify human), loop detection (same action+params ×5 → block, flag for re-plan), cost spike (>$0.50 → pre-flight Guard approval).

**Pre-flight sequence:** Routing Agent prepares → Budget Tracker checks all scopes → Guard Agent evaluates risk → only if both pass does delegation reach the sub-agent.

**Audit Trail:** Every action (or block) recorded to `memory.action_log` — who, what, risk level, result, cost, duration, who approved, circuit state. Queryable, summarizable, feedable back into Thinking for reflection.

**What it deliberately does NOT do:** decide what to do (Thinking's job), classify input (Thinking's job), or store memory directly (Memory's job).

---

## How the Modules Interact

### Normal Flow: Stimulus → Response

```
1. External event occurs (email, message, system alert, webhook)

2. PERCEPTION receives it
   ├─ Channel adapter normalizes to Stimulus
   ├─ Dedup check (drop if duplicate)
   ├─ Override check (is this PAUSE/STOP/etc? → if yes, skip to override flow)
   ├─ Log to Memory raw input log
   └─ Route to Thinking

3. THINKING runs the Reasoning Loop
   ├─ PERCEIVE: ingest stimulus, check idempotency
   ├─ PRIORITIZE: score against active goals. Score 0 → log & done
   ├─ RETRIEVE: query Memory for context (episodes, facts, procedures — 5 calls max)
   ├─ REASON: apply reasoning patterns to interpret, map to goals, generate options
   ├─ DECIDE: NO_ACTION | ACKNOWLEDGE | INVOKE_ACTION | ASK_USER | REFUSE
   └─ REFLECT: log episode, extract facts, update procedures, calibrate

4. MEMORY serves context and stores results
   ├─ Raw Input Log: immutable stimulus record (written by Perception)
   ├─ Episodic Memory: what happened, what was decided (written by Thinking)
   ├─ Facts: people, projects, preferences (queried & updated over time)
   └─ Procedures: known-good action patterns (success/failure stats evolve)

5. ACTION executes (if Thinking decided INVOKE_ACTION)
   ├─ Routing Agent classifies task → selects sub-agent
   ├─ Budget Tracker: check all scopes → PROCEED or BLOCK
   ├─ Guard Agent: evaluate risk class → PROCEED or ASK_HUMAN
   ├─ If both pass → delegate to sub-agent via handoff protocol
   ├─ Sub-agent executes using its MCP tools
   └─ Result returned → logged to action_log → fed back to Thinking
```

### Override Flow: Emergency Brake

```
1. Human issues PAUSE (via CLI, chat, or control note change)

2. PERCEPTION detects override keyword
   ├─ Sets stimulus.override = { is_override: true, command: "pause" }
   ├─ Routes to Guard Agent (primary — execution)
   ├─ Also sends copy to Thinking (informational, for situational awareness)
   └─ Logs to Memory raw input log

3. GUARD AGENT executes the override
   ├─ Validates the command
   ├─ Blocks all pending and future delegations
   └─ Logs the override to memory.action_log

4. THINKING observes the pause
   └─ Annotates current tasks as "paused_by_human" in episodic memory

5. When human issues RESUME:
   ├─ Guard lifts the block
   ├─ Thinking picks up where it left off (using Memory for context)
   └─ Normal loop resumes
```

### Data Contracts Between Modules

| Boundary | Format | Transport | Direction | Notes |
|----------|--------|-----------|-----------|-------|
| **Perception → Thinking** | `Stimulus` (JSON, schema versioned) | Message bus | One-way, async | The only format that crosses this boundary |
| **Thinking → Memory (reads)** | HTTP/gRPC request | HTTP/gRPC | Sync, blocking | Thinking blocks during RETRIEVE until response arrives |
| **Thinking → Memory (writes)** | Bus event | Message bus | One-way, async | Episodes, facts, goal updates — fire-and-forget |
| **Thinking → Action** | Action Request (intent + params + risk + `correlation_id`) | Message bus | One-way, async | Always async; Thinking saves state and suspends |
| **Action → Thinking (result)** | Result event (keyed by `correlation_id`) | Message bus | One-way, async | Resumes Thinking's saved reasoning state |
| **Routing Agent → Sub-Agent** | Handoff JSON (task_id, action_type, intent, params, context) | Internal | One-way per delegation | Structured protocol; sub-agent returns result + artifacts + memory suggestions |
| **Action → Memory** | Bus event (action_log, episodes, facts) | Message bus | One-way, async | Fire-and-forget; action logs all attempts and results |
| **Perception → Memory** | Bus event (raw_inputs) | Message bus | One-way, async | Immutable append-only; fire-and-forget |
| **Perception → Guard Agent** | Override Stimulus | Message bus | One-way, bypass | Only PAUSE/STOP/RESUME/STATUS/BUDGET INCREASE |
| **Budget Tracker → Routing Agent** | PROCEED / BLOCK | Internal | Pre-flight check | Governance layer |

### Key Design Principles Across All Modules

1. **Separation of concerns is absolute.** Perception doesn't think. Thinking doesn't execute. Action doesn't decide. Memory doesn't interpret.

2. **The raw input log is immutable.** Every stimulus and action is recorded verbatim. Corrections go alongside the original, never replacing it. Forget requests annotate but don't delete from the raw log.

3. **Override commands bypass intelligence, not safety.** PAUSE/STOP skip Thinking but still go through Guard — the human can always pull the brake, and the brake always works.

4. **Default closed, progressively open.** Every channel must authenticate. Every action must be approved. New agents start with tight budgets and high risk classification. Trust is earned in production, not granted at design time.

5. **Resilience through local fallback.** If the DB is down, Perception and Action fall back to local append-only logs. They replay on reconnect. The agent may be blind to history, but it doesn't lose data.

6. **One canonical format at each boundary.** `Stimulus` crosses Perception → Thinking. Action Requests cross Thinking → Action. Handoff JSON crosses Routing Agent → Sub-Agent. No module parses another module's internal formats.

7. **The LLM is the memory architect.** We don't pre-define schemas for knowledge. We provide tools (Supabase + pgvector) and patterns; the agent designs, extends, and maintains its own storage.

8. **Goals are the engine.** Every perception is evaluated against active goals. The human is the ultimate authority on what matters and how much it matters. The agent proposes; the human decides value judgments.

9. **Errors are learning opportunities.** Refusal is graceful (acknowledge, explain, offer, invite). Error recovery is a protocol (apologize, log, adjust, retry). Systemic patterns surface to the human.

10. **Two-tier transport: bus for async, HTTP/gRPC for sync.** The [[Messaging Bus]] handles all fire-and-forget and event-driven communication (stimulus delivery, action dispatch, action results, log writes). Memory read queries during Thinking's RETRIEVE step are the exception — they are synchronous HTTP/gRPC calls, because Thinking cannot continue until it has the answer. See `docs/superpowers/specs/2026-06-27-inter-module-communication-design.md` for the full decision matrix and rationale.

---

## System Architecture

```
                        ┌──────────────────────────────┐
                        │         EXTERNAL WORLD        │
                        │   Email  Chat  Web  CLI  API  │
                        └──────────────┬───────────────┘
                                       │
                                       ▼
                        ┌──────────────────────────────┐
                        │       PERCEPTION MODULE       │
                        │   Port :9001 (own process)    │
                        │                               │
                        │   Push: Webhook, Messages,    │
                        │         Browser, CLI          │
                        │   Pull: Email, System, Notes, │
                        │         Calendar, TODOs       │
                        │                               │
                        │   → Normalize to Stimulus     │
                        │   → Dedup                     │
                        │   → Override detection        │
                        └───────┬──────────┬───────────┘
                                │          │
                    Normal      │          │  Override
                    Stimulus    │          │  (PAUSE/STOP/etc)
                                ▼          ▼
                        ┌──────────────┐ ┌──────────────────┐
                        │   THINKING   │ │   GUARD AGENT    │
                        │   MODULE     │ │   (in Action)    │
                        │              │ │                  │
                        │  System      │ │  Enforce         │
                        │  Prompt      │ │  boundaries      │
                        │  (constitution)│ │  Risk classify   │
                        │              │ │  Human approval  │
                        │  Reasoning   │ │                  │
                        │  Loop (6     │ └────────┬─────────┘
                        │  steps)      │          │
                        │              │          │
                        │  Goals       │          │
                        │  Engine      │          │
                        └──────┬───────┘          │
                               │                  │
                               ▼                  ▼
              ┌────────────────────────────────────────────────────┐
              │                  ACTION MODULE                      │
              │                                                     │
              │  ┌──────────────┐   ┌──────────────────────────┐   │
              │  │   ROUTING    │   │      GOVERNANCE           │   │
              │  │   AGENT      │   │  Budgets · Circuit        │   │
              │  │              │   │  Breakers · Cost Est.     │   │
              │  └──────┬───────┘   └──────────────────────────┘   │
              │         │                                           │
              │    ┌────┼────┬──────────┬──────────┐               │
              │    ▼    ▼    ▼          ▼          ▼               │
              │  ┌────┐┌────┐┌────┐  ┌──────┐  ┌──────┐           │
              │  │ DB ││ FS ││Web │  │Comm  │  │Code  │           │
              │  │Agt ││Agt ││Agt │  │Agent │  │Agent │           │
              │  └────┘└────┘└────┘  └──────┘  └──────┘           │
              │                                                     │
              └──────────────────────┬──────────────────────────────┘
                                     │
                                     ▼
                         ┌───────────────────────┐
                         │     MEMORY MODULE     │
                         │                       │
                         │  Supabase + pgvector  │
                         │                       │
                         │  Raw Input Log        │  (immutable)
                         │  Episodes             │  (experiences)
                         │  Facts + Embeddings   │  (knowledge)
                         │  Procedures           │  (patterns)
                         │  Goals                │  (priorities)
                         │  Action Log           │  (audit trail)
                         └───────────────────────┘
```

---

## Technology Stack

| Layer | Technology | Notes |
|-------|-----------|-------|
| **Database** | Supabase (Postgres + pgvector) | One DB for relational + vector. Self-built schema. |
| **Vector Search** | pgvector `<=>` cosine distance | Semantic retrieval for facts and episodes |
| **LLM** | Single session per agent | Thinking gets one; each sub-agent gets its own |
| **MCP Tools** | Agent-specific tool sets | Supabase, Obsidian, LSP, web search, filesystem |
| **Perception Runtime** | Python or TypeScript (FastAPI/Express) | Dedicated process, port :9001 |
| **Perception Channels** | IMAP, CalDAV, webhooks, chat APIs, fs watch | Push + pull channel adapters |
| **Message Bus** | Configurable message broker | Async inter-module communication — stimulus delivery, action dispatch, result events, log writes. See [[Messaging Bus]]. |
| **Service API** | HTTP or gRPC | Synchronous Memory reads — Thinking's RETRIEVE step calls Memory's read API directly and blocks for the response. |

---

## Implementation Strategy

Modules built in dependency order. Each produces a working, testable slice.

| Phase | Module | What to Build |
|-------|--------|---------------|
| **1** | [[Memory module]] | DB schema via Supabase. Raw input log, episodes, facts, procedures, goals, action_log tables. pgvector extension. DB Agent as first sub-agent. |
| **2** | [[Perception module]] | Server process, Stimulus schema, one push channel (Webhook), one pull channel (Note Watcher), CLI adapter, dedup cache, raw input logging to Memory. |
| **3** | [[Thinking module]] | System prompt template, goals table + loading, reasoning loop skeleton, RETRIEVE → Memory integration, DECIDE → Action handoff. Start rule-based; add LLM integration progressively. |
| **4** | [[Action module]] | Routing Agent + one sub-agent (DB Agent already exists). Add FS Agent, Web Agent. Guard Agent for override handling. Governance: budget tracker, circuit breakers, action_log. Remaining agents (Comm, Code) follow. |

Perception without Thinking can still log stimuli. Thinking without Action can still produce decisions (visible in episodes). Action with only DB Agent can still store and retrieve. Each phase delivers value.

---

## Related

- [[Perception module]] — channels, Stimulus schema, adapters, configuration (fully designed)
- [[Thinking module]] — goals, reasoning loop, refusal, error recovery, session model (fully designed)
- [[Memory module]] — self-building schema, four memory types, consolidation, forget mechanism (fully designed)
- [[Action module]] — sub-agent fleet, routing, governance, budget/circuit breakers (fully designed)
