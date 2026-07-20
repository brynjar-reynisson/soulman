# Dashboard: Merge System Monitor into Status, Raw Input Detail Modal

**Date:** 2026-07-20
**Status:** Approved
**Phase:** Soulman Phase 2 — reworks `docs/superpowers/specs/2026-07-20-system-monitor-dashboard-panel-design.md`'s separate panel (which never actually reached users — see Background) and extends `docs/superpowers/specs/2026-07-19-soulman-web-dashboard-design.md`'s `RawInputsPanel`.

---

## Background

The previous feature added a standalone "System Monitor" panel to the dashboard. After merging and restarting, the panel didn't appear at all — not a code bug, but a deployment gap: restarting `perception-svc`/`web-svc` (the two Go backends) never rebuilt the `web` frontend process itself, which kept serving its pre-merge build. Rather than deploy that design as-is and iterate, the user reviewed a screenshot of the current dashboard and asked for a different layout: no separate panel, fold the System Monitor data straight into the existing "System Status" panel.

---

## Part 1: Merge System Monitor into `StatusPanel`

`StatusPanel` currently renders only Soulman's own four services (via the `initialStatus` prop, fetched once by `App.tsx` as part of its auth-gating flow — this stays unchanged, since `App.tsx` depends on that fetch's success/403 to decide `dashboard` vs `restricted` view). It becomes a hybrid: still receives `initialStatus` as a prop, but also self-fetches `getSystemMonitorStatus` on mount (same pattern every other panel already uses). One list, three groups in order:

1. **Soulman's own services** (unchanged rendering: name, `up`/`down` in green/red) — from `initialStatus`.
2. **External services** (`service_health` checks): name (`key`), `up`/`down` — `critical` → `down` (red), anything else → `up` (green). Binary, no third state, matching the check type's own binary design.
3. **Resources** (`disk_space`/`memory`/`cpu`): label (`Disk C:\`, `Memory`, `CPU`) and `NN%`, colored by severity — green `ok`, amber `warning`, red `critical`.

If the system-monitor fetch fails, it fails silently (no error banner) — Soulman's own service status is the panel's primary job and must not be hidden by a secondary data source's failure. The "No status data" placeholder only shows when **all three** groups are empty, not just the first.

`SystemMonitorPanel.tsx` and `SystemMonitorPanel.test.tsx` are deleted; `Dashboard.tsx` drops the import and the `<SystemMonitorPanel />` element it added last iteration.

### Resource/service labeling and severity-color helpers

Reused verbatim from the deleted `SystemMonitorPanel.tsx` (same logic, same output) rather than reinvented:

```ts
const RESOURCE_TYPES = new Set(['disk_space', 'memory', 'cpu']);

function resourceLabel(c: CheckStatus): string {
  if (c.type === 'disk_space') return `Disk ${c.key ?? ''}`;
  if (c.type === 'memory') return 'Memory';
  if (c.type === 'cpu') return 'CPU';
  return c.type;
}

function severityColor(severity: string): string {
  if (severity === 'critical') return 'text-red-600';
  if (severity === 'warning') return 'text-amber-600';
  return 'text-green-600';
}
```

---

## Part 2: `RawInputsPanel` — truncate to 2 lines, click to expand

Each entry's `normalized_text` gets Tailwind's `line-clamp-2` utility (native in Tailwind v4, this project's version — no plugin needed), so long text is visually cut to two lines instead of growing the list item indefinitely.

Each row becomes clickable (`cursor-pointer`, a `role="button"` + `onClick`), opening a new modal component, `RawInputModal.tsx`, showing the complete record for that one `RawInput`:

- `stimulus_id`
- `channel`
- `received_at`
- `override_cmd` (only if present)
- `raw_payload`, pretty-printed (`JSON.stringify(raw_payload, null, 2)` in a `<pre>` block)

The modal is a simple, self-built overlay (no new dependency) — a fixed, semi-transparent backdrop behind a centered white card. Closes on: clicking the backdrop, a close button, or pressing Escape. `RawInputsPanel` owns which input (if any) is selected as local state (`selectedInput: RawInput | null`); the modal is only mounted when non-null.

### Component shape

```tsx
// RawInputModal.tsx
export function RawInputModal({ input, onClose }: { input: RawInput; onClose: () => void }) {
  // Escape-key listener (useEffect), backdrop click, close button — all call onClose.
  // Renders stimulus_id/channel/received_at/override_cmd as labeled rows,
  // then <pre>{JSON.stringify(input.raw_payload, null, 2)}</pre>.
}
```

`RawInputsPanel.tsx` renders `{selectedInput && <RawInputModal input={selectedInput} onClose={() => setSelectedInput(null)} />}` alongside its existing list.

---

## Error Handling

| Failure | Behaviour |
|---|---|
| `getSystemMonitorStatus` fails in `StatusPanel` | Silently ignored — Soulman's own service list (from `initialStatus`) still renders; no error banner, since this fetch is supplementary to the panel's primary purpose |
| `initialStatus` is `null` AND system-monitor fetch also fails/returns empty | "No status data" placeholder, same as today |
| A `RawInput`'s `raw_payload` is not valid JSON-serializable data (shouldn't happen — it's already parsed JSON from the API) | `JSON.stringify` on already-decoded data cannot fail for this shape; no special handling needed |

---

## Testing

- `StatusPanel.test.tsx`: rewritten to mock `getSystemMonitorStatus` (mirroring `EpisodesPanel.test.tsx`'s mocking pattern). Cases: Soulman services render from `initialStatus` alone; placeholder shows only when both sources are empty; external service checks render as up/down; resource checks render with their `NN%` value and correct color; a system-monitor fetch failure still renders Soulman's own services (proving the silent-failure behavior).
- `RawInputsPanel.test.tsx` (new — this component currently has no test file): loading/error/empty states (mirroring `EpisodesPanel.test.tsx`'s existing coverage of that shape), `line-clamp-2` class present on entries, clicking an entry opens the modal with that entry's data, closing via backdrop/button/Escape all work.
- `RawInputModal.test.tsx`: renders all fields including pretty-printed `raw_payload`; omits `override_cmd` row when absent; each close mechanism (button click, backdrop click, Escape keydown) calls `onClose`.

---

## Out of Scope (this iteration)

- Any change to `App.tsx`'s auth-gating fetch of `getStatus` — untouched, still the sole source for `dashboard` vs `restricted` view decisions.
- Editing or acting on a raw input from the modal — view-only.
- Reusing `RawInputModal` as a generic/shared modal primitive for other panels — built specifically for this one use case; a shared primitive is a future refactor if a second modal need arises, not built speculatively now.

---

## Related

- `docs/superpowers/specs/2026-07-20-system-monitor-dashboard-panel-design.md` — the design this supersedes for placement (data flow — `perception-svc` → `web-svc` → `getSystemMonitorStatus` — is unchanged and reused as-is).
- `docs/superpowers/specs/2026-07-19-soulman-web-dashboard-design.md` — base dashboard design (`StatusPanel`, `RawInputsPanel`, auth flow).
