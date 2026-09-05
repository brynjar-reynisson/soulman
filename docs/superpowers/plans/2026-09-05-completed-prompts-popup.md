# Completed Prompts Popup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a "Completed" link at the bottom of the Prompts box in the Projects dashboard that opens a popup listing the last 20 finished (`DONE`) prompts — project name, task name, and prompt text — most recently completed first.

**Architecture:** Frontend-only change inside `web/` (React + TypeScript + Vite + Tailwind, tested with vitest + @testing-library/react). A new `CompletedPromptsModal` component derives its list from the `prompts` array `PromptsPanel` already fetches via the existing `getPrompts()` call — no new API call, no backend/schema change. The modal follows the exact overlay/close pattern already established by `RawInputModal.tsx`.

**Tech Stack:** React, TypeScript, Tailwind CSS, vitest, @testing-library/react.

**Spec:** `docs/superpowers/specs/2026-09-05-completed-prompts-popup-design.md`

## Global Constraints

- No backend, `web-svc` proxy, `web/src/api.ts`, or database changes — the `Prompt` type and `getPrompts()` already exist and already carry `done_at` (`web/src/api.ts:286-297`, `:311-312`).
- No completion timestamp rendered in the popup — `done_at` is used only for sort order.
- Cap the popup list at exactly 20 entries, most recently completed (`done_at` descending) first.
- Match existing styling exactly: overlay `fixed inset-0 z-50 flex items-center justify-center bg-black/50`; card `max-h-[80vh] w-full max-w-lg overflow-auto rounded bg-white p-4 shadow-lg`; secondary text-links `text-xs text-gray-500 underline` (header-row links) or `text-sm text-gray-500 underline` (modal Close button) — copy from `RawInputModal.tsx` verbatim.
- The "Completed" link goes at the **bottom** of the Prompts box, below the existing "Add Prompt" button — not next to "Refresh".

---

### Task 1: `CompletedPromptsModal` component

**Files:**
- Create: `web/src/components/CompletedPromptsModal.tsx`
- Test: `web/src/components/CompletedPromptsModal.test.tsx`

**Interfaces:**
- Consumes: `Prompt` type from `web/src/api.ts` (fields: `id`, `project_name`, `task_name`, `prompt_text`, `state`, `done_at?`).
- Produces: `CompletedPromptsModal({ prompts, onClose }: { prompts: Prompt[]; onClose: () => void })` — a React component. `PromptsPanel` (Task 2) renders it conditionally and passes its full `prompts` array straight through; the component itself filters/sorts/caps to the last 20 `DONE` entries.

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/CompletedPromptsModal.test.tsx`:

```tsx
import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { CompletedPromptsModal } from './CompletedPromptsModal';
import type { Prompt } from '../api';

function makePrompt(overrides: Partial<Prompt>): Prompt {
  return {
    id: 1,
    project_name: 'proj',
    task_name: 'task',
    prompt_text: 'do the thing',
    state: 'DONE',
    created_at: '2026-01-01T00:00:00Z',
    ...overrides,
  };
}

describe('CompletedPromptsModal', () => {
  it('shows the empty state when there are no DONE prompts', () => {
    render(
      <CompletedPromptsModal
        prompts={[makePrompt({ id: 1, state: 'NOT_STARTED' })]}
        onClose={vi.fn()}
      />,
    );
    expect(screen.getByText('No completed tasks yet.')).toBeInTheDocument();
  });

  it('renders project, task, and prompt text for a DONE prompt', () => {
    render(
      <CompletedPromptsModal
        prompts={[
          makePrompt({
            id: 1,
            project_name: 'soulman',
            task_name: 'Add feature X',
            prompt_text: 'Implement feature X end to end.',
            state: 'DONE',
            done_at: '2026-09-01T00:00:00Z',
          }),
        ]}
        onClose={vi.fn()}
      />,
    );
    expect(screen.getByText('soulman')).toBeInTheDocument();
    expect(screen.getByText('Add feature X')).toBeInTheDocument();
    expect(screen.getByText('Implement feature X end to end.')).toBeInTheDocument();
  });

  it('excludes non-DONE prompts', () => {
    render(
      <CompletedPromptsModal
        prompts={[
          makePrompt({ id: 1, task_name: 'not done', state: 'IMPLEMENTING' }),
          makePrompt({ id: 2, task_name: 'done one', state: 'DONE', done_at: '2026-09-01T00:00:00Z' }),
        ]}
        onClose={vi.fn()}
      />,
    );
    expect(screen.queryByText('not done')).not.toBeInTheDocument();
    expect(screen.getByText('done one')).toBeInTheDocument();
  });

  it('orders DONE prompts most-recently-completed first', () => {
    render(
      <CompletedPromptsModal
        prompts={[
          makePrompt({ id: 1, task_name: 'older', state: 'DONE', done_at: '2026-09-01T00:00:00Z' }),
          makePrompt({ id: 2, task_name: 'newer', state: 'DONE', done_at: '2026-09-03T00:00:00Z' }),
        ]}
        onClose={vi.fn()}
      />,
    );
    const taskCells = screen.getAllByText(/older|newer/);
    expect(taskCells[0]).toHaveTextContent('newer');
    expect(taskCells[1]).toHaveTextContent('older');
  });

  it('caps the list at 20 entries even when more DONE prompts exist', () => {
    const prompts = Array.from({ length: 25 }, (_, i) =>
      makePrompt({
        id: i + 1,
        task_name: `task-${i}`,
        state: 'DONE',
        done_at: `2026-09-${String((i % 28) + 1).padStart(2, '0')}T00:00:00Z`,
      }),
    );
    render(<CompletedPromptsModal prompts={prompts} onClose={vi.fn()} />);
    expect(screen.getAllByText(/^task-\d+$/)).toHaveLength(20);
  });

  it('calls onClose when the close button is clicked', () => {
    const onClose = vi.fn();
    render(<CompletedPromptsModal prompts={[]} onClose={onClose} />);
    fireEvent.click(screen.getByLabelText('Close'));
    expect(onClose).toHaveBeenCalled();
  });

  it('calls onClose when the backdrop is clicked', () => {
    const onClose = vi.fn();
    const { container } = render(<CompletedPromptsModal prompts={[]} onClose={onClose} />);
    fireEvent.click(container.firstChild as Element);
    expect(onClose).toHaveBeenCalled();
  });

  it('does not close when clicking inside the card', () => {
    const onClose = vi.fn();
    render(<CompletedPromptsModal prompts={[]} onClose={onClose} />);
    fireEvent.click(screen.getByText('Completed Tasks'));
    expect(onClose).not.toHaveBeenCalled();
  });

  it('calls onClose on Escape keydown', () => {
    const onClose = vi.fn();
    render(<CompletedPromptsModal prompts={[]} onClose={onClose} />);
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose).toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npm test -- CompletedPromptsModal.test.tsx` (from `web/`)
Expected: FAIL — `Failed to resolve import "./CompletedPromptsModal"` (module doesn't exist yet).

- [ ] **Step 3: Write the component implementation**

Create `web/src/components/CompletedPromptsModal.tsx`:

```tsx
import { useEffect } from 'react';
import type { Prompt } from '../api';

function lastTwentyDone(prompts: Prompt[]): Prompt[] {
  return prompts
    .filter((p) => p.state === 'DONE')
    .sort((a, b) => (b.done_at ?? '').localeCompare(a.done_at ?? ''))
    .slice(0, 20);
}

export function CompletedPromptsModal({
  prompts,
  onClose,
}: {
  prompts: Prompt[];
  onClose: () => void;
}) {
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [onClose]);

  const done = lastTwentyDone(prompts);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={onClose}>
      <div
        className="max-h-[80vh] w-full max-w-lg overflow-auto rounded bg-white p-4 shadow-lg"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-2 flex items-center justify-between">
          <h3 className="font-medium">Completed Tasks</h3>
          <button onClick={onClose} className="text-sm text-gray-500 underline" aria-label="Close">
            Close
          </button>
        </div>
        {done.length === 0 && <p className="text-sm text-gray-500">No completed tasks yet.</p>}
        {done.length > 0 && (
          <ul className="space-y-3 text-sm">
            {done.map((p) => (
              <li key={p.id} className="border-t border-gray-100 pt-2 first:border-t-0 first:pt-0">
                <dl className="space-y-1">
                  <div className="flex justify-between">
                    <dt className="text-gray-500">Project</dt>
                    <dd>{p.project_name}</dd>
                  </div>
                  <div className="flex justify-between">
                    <dt className="text-gray-500">Task</dt>
                    <dd>{p.task_name}</dd>
                  </div>
                  <div>
                    <dt className="text-gray-500">Prompt</dt>
                    <dd className="whitespace-pre-wrap">{p.prompt_text}</dd>
                  </div>
                </dl>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npm test -- CompletedPromptsModal.test.tsx` (from `web/`)
Expected: PASS (all 9 tests).

- [ ] **Step 5: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add web/src/components/CompletedPromptsModal.tsx web/src/components/CompletedPromptsModal.test.tsx
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "feat: add CompletedPromptsModal component

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01UkzQSup9eKb96jc66Vjjqv"
```

---

### Task 2: Wire "Completed" link into `PromptsPanel`

**Files:**
- Modify: `web/src/components/PromptsPanel.tsx`
- Test: `web/src/components/PromptsPanel.test.tsx` (new file — no test currently exists for this component)

**Interfaces:**
- Consumes: `CompletedPromptsModal` from Task 1 (`{ prompts: Prompt[]; onClose: () => void }`); `getPrompts`, `createPrompt`, `updatePromptState`, `ApiError`, `Prompt`, `Project` from `../api` (already imported by this file).
- Produces: no new exports — `PromptsPanel` remains the default integration point rendered by `ProjectsPage.tsx`.

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/PromptsPanel.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import type { Prompt } from '../api';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockGetPrompts = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    getPrompts: (...args: unknown[]) => mockGetPrompts(...args),
    createPrompt: vi.fn(),
    updatePromptState: vi.fn(),
  };
});

beforeEach(() => vi.clearAllMocks());

const activePrompt: Prompt = {
  id: 1,
  project_name: 'soulman',
  task_name: 'active task',
  prompt_text: 'work in progress',
  state: 'IMPLEMENTING',
  created_at: '2026-09-01T00:00:00Z',
};

const donePrompt: Prompt = {
  id: 2,
  project_name: 'soulman',
  task_name: 'finished task',
  prompt_text: 'already done',
  state: 'DONE',
  created_at: '2026-09-01T00:00:00Z',
  done_at: '2026-09-02T00:00:00Z',
};

describe('PromptsPanel', () => {
  it('renders the Completed link at the bottom of the box', async () => {
    mockGetPrompts.mockResolvedValue([activePrompt, donePrompt]);
    const { PromptsPanel } = await import('./PromptsPanel');
    render(<PromptsPanel projects={[]} refreshProjects={vi.fn()} />);

    expect(await screen.findByText('Completed')).toBeInTheDocument();
  });

  it('opens the CompletedPromptsModal showing a DONE prompt when Completed is clicked', async () => {
    mockGetPrompts.mockResolvedValue([activePrompt, donePrompt]);
    const { PromptsPanel } = await import('./PromptsPanel');
    render(<PromptsPanel projects={[]} refreshProjects={vi.fn()} />);

    await waitFor(() => expect(mockGetPrompts).toHaveBeenCalled());
    fireEvent.click(screen.getByText('Completed'));

    expect(await screen.findByText('finished task')).toBeInTheDocument();
    expect(screen.getByText('Completed Tasks')).toBeInTheDocument();
  });

  it('closes the modal without disturbing the existing prompts table', async () => {
    mockGetPrompts.mockResolvedValue([activePrompt, donePrompt]);
    const { PromptsPanel } = await import('./PromptsPanel');
    render(<PromptsPanel projects={[]} refreshProjects={vi.fn()} />);

    await waitFor(() => expect(mockGetPrompts).toHaveBeenCalled());
    fireEvent.click(screen.getByText('Completed'));
    await screen.findByText('Completed Tasks');
    fireEvent.click(screen.getByLabelText('Close'));

    expect(screen.queryByText('Completed Tasks')).not.toBeInTheDocument();
    expect(screen.getByText('active task')).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npm test -- PromptsPanel.test.tsx` (from `web/`)
Expected: FAIL — no "Completed" text found in the rendered output.

- [ ] **Step 3: Modify `PromptsPanel.tsx`**

Add the import and state, and the link + modal render. Apply these changes to `web/src/components/PromptsPanel.tsx`:

Change the import block (currently lines 1-10) to add the modal import:

```tsx
import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import {
  getPrompts,
  createPrompt,
  updatePromptState,
  ApiError,
  type Prompt,
  type Project,
} from '../api';
import { CompletedPromptsModal } from './CompletedPromptsModal';
```

Add a new state variable alongside the existing ones (after the `promptText` state, currently line 28):

```tsx
  const [showCompleted, setShowCompleted] = useState(false);
```

Replace the closing of the component's returned JSX — currently:

```tsx
        <button onClick={handleAdd} className="self-start rounded bg-gray-800 px-3 py-1 text-sm text-white">
          Add Prompt
        </button>
      </div>
    </div>
  );
}
```

with:

```tsx
        <button onClick={handleAdd} className="self-start rounded bg-gray-800 px-3 py-1 text-sm text-white">
          Add Prompt
        </button>
        <button
          onClick={() => setShowCompleted(true)}
          className="self-start text-xs text-gray-500 underline"
        >
          Completed
        </button>
      </div>
      {showCompleted && (
        <CompletedPromptsModal prompts={prompts ?? []} onClose={() => setShowCompleted(false)} />
      )}
    </div>
  );
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npm test -- PromptsPanel.test.tsx CompletedPromptsModal.test.tsx` (from `web/`)
Expected: PASS (all tests in both files).

- [ ] **Step 5: Run the full frontend test suite**

Run: `npm test` (from `web/`)
Expected: PASS — no regressions in other components.

- [ ] **Step 6: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add web/src/components/PromptsPanel.tsx web/src/components/PromptsPanel.test.tsx
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "feat: add Completed link to Prompts box opening last-20-done popup

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01UkzQSup9eKb96jc66Vjjqv"
```

---

## Post-Implementation

- Update `CLAUDE.md`'s `web-svc`/`projects-svc` frontend description if it enumerates dashboard UI features in detail (check current wording — as of this plan's writing, the root `CLAUDE.md` does not enumerate individual Prompts-box UI affordances, so likely no change needed; confirm before skipping).
- Follow `superpowers:finishing-a-development-branch` to merge `feature/completed-prompts-popup` back to `main` once both tasks are committed and tests pass.
