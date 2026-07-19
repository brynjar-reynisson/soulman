# Thinking Module

The central reasoning engine of Soulman. An LLM agent driven by **goals** defined in its system prompt, receiving near real-time input from the [[Perception module]], consulting the [[Memory module]] for context, and delegating execution to the [[Action module]] when real-world consequences are involved. This is the "brain" — Perception is the senses, Memory is the hippocampus, Action is the motor cortex.

---

## Core Principle

**Goals drive attention. Attention drives reasoning. Reasoning drives action.**

The Thinking module is not a passive processor. It is an active, goal-seeking agent that:
- Wakes on input from Perception
- Filters signal from noise based on active goals
- Retrieves relevant context from Memory
- Reasons about what (if anything) to do
- Decides and delegates to Action
- Reflects on outcomes and updates goals

All other modules serve Thinking. Thinking serves the goals.

---

## Architecture

```
┌─────────────────────────────────────────┐
│              SYSTEM PROMPT               │
│  ┌───────────────────────────────────┐  │
│  │  Identity · Goals · Constraints   │  │
│  │  Decision rules · Personality     │  │
│  └───────────────────────────────────┘  │
│                    │                     │
│                    ▼                     │
│  ┌───────────────────────────────────┐  │
│  │         REASONING LOOP            │  │
│  │                                   │  │
│  │  ┌─────────┐    ┌─────────────┐   │  │
│  │  │Perceive │───▶│  Prioritize │   │  │
│  │  └─────────┘    └──────┬──────┘   │  │
│  │       ▲                │          │  │
│  │       │         ┌──────▼──────┐   │  │
│  │       │         │  Retrieve   │   │  │
│  │       │         │  (Memory)   │   │  │
│  │       │         └──────┬──────┘   │  │
│  │       │                │          │  │
│  │       │         ┌──────▼──────┐   │  │
│  │       │         │   Reason    │   │  │
│  │       │         └──────┬──────┘   │  │
│  │       │                │          │  │
│  │       │         ┌──────▼──────┐   │  │
│  │       │         │   Decide    │───┼──▶ Action
│  │       │         └──────┬──────┘   │  │
│  │       │                │          │  │
│  │       │         ┌──────▼──────┐   │  │
│  │       └─────────│  Reflect    │   │  │
│  │                 └─────────────┘   │  │
│  └───────────────────────────────────┘  │
└─────────────────────────────────────────┘
```

A single LLM session runs the reasoning loop. Thinking decides *what* and *why*; Action figures out *how* and *who*.

---

## The System Prompt

The system prompt is the constitution of the Thinking module. It defines everything the agent is and wants.

### Prompt Layers

| Layer | Content | Mutability |
|-------|---------|------------|
| **Identity** | Who the agent is. Name, role, relationship to user. | Static (user edits) |
| **Goals** | Active goals with priorities. Sourced from DB at session start. | Human-managed via natural language. Agent can propose changes; human approves. |
| **Constraints** | Hard rules. What the agent must never do. | Static + governance overrides |
| **Decision Rules** | How to resolve ambiguity. Defaults, heuristics, escalation thresholds. | Evolves via procedural memory |
| **Personality** | Tone, verbosity, initiative level, session mode. | User-configurable |
| **Context Window** | Ephemeral. Current task, recent perceptions, retrieved memories, active reasoning. | Resets per session |

---

## Goals System

Goals are the engine of the agent. Every perception is evaluated against active goals. Goals are a **living set** that the human and agent co-manage over time. The human is the ultimate authority on what matters and how much it matters.

### Where Goals Live

**`memory.goals` table** (DB, via [[Memory module]]) — the source of truth, durable across sessions.
**System Prompt** — the active copy, loaded from DB at session start. Only `active` and `paused` goals are loaded.

Table schema: `id` (uuid), `description` (text), `priority` (integer 1-10), `state` (active/paused/achieved/abandoned), `trigger_conditions` (jsonb), `success_criteria` (text), `parent_goal_id` (uuid|null), `created_at`, `updated_at`, `created_by` (human|agent), `last_modified_by` (human|agent).

### Goal Lifecycle

ACTIVE → PAUSED (human pauses, or agent pauses with notification when goal conflicts with higher-priority goal) → ACHIEVED (success criteria met; removed from prompt, kept in DB) → ABANDONED (human: "I don't care about that anymore"; removed from prompt, kept in DB). States are mutually exclusive. Only active and paused are loaded into the system prompt.

---

## Human Goal Management

The human manages goals through **any Perception channel** using natural language. No special syntax required.

### Creating, Editing, Pausing, Deleting

- **Create**: "Remind me to check server disk space every Friday." → Thinking extracts description, triggers, priority → DB Agent inserts → confirms.
- **Ambiguity**: Agent asks clarifying questions rather than guessing.
- **Edit**: "Change the check to Monday instead." → Field-level update.
- **Pause/Resume**: "Pause server monitoring while I'm on vacation." → State toggle; paused goals stay in prompt at priority 0.
- **Delete/Abandon**: State → `abandoned`, never hard-deleted.

### Changing Priority

Three intents: **relative reorder** ("X more important than Y" — swap/adjust preserving order), **absolute priority** ("make X priority 2" — set and shift others to make room, don't silently overwrite), **top/bottom** ("above everything else" / "lowest priority" — set to 10 or 1, shifting others). If shifting pushes above 10, cap at 10 with `updated_at` tiebreaker. Thinking always reports the full new ordering.

### Bulk Operations

"Show me all my goals" → queries active + paused ordered by priority DESC. "Re-order goals: 1) X, 2) Y, 3) Z" → single transaction update.

### Agent-Proposed Changes

Agent can **propose** but not **unilaterally apply**: new goals, priority changes require human approval. Agent **can** auto-mark `achieved` when success criteria are met (factual, not judgmental). Agent **can** auto-pause a goal conflicting with a higher-priority active goal, with notification. Agent **never** abandons a goal without human approval.

The distinction: factual state changes are auto-applied. Value judgments go through the human.

### Goal Creation via Observation

Agent notices patterns and proposes: "I've noticed you check server disk space manually every Friday. Want me to make that an automatic goal?" → Human confirms → Goal created.

### Goal Conflict Resolution

**Conflict recognition**: Two+ active goals suggest different responses to same input, or pursuing A undermines B, both priority ≥ 4, not hierarchically related.

**Resolution hierarchy**: (1) Priority score — gap ≥ 2, auto-resolve. (2) Precedent — same conflict in past episodes. (3) Human-in-the-loop — priority gap is 1, no precedent, or value judgment. (4) Default conservatism — human unreachable, defer to safer/reversible option.

**Never**: silently deprioritize, abandon, or hide conflicts. Every conflict logged to `memory.episodes` with tags `goal-conflict`.

**Auto-resolve when**: priority gap ≥ 2, known precedent exists, one goal paused, conflict is scheduling not values.

Principle: **numeric ordering resolves ordering problems. The human resolves value problems.**

---

## The Reasoning Loop

Every wake cycle follows this sequence:

### 1. PERCEIVE — Ingest input

Input arrives from [[Perception module]] as structured JSON with `source`, `timestamp`, `channel`, `content`, `priority_hint`, `idempotency_key`. Agent checks idempotency against `memory.episodes`. Duplicate → skip. New → prioritize.

### 2. PRIORITIZE — Filter against goals

Evaluates: goal trigger match, priority hint, interruption rules, novelty check. Output: priority score 0-10. Score 0 → logged as ignored, loop ends.

### 3. RETRIEVE — Gather context

Queries [[Memory module]]: recent episodes, semantic search (pgvector), matching procedures, active goals, past decisions. Iterative — up to 5 queries per wake cycle (retrieval budget).

### 4. REASON — Think before deciding

Core LLM reasoning. 8 questions: interpretation, relevance mapping, option generation, impact analysis, precedent, pattern match, risk classification, information gaps.

Reasoning patterns: Chain of thought, Pros/cons analysis, Pre-mortem, Root cause analysis, First principles, Precedent comparison. Agent selects (and can combine) based on input type and risk level.

### 5. DECIDE — Choose and commit

Decision types: NO_ACTION, ACKNOWLEDGE, INVOKE_ACTION, ASK_USER, UPDATE_GOAL, LEARN, REFUSE.

For INVOKE_ACTION: includes `intent`, `action_hint`, `parameters`, `risk_level`, `urgency`, `expected_outcome`, `fallback`. The handoff is always async:

1. Generate a `correlation_id`
2. Save reasoning state to Memory (stimulus summary, conclusions, action dispatched, `correlation_id`)
3. Publish action request to bus
4. Suspend — await result event matching `correlation_id`

REFLECT does not run immediately. It runs when the result arrives and the reasoning thread is resumed (see State Persistence).

### Refusal Protocol

Agent refuses when a request violates constraints (security, destructive action, impersonation, privacy, budget, technical impossibility, ethical boundary).

**4-part structure**: Acknowledge (show you understood) → Explain (which constraint, no jargon) → Offer alternatives (something the agent *can* do) → Invite (leave door open for re-direction).

**Anti-patterns**: never just say "I can't" with no explanation, pretend to comply, moralize, refuse without alternatives, escalate every refusal, or comply because "the human insisted."

**Push-back**: if human insists, restate constraint, offer override path if available (Guard Agent), hold firm on hard constraints (privacy, security, ethics). Refusal logged to `memory.episodes` with constraint tag.

### Error Recovery

The agent will be wrong. Recovery is a protocol.

**Discovery paths**: action result contradiction, human correction, later evidence, self-detection during reflection, circuit breaker.

**4-step recovery**: (1) Acknowledge + Apologize — own it without defensiveness. (2) Log — write to episodic memory, tag `error` + `recovery`, reduce confidence on wrong fact/procedure. (3) Adjust confidence — downgrade the specific thing that was wrong, don't over-correct. (4) Retry with different reasoning — switch reasoning pattern from the one that produced the error.

**Error taxonomy** (tags): `error:misinterpretation`, `error:stale-fact`, `error:wrong-procedure`, `error:reasoning`, `error:execution`, `error:overconfidence`.

**Systemic detection**: 3+ same error in a week → surface to human. Procedure success < 50% → flag, stop auto-using. 5+ corrections in a day → propose check-in. High-confidence facts contradicted 3+ times → broader recalibration.

Relationship to Reflection: error recovery is specialized reflection — not just "did this work?" but "why didn't it work, and how do I make sure it doesn't happen again?"

### 6. REFLECT — Learn from outcome

1. Log episode to `memory.episodes`.
2. Check for error — if outcome contradicts expectations, trigger Error Recovery first.
3. Extract facts if new information learned.
4. Update procedures (success/failure counters).
5. Goal check — achieved? New goal suggested?
6. Confidence calibration.

---

## Integration Points

### Input: [[Perception module]]
Event-driven, via the [[Messaging Bus]]. Perception publishes Stimulus objects to a topic; Thinking subscribes and sleeps between inputs. Contract: structured JSON payload with idempotency key. Thinking checks dedup, Perception sets priority hints but Thinking has final say.

### Context: [[Memory module]]
Thinking queries Memory via **synchronous HTTP/gRPC calls** during RETRIEVE — semantic search, recent episodes, procedure lookup, active goals. Up to 5 calls per cycle (retrieval budget). Thinking blocks until each response arrives; it cannot reason without the context. Write operations (episodes, facts, goal updates) go back via fire-and-forget bus events.

### Output: [[Action module]]
All action requests are published to the [[Messaging Bus]] — always async, regardless of expected action duration. Thinking never blocks waiting for an action to complete. See State Persistence below for how it resumes when the result arrives.

### Feedback loop
Perception → Thinking → Action → World → Result (bus event) → Thinking resumes → Memory → (next Perception closes the loop).

---

## State Persistence

When Thinking dispatches an action it suspends rather than blocks. Saved state allows it to resume coherently when the result arrives.

### What is saved (to `memory.thinking_state`)

| Field | Content |
|-------|---------|
| `correlation_id` | UUID matching the pending action result |
| `stimulus_summary` | The input that triggered this reasoning thread |
| `conclusions` | What Thinking determined before dispatching |
| `action_dispatched` | Type and parameters of the action sent |
| `saved_at` | Timestamp (for orphan detection) |

### Resumption

When an action result bus event arrives carrying a `correlation_id`:

1. Load saved state by `correlation_id`
2. Reconstruct reasoning context
3. Run REFLECT with result in hand
4. Clear saved state

### Priority interrupts during wait

While suspended, incoming stimuli are handled as follows:

| Stimulus | Behaviour |
|----------|-----------|
| Normal | Queued; processed after current thread resumes and completes |
| High-priority (override, critical alert, budget exhaustion) | Handled immediately in a fresh reasoning cycle; saved state untouched, resumed afterward |

One active reasoning thread at a time.

---

## Session Model

### Session Lifecycle
Session Start (load prompt + goals + recent episodes, reset budget) → Active Loop (process, reason, decide, reflect) → Session End (summarize, consolidate, persist).

### Session Modes
Configurable, user-switchable: **Interaction** (one exchange), **Daily** (calendar day, resets at midnight or after N hours idle), **Idle-timeout** (alive until N minutes of inactivity, default 30). Default: interaction mode. Parameters: `session_mode` and `session_timeout_minutes`.

### Session-Start Goal Loading
Query `memory.goals WHERE state IN ('active', 'paused') ORDER BY priority DESC, updated_at DESC`. Render as priority-ordered list in prompt. Mid-session changes update both DB and in-prompt copy.

### Sleep Mode (Consolidation Windows)
Triggered by schedule, idle-timeout, or manual command. Tasks: summarize old episodes (weekly), merge duplicate facts (weekly), update procedure stats (daily), prune stale data (monthly), generate missing embeddings (daily), goal health report (daily), proactive observations (daily). Separate budget: 20 calls / 100K tokens / $0.50 per session, max 2 sessions/day. Never wakes human. Logs to `memory.episodes` with `source: system`, `channel: consolidation`.

### Idle Behaviour
When not in sleep mode: run scheduled goal checks, or wait. Initiative levels: `passive`, `moderate`, `proactive`.

---

## Multi-Agent Thinking?

Designed as **single agent** — one session, one prompt, one loop. Rationale: coherence, simplicity, accountability, decomposition happens in Action. Future extensions possible (adversarial review, specialized reasoning, parallel hypothesis) but as consultative sub-agents invoked by Thinking, not peers.

---

## System Prompt Template

```
You are Soulman, a personal AI assistant.

IDENTITY
- You are helpful, thoughtful, and appropriately proactive.
- You reason carefully before acting.
- You know what you don't know and ask when uncertain.
- When you must refuse a request, you do so gracefully: acknowledge, explain, offer alternatives, invite re-direction.
- When you are wrong, you recover gracefully: apologize, log, adjust confidence, retry with different reasoning.

GOALS (active, priority-ordered)
{goals_list}

CONSTRAINTS
- Never expose credentials, tokens, or private keys.
- Never take destructive action without explicit user confirmation.
- Never send messages as the user without clear intent to do so.
- Respect budget limits. Prefer fewer, higher-quality actions over many small ones.
- If a decision is high-risk (red or orange), pause and ask the user.

DECISION RULES
- If input is spam or noise, log and ignore.
- If a known procedure exists and confidence is high, follow it.
- If multiple goals conflict, prioritize by goal priority score.
- If uncertain, default to ASK_USER over guessing.
- Prefer idempotent actions when possible.
- If the human gives a goal-management command, execute it immediately and confirm.
- If the human's request violates a constraint, refuse gracefully using the 4-part refusal structure.
- When you discover you were wrong, follow the 4-step error recovery pattern. Never hide errors.

PERSONALITY
- Tone: {tone_preference}
- Initiative: {initiative_level}
- Verbosity: {verbosity_level}
- Session mode: {session_mode} (timeout: {session_timeout_minutes}m)

CONTEXT
- Current session: {session_id}
- Recent episodes: {recent_episodes_summary}
- Active budget: {budget_remaining}
```

---

## Design Decisions

| Decision | Rationale |
|----------|-----------|
| **Single agent, not multi-agent** | Coherence > parallelism for reasoning core. Decomposition in Action. |
| **Goal-driven, not rule-driven** | Goals provide direction without exhaustively specifying behaviour. |
| **Event-driven, not polling** | Perception pushes. Thinking sleeps. Saves tokens. |
| **System prompt as constitution** | Source of truth for identity, goals, constraints. User-editable. |
| **DB source of truth for goals; prompt is active copy** | Goals survive restarts. DB durable, prompt is working set. |
| **Human is ultimate authority on goals** | Agent proposes, human decides value judgments. |
| **Retrieval budget** | Prevents infinite memory-chaining. 5 queries per cycle. |
| **Reasoning patterns explicitly named** | Makes thinking legible. Easier to debug and trust. |
| **Reflection is mandatory** | Every cycle ends with reflection. No unexamined actions. |
| **Goal changes take effect mid-session** | DB update + prompt update same step. No restart needed. |
| **Graceful refusal as a design requirement** | 4-part structure ensures human feels served even when answer is no. |
| **Graceful error recovery as a design requirement** | Recovery is a protocol: apologize, log, adjust, retry. Errors are learning opportunities. |

---

## Open Questions

| Question | Status | Answer |
|----------|--------|--------|
| Should the agent ever modify its own system prompt? | ✅ Resolved | No. Agent can propose edits; human applies them. |
| How long should a session be? | ✅ Resolved | Configurable: interaction, daily, or idle-timeout modes. |
| Should there be a sleep mode for low-priority backlog? | ✅ Resolved | Yes — scheduled consolidation windows with separate budget. |
| How to handle goal conflicts? | ✅ Resolved | Priority score first; human resolves value trade-offs. |
| Can the agent refuse a user request? | ✅ Resolved | Yes, with graceful 4-part refusal protocol. |
| How does the agent handle being wrong? | ✅ Resolved | Apologize, log, adjust confidence, retry with different reasoning. |
| Should goals have an expiration / TTL? | Open | Some goals are temporary. A `valid_until` field could auto-pause or auto-abandon. |
| Can the human batch-import goals? | ✅ Resolved | Start simple: one goal at a time via natural language. If the human lists multiple goals in one message ("Here are three things I want you to track..."), the agent processes them sequentially — confirm each before moving to the next, or summarize all at the end. No bulk CSV import or structured format needed yet. Batch import as a first-class feature (e.g., importing from a note or file) can be added later if the need arises. |

---

## Wake Triggers Reference

| Trigger Source | Example | Priority Hint |
|----------------|---------|---------------|
| User message | Telegram DM, Obsidian note edit, web app chat | normal / urgent |
| Scheduled | Cron: "every morning at 08:00", "check server every hour" | normal |
| Webhook | GitHub push, Stripe payment, server alert | normal / urgent |
| System | Budget exhausted, circuit breaker tripped | urgent / critical |
| Action result | "Email sent", "DB migration applied" | low / normal |
| Human override | "PAUSE" note, "approve action" message | critical |

---

## Next Steps

- [ ] Write the full system prompt as a working Obsidian note
- [ ] Define the initial goal set with the user
- [ ] Create the `memory.goals` table via DB Agent
- [ ] Implement the idempotency check against `memory.episodes`
- [ ] Wire Perception → Thinking → Memory → Action integration
- [ ] Define the reflection-to-consolidation pipeline
- [ ] Test the full loop end-to-end with a simple goal
- [ ] Test goal creation, reorder, pause, and abandon via natural language
- [ ] Test refusal protocol with constraint-violating requests
- [ ] Test error recovery with deliberate mistakes

---

## Related Modules

- [[Perception module]] — feeds Thinking with normalized stimuli
- [[Memory module]] — consulted during RETRIEVE step; also the primary consumer of REFLECT output
- [[Action module]] — receives INVOKE_ACTION decisions and returns results
