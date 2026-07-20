# System Monitor: Service Health Checks Design

**Date:** 2026-07-19
**Status:** Approved
**Phase:** Soulman Phase 2 — extends the System Monitor pull channel (`docs/superpowers/specs/2026-07-18-system-monitor-channel-design.md`), which explicitly listed "service health checks" as out of scope for its first iteration.

---

## Summary

Adds a fourth check type, `service_health`, to `perception-svc/sysmonitor`'s existing `checks` list — reusing the same poll loop, edge-triggered state machine, and mechanical `thinking-svc` rule the disk/memory/CPU checks already use. Unlike those three, `service_health` checks are binary (`ok`/`critical`, no `warning` tier) and probe an external target instead of a local OS syscall: either a bare `host:port` (TCP dial) or a full `http(s)://` URL (GET, any 2xx is healthy). No new `action-svc` code, no new poll interval, no new config knobs beyond the check entries themselves.

Initial services to monitor (dev config, actual ports/URLs from the requester):

| Name | Target |
|---|---|
| `digital-me-frontend` | `http://localhost:5173/` |
| `digital-me-backend` | `http://127.0.0.1:8080/actuator/health` |
| `agent-suite-frontend` | `https://agent.breynisson.org` |
| `agent-suite-backend` | `http://localhost:8091/health` |

---

## Config: `common/sharedconfig`

`CheckConfig` gains two new optional fields, used only by `service_health`:

```go
type CheckConfig struct {
    Type                     string  `json:"type"` // "disk_space" | "memory" | "cpu" | "service_health"
    Path                     string  `json:"path,omitempty"`   // disk_space only
    Name                     string  `json:"name,omitempty"`   // service_health only
    Target                   string  `json:"target,omitempty"` // service_health only: "host:port" or "http(s)://..."
    WarningThresholdPercent  float64 `json:"warning_threshold_percent,omitempty"`  // unused by service_health
    CriticalThresholdPercent float64 `json:"critical_threshold_percent,omitempty"` // unused by service_health
}
```

`Target` is polymorphic, detected by prefix: `http://` or `https://` → HTTP GET check; anything else → treated as `host:port` for a raw TCP dial. This lets a single check list mix "just tell me the port is open" services with services that expose a real health endpoint, without a second check type.

No new `poll_interval_seconds` knob — `service_health` checks run on the same shared interval (300s in both `config/dev.json` and `config/prod.json`) as disk/memory/CPU. No per-check timeout config either — the dial/GET timeout is a fixed 5-second constant in `sysmonitor` (generous enough to avoid false positives on a slow-but-alive local service, short enough not to stall the poll loop).

### `config/dev.json` and `config/prod.json`

Both get four new entries appended to the existing `system_monitor.checks` array:

```json
{ "type": "service_health", "name": "digital-me-frontend", "target": "http://localhost:5173/" },
{ "type": "service_health", "name": "digital-me-backend", "target": "http://127.0.0.1:8080/actuator/health" },
{ "type": "service_health", "name": "agent-suite-frontend", "target": "https://agent.breynisson.org" },
{ "type": "service_health", "name": "agent-suite-backend", "target": "http://localhost:8091/health" }
```

Same caveat as the existing disk/memory/CPU checks: dev and prod run on the same physical machine and will each independently poll and alert on the same real service state — accepted duplication, consistent with the existing spec's reasoning for the Gmail channel and the other three check types.

---

## Package: `perception-svc/sysmonitor`

### New seam: `healthChecker`

Deliberately **not** added to the existing `statsProvider` interface — `statsProvider` mirrors local OS syscalls (`golang.org/x/sys/windows`); a service health check is network I/O with different failure modes (timeouts, DNS, refused connections, HTTP status codes) and deserves its own seam for fakability in tests:

```go
type healthChecker interface {
    Check(target string, timeout time.Duration) (healthy bool, detail string)
}
```

Real implementation (no build tag needed, same reasoning as `checks_windows.go`):

- Prefix `http://` / `https://` → `http.Client{Timeout: timeout}.Get(target)`. Any `2xx` status is healthy. Otherwise `detail` is `"status <code>"` or the request error (timeout, connection refused, DNS failure, TLS error). Default `http.Client` redirect-following behavior is unchanged (no special redirect policy needed for these four targets).
- No recognized prefix → `net.DialTimeout("tcp", target, timeout)`. Success (then immediately closed) is healthy; otherwise `detail` is the dial error.
- Timeout is a fixed 5-second package constant.

### Integration into the existing poll loop

`checkKey` gains a case for `service_health`, keyed on `Name` (mirrors how `disk_space` is keyed on `Type+Path`):

```go
func checkKey(c CheckConfig) string {
    switch c.Type {
    case "disk_space":
        return c.Type + ":" + c.Path
    case "service_health":
        return c.Type + ":" + c.Name
    default:
        return c.Type
    }
}
```

`runCheck` branches by type: `service_health` skips `measure`/`deriveSeverity` (no percentage, no threshold — binary per the approved design) and instead calls `healthChecker.Check`, mapping the boolean directly to `severityOK`/`severityCritical` — there is no `warning` tier for this check type. Everything else in `runCheck` is unchanged and reused as-is: the in-memory `state` map, first-poll-quiet-if-`ok` (a service that's already down on `perception-svc` startup still publishes immediately — only a healthy first sighting is suppressed), publish-failure leaves state unadvanced for retry next poll.

`checkLabel`/`formatMessage` get a `service_health` case:

- critical: `"Service down: <name> unreachable (<detail>)"`
- ok (recovery): `"Service recovered: <name> is back up"`

### Stimulus construction

Same `Stimulus` envelope as the other three check types (channel `"system-monitor"`, `source.identity` `"system-monitor"`, etc. — see the base design spec), except `channel_metadata.channel_specific`, which for `service_health` is a distinct shape (no `value_percent`/`threshold_percent`, since neither applies to a binary check):

```json
{
  "check_type": "service_health",
  "name": "agent-suite-backend",
  "severity": "critical",
  "error": "dial tcp 127.0.0.1:8091: connect: connection refused"
}
```

`error` is omitted (`omitempty`) when `severity` is `ok`. `hints.tags` follows the existing pattern: `["system", "system-monitor", "service_health"]`. `hints.priority` reuses the existing `priorityFor` mapping (`critical` → `"critical"`, recovery to `ok` → `"normal"`).

`channel_metadata.message_id` reuses the base spec's `computeMessageID(checkType, path, severity, occurredAt)` helper unmodified, passing `Name` in the `path` argument slot — the dedup key becomes `sha256("service_health" + name + severity + occurred_at)`, still unique per check per transition.

---

## Thinking Rule: `thinking-svc/rules/system_monitor.go`

One small addition to `systemMonitorSourcePath`, which currently only reads `check_type`/`path` from `channel_specific`. It needs to also try `name` so a `service_health` alert resolves to `system-monitor/service_health/<name>` instead of falling back to the bare `system-monitor/service_health`:

```go
var meta struct {
    CheckType string `json:"check_type"`
    Path      string `json:"path"`
    Name      string `json:"name"`
}
if len(s.ChannelMeta.ChannelSpecific) > 0 {
    _ = json.Unmarshal(s.ChannelMeta.ChannelSpecific, &meta)
}
id := meta.Path
if id == "" {
    id = meta.Name
}
if id == "" {
    return "system-monitor/" + meta.CheckType
}
return "system-monitor/" + meta.CheckType + "/" + id
```

No other change to `handleSystemMonitor` — `summary`/`raw_content` still pass `s.Content.RawText` verbatim, `action_hint` is still `append_daily_report_entry`. No `action-svc` changes at all; a resulting report entry reads like:

```
2026-07-19 10:05  [system-monitor/service_health/agent-suite-backend]  Service down: agent-suite-backend unreachable (dial tcp 127.0.0.1:8091: connect: connection refused)
```

---

## Error Handling

| Failure | Behaviour |
|---|---|
| Dial times out / connection refused / DNS failure | `critical`; `detail` captures which one |
| HTTP GET succeeds but returns a non-2xx status (e.g. actuator reporting a down component) | `critical`; `detail` is `"status <code>"` |
| HTTP GET times out or TLS handshake fails | `critical`; `detail` is the underlying error |
| One service's check fails | Logged and skipped that poll tick; other checks (including other services and the disk/memory/CPU checks) still run — same per-check isolation as today |
| Publish fails | Severity not advanced; retried next poll — unchanged existing behavior |

---

## Testing

- `sysmonitor_test.go`: extend with a fake `healthChecker` (scripted healthy/unhealthy sequence), same shape as the existing fake `statsProvider`. Assertions: healthy-throughout → no stimulus; `ok→critical` and `critical→ok` → exactly one stimulus each with correct message and `severity`; a service already down on the very first poll → publishes immediately (only a healthy first sighting is suppressed); a simulated publish failure → severity not advanced, same transition re-fires next poll.
- New `servicehealth_windows_test.go` (or colocated with `checks_windows_test.go`): smoke test the real `healthChecker` against an `httptest.Server` for the HTTP path (2xx → healthy, 503 → unhealthy) and a closed TCP listener for the dial path (→ unhealthy).
- `thinking-svc/rules/system_monitor_test.go`: add a case feeding a `service_health` stimulus, asserting `source_path` resolves via `name` (e.g. `system-monitor/service_health/agent-suite-backend`).

---

## Out of Scope (this iteration)

- **SSL certificate expiry checking** — still separately out of scope, distinct from reachability/status checking; the `https://` targets here are only checked for a successful 2xx response, not certificate validity or expiry (this was listed alongside service health checks in `Perception module.md`'s System Monitor row, but only service health is being built now).
- Per-check configurable timeouts (fixed 5s constant for all `service_health` checks).
- A separate, faster poll interval for service checks (shared 300s interval with disk/memory/CPU).
- Response-time-based degraded/warning state (binary `ok`/`critical` only, per the approved design).
- Any severity-aware routing in `action-svc` beyond the existing mechanical report-entry write — same deferred item as the base System Monitor spec.

---

## Related

- `docs/superpowers/specs/2026-07-18-system-monitor-channel-design.md` — base System Monitor design this extends; disk/memory/CPU checks, the poll loop, edge-triggered state machine, and Stimulus/Thinking-rule shape are all reused unmodified.
- `Perception module.md` — System Monitor channel row, listing service health checks and SSL certificate expiry as the channel's eventual full scope.
- `perception-svc/NOTES.md`, `thinking-svc/NOTES.md` — operational notes for the services this feature touches.
