# Memory Module

Self-building knowledge storage. The LLM has tool access (Supabase) and creates, structures, and queries its own memory on the go. The design lives here; the data lives in the DB.

---

## Core Principle

The LLM *is* the memory architect. It decides:
- What to remember
- How to structure it (tables, indexes, vectors)
- When to retrieve
- When to prune or consolidate

We don't pre-define schemas for knowledge. We define the **tools and patterns** the LLM uses to manage its own storage.

---

## Memory Types

### 1. Working Memory (Short-term)
- **What**: Active context for the current task or conversation.
- **Where**: LLM context window / prompt.
- **Lifetime**: Duration of a single session or task.
- **Managed by**: [[Thinking module]] — it holds relevant facts inline.

### 2. Episodic Memory (Interaction log)
- **What**: Record of past interactions, decisions, and outcomes.
- **Where**: `memory.episodes` table in Supabase.
- **Schema** (LLM-designed, starting point): `id`, `timestamp`, `source` ([[Perception module]] channel), `summary`, `decision`, `outcome`, `tags`
- **Use**: What happened last time? Did that approach work?

### 3. Semantic Memory (Facts & Knowledge)
- **What**: Facts about the user, preferences, learned patterns, domain knowledge.
- **Where**: `memory.facts` table + vector embeddings via `pgvector`.
- **Schema** (LLM-designed, starting point): `id`, `created_at`, `fact`, `source_episode_id`, `confidence`, `embedding`, `tags`
- **Use**: Who is the user? What do they prefer? What is known to be true?

### 4. Procedural Memory (Patterns & Skills)
- **What**: Learned procedures, successful action sequences, reusable strategies.
- **Where**: `memory.procedures` table.
- **Schema** (LLM-designed, starting point): `id`, `created_at`, `name`, `trigger_condition`, `steps` (JSON array), `success_rate`, `last_used`
- **Use**: When X happens, do Y. Known-good response patterns.

---

## Service API (Synchronous Reads)

Memory exposes a small HTTP or gRPC service for the four read operations that [[Thinking module]] needs during its RETRIEVE step. These are synchronous — the caller blocks until the response arrives.

| Endpoint | Operation |
|----------|-----------|
| `GET /memory/search` | Semantic search via pgvector cosine similarity |
| `GET /memory/episodes` | Recent episodes, filterable by time range and tags |
| `GET /memory/procedures` | Procedures matching a trigger condition |
| `GET /memory/goals` | Active + paused goals, priority-ordered |

Write operations (raw input log entries from Perception, action log entries from Action) arrive as fire-and-forget events on the [[Messaging Bus]] — Memory consumes them asynchronously. No caller waits on a write to complete.

---

## Tool Interface

The LLM accesses memory through Supabase tools:

| Tool | Purpose |
|------|---------|
| `execute_sql` | Create tables, indexes, insert/update/delete rows |
| `apply_migration` | Schema changes with versioning |
| `search_docs` | Supabase documentation (pgvector, RLS, etc.) |

### Key Operations

**Store** — The LLM writes SQL to insert into memory tables. It creates the table first if it doesn't exist (self-building).

**Retrieve** — The LLM queries by:
- Exact match (SQL `WHERE`)
- Semantic similarity (pgvector `<=>` cosine distance on embeddings)
- Time range (recent episodes)
- Tag filtering

**Consolidate** — Periodically the LLM should:
- Summarize old episodes into facts
- Merge duplicate or contradictory facts
- Prune low-confidence or stale information
- Update procedure success rates

---

## Self-Building Pattern

The first time the LLM needs memory, it bootstraps:

1. Check if memory schema exists (check migrations)
2. If not: apply migration to create base tables + pgvector extension
3. Use the fresh tables to store/retrieve

The LLM can extend the schema at any time:
- Add columns as new types of facts emerge
- Add new tables for new memory categories
- Add indexes when queries get slow

---

## Retrieval Flow

When [[Thinking module]] consults memory:

1. Extract search intent from current context
2. Generate embedding for semantic search
3. Query facts table with pgvector similarity
4. Also query recent episodes for temporal context
5. Also check procedures for matching triggers
6. Return combined results to Thinking module

---

## Design Decisions

| Decision | Rationale |
|----------|-----------|
| **Postgres + pgvector** (via Supabase) | One DB for relational + vector. No separate vector store to manage. |
| **LLM-designed schema** | The agent evolves its own structure. We don't guess upfront what it needs. |
| **Confidence scores** | Facts can be uncertain. Confidence lets the LLM weigh evidence. |
| **Source tracking** | Every fact links back to the episode it came from — provenance matters. |
| **No predefined ontology** | Tags are freeform. The LLM organises as it learns. |

---

## Open Questions

### When should consolidation run?

**Decision:** Start with a **schedule-based** approach. Run consolidation on a fixed cadence (e.g., every N minutes, hourly, or daily depending on volume expectations). This keeps the initial implementation simple and predictable — easy to reason about, easy to debug.

**Future consideration:** Once the system has real usage and traffic patterns emerge, consider adding **volume-based triggers** as a supplement or replacement:

- **Event-count threshold:** Trigger consolidation after X new raw inputs or episodic entries accumulate
- **Size threshold:** Trigger when the unconsolidated partition reaches a certain storage size
- **Hybrid:** Scheduled with a volume escape hatch — run every hour, but also run immediately if 10k+ events pile up

**Tradeoff:** Schedule-based is simple but can waste cycles during quiet periods or fall behind during spikes. Volume-based is efficient but harder to tune and reason about (what happens if the threshold is never reached? what about partial consolidation?). Start simple, gather data, then optimize.


### How to handle conflicting facts?

**Decision:** Two-pronged approach:

1. **Majority vote** — When multiple sources assert the same fact with different values (e.g., "your meeting is at 2pm" vs "your meeting is at 3pm"), count the sources. If one value has a clear majority, accept it as the canonical fact. This handles noise and single-source errors gracefully.

2. **Explicit conflict flag** — When there is no clear majority (tie, or close to a tie), flag the fact as **CONFLICTED** rather than silently picking a winner. The conflict flag surfaces the ambiguity to Thinking so it can:
   - Ask the human for clarification
   - Weight sources by trust/reliability
   - Use the most recent value as a tiebreaker
   - Defer the fact until more evidence arrives

**Rationale:** Majority vote prevents the system from flip-flopping on every single contradictory input. The explicit conflict flag ensures the system is transparent about uncertainty instead of papering over it. Together they give us the best of both: robustness to noise and honesty about ambiguity.


### Should there be a "forget" mechanism for GDPR-style data removal?

**Decision:** Yes. Even though Soulman is designed as a single-user system, a forget mechanism is essential for several reasons:

- **User control** — The human should always be able to say "forget everything about X" and have it actually happen
- **Mistakes happen** — Wrong information gets stored; the user needs a way to purge it
- **Future-proofing** — Even single-user systems can grow into multi-user ones; building forget in now is cheap compared to retrofitting later
- **Trust** — Knowing you can delete data makes you more willing to let the agent store it in the first place

**Implementation considerations:**
- The **raw input log is immutable** by design — forget requests don't delete from it, but they annotate it (e.g., `forgotten_at` timestamp) so downstream systems know to skip that record
- **Episodic memory and facts** should support hard deletion — these are derived/processed data, not the audit trail
- A forget request should cascade: forget a fact → forget all episodic memories that reference it → annotate the raw inputs that sourced it
- The API should support both "forget this specific record" and "forget everything about this topic/person/date range"

---

## Related Modules

- [[Perception module]] — feeds raw input log, the primary source of episodic memory
- [[Thinking module]] — consulted during RETRIEVE step; also the primary producer of episodic memory
- [[Action module]] — called when consolidation requires DB operations (DB Agent)
- [[Messaging Bus]] — other modules route memory operation requests via bus topics; Memory itself accesses Supabase directly through its DB tools
