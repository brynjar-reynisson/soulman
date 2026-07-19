# Action Module

Executes decisions made by the [[Thinking module]]. Rather than a monolithic action handler, actions are carried out by a collection of **specialized sub-agents**, each with access to a specific set of MCP tools. A **Routing Agent** sits at the entry point and hands off tasks to the right agent.

---

## Core Principle

**The right tool for the right job — and the right agent for the right domain.**

Each sub-agent is an LLM session scoped to a specific domain:
- It only sees the MCP tools relevant to its domain
- It understands the idioms and constraints of that domain
- It can be added, removed, or upgraded independently
- New agents come online as new MCP servers are connected

The Routing Agent doesn't do the work — it knows *who* can do the work and passes control.

---

## Architecture

```
Thinking Module
      │
      ▼
┌─────────────────┐
│  Routing Agent   │  ← Single entry point. Decides who handles what.
└────────┬────────┘
         │
    ┌────┼────┬──────────────┐
    ▼    ▼    ▼              ▼
┌────┐┌────┐┌────┐     ┌──────────┐
│ DB ││ FS ││Web │ ... │ (future) │
│Agt ││Agt ││Agt │     │          │
└────┘└────┘└────┘     └──────────┘
```

- **Routing Agent** receives the action intent + context from Thinking.
- It classifies the task and delegates to the appropriate sub-agent.
- The sub-agent executes using its MCP tools and returns the result.
- Routing Agent passes the result back to Thinking → stored in [[Memory module]].

---

## The Routing Agent

### Responsibilities
1. **Receive** action intent from Thinking via the [[Messaging Bus]] — subscribes to `soulman.thinking.request`; each request carries a `correlation_id` used to route the result back
2. **Classify** the task into one or more domains
3. **Select** the best-fit sub-agent based on capability and past success
4. **Delegate** — hand off context + parameters to the sub-agent
5. **Aggregate** — if multiple agents are involved, combine their results
6. **Return** the outcome to Thinking for memory storage

### Classification Strategy

| Signal | How it's used |
|--------|---------------|
| **Explicit action type** | Thinking module may specify "send_email" or "query_db" — direct match |
| **Intent matching** | Natural language task description matched against agent capability descriptions |
| **Historical success** | Check [[Memory module]] procedural memory for which agent handled similar tasks successfully |
| **Tool availability** | Only agents with the required MCP tools are candidates |

### Fallback & Escalation
- If no agent matches: Routing Agent asks Thinking to clarify or rephrase
- If an agent fails: try next best candidate, or decompose the task differently
- If all fail: log to episodic memory with failure reason; notify Thinking for human escalation

---

## Sub-Agent Design

Each sub-agent is defined by three things:

### 1. Identity
- **Name** — unique identifier (e.g. `db-agent`, `fs-agent`, `web-agent`)
- **Description** — natural language capability statement used for routing match
- **Domain** — the category of actions it handles

### 2. MCP Tool Set
- A curated list of MCP server names and tools the agent has access to
- The agent session is initialized with only these tools — no clutter, no confusion
- Tool sets can overlap between agents when it makes sense

### 3. Action Catalog
- Known action types this agent can perform
- Each action maps to a procedure (stored in procedural memory)
- New action types can be learned and added over time

---

## Proposed Agent Roster

### DB Agent (`db-agent`)
- **Domain**: Data persistence, queries, schema management
- **MCP Tools**: `supabase` (execute_sql, apply_migration, list_tables, list_migrations)
- **Actions**: Run queries, create/alter tables, store/retrieve facts, run migrations, manage embeddings
- **Used by**: [[Memory module]] heavily. Also [[Perception module]] (storing raw input logs) and Thinking (analytical queries)

### Filesystem Agent (`fs-agent`)
- **Domain**: File operations, note management, project scaffolding
- **MCP Tools**: `obsidian` (create-note, read-note, edit-note, search-vault, move-note, delete-note), plus local filesystem access
- **Actions**: Create/update notes, organize vault, search knowledge base, manage project files, generate reports
- **Used by**: Thinking (writing structured output), Memory (knowledge base notes), [[Perception module]] (logging raw input)

### Web Agent (`web-agent`)
- **Domain**: External APIs, webhooks, internet search, HTTP requests
- **MCP Tools**: `webSearch`, `webFetch`, plus HTTP client capabilities
- **Actions**: Search the web, fetch documentation, call external APIs, send webhooks, monitor endpoints
- **Used by**: Thinking (research), [[Perception module]] (incoming webhooks), Action (outgoing calls)

### Communication Agent (`comm-agent`)
- **Domain**: Messaging, notifications, email
- **MCP Tools**: Email (SMTP/IMAP), messaging platform APIs (Slack, Telegram, etc.)
- **Actions**: Send/receive email, post messages, trigger notifications, summarize threads
- **Used by**: [[Perception module]] (inbound messages), Action (outbound replies/alerts)

### Code Agent (`code-agent`)
- **Domain**: Software development, code generation, refactoring, testing
- **MCP Tools**: `agent-lsp` (find_references, apply_edit, rename_symbol, run_tests, run_build, etc.)
- **Actions**: Write code, refactor, find usages, run tests, debug, review
- **Used by**: Thinking (when action involves code changes)

### Guard Agent (`guard-agent`)
- **Domain**: Safety gate for high-risk actions
- **MCP Tools**: None of its own — it intercepts handoff messages before they reach the target agent
- **Actions**: Evaluate risk level of a proposed action, pause for human confirmation, enforce timeouts
- **Used by**: Routing Agent (every delegation passes through the guard when risk threshold is met)

---

## Handoff Protocol

When Routing Agent delegates to a sub-agent:

```
Routing Agent → Sub-Agent

{
  "task_id": "uuid",
  "action_type": "query_db | create_note | send_email | ...",
  "intent": "natural language description of what to accomplish",
  "parameters": { ... domain-specific params ... },
  "context": {
    "thinking_summary": "why this action was decided",
    "relevant_memory": [ ... facts/episodes from Memory module ... ],
    "previous_attempts": [ ... if retrying ... ]
  },
  "expected_output": {
    "format": "json | text | markdown | void",
    "schema_hint": "optional structure hint"
  }
}

Sub-Agent → Routing Agent

{
  "task_id": "uuid",
  "status": "success | partial | failed",
  "result": { ... domain-specific output ... },
  "artifacts": [ ... URLs, file paths, record IDs created ... ],
  "memory_updates": [
    { "type": "episode", "summary": "..." },
    { "type": "fact", "fact": "...", "confidence": 0.9 }
  ],
  "error": "if failed: what went wrong"
}
```

---

## Agent Registration & Discovery

Agents are defined in a **registry** (stored in Memory as facts + procedures):

| Field | Purpose |
|-------|---------|
| `agent_name` | Unique identifier |
| `description` | Natural language capability summary (used for routing match) |
| `mcp_servers` | List of MCP server names this agent uses |
| `action_types` | List of action type strings this agent handles |
| `status` | `active`, `degraded`, `offline` |
| `success_rate` | Rolling success rate from procedural memory |

New agents can be registered at any time — just add the fact. The Routing Agent re-reads the registry on each classification (or caches with a TTL).

---

## Multi-Agent Orchestration

Some tasks require multiple agents in sequence or parallel:

### Sequential (pipeline)
```
Thinking: "Research X and save findings to the vault"
  → Web Agent: search + fetch results
  → FS Agent: create note with findings
```

### Parallel (scatter-gather)
```
Thinking: "Check email and DB for updates"
  → Comm Agent: fetch unread emails
  → DB Agent: query recent episodes
  → Routing Agent merges both results
```

### Conditional (branching)
```
  → Try DB Agent for fact lookup
  → If not found: Web Agent search
  → Store result via DB Agent
```

The Routing Agent handles orchestration. Complex, recurring patterns get stored as **Procedural Memory** entries in the [[Memory module]].

---

## Design Decisions

| Decision | Rationale |
|----------|-----------|
| **Separate agents per MCP domain** | Keeps each agent's tool surface small and focused. Reduces confusion and token waste. |
| **Routing Agent as single entry point** | Thinking doesn't need to know about all agents. One interface, smart dispatch. |
| **Registry from memory** | Agents are data, not hardcoded. Add/remove without code changes. |
| **Structured handoff protocol** | Consistent interface between agents. Easy to log, debug, and improve. |
| **Agents can update memory** | Sub-agents can push facts and episodes directly — no bottleneck through Thinking. |
| **MCP tools as capability boundary** | An agent's power is exactly its MCP tools. No magic. Easy to audit and constrain. |

---

## Resolved Questions

| Question | Decision | Rationale |
|----------|----------|-----------|
| **Separate LLM sessions or prompt-partitioned?** | Separate LLM sessions. | Clean isolation: each agent gets its own context window, tool set, and failure domain. No risk of tool leakage or prompt contamination. A crash in one doesn't touch the others. |
| **Agent authentication / elevated privileges?** | Deferred. | Some agents likely will need elevated privileges (destructive DB ops, sending email as the user). The mechanism is left for when it becomes a concrete problem rather than a hypothetical one. |
| **Human-in-the-loop for high-risk actions?** | Yes — **Guard Agent**. | A dedicated Guard Agent that intercepts high-risk actions before execution (data deletion, mass email, destructive migrations, production pushes). Presents the action to the user for approval with a timeout for auto-rejection. |
| **Direct agent-to-agent or all through Routing?** | All through Routing, for now. | Simpler to reason about, easier to log, keeps the topology a star rather than a mesh. Premature mesh complexity costs more than routing overhead at this stage. |
| **Versioning agent capabilities when tools change?** | Deferred. | With ~5 agents in MVP, manual tracking suffices. Future: tool-version fields in the agent registry with automated compatibility checks. |
| **Routing Agent: learn over time or rule-based?** | Rule-based for now. | Predictable, debuggable, no cold-start problem. Procedural memory already captures success/failure patterns that the rule-based router can consult without needing ML. |

---

## Governance

Governance constrains **how much, how fast, and at what cost** the Action Module can operate. The Routing Agent delegates tasks — Governance ensures that delegation doesn't exceed safe boundaries. It sits between the Routing Agent and sub-agents as a pre-flight check, alongside the Guard Agent.

### Why Governance Exists

Without it, nothing prevents:

| Risk | Example |
|------|---------|
| **Cost explosion** | Web Agent makes 500 searches because a task is ambiguous |
| **API rate-limit trip** | Comm Agent fires 50 emails in 10 seconds, gets blacklisted |
| **Resource exhaustion** | FS Agent creates 10,000 notes in a loop |
| **Runaway retries** | DB Agent retries a failing query 1,000 times |
| **Gradual drift** | 100 "small" code edits accumulate without human review |

The [[#Guard Agent]] already covers *risk classification* (should a human approve this destructive action?). Governance covers *volume, velocity, and cost*.

---

### Budget Dimensions

Four dimensions are tracked per action:

| Dimension | Unit | Example Limit |
|-----------|------|---------------|
| **Tool Calls** | count | 50 calls / session |
| **Tokens** | tokens | 100K tokens / task |
| **Wall Time** | seconds | 300s timeout / task |
| **Cost** | $ estimated | $2.00 / session |

Each delegation from the Routing Agent to a sub-agent is evaluated against all four dimensions.

---

### Quota Scopes

Budgets apply at multiple levels — each with its own lifespan:

| Scope | Lifespan | Example |
|-------|----------|---------|
| **Per-task** | Resets on each delegation | "This web search gets max 5 tool calls" |
| **Per-session** | Resets on each user interaction / conversation start | "This conversation: max 30 tool calls" |
| **Per-day** | Cross-session, persisted in DB | "Max 200 emails sent per calendar day" |
| **Per-agent** | Override for specific agents | "Code Agent: max 15 edits per session" |

Scopes are checked in order: per-task first (narrowest), per-day last (broadest). Any exhausted scope blocks the delegation.

---

### Enforcement Mechanisms

```
Routing Agent
      │
      ▼
┌──────────────────┐
│  Budget Tracker   │  ← Shared state: counts, balances, rates
│  (persisted in   │
│   DB via DB Agent)│
└────────┬─────────┘
         │
    ┌────▼──────────────────────────┐
    │  Pre-flight check             │
    │  "Can this delegation proceed │
    │   within remaining budgets?"  │
    └────┬──────────────────────────┘
         │
    ┌────▼─────────┐    ┌───────────────────┐
    │  PROCEED      │    │  BLOCK            │
    │  (decrement   │    │  → Return error   │
    │   counters)   │    │  → Escalate to    │
    │               │    │    Thinking       │
    │               │    │  → Optionally ask │
    │               │    │    human          │
    └───────────────┘    └───────────────────┘
```

Three enforcement tiers:

| Tier | Trigger | Behaviour |
|------|---------|-----------|
| **Normal** | Budget > 30% remaining | Delegations proceed without interruption |
| **Soft Throttle** | Budget ≤ 30% remaining | Linear backoff: delay increases as budget drains. Agent sees a warning: "3 calls remaining this session — proceed carefully." |
| **Hard Cap** | Budget exhausted (0 remaining) | Delegation refused. Error returned to Routing Agent, which must either re-plan with fewer calls, escalate to Thinking, or prompt the user. |

---

### Circuit Breakers

Separate from budget exhaustion — circuit breakers detect **failure patterns** and halt operations regardless of remaining budget:

| Breaker | Triggers When | Action |
|---------|--------------|--------|
| **Consecutive errors** | 3 failures in a row from the same agent | Suspend that agent for the remainder of the session |
| **Error rate spike** | >50% failure rate over last 10 calls (any agent) | Pause all actions, notify Thinking, surface to human |
| **Loop detection** | Same `action_type` + same `parameters` repeated >5 times | Block the delegation, flag for Thinking to re-plan |
| **Cost spike** | Single delegation estimated to cost >$0.50 | Pre-flight approval by Guard Agent required |

Circuit breakers reset when the session resets.

---

### Cost Estimation

Estimated cost for each delegation:

```
estimated_cost = (estimated_tokens × model_price_per_token) + known_api_costs
```

| Component | How it's calculated |
|-----------|-------------------|
| **Model tokens** | Input tokens (handoff context) + estimated output tokens (based on `expected_output` size hint) |
| **Model pricing** | From model config (e.g. GPT-4o = $2.50/1M input, $10/1M output) |
| **Known API costs** | Hardcoded cost table: email ($0.0001/email via SendGrid), web search (free — included in model), Supabase (free within quota), etc. |
| **Unknown costs** | Default to $0.00 with a `cost_unknown: true` flag — flagged for human review if frequent |

Cost is an estimate, not a charge. It serves as a governor, not an accounting system.

---

### Integration with Guard Agent

Governance and Guard Agent are complementary, not redundant:

| Concern | Handled By |
|---------|-----------|
| *Should* this action happen at all? (destructive?) | Guard Agent |
| *How much* of this action can happen? (budget?) | Governance / Budget Tracker |
| *Is this action failing too often?* (errors?) | Circuit Breakers |

The pre-flight sequence:

```
1. Routing Agent prepares delegation
2. Budget Tracker: check all scopes → PROCEED or BLOCK
3. Guard Agent: evaluate risk class → PROCEED or ASK_HUMAN
4. Delegation to sub-agent (only if both passed)
```

If either step blocks, the delegation never reaches the sub-agent. The Routing Agent receives a structured rejection with the reason and must re-plan.

---

### Risk Classification

Every action has a risk level. The Guard Agent uses this to decide what needs human sign-off.

| Risk Level | Criteria | Requires |
|------------|----------|----------|
| **🟢 Low** | Read-only, idempotent, no external side effects | Auto-approved |
| **🟡 Medium** | Writes data, sends messages, modifies files | Logged; auto-approved within budget |
| **🟠 High** | Destructive ops, mass actions, production changes | Guard Agent review; user confirmation if > threshold |
| **🔴 Critical** | Data deletion, credential changes, financial actions | Always requires explicit human approval |

#### Risk Tags (per action)
- `read-only` / `write` / `destructive`
- `idempotent` / `non-idempotent`
- `internal` / `external` (does it touch third-party services?)
- `reversible` / `irreversible`
- `cost_estimate` in currency

---

### Budget State Persistence

Budgets are stored in the database via DB Agent:

- **Session counters** (tool calls, tokens, cost this session): written at session start, updated on each delegation, cleared on session end.
- **Daily counters** (emails sent, web searches, DB migrations): persisted across sessions, reset at midnight UTC.
- **Agent-level overrides**: stored as facts in Memory (`fact: "code-agent max_edits_per_session = 15"`) and read at session init.

If the DB Agent is unavailable at startup, Governance falls back to in-memory defaults with a `persistence: degraded` flag.

---

### Audit Trail

Every action the system takes (or blocks) is recorded. No invisible execution.

| Field | Purpose |
|-------|---------|
| `action_id` | UUID for the action |
| `timestamp` | When it happened |
| `agent` | Which sub-agent executed |
| `action_type` | What kind of action |
| `risk_level` | 🟢🟡🟠🔴 at time of execution |
| `parameters_hash` | Deterministic hash of input (for loop detection) |
| `result` | success / partial / failed / blocked |
| `cost` | Estimated cost incurred |
| `duration_ms` | How long it took |
| `approved_by` | `auto` / `guard` / `user:{id}` |
| `circuit_state` | Was a breaker active when this ran? |

Stored in `memory.action_log` table via DB Agent. Queryable, summarizable, feedable back into [[Thinking module]] for self-reflection.

---

### Budget Exhaustion Recovery

When a scope is exhausted:

| Scope | Recovery |
|-------|----------|
| **Per-task** | Resets automatically when the next task is delegated |
| **Per-session** | Resets automatically at the start of the next user session |
| **Per-day** | Resets at midnight UTC |
| **Per-agent** | Resets with the scope it's tied to (session or daily) |

There is no manual "refill" mechanism. The user can override budget defaults in configuration, which takes effect at the next session start.

---

### Human Override Channels

The user is the ultimate circuit breaker.

| Channel | How |
|---------|-----|
| **Obsidian note** | Write `PAUSE` or `STOP` to a watched note → [[Perception module]] picks it up |
| **Message** | Send "pause soulman" via any connected messaging channel |
| **CLI / Dashboard** | Direct command to the runtime |
| **Budget re-up** | "approve this action" or "increase daily budget to $X" |

All override commands bypass the normal [[Perception module]] → Think → Act loop and go straight to the Guard Agent (or Routing Agent if Guard is the one misbehaving).

---

### Default Budget Configuration

```
per_task:
  max_tool_calls: 10
  max_tokens: 50000
  max_wall_time_seconds: 120
  max_cost_usd: 0.50

per_session:
  max_tool_calls: 50
  max_tokens: 250000
  max_cost_usd: 2.00

per_day:
  max_emails_sent: 50
  max_db_migrations: 5
  max_web_searches: 100
  max_code_edits: 30

per_agent_overrides:
  code-agent:
    per_session:
      max_tool_calls: 20       # code edits are expensive to review
  web-agent:
    per_session:
      max_tool_calls: 15       # web searches have latency + cost
  comm-agent:
    per_day:
      max_emails_sent: 20      # lower than global daily cap

circuit_breakers:
  consecutive_errors: 3
  error_rate_threshold: 0.5
  error_rate_window: 10
  loop_detection_repeats: 5
  cost_spike_threshold_usd: 0.50
```

---

### Design Principle

> **Default closed, progressively open.**
>
> New agents start with tight limits and high risk classification. As they prove reliable and cost-effective (tracked in procedural memory), their budgets and auto-approval thresholds can expand. Trust is earned in production, not granted at design time.

---

### Design Decisions

| Decision | Rationale |
|----------|-----------|
| **Persisted in DB Agent** | Survives crashes and restarts. Daily counters must span sessions. |
| **Hardcoded defaults → user overrides** | Sensible defaults prevent runaway costs out of the box. User can tune as trust builds. No ML needed. |
| **Cost estimation via tokens + known prices** | Good enough for governance. Exact accounting is a separate concern. |
| **Routing Agent's own calls count** | No free pass — the router's LLM calls consume budget like any other agent. |
| **Auto-reset on next session** | No manual refill friction. A fresh session is a fresh budget. Daily caps are the backstop against session-hopping abuse. |
| **Soft throttle before hard cap** | Agents get a warning before they run out. Encourages efficient use without sudden failures. |

---

### Resolved Questions

| Question | Decision | Rationale |
|----------|----------|-----------|
| **Where does budget state live?** | DB Agent (persisted, survives restarts) | Session counters could be in-memory, but daily counters must persist across sessions. Unified in DB for simplicity. |
| **Who configures budgets?** | Hardcoded defaults → user overrides | Safe out of the box. User-facing config file for tuning. No self-modifying budgets (prevents agents from raising their own limits). |
| **How is cost estimated?** | Token count × model pricing + known API costs | Sufficiently accurate for governance. Not an accounting system — just a governor. |
| **Does budget tracking apply to the Routing Agent?** | Yes — its own LLM calls count against session budget | No special exemption. The Routing Agent's classification and orchestration calls consume tokens and cost money. |
| **Recovery from budget exhaustion?** | Auto-reset next session (task/session scopes), midnight UTC (daily scopes) | Friction-free recovery. Daily caps prevent abuse across rapid session cycling. |
