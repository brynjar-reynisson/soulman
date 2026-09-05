# Completed Prompts Popup — Design

**Status:** Approved
**Date:** 2026-09-05

## Problem

The Projects tool's Prompts box (`PromptsPanel.tsx`) hides `DONE` prompts from its table entirely — there is currently no way to see what work has already been finished. The user wants a way to glance back at recently completed tasks: which project, which task, and what the original prompt text was.

## Goals

- A "Completed" link at the bottom of the Prompts box, styled like the existing "Refresh" link.
- Clicking it opens a popup listing the last 20 finished (`DONE`) prompts, most recently completed first, each showing project name, task name, and prompt text.
- No backend or schema changes — the frontend already fetches every prompt (including `DONE` ones) via `getPrompts()`; the popup derives its list client-side.

## Non-Goals

- No completion timestamp shown in the popup (available in the data via `done_at`, but not displayed — matches the minimal scope requested).
- No pagination beyond the fixed 20-row cap, no filtering/search within the popup.
- No change to the existing table's behavior of hiding `DONE` rows — this popup is purely additive.
- No server-side filtering endpoint. At current and expected scale, shipping the full prompt list to the browser (already happening today) and slicing client-side is sufficient; this can be revisited if the prompt table grows large enough to matter.

## Design

**`PromptsPanel.tsx`**: add a "Completed" text-link next to the existing "Refresh" link (same `text-xs text-gray-500 underline` styling). Clicking it sets `showCompleted` state to `true`, rendering a new `CompletedPromptsModal` conditionally, following the same conditional-render-by-parent pattern `RawInputsPanel.tsx` uses for `RawInputModal`.

**Derived list**: from the same `prompts` state already held by `PromptsPanel` (populated by the existing `getPrompts()` call — no new fetch), compute:

```
prompts
  .filter(p => p.state === 'DONE')
  .sort((a, b) => (b.done_at ?? '').localeCompare(a.done_at ?? ''))
  .slice(0, 20)
```

`done_at` is reliably stamped by the backend on the transition to `DONE` (`projects-svc/store/store.go:194`, covered by `store_test.go:216-220`) and already flows through the `Prompt` API type (`web/src/api.ts:296`) — it's used here only for sort order, not rendered.

**New component `web/src/components/CompletedPromptsModal.tsx`**, following `RawInputModal.tsx`'s established popup pattern:
- `fixed inset-0 z-50 flex items-center justify-center bg-black/50` overlay, `onClick={onClose}`.
- Inner box `max-h-[80vh] w-full max-w-lg overflow-auto rounded bg-white p-4 shadow-lg`, `onClick={(e) => e.stopPropagation()}`.
- Header row: "Completed Tasks" title + "Close" text-button.
- `Escape` key closes it (same `useEffect`/`keydown` listener as `RawInputModal`).
- Body: one block per prompt showing Project, Task, and Prompt text (e.g. a `<dl>` per row, consistent with `RawInputModal`'s key/value styling), separated by a top border between entries.
- Empty state: if the derived list has zero entries, show "No completed tasks yet." instead of the list.

**Props**: `{ prompts: Prompt[]; onClose: () => void }` — the modal receives the already-fetched full prompt list and does the filter/sort/slice itself (keeping the derivation logic colocated and unit-testable), rather than `PromptsPanel` passing down an already-derived array. Either placement is reasonable; colocating in the modal keeps `PromptsPanel` unchanged except for the trigger link and boolean state.

## Testing

- A unit test for the derivation logic (filter/sort/slice) — verifies: only `DONE` prompts included, ordered most-recently-completed first, capped at 20 even when more exist.
- A render test for `CompletedPromptsModal`: renders project/task/prompt for each row, shows the empty-state message when there are no `DONE` prompts, and calls `onClose` on the Close button, overlay click, and Escape key.
- A render test for `PromptsPanel`: clicking "Completed" opens the modal; existing "Refresh"/table behavior is unaffected.

## Architecture

Frontend-only change, entirely within the existing `web/` React app:

```
PromptsPanel.tsx
   │  "Completed" link click → setShowCompleted(true)
   ▼
CompletedPromptsModal.tsx (new)
   │  derives DONE-last-20 from the `prompts` prop
   │  (already fetched via existing getPrompts() call — no new API call)
   ▼
renders project / task / prompt for each row
```

No changes to `projects-svc` (backend), `web-svc` (proxy), `web/src/api.ts`, or the database schema.
