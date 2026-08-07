# Obsidian File Viewer/Editor Design

**Status:** Approved
**Date:** 2026-08-07

## Problem

The soulman dashboard (`web-svc`/`web`) currently only surfaces data the
backend services already produce (status, episodes, raw inputs, reports).
There's no way to browse or edit the plain-text/Markdown vaults that live
under `C:\Users\Lenovo\Documents\obsidian` (currently `brynjar-obsidian`,
`soulman`, `tildra`) from the dashboard — doing so today means being at a
machine with a file browser and text editor, which rules out editing from
a phone via the tunneled prod dashboard.

## Goals

- Browse the folders directly under the obsidian root, and the `.txt`/
  `.md` files directly inside a selected folder (one level deep — no
  recursive subfolder browsing).
- View a selected file's content: `.md` rendered as Markdown, `.txt` as
  plain text.
- Edit an existing file's content (plain-text editor, explicit save or
  discard).
- Create a new `.txt`/`.md` file in a folder, and rename an existing one.
- Reuse `web-svc`'s existing owner-only auth — no new auth mechanism.

## Non-Goals (this iteration)

- Deleting files or folders.
- Creating, renaming, or deleting folders.
- Recursing into subfolders beneath the top-level vault folders.
- Any Markdown *editing* affordance beyond raw text (no WYSIWYG, no
  preview-while-editing split view).
- Concurrent-edit conflict detection (last write wins, same as opening the
  same file in two editors today).

## Architecture

No new service. `web-svc` already reads files off disk for the reports
feature and is the only service with the owner-only JWT auth wired up;
this is an incremental extension of the same pattern.

```
web/src/components/ObsidianPage.tsx
  ├─ FolderList (accordion)
  │    └─ FileList (per expanded folder)
  │         ├─ FileViewer (rendered .md / plain .txt, pen "Edit" icon)
  │         └─ FileEditor (textarea, Save icon + X/discard icon)
  ▼ fetch (relative /api/obsidian/*, same pattern as api.ts today)
web-svc/httpserver/obsidian_handler.go
  ▼ calls
web-svc/obsidian/obsidian.go  (path validation + filesystem ops)
  ▼ reads/writes under
config: web.obsidian_root  (C:\Users\Lenovo\Documents\obsidian)
```

## Components

### Config

`common/sharedconfig.WebConfig` gains one required field, consistent with
the existing comment that "every field here is required — web-svc has no
degraded partially configured mode":

```go
type WebConfig struct {
    OwnerEmail        string `json:"owner_email"`
    CORSAllowedOrigin string `json:"cors_allowed_origin"`
    PerceptionSvcURL  string `json:"perception_svc_url"`
    MemorySvcURL      string `json:"memory_svc_url"`
    ThinkingSvcURL    string `json:"thinking_svc_url"`
    ActionSvcURL      string `json:"action_svc_url"`
    ObsidianRoot      string `json:"obsidian_root"`
}
```

`web-svc/config/config.go`'s `Load()` gets one more required-field check,
mirroring the existing five. `config/dev.json` and `config/prod.json` both
set `"obsidian_root": "C:\\Users\\Lenovo\\Documents\\obsidian"` (same
absolute path in both — this isn't a per-environment resource, unlike
`ReportsRoot`/`SoulmanRoot`).

### `web-svc/obsidian` (new package)

Mirrors `web-svc/reports`'s shape: plain functions taking the root path
explicitly, no package-level state.

```go
package obsidian

// ListFolders returns directory names directly under root, sorted.
func ListFolders(root string) ([]string, error)

// ListFiles returns .txt/.md filenames directly inside root/folder, sorted.
func ListFiles(root, folder string) ([]string, error)

// ReadFile returns the content of root/folder/file.
func ReadFile(root, folder, file string) (string, error)

// WriteFile overwrites an existing file's content. Returns ErrNotFound
// if it doesn't already exist.
func WriteFile(root, folder, file, content string) error

// CreateFile creates a new file. Returns ErrExists if it already exists.
func CreateFile(root, folder, file, content string) error

// RenameFile renames a file within the same folder. Returns ErrNotFound
// if the source doesn't exist, ErrExists if the destination does.
func RenameFile(root, folder, oldName, newName string) error
```

**Path validation** (`resolvePath` internal helper, called by every
function above before touching the filesystem):

1. `folder`, `file`/`oldName`/`newName` must be non-empty and must not
   contain `/`, `\`, or `..` as a path segment — rejects traversal
   outright rather than relying on cleanup.
2. File names (not folder names) must end in `.txt` or `.md`
   (case-insensitive).
3. Defense in depth: after `filepath.Join(root, folder, [file])` and
   `filepath.Clean`, verify the result still has `filepath.Clean(root)`
   as a path prefix before any read/write/rename call.

This matters more than it would for a purely local tool because prod's
dashboard is reachable from the public internet via the `cloudflared`
tunnel (owner-JWT-gated, but a path-traversal bug in a file-write endpoint
would be a serious hole regardless).

### `web-svc/httpserver` additions

`server.go`'s `buildRouter`: CORS `AllowedMethods` gains `POST`, `PUT`
(currently `GET`, `OPTIONS` only). New routes, all inside the existing
`r.Use(s.verifier.Middleware)` group:

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/obsidian/folders` | — | `{"folders": ["brynjar-obsidian", "soulman", "tildra"]}` |
| GET | `/api/obsidian/files?folder=X` | — | `{"files": ["NOTES.md", "todo.txt"]}` |
| GET | `/api/obsidian/file?folder=X&file=Y` | — | `{"content": "..."}` |
| PUT | `/api/obsidian/file` | `{"folder","file","content"}` | 200 empty / 404 if missing |
| POST | `/api/obsidian/file` | `{"folder","file","content"}` | 200 empty / 409 if exists |
| POST | `/api/obsidian/file/rename` | `{"folder","file","new_name"}` | 200 empty / 404 / 409 |

New file `web-svc/httpserver/obsidian_handler.go`, following
`reports_handler.go`'s style: thin handlers that call into the `obsidian`
package and map its sentinel errors (`ErrNotFound`, `ErrExists`,
validation errors) to HTTP status via `writeJSONError` (404/409/400
respectively); anything else is 500.

### Frontend (`web/src`)

**`api.ts`** — new typed functions following the existing `getJSON`
pattern, plus a `postJSON`/`putJSON` helper (doesn't exist yet — every
current call is a GET):

```ts
export const getObsidianFolders = (token): Promise<{folders: string[]}> => ...
export const getObsidianFiles = (token, folder): Promise<{files: string[]}> => ...
export const getObsidianFile = (token, folder, file): Promise<{content: string}> => ...
export const saveObsidianFile = (token, folder, file, content): Promise<void> => ...   // PUT
export const createObsidianFile = (token, folder, file, content): Promise<void> => ... // POST
export const renameObsidianFile = (token, folder, file, newName): Promise<void> => ... // POST rename
```

**`App.tsx`** — `ViewState` gains `'obsidian'`. `Dashboard` gets an
"Obsidian" link in its header next to "Sign out" that flips `App`'s view
state; the new page renders instead of `Dashboard`, with its own
"← Soulman" back link at the top that flips it back. No routing library
added — same pattern as the existing `loading`/`login`/`restricted`/
`dashboard` state machine.

**`components/ObsidianPage.tsx`** (new) — top-level page: back link,
error banner, and the `FolderList`.

**`components/ObsidianFolderList.tsx`** (new) — fetches
`getObsidianFolders` on mount. Renders each folder as a button; clicking
one sets `expandedFolder` state (selecting a different folder replaces
it, collapsing whichever was open — a single `string | null`, not a set).
The expanded folder renders `ObsidianFileList` beneath it.

**`components/ObsidianFileList.tsx`** (new) — fetches `getObsidianFiles`
for its folder on mount/when the folder changes. Renders each filename as
a button (selects it for viewing) plus a small rename icon next to it
(inline rename: click → filename becomes a text input + confirm/cancel).
A "+ New file" control at the bottom opens an inline filename input;
submitting calls `createObsidianFile` with empty content and selects the
new file for editing immediately (skip an empty view step). Selecting a
file sets `selectedFile` state (shared with `ObsidianFileList`'s parent,
`ObsidianPage`, via a callback prop — same lift-state-up pattern
`ReportsPanel`/`Dashboard` already use, no context/store needed for a
tree this shallow).

**`components/ObsidianFileViewer.tsx`** (new) — given `{folder, file}`,
fetches `getObsidianFile`, and renders:
- `.md`: via `react-markdown` (new dependency — renders to React elements
  directly, no `dangerouslySetInnerHTML`, so no XSS surface even though
  vault content is otherwise trusted).
- `.txt`: `<pre className="whitespace-pre-wrap">`, same as `ReportsPanel`
  today.

Header has the filename and a pen-icon "Edit" button (inline SVG, title
attribute for the tooltip — no icon library added, matching the rest of
`web/`'s zero-icon-dependency style today). Clicking it swaps to
`ObsidianFileEditor`.

**`components/ObsidianFileEditor.tsx`** (new) — `<textarea>` seeded with
the already-fetched content (no extra fetch), full width/height, plus
exactly two controls: a save icon (calls `saveObsidianFile`, then hands
control back to the viewer with the new content) and an X icon (discards
edits, hands control back to the viewer with the *original* content,
unchanged). No autosave, no other buttons — matches the requirement
directly.

## Data Flow (create + edit example)

1. User opens the Obsidian page, sees `brynjar-obsidian` / `soulman` /
   `tildra`.
2. Clicks `soulman` → `GET /api/obsidian/files?folder=soulman` → file list
   appears (e.g. `CLAUDE.md`, `NOTES.md`, ...).
3. Clicks `+ New file`, types `scratch.md` → `POST /api/obsidian/file`
   `{folder: "soulman", file: "scratch.md", content: ""}` → 200 → file
   list refreshes, `scratch.md` auto-selected in edit mode.
4. Types content, clicks Save → `PUT /api/obsidian/file` with the same
   folder/file and the typed content → 200 → viewer shows the rendered
   Markdown.
5. Later, clicks the pen icon → editor reopens with current content;
   clicks X instead of Save → viewer reappears unchanged, no request
   sent.

## Error Handling & Edge Cases

- **Root folder listing**: only directories are listed (the stray `.zip`
  backup files currently sitting at the obsidian root are filtered out
  since they aren't directories); no dotfile/dotfolder filtering beyond
  that — auto-discovery means whatever's there shows up.
- **Non-`.txt`/`.md` files inside a folder**: simply not listed (e.g. a
  vault's `.obsidian/` config directory and any images never appear,
  since `ListFiles` only lists files directly in the folder matching the
  two extensions, and never recurses into subdirectories at all).
- **Path traversal attempt** (e.g. `folder=..%2F..%2Fwindows`): rejected
  by `resolvePath`'s segment check before any filesystem call, regardless
  of URL-encoding — Go's `net/url` decodes query params before the
  handler ever sees them, so validation happens on the decoded string.
- **Create when file already exists** / **rename onto an existing name**:
  409, frontend shows an inline error, no filesystem mutation attempted
  in the first place (`CreateFile`/`RenameFile` check-then-act — accepted
  as a narrow TOCTOU race given this is a single-owner tool, not a
  multi-tenant system).
- **Edit a file that was deleted/moved outside the dashboard between view
  and save**: `WriteFile`'s `ErrNotFound` surfaces as a 404, frontend
  shows an inline error and stays in edit mode so the user doesn't lose
  their typed content.
- **Very large file**: no size cap in this iteration — vault notes are
  expected to be small; add one later if it becomes a real problem
  (YAGNI).

## Testing

- `web-svc/obsidian`: table tests for path validation (rejects `..`, `/`,
  `\`, empty, wrong extension) and for each function's happy path +
  not-found/exists error using a `t.TempDir()`-rooted fixture tree —
  mirrors `reports_test.go`'s style.
- `web-svc/httpserver/obsidian_handler_test.go`: handler-level tests
  asserting status codes and JSON shapes for each route, including the
  404/409/400 mapping — mirrors `reports_handler_test.go`.
- `web/src/components/*.test.tsx`: `ObsidianFolderList` (accordion
  collapse-on-select), `ObsidianFileViewer`/`ObsidianFileEditor` (view ↔
  edit swap, save updates displayed content, X discards), create/rename
  inline-form flows — mirrors `EpisodesPanel.test.tsx`/
  `RawInputModal.test.tsx`'s use of a mocked `api.ts`.
- Manual: exercise the full flow against dev (`localhost:5190`) — browse,
  view a `.md` and a `.txt`, create, edit + save, rename, refresh and
  confirm persistence — before treating this as done.
