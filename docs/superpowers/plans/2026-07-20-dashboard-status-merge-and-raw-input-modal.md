# Dashboard: Status Merge + Raw Input Modal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fold the System Monitor status (external services + resources) into the existing "System Status" panel, removing the standalone panel from the previous iteration; truncate Recent Raw Inputs entries to 2 lines with a click-to-expand detail modal.

**Architecture:** `StatusPanel` becomes a hybrid — keeps its existing `initialStatus` prop (Soulman's own services, fed by `App.tsx`'s auth-gating fetch, unchanged) and adds its own self-fetch of `getSystemMonitorStatus` (same pattern every other panel uses), rendering one unified list. `SystemMonitorPanel` is deleted. A new standalone `RawInputModal` component renders one `RawInput`'s full record; `RawInputsPanel` gains local `selected` state and Tailwind's native `line-clamp-2` utility.

**Tech Stack:** React + TypeScript + Vitest (`web/`), Tailwind v4 (no plugin needed for `line-clamp-2`).

## Global Constraints

- `App.tsx`'s `getStatus` fetch (which decides `dashboard` vs `restricted` view) is untouched.
- If `getSystemMonitorStatus` fails inside `StatusPanel`, fail silently — Soulman's own service list must still render, no error banner.
- The "No status data" placeholder shows only when Soulman services, external services, AND resources are all empty.
- `RawInputModal` is view-only — no editing/acting on the raw input from the modal.
- Spec of record: `docs/superpowers/specs/2026-07-20-dashboard-status-merge-and-raw-input-modal-design.md`.

---

### Task 1: `StatusPanel` — merge in System Monitor data

**Files:**
- Modify: `web/src/components/StatusPanel.tsx`
- Modify: `web/src/components/StatusPanel.test.tsx`

**Interfaces:**
- Consumes: `getSystemMonitorStatus`, `CheckStatus` (existing, from `web/src/api.ts`).
- Produces: `StatusPanel`'s public prop signature (`{ initialStatus: ServiceStatus | null }`) is unchanged — Task 2 (`Dashboard.tsx`) needs no changes to how it renders `StatusPanel`.

- [ ] **Step 1: Write the failing tests**

Replace `web/src/components/StatusPanel.test.tsx` entirely:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockGetSystemMonitorStatus = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return { ...actual, getSystemMonitorStatus: (...args: unknown[]) => mockGetSystemMonitorStatus(...args) };
});

beforeEach(() => {
  vi.clearAllMocks();
  mockGetSystemMonitorStatus.mockResolvedValue([]);
});

describe('StatusPanel', () => {
  it('renders each Soulman service and its up/down state', async () => {
    const { StatusPanel } = await import('./StatusPanel');
    render(<StatusPanel initialStatus={{ 'memory-svc': 'up', 'action-svc': 'down' }} />);
    expect(screen.getByText('memory-svc')).toBeInTheDocument();
    expect(screen.getByText('up')).toBeInTheDocument();
    expect(screen.getByText('action-svc')).toBeInTheDocument();
    expect(screen.getByText('down')).toBeInTheDocument();
  });

  it('shows a placeholder only when there is no status data from either source', async () => {
    const { StatusPanel } = await import('./StatusPanel');
    render(<StatusPanel initialStatus={null} />);
    expect(await screen.findByText(/no status data/i)).toBeInTheDocument();
  });

  it('shows external service checks as up/down alongside Soulman services', async () => {
    mockGetSystemMonitorStatus.mockResolvedValue([
      {
        type: 'service_health',
        key: 'agent-suite-backend',
        severity: 'critical',
        detail: 'connection refused',
        checked_at: '2026-07-20T00:00:00Z',
      },
    ]);
    const { StatusPanel } = await import('./StatusPanel');
    render(<StatusPanel initialStatus={{ 'memory-svc': 'up' }} />);

    expect(await screen.findByText('agent-suite-backend')).toBeInTheDocument();
    expect(screen.getByText('down')).toBeInTheDocument();
  });

  it('shows resource checks with their percentage value', async () => {
    mockGetSystemMonitorStatus.mockResolvedValue([
      { type: 'disk_space', key: 'C:\\', severity: 'warning', value_percent: 88, checked_at: '2026-07-20T00:00:00Z' },
    ]);
    const { StatusPanel } = await import('./StatusPanel');
    render(<StatusPanel initialStatus={{ 'memory-svc': 'up' }} />);

    expect(await screen.findByText('Disk C:\\')).toBeInTheDocument();
    expect(await screen.findByText('88%')).toBeInTheDocument();
  });

  it('still renders Soulman services if the system-monitor fetch fails', async () => {
    mockGetSystemMonitorStatus.mockRejectedValue(new Error('network error'));
    const { StatusPanel } = await import('./StatusPanel');
    render(<StatusPanel initialStatus={{ 'memory-svc': 'up' }} />);

    expect(await screen.findByText('memory-svc')).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd web && npx vitest run src/components/StatusPanel.test.tsx`
Expected: FAIL — the current `StatusPanel` doesn't fetch `getSystemMonitorStatus` at all, so the external-service and resource tests find no matching text; the mock module factory referencing `getSystemMonitorStatus` still resolves fine (it's a real export already), so the failure is in the assertions, not a crash.

- [ ] **Step 3: Implement**

Replace `web/src/components/StatusPanel.tsx` entirely:

```tsx
import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { getSystemMonitorStatus, type ServiceStatus, type CheckStatus } from '../api';

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

export function StatusPanel({ initialStatus }: { initialStatus: ServiceStatus | null }) {
  const [checks, setChecks] = useState<CheckStatus[] | null>(null);

  useEffect(() => {
    let active = true;
    (async () => {
      const token = await getAccessToken();
      try {
        const data = await getSystemMonitorStatus(token);
        if (active) setChecks(data);
      } catch {
        // System monitor data is supplementary — Soulman's own service
        // status (initialStatus) still renders even if this fetch fails.
      }
    })();
    return () => {
      active = false;
    };
  }, []);

  const services = initialStatus ? Object.entries(initialStatus) : [];
  const externalServices = checks?.filter((c) => c.type === 'service_health') ?? [];
  const resources = checks?.filter((c) => RESOURCE_TYPES.has(c.type)) ?? [];
  const hasAnyData = services.length > 0 || externalServices.length > 0 || resources.length > 0;

  return (
    <div className="rounded border bg-white p-4">
      <h2 className="mb-2 font-medium">System Status</h2>
      {!hasAnyData ? (
        <p className="text-sm text-gray-500">No status data</p>
      ) : (
        <ul className="space-y-1">
          {services.map(([name, state]) => (
            <li key={name} className="flex justify-between text-sm">
              <span>{name}</span>
              <span className={state === 'up' ? 'text-green-600' : 'text-red-600'}>{state}</span>
            </li>
          ))}
          {externalServices.map((c) => (
            <li key={`service:${c.key ?? ''}`} className="flex justify-between text-sm">
              <span>{c.key}</span>
              <span className={severityColor(c.severity)}>{c.severity === 'critical' ? 'down' : 'up'}</span>
            </li>
          ))}
          {resources.map((c) => (
            <li key={`resource:${c.type}:${c.key ?? ''}`} className="flex justify-between text-sm">
              <span>{resourceLabel(c)}</span>
              <span className={severityColor(c.severity)}>
                {c.value_percent !== undefined ? `${Math.round(c.value_percent)}%` : c.severity}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd web && npx vitest run src/components/StatusPanel.test.tsx`
Expected: PASS — all 5 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/StatusPanel.tsx web/src/components/StatusPanel.test.tsx
git commit -m "web: merge system monitor checks into StatusPanel"
```

---

### Task 2: Remove the standalone `SystemMonitorPanel`

**Files:**
- Delete: `web/src/components/SystemMonitorPanel.tsx`
- Delete: `web/src/components/SystemMonitorPanel.test.tsx`
- Modify: `web/src/components/Dashboard.tsx`

**Interfaces:** None (removal + one import/usage edit).

- [ ] **Step 1: Delete the two files**

```bash
git rm web/src/components/SystemMonitorPanel.tsx web/src/components/SystemMonitorPanel.test.tsx
```

- [ ] **Step 2: Update `Dashboard.tsx`**

Replace `web/src/components/Dashboard.tsx` entirely:

```tsx
import type { ServiceStatus } from '../api';
import { StatusPanel } from './StatusPanel';
import { EpisodesPanel } from './EpisodesPanel';
import { RawInputsPanel } from './RawInputsPanel';
import { ReportsPanel } from './ReportsPanel';

export function Dashboard({
  initialStatus,
  onSignOut,
}: {
  initialStatus: ServiceStatus | null;
  onSignOut: () => void;
}) {
  return (
    <div className="min-h-screen bg-gray-50 p-6">
      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Soulman Dashboard</h1>
        <button onClick={onSignOut} className="text-sm text-gray-500 underline">
          Sign out
        </button>
      </div>
      <div className="grid grid-cols-1 gap-6 md:grid-cols-2">
        <StatusPanel initialStatus={initialStatus} />
        <ReportsPanel />
        <EpisodesPanel />
        <RawInputsPanel />
      </div>
    </div>
  );
}
```

- [ ] **Step 3: Run the full frontend test suite**

Run: `cd web && npx vitest run`
Expected: PASS — every remaining test (the deleted `SystemMonitorPanel.test.tsx` no longer runs), no failures, no lingering reference to the deleted component anywhere.

- [ ] **Step 4: Verify the typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no errors (confirms no other file still imports `SystemMonitorPanel`).

- [ ] **Step 5: Commit**

```bash
git add web/src/components/Dashboard.tsx
git commit -m "web: remove standalone SystemMonitorPanel, superseded by StatusPanel merge"
```

---

### Task 3: `RawInputModal` — new standalone detail modal

**Files:**
- Create: `web/src/components/RawInputModal.tsx`
- Test: `web/src/components/RawInputModal.test.tsx`

**Interfaces:**
- Consumes: `RawInput` (existing type, from `web/src/api.ts`).
- Produces: `RawInputModal({ input: RawInput, onClose: () => void })` — Task 4 (`RawInputsPanel.tsx`) renders it.

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/RawInputModal.test.tsx`:

```tsx
import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { RawInputModal } from './RawInputModal';
import type { RawInput } from '../api';

const baseInput: RawInput = {
  stimulus_id: 'stim-1',
  received_at: '2026-07-20T00:00:00Z',
  channel: 'cli-note',
  normalized_text: 'hello world',
  raw_payload: { foo: 'bar' },
};

describe('RawInputModal', () => {
  it('renders the full record including pretty-printed raw_payload', () => {
    render(<RawInputModal input={baseInput} onClose={vi.fn()} />);
    expect(screen.getByText('stim-1')).toBeInTheDocument();
    expect(screen.getByText('cli-note')).toBeInTheDocument();
    expect(screen.getByText('2026-07-20T00:00:00Z')).toBeInTheDocument();
    expect(screen.getByText(/"foo": "bar"/)).toBeInTheDocument();
  });

  it('omits the override command row when absent', () => {
    render(<RawInputModal input={baseInput} onClose={vi.fn()} />);
    expect(screen.queryByText('Override Command')).not.toBeInTheDocument();
  });

  it('shows the override command row when present', () => {
    render(<RawInputModal input={{ ...baseInput, override_cmd: 'PAUSE' }} onClose={vi.fn()} />);
    expect(screen.getByText('Override Command')).toBeInTheDocument();
    expect(screen.getByText('PAUSE')).toBeInTheDocument();
  });

  it('calls onClose when the close button is clicked', () => {
    const onClose = vi.fn();
    render(<RawInputModal input={baseInput} onClose={onClose} />);
    fireEvent.click(screen.getByLabelText('Close'));
    expect(onClose).toHaveBeenCalled();
  });

  it('calls onClose when the backdrop is clicked', () => {
    const onClose = vi.fn();
    const { container } = render(<RawInputModal input={baseInput} onClose={onClose} />);
    fireEvent.click(container.firstChild as Element);
    expect(onClose).toHaveBeenCalled();
  });

  it('does not close when clicking inside the card', () => {
    const onClose = vi.fn();
    render(<RawInputModal input={baseInput} onClose={onClose} />);
    fireEvent.click(screen.getByText('Raw Input'));
    expect(onClose).not.toHaveBeenCalled();
  });

  it('calls onClose on Escape keydown', () => {
    const onClose = vi.fn();
    render(<RawInputModal input={baseInput} onClose={onClose} />);
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose).toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd web && npx vitest run src/components/RawInputModal.test.tsx`
Expected: FAIL — `Cannot find module './RawInputModal'`.

- [ ] **Step 3: Implement**

Create `web/src/components/RawInputModal.tsx`:

```tsx
import { useEffect } from 'react';
import type { RawInput } from '../api';

export function RawInputModal({ input, onClose }: { input: RawInput; onClose: () => void }) {
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [onClose]);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={onClose}>
      <div
        className="max-h-[80vh] w-full max-w-lg overflow-auto rounded bg-white p-4 shadow-lg"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-2 flex items-center justify-between">
          <h3 className="font-medium">Raw Input</h3>
          <button onClick={onClose} className="text-sm text-gray-500 underline" aria-label="Close">
            Close
          </button>
        </div>
        <dl className="mb-3 space-y-1 text-sm">
          <div className="flex justify-between">
            <dt className="text-gray-500">Stimulus ID</dt>
            <dd>{input.stimulus_id}</dd>
          </div>
          <div className="flex justify-between">
            <dt className="text-gray-500">Channel</dt>
            <dd>{input.channel}</dd>
          </div>
          <div className="flex justify-between">
            <dt className="text-gray-500">Received At</dt>
            <dd>{input.received_at}</dd>
          </div>
          {input.override_cmd && (
            <div className="flex justify-between">
              <dt className="text-gray-500">Override Command</dt>
              <dd>{input.override_cmd}</dd>
            </div>
          )}
        </dl>
        <pre className="overflow-auto rounded bg-gray-50 p-2 text-xs">
          {JSON.stringify(input.raw_payload, null, 2)}
        </pre>
      </div>
    </div>
  );
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd web && npx vitest run src/components/RawInputModal.test.tsx`
Expected: PASS — all 7 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/RawInputModal.tsx web/src/components/RawInputModal.test.tsx
git commit -m "web: add RawInputModal for viewing a raw input's full record"
```

---

### Task 4: `RawInputsPanel` — truncate to 2 lines, wire in the modal

**Files:**
- Modify: `web/src/components/RawInputsPanel.tsx`
- Create: `web/src/components/RawInputsPanel.test.tsx` (this component currently has no test file)

**Interfaces:**
- Consumes: `RawInputModal` (Task 3).

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/RawInputsPanel.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockGetRawInputs = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return { ...actual, getRawInputs: (...args: unknown[]) => mockGetRawInputs(...args) };
});

beforeEach(() => vi.clearAllMocks());

const sampleInput = {
  stimulus_id: 'stim-1',
  received_at: '2026-07-20T00:00:00Z',
  channel: 'cli-note',
  normalized_text: 'hello world',
  raw_payload: { foo: 'bar' },
};

describe('RawInputsPanel', () => {
  it('shows raw inputs once loaded, with line-clamp applied to the text', async () => {
    mockGetRawInputs.mockResolvedValue([sampleInput]);
    const { RawInputsPanel } = await import('./RawInputsPanel');
    render(<RawInputsPanel />);

    const text = await screen.findByText('hello world');
    expect(text).toHaveClass('line-clamp-2');
  });

  it('shows an error banner without throwing when the fetch fails', async () => {
    mockGetRawInputs.mockRejectedValue(new Error('network error'));
    const { RawInputsPanel } = await import('./RawInputsPanel');
    render(<RawInputsPanel />);

    expect(await screen.findByText(/raw inputs unavailable/i)).toBeInTheDocument();
  });

  it('shows an empty state when there are no raw inputs', async () => {
    mockGetRawInputs.mockResolvedValue([]);
    const { RawInputsPanel } = await import('./RawInputsPanel');
    render(<RawInputsPanel />);

    expect(await screen.findByText(/no raw inputs yet/i)).toBeInTheDocument();
  });

  it('opens the detail modal when an entry is clicked, showing its raw_payload', async () => {
    mockGetRawInputs.mockResolvedValue([sampleInput]);
    const { RawInputsPanel } = await import('./RawInputsPanel');
    render(<RawInputsPanel />);

    const row = await screen.findByText('hello world');
    fireEvent.click(row);

    expect(await screen.findByText(/"foo": "bar"/)).toBeInTheDocument();
  });

  it('closes the modal when its close button is clicked', async () => {
    mockGetRawInputs.mockResolvedValue([sampleInput]);
    const { RawInputsPanel } = await import('./RawInputsPanel');
    render(<RawInputsPanel />);

    fireEvent.click(await screen.findByText('hello world'));
    fireEvent.click(await screen.findByLabelText('Close'));

    expect(screen.queryByText(/"foo": "bar"/)).not.toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd web && npx vitest run src/components/RawInputsPanel.test.tsx`
Expected: FAIL — the current component has no `line-clamp-2` class and no click handling, so the click-to-open and class assertions fail.

- [ ] **Step 3: Implement**

Replace `web/src/components/RawInputsPanel.tsx` entirely. Note the layout change from the current single inline line to a two-part row (a metadata header line, then the clamped text on its own line below) — necessary for `line-clamp-2` to work correctly, since the CSS line-clamp technique requires the clamped text to be its own block-level element, not mixed inline with sibling text:

```tsx
import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { getRawInputs, type RawInput } from '../api';
import { RawInputModal } from './RawInputModal';

export function RawInputsPanel() {
  const [inputs, setInputs] = useState<RawInput[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<RawInput | null>(null);

  useEffect(() => {
    let active = true;
    (async () => {
      const token = await getAccessToken();
      try {
        const data = await getRawInputs(token);
        if (active) setInputs(data);
      } catch {
        if (active) setError('Raw inputs unavailable');
      }
    })();
    return () => {
      active = false;
    };
  }, []);

  return (
    <div className="rounded border bg-white p-4">
      <h2 className="mb-2 font-medium">Recent Raw Inputs</h2>
      {error && <p className="text-sm text-red-600">{error}</p>}
      {!error && inputs === null && <p className="text-sm text-gray-500">Loading...</p>}
      {!error && inputs?.length === 0 && <p className="text-sm text-gray-500">No raw inputs yet</p>}
      {!error && inputs && inputs.length > 0 && (
        <ul className="space-y-2">
          {inputs.map((i) => (
            <li
              key={i.stimulus_id}
              className="cursor-pointer text-sm"
              role="button"
              onClick={() => setSelected(i)}
            >
              <div className="text-gray-400">
                {i.received_at} [{i.channel}]
              </div>
              <div className="line-clamp-2">{i.normalized_text ?? '(no text)'}</div>
            </li>
          ))}
        </ul>
      )}
      {selected && <RawInputModal input={selected} onClose={() => setSelected(null)} />}
    </div>
  );
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd web && npx vitest run src/components/RawInputsPanel.test.tsx`
Expected: PASS — all 5 tests.

- [ ] **Step 5: Run the full frontend suite**

Run: `cd web && npx vitest run && npx tsc --noEmit`
Expected: PASS, no regressions, no type errors.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/RawInputsPanel.tsx web/src/components/RawInputsPanel.test.tsx
git commit -m "web: truncate raw input entries to 2 lines, open detail modal on click"
```

---

### Task 5: Update `CLAUDE.md`

**Files:**
- Modify: `CLAUDE.md`

**Interfaces:** None (documentation only).

- [ ] **Step 1: Append the new spec to `web-svc`'s Specs list**

In `CLAUDE.md`, find the `web-svc` bullet's Specs line (currently ends with `2026-07-20-system-monitor-dashboard-panel-design.md`) and append the new spec:

```
- Specs: `2026-07-19-soulman-web-dashboard-design.md`, `2026-07-20-system-monitor-dashboard-panel-design.md`, `2026-07-20-dashboard-status-merge-and-raw-input-modal-design.md`
```

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: note the dashboard status merge and raw input modal spec"
```

---

## Final Verification

After all 5 tasks:

- [ ] `cd web && npx vitest run && npx tsc --noEmit` — expect all frontend tests PASS (including the new/rewritten ones) and no type errors.
- [ ] Confirm `web/src/components/SystemMonitorPanel.tsx` and `.test.tsx` no longer exist (`git status` shows them deleted, not just untracked).
- [ ] Manually load the dashboard: confirm "System Status" shows Soulman's services, the 4 external services (up/down), and disk/memory/CPU (percentages) all in one list; confirm no separate "System Monitor" panel remains; confirm clicking a Raw Inputs entry opens the modal with the full record, and Escape/backdrop/close-button all dismiss it.
