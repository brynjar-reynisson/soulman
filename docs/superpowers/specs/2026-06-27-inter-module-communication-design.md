# Inter-Module Communication Architecture

**Date:** 2026-06-27
**Status:** Approved

## Summary

Project Soulman uses a **two-tier transport model** for inter-module communication: the message bus for async patterns, and HTTP/gRPC for synchronous request-response patterns. The split follows the natural shape of each interaction — not a one-size-fits-all choice.

## Decision Matrix

| Boundary | Pattern | Transport | Notes |
|----------|---------|-----------|-------|
| Perception → Thinking | Async | Message bus | Durable delivery; Thinking subscribes |
| Thinking → Memory (reads) | Sync | HTTP/gRPC | Caller blocks; needed to continue reasoning |
| Thinking → Action (all) | Async | Message bus | Always async, regardless of action duration |
| Action result → Thinking | Async | Message bus | Treated as a stimulus; resumes saved state |
| Perception → Memory (writes) | Async | Message bus | Fire-and-forget; no result needed |
| Action → Memory (writes) | Async | Message bus | Fire-and-forget; no result needed |

## Memory as a Service (Sync Reads)

Memory exposes a small HTTP or gRPC service API covering only the four read operations Thinking needs during RETRIEVE:

1. Semantic search (pgvector cosine similarity)
2. Recent episodes (time-range query)
3. Procedure lookup (trigger condition match)
4. Goal query (active + paused, priority-ordered)

Thinking calls these directly and awaits. The hot path runs up to 5 queries per reasoning cycle; keeping it synchronous minimises latency and keeps debugging simple.

Write operations (raw input log from Perception, action log from Action) arrive as fire-and-forget bus events — no caller waits on them.

## Thinking's State Persistence Model

Thinking never blocks on action execution. All action dispatch is async regardless of expected duration. This keeps the reasoning loop available for high-priority interrupts and makes the execution model uniform — no special cases for "fast" vs "slow" actions.

### Dispatch sequence (INVOKE_ACTION)

1. Generate a `correlation_id` (UUID) for this action request
2. Save reasoning state to Memory — originating stimulus summary, conclusions reached, action dispatched, `correlation_id`
3. Publish action request to bus with `correlation_id`
4. Suspend — subscribed for result on a topic filtered by `correlation_id`

### Resumption sequence (result arrives as bus event)

5. Load saved reasoning state from Memory by `correlation_id`
6. Resume reasoning loop at REFLECT step with result in hand
7. Clear saved state from Memory

### What gets saved

The saved state must contain enough for Thinking to resume coherently:
- Summary of the originating stimulus
- Reasoning conclusions that led to the action
- Action type and parameters dispatched
- `correlation_id` to match the incoming result

## Priority Handling During Wait

While Thinking is suspended, new stimuli continue arriving on the bus.

| Stimulus type | Behaviour |
|--------------|-----------|
| Normal stimulus | Queued; processed after current reasoning thread completes |
| High-priority (override, critical alert, budget exhaustion) | Interrupts immediately in a fresh reasoning cycle; waiting state preserved and resumed afterward |

One active reasoning thread at a time. Priority interrupts ensure the human can always pull the brake.

## Rationale

**Why async-always for Action?**
Blocking Thinking while an action executes ties up the reasoning loop for the duration of that action — potentially seconds or minutes. Async dispatch with state persistence keeps Thinking available for high-priority interrupts and makes the model uniform. No special-casing "fast" vs "slow."

**Why sync for Memory reads?**
RETRIEVE is a blocking dependency — Thinking cannot reason or decide until it has the context it requested. Framing this as async and waiting for a bus event adds latency and correlation complexity with no decoupling benefit, since there is only ever one consumer (Thinking) per query.

**Why async for Memory writes?**
The callers (Perception, Action) don't need confirmation before continuing. The bus provides durability — writes survive a Memory service restart. A slow write path cannot stall Perception from processing the next stimulus.
