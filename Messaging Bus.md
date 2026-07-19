# Messaging Bus

> **NATS** is the internal message bus for Project Soulman. It decouples modules from each other, enables durable input queues, and exposes output channels to non-Soulman consumers on the home network — without exposing anything to the internet.

---

## Two-Tier Transport Model

Not all inter-module communication goes through the bus. Soulman uses a **two-tier model**:

| Pattern | Transport | Used for |
|---------|-----------|---------|
| Async (fire-and-forget, event-driven) | Message bus | Stimulus delivery, action dispatch, action results, log writes |
| Sync (request-response) | HTTP/gRPC | Memory read queries — Thinking blocks until it gets context back |

The bus handles everything where the caller does not need to wait. Memory reads during Thinking's RETRIEVE step are the exception: they are synchronous direct calls to Memory's service API, because Thinking cannot continue reasoning until it has the answer.

See [[Messaging Bus]] subject hierarchy below for which topics are used for what. See the spec at `docs/superpowers/specs/2026-06-27-inter-module-communication-design.md` for full rationale.

---

## Role in the Architecture

The bus is the backbone for all async inter-module communication. Modules that use the bus publish to topics and subscribe — no direct calls, no shared memory, no polling.

```
┌─────────────────────────────────────────────────────────────────┐
│                        NATS MESSAGE BUS                          │
│                                                                  │
│  External          input.*          soulman.*        output.*    │
│  Processes  ──▶  ──────────  ──▶  ──────────  ──▶  ─────────── │
│  (this machine)  [JetStream]       [internal]       [JetStream]  │
│                                                         │        │
│                                              Home-network        │
│                                              consumers           │
└─────────────────────────────────────────────────────────────────┘
```

---

## Subject Hierarchy

| Prefix | Direction | Description |
|--------|-----------|-------------|
| `input.<source>` | External → Soulman | Stimuli arriving from non-Soulman processes on this machine (e.g. `input.discord`, `input.email`, `input.cli`) |
| `soulman.stimulus.raw` | Perception → Thinking | Normalized Stimulus events crossing the Perception/Thinking boundary |
| `soulman.thinking.request` | Thinking → Action | Action requests produced by Thinking (always async; carries `correlation_id`) |
| `soulman.action.result.<correlation_id>` | Action → Thinking | Action results returned to Thinking; filtered by `correlation_id` to resume the right reasoning thread |
| `soulman.action.<agent>` | Action routing | Routed to specific sub-agents (e.g. `soulman.action.db`, `soulman.action.fs`) |
| `soulman.memory.write` | Perception / Action → Memory | Fire-and-forget write events (raw input log, action log). **Read queries go via HTTP/gRPC, not this topic.** |
| `output.<type>` | Soulman → External | Events readable by home-network consumers (e.g. `output.tts`, `output.dashboard`, `output.notification`) |

The `soulman.*` namespace is internal — no external process should publish or subscribe to it. The `input.*` and `output.*` namespaces are the public contract.

---

## JetStream Streams

Two durable streams are created at bootstrap. Internal `soulman.*` traffic is ephemeral (core NATS, no persistence needed by default).

### STIMULUS stream
- **Subjects:** `input.>`, `soulman.stimulus.raw`
- **Retention:** `limits` — aligns with the immutable raw input log requirement
- **Max age:** 30 days
- **Storage:** file

### OUTPUT stream
- **Subjects:** `output.>`
- **Retention:** `limits`
- **Max age:** 7 days
- **Storage:** file

Bootstrap commands:
```sh
nats stream add STIMULUS \
  --subjects "input.>,soulman.stimulus.raw" \
  --retention limits \
  --max-age 30d \
  --storage file

nats stream add OUTPUT \
  --subjects "output.>" \
  --retention limits \
  --max-age 7d \
  --storage file
```

---

## Authorization

No authentication. The firewall is the perimeter — port 4222 is restricted to the LAN subnet at the OS level. Any process that can reach the bus is already on the trusted home network; if a malicious process has that access, the message bus is the least of the concerns.

---

## Network Scope

- NATS binds on `0.0.0.0:4222` — reachable from the home network.
- Access is restricted to the LAN subnet at the OS firewall level. No internet exposure.
- Processes on this machine connect to `nats://localhost:4222`.
- Home-network consumers connect to `nats://<machine-LAN-IP>:4222`.

---

## Installation

1. Download NATS Server from https://nats.io/download/
2. Place binary in `C:/nats/nats-server.exe`
3. Copy `docs/specs/nats-server.conf` to `C:/nats/nats-server.conf`
4. Create `C:/nats/data/` for JetStream storage
5. Register as a Windows service:
   ```sh
   nats-server.exe --signal install --config C:/nats/nats-server.conf
   ```
6. Run bootstrap commands above to create streams

---

## Design Constraints

- **JetStream persistence on input** — Thinking can restart without losing stimuli in flight.
- **No cross-machine writes** — the bus is home-network scoped; all writes originate on this machine.
- **Firewall is the access control** — no application-level auth; OS firewall restricts port 4222 to the LAN subnet.
