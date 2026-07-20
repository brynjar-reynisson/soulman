# System Monitor Dashboard Panel Design

**Date:** 2026-07-20
**Status:** Approved
**Phase:** Soulman Phase 2 — extends the web dashboard (`docs/superpowers/specs/2026-07-19-soulman-web-dashboard-design.md`) and the System Monitor channel (`docs/superpowers/specs/2026-07-18-system-monitor-channel-design.md`, extended by `docs/superpowers/specs/2026-07-19-system-monitor-service-health-design.md`).

---

## Summary

The System Monitor channel (disk/memory/CPU + the 4 `service_health` checks) only ever surfaces as a daily report entry, and only on a severity *transition* — while everything stays healthy, there is no way to see current state anywhere. This adds a read path: `perception-svc` starts tracking each check's latest result (not just severity, for transition detection) in a small thread-safe status map, exposes it over HTTP, `web-svc` proxies it through the existing auth gate, and the web dashboard gets a new panel showing it, grouped into **Resources** (disk/memory/CPU) and **Services** (the 4 `service_health` checks).

Data is last-poll-fresh, not on-demand — the panel shows whatever `perception-svc`'s background poll loop (still on its existing 300s interval, unchanged) last observed, up to 5 minutes stale in the worst case. No new poll path, no on-demand check triggered by opening the dashboard.

The existing `StatusPanel` (Soulman's own four services' up/down state, via `GET /api/status`) is unrelated and unchanged — this is a separate, new panel for the checks System Monitor performs, not for Soulman's own service health.

---

## `perception-svc/sysmonitor`: richer, mutex-guarded state

`Watcher.state` today is `map[string]severity` — sufficient for edge-triggered publish decisions, insufficient for display (no current value, no detail, no timestamp, and only updated on `publishTransition`'s publish path, not every poll). Replaced with:

```go
type CheckStatus struct {
	Type         string    `json:"type"`
	Key          string    `json:"key,omitempty"`          // path (disk_space) or name (service_health); absent for memory/cpu
	Severity     string    `json:"severity"`
	ValuePercent *float64  `json:"value_percent,omitempty"` // disk_space/memory/cpu only
	Detail       string    `json:"detail,omitempty"`        // service_health only, set when severity is critical
	CheckedAt    time.Time `json:"checked_at"`
}
```

`Watcher` gains:

```go
type Watcher struct {
	// ...existing fields...
	mu     sync.Mutex
	status map[string]CheckStatus // keyed the same as the existing state map (checkKey(c))
}

func (w *Watcher) Status() []CheckStatus
```

`runCheck` (both the percent-based path and the `service_health` path) writes a `CheckStatus` into `w.status[key]` on **every poll**, not just on a transition — this is the one behavioral difference from the existing `state` map, which `publishTransition` still owns unchanged for its edge-triggered publish decision. The two maps track related but distinct things: `state` answers "did severity change since last poll" (publish-gating), `status` answers "what did the most recent poll observe" (display). `Status()` returns a sorted-by-key snapshot copied out under the mutex, safe to call from the HTTP handler's goroutine concurrently with the poll loop.

`perception-svc/httpserver.Server` gains a `systemMonitorStatus func() []sysmonitor.CheckStatus` field (same shape as the existing `natsStatus func() string`, wired the same way from `main.go`) and a new route:

```
GET /api/system-monitor/status  →  200 application/json, []CheckStatus
```

No auth on this route — matches every other `perception-svc` route today (`/health`, `/api/perceive/*`); the dashboard's auth gate is enforced one hop later, in `web-svc`.

---

## `web-svc`: proxy

A new route, following the exact existing pattern `GET /api/episodes` and `GET /api/raw-inputs/recent` already use (auth-gated via the existing owner-email JWT check, proxies to `perception_svc_url` from shared config, passes the response JSON through unmodified — no reshaping):

```
GET /api/system-monitor  →  200 application/json, []CheckStatus (passthrough from perception-svc)
```

---

## `web/` frontend: new panel

`web/src/api.ts` gains a matching type and fetch function:

```ts
export interface CheckStatus {
  type: string;
  key?: string;
  severity: 'ok' | 'warning' | 'critical';
  value_percent?: number;
  detail?: string;
  checked_at: string;
}

export const getSystemMonitorStatus = (token: string | null): Promise<CheckStatus[]> =>
  getJSON('/api/system-monitor', token);
```

New `web/src/components/SystemMonitorPanel.tsx`, self-fetching on mount — same shape as `RawInputsPanel`/`EpisodesPanel` (own `useState`/`useEffect`, own loading/error state, no props). Splits the fetched list into two sections by `type`:

- **Resources** — `disk_space`/`memory`/`cpu` entries: label (disk path or "Memory"/"CPU"), `value_percent` formatted as `NN%`, severity-colored (green `ok`, amber `warning`, red `critical` — matching `StatusPanel`'s existing green/red convention, extended with amber for the one state `StatusPanel` doesn't have).
- **Services** — `service_health` entries: `key` (the service name) as the label, severity-colored up/down (green `ok`, red `critical` — no `warning` tier, per the existing binary design), and `detail` shown as secondary text only when `severity` is `critical`.

Added to `Dashboard.tsx`'s existing grid alongside the four current panels; `StatusPanel` itself is not modified.

---

## Error Handling

| Failure | Behaviour |
|---|---|
| `perception-svc` unreachable from `web-svc` | `web-svc`'s proxy returns an error status (same as the existing episodes/raw-inputs proxy failure path); the panel shows its existing error state ("System monitor status unavailable"), matching `RawInputsPanel`'s established pattern |
| No checks configured (empty list) | Panel renders both sections with an empty-state message, matching `StatusPanel`'s "No status data" convention |
| A single check missing `value_percent`/`detail` (not yet polled once, e.g. right after a restart) | That entry is simply omitted from the list until `Status()` has a value for it — `w.status` only gets an entry once `runCheck` has actually run for it, which happens within the first poll cycle at `Start()`, so this window is at most one immediate-poll's duration, not a steady-state concern |

---

## Testing

- `sysmonitor_test.go`: extend to assert `Status()` reflects every poll (not just transitions) — steady-state polls update `ValuePercent`/`CheckedAt` even though no `Stimulus` is published; assert the two maps (`state` for publish-gating, `status` for display) stay independently correct across a transition sequence.
- `httpserver/server_test.go`: new test for `GET /api/system-monitor/status`, asserting the JSON shape and that it calls through the injected `systemMonitorStatus` function.
- `web-svc/httpserver/server_test.go`: new test for `GET /api/system-monitor`, mirroring the existing episodes/raw-inputs proxy tests (success passthrough, upstream-error passthrough, auth-gate enforcement).
- `web/src/components/SystemMonitorPanel.test.tsx`: new test file, mirroring `RawInputsPanel`'s/`ReportsPanel`'s existing test shape (loading → success renders both sections; loading → error renders the error state).

---

## Out of Scope (this iteration)

- On-demand / "check now" refresh — data is last-poll-fresh only, per the approved design (no new poll path).
- Historical/trend view (e.g. a sparkline of past values) — only current state.
- Any change to `StatusPanel` or `GET /api/status` (Soulman's own service health) — untouched.
- Auth on `perception-svc`'s new endpoint — matches every existing `perception-svc` route (none are authenticated); the dashboard's auth gate is `web-svc`'s job, unchanged from the existing pattern.

---

## Related

- `docs/superpowers/specs/2026-07-18-system-monitor-channel-design.md` — base System Monitor design (disk/memory/CPU, the poll loop, `state`/edge-triggered publish machinery this reuses unmodified).
- `docs/superpowers/specs/2026-07-19-system-monitor-service-health-design.md` — the `service_health` check type this panel also displays.
- `docs/superpowers/specs/2026-07-19-soulman-web-dashboard-design.md` — base dashboard design (`StatusPanel`, auth flow, the proxy pattern this reuses).
- `perception-svc/NOTES.md`, `web-svc/NOTES.md` — operational notes for the services this feature touches.
