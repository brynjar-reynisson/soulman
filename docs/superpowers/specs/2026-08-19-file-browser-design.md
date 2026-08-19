# File Browser (Download/Upload) Design

**Status:** Approved
**Date:** 2026-08-19

## Problem

There's no way to grab an arbitrary file off the machine (or drop one onto
it) from the soulman dashboard — doing so today means being physically at
the machine. Unlike the Obsidian editor (scoped to `.txt`/`.md` vault
notes) or the Claude launcher (scoped to project folders you'd `cd` into),
this is meant for arbitrary files: photos, PDFs, archives, whatever's in
Documents or Downloads.

## Goals

- Browse a curated set of root folders, drilling into subfolders at
  arbitrary depth (unlike Obsidian's one-level-deep browsing).
- Download any file found while browsing, regardless of type or size.
- Upload a file into the currently-browsed folder.
- Reuse `web-svc`'s existing owner-only JWT auth — no new auth mechanism.

## Non-Goals (this iteration)

- Full filesystem access. Scoped to a configured allow-list of roots
  (`web.file_browser_roots`), same posture as `claude_project_roots` —
  not open to any absolute path. Starting roots: Documents, Downloads.
  More can be added later purely via config, no code change.
- Delete, rename, or folder creation. Upload + download + browse only —
  the smallest useful blast radius for a filesystem endpoint reachable
  over the internet via prod's `cloudflared` tunnel.
- Any file-extension restriction (unlike Obsidian's `.txt`/`.md` gate) —
  "any file" is the point.
- In-browser preview or editing of file contents.
- Conflict resolution beyond a simple overwrite flag (no versioning, no
  merge).

## Architecture

Same shape as the Obsidian and Claude features: no new service, an
incremental extension of `web-svc`'s existing owner-only-JWT pattern.

```
web/src/components/FilesPage.tsx
  ├─ FileRootList (pick a configured root)
  └─ FileBrowser (breadcrumb + folder/file list for the current path)
       ├─ per-file Download button
       └─ Upload control (adds to current folder)
  ▼ fetch (relative /api/files/*, same pattern as api.ts today)
web-svc/httpserver/files_handler.go
  ▼ calls
web-svc/filebrowser/filebrowser.go  (path validation + filesystem ops)
  ▼ reads/writes under
config: web.file_browser_roots  (Documents, Downloads, ...)
```

## Components

### Config

`common/sharedconfig.WebConfig` gains one field, following the exact
precedent `ClaudeProjectRoots` set (required to be non-empty; entries'
paths are not required to exist at startup — a missing root is reported
as such per-request):

```go
// FileBrowserRoot is one curated root the file browser (web-svc/filebrowser)
// offers for browsing, download, and upload: a human-readable label
// (matched against a request's "root" field) and the filesystem path it
// corresponds to. A distinct type from ClaudeProjectRoot even though the
// shape is identical — they represent different concerns (small
// independent duplication over a shared type, consistent with this
// repo's existing preference — see web-svc/NOTES.md).
type FileBrowserRoot struct {
    Label string `json:"label"`
    Path  string `json:"path"`
}

type WebConfig struct {
    // ...existing fields...
    FileBrowserRoots []FileBrowserRoot `json:"file_browser_roots"`
}
```

`web-svc/config/config.go`'s `Load()` gets one more required-non-empty
check, mirroring `ClaudeProjectRoots`'s. `config/dev.json` and
`config/prod.json` both add:

```json
"file_browser_roots": [
  { "label": "Documents", "path": "C:\\Users\\Lenovo\\Documents" },
  { "label": "Downloads", "path": "C:\\Users\\Lenovo\\Downloads" }
]
```

(Same absolute paths in both dev and prod configs — this is "my machine,"
not a per-environment resource, same treatment as `obsidian_root`.)

### `web-svc/filebrowser` (new package)

Mirrors `web-svc/claudesession`'s shape (`Root`/`RootListing`/`ListRoots`)
for the root-listing half, and `web-svc/obsidian`'s validated-path shape
for the browse/read/write half — extended to arbitrary depth, since
unlike Obsidian's one-level browsing this needs to drill into subfolders.

```go
package filebrowser

type Root struct {
    Label string
    Path  string
}

type RootListing struct {
    Label  string
    Path   string
    Exists bool
}

// ListRoots reports each configured root's current existence, mirroring
// claudesession.ListRoots — a temporarily missing root is reported as
// such, not omitted or treated as an error.
func ListRoots(roots []Root) []RootListing

type FileInfo struct {
    Name string
    Size int64
}

// List returns the subfolder names and files (name + size) directly
// inside root/relPath, sorted. relPath is "" for the root itself, or a
// "/"-joined relative path (e.g. "Projects/2026") for a subfolder —
// always "/"-joined regardless of OS, since this is the wire format the
// frontend sends; segments are validated individually before any
// filesystem call. Returns ErrNotFound if relPath doesn't resolve to an
// existing directory.
func List(root Root, relPath string) (folders []string, files []FileInfo, err error)

// ResolveFile validates relPath (a folder) and filename (a single path
// segment) and returns the file's absolute path for a caller to stream
// (e.g. via http.ServeFile). Returns ErrNotFound if it doesn't exist.
func ResolveFile(root Root, relPath, filename string) (string, error)

// Save writes r's contents as relPath/filename. relPath's folder must
// already exist (no folder creation in this iteration) — returns
// ErrNotFound otherwise. Returns ErrExists if filename already exists
// and overwrite is false.
func Save(root Root, relPath, filename string, r io.Reader, overwrite bool) error
```

**Path validation** (`resolveDir` internal helper, shared by `List`,
`ResolveFile`, and `Save`):

1. Split `relPath` on `/`; each segment must pass the same `validSegment`
   check `obsidian`/`claudesession` already use (non-empty, no `/`/`\`,
   not `.`/`..`, `filepath.IsLocal`) — rejects traversal and NTFS
   alternate-data-stream tricks per segment, not just on the whole
   string (a single check against the raw joined string would miss
   e.g. `good/../../../windows` if some segment were individually
   otherwise-legal).
2. Join segments onto `root.Path` one at a time via `filepath.Join`.
3. Defense in depth: after joining, confirm the result is still
   contained within `filepath.Clean(root.Path)` via the same
   `filepath.Rel`-based `isWithin` check as the other two packages.
4. `filename` (in `ResolveFile`/`Save`) gets the identical single-segment
   `validSegment` check, then the same joined-and-`isWithin`-checked
   defense in depth against the resolved directory. **No extension
   check** — this is the one deliberate difference from `obsidian`'s
   `hasValidExtension` gate.

This matters here for the same reason it matters for Obsidian: prod's
dashboard is reachable from the public internet via the `cloudflared`
tunnel, owner-JWT-gated but not a substitute for filesystem-level
containment.

### `web-svc/httpserver` additions

New file `web-svc/httpserver/files_handler.go`, following
`obsidian_handler.go`'s style: thin handlers calling into `filebrowser`,
mapping its sentinel errors to HTTP status the same way (`ErrNotFound`
→404, `ErrExists`→409, `ErrInvalidName`→400, anything else→500). New
routes, all inside the existing `r.Use(s.verifier.Middleware)` group:

| Method | Path | Body/Query | Response |
|---|---|---|---|
| GET | `/api/files/roots` | — | `{"roots": [{"label","path","exists"}]}` |
| GET | `/api/files/list?root=X&path=Y` | — | `{"folders": [...], "files": [{"name","size"}]}` |
| GET | `/api/files/download?root=X&path=Y&file=Z` | — | file bytes, `Content-Disposition: attachment` |
| POST | `/api/files/upload?root=X&path=Y&overwrite=bool` | multipart, field `file` | 200 empty / 409 if exists and not overwriting |

`root` is matched against configured labels server-side exactly like
Claude's `findClaudeRoot` — an unrecognized label is a 400 before any
path resolution happens.

**Download** uses `http.ServeFile(w, r, absPath)` after
`filebrowser.ResolveFile` resolves and validates the path — this gets
correct `Content-Type` sniffing, `Content-Length`, and HTTP Range
support (resumable/partial downloads) for free, rather than a manual
`io.Copy`. The handler sets `Content-Disposition: attachment;
filename="..."` before calling `ServeFile` so the browser downloads
rather than navigates.

**Upload** is the one genuinely new concern relative to the existing
features: `web-svc` has no request-body size limiting anywhere today
(fine for small JSON/markdown bodies, not fine for arbitrary uploads on
a publicly-tunneled endpoint). The upload handler wraps the request body
in `http.MaxBytesReader(w, r.Body, maxUploadBytes)` (a package-level
`const maxUploadBytes = 200 << 20 // 200MB` in `files_handler.go`)
before calling `r.ParseMultipartForm`; exceeding it surfaces as a 413.
This constant is deliberately hardcoded rather than added to the config
schema — bump it in code if 200MB turns out to be wrong, matching the
"no cap at all, add one if it becomes a real problem" YAGNI precedent
Obsidian set, just starting from *some* cap instead of none given this
is binary/large-file upload rather than markdown notes.

### Frontend (`web/src`)

**`api.ts`** — new typed functions:

```ts
export interface FileBrowserRootListing {
  label: string;
  path: string;
  exists: boolean;
}
export interface FileEntry { name: string; size: number; }
export interface FileListing { folders: string[]; files: FileEntry[]; }

export const getFileBrowserRoots = (token): Promise<{roots: FileBrowserRootListing[]}> =>
  getJSON('/api/files/roots', token);

export const listFiles = (token, root: string, path: string): Promise<FileListing> =>
  getJSON(`/api/files/list?root=${encodeURIComponent(root)}&path=${encodeURIComponent(path)}`, token);

export async function downloadFile(token, root: string, path: string, file: string): Promise<void> {
  const response = await fetch(
    `/api/files/download?root=${encodeURIComponent(root)}&path=${encodeURIComponent(path)}&file=${encodeURIComponent(file)}`,
    { headers: token ? { Authorization: `Bearer ${token}` } : {} },
  );
  if (!response.ok) throw new ApiError(response.status, `download failed (${response.status})`);
  const blob = await response.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = file;
  a.click();
  URL.revokeObjectURL(url);
}

export async function uploadFile(
  token, root: string, path: string, file: File, overwrite: boolean,
): Promise<void> {
  const formData = new FormData();
  formData.append('file', file);
  const response = await fetch(
    `/api/files/upload?root=${encodeURIComponent(root)}&path=${encodeURIComponent(path)}&overwrite=${overwrite}`,
    { method: 'POST', headers: token ? { Authorization: `Bearer ${token}` } : {}, body: formData },
  );
  if (!response.ok) throw new ApiError(response.status, `upload failed (${response.status})`);
}
```

`downloadFile`/`uploadFile` don't reuse `getJSON`/`mutateJSON` — download
returns a `Blob` (needs the click-a-synthetic-`<a>` dance since a plain
`<a href>` can't carry the Bearer token), and upload sends `FormData`
(the browser sets the multipart boundary itself; an explicit
`Content-Type` header must NOT be set or the boundary breaks).

**`App.tsx`** — `ViewState` gains `'files'`; `viewFromPageParam` gets a
`page === 'files'` branch. `Dashboard` gets a "Files" button next to
"Claude"/"Obsidian"/"Sign out", wired to `setParams({ page: 'files' })`.

**`components/FilesPage.tsx`** (new) — top-level page: back link, error
banner, `FileRootList`.

**`components/FileRootList.tsx`** (new) — fetches `getFileBrowserRoots`
on mount, renders each as a button (disabled/greyed if `!exists`,
mirroring `ClaudeRootList`'s existing treatment of missing roots).
Selecting one sets `selectedRoot` state and renders `FileBrowser`.

**`components/FileBrowser.tsx`** (new) — given `{root}`, holds
`currentPath` state (`string`, `""` = root itself). Fetches `listFiles`
on mount and whenever `currentPath` changes. Renders:
- Breadcrumbs built by splitting `currentPath` on `/` (root label as the
  first crumb; clicking any crumb truncates `currentPath` to that
  depth and re-fetches).
- Each folder as a button that appends its name to `currentPath`.
- Each file as a row with its name, human-readable size, and a Download
  button calling `downloadFile`.
- An upload control (`<input type="file">` + a Save button) that calls
  `uploadFile` with the selected `File` object and the current path;
  on a 409 it shows an inline "already exists — replace?" confirm that
  retries with `overwrite=true`.

Current path (root + path) is persisted to the URL query string via the
existing `urlState.ts` helpers (`?page=files&fileRoot=X&filePath=Y`),
matching Obsidian's/Claude's precedent so a reload doesn't lose place.

## Data Flow (browse + download + upload example)

1. User opens the Files page, sees "Documents" / "Downloads" as root
   buttons (both `exists: true`).
2. Clicks "Documents" → `GET /api/files/list?root=Documents&path=` →
   breadcrumb shows "Documents", folder/file list appears.
3. Clicks a subfolder "Taxes" → `currentPath` becomes `"Taxes"` →
   `GET /api/files/list?root=Documents&path=Taxes` → breadcrumb becomes
   "Documents / Taxes".
4. Clicks Download on `2025-return.pdf` →
   `GET /api/files/download?root=Documents&path=Taxes&file=2025-return.pdf`
   → browser downloads the file via the blob/synthetic-`<a>` path.
5. Selects a local file, clicks upload → `POST
   /api/files/upload?root=Documents&path=Taxes&overwrite=false` with the
   file as multipart body → 200 → list re-fetches, new file appears.

## Error Handling & Edge Cases

- **Path traversal attempt** (e.g. `path=..%2F..%2Fwindows` or a segment
  like `..`): rejected by `resolveDir`'s per-segment `validSegment`
  check before any filesystem call, same guarantee as Obsidian/Claude.
- **Root label not recognized**: 400 before any path resolution, mirrors
  Claude's `findClaudeRoot`.
- **Download a file that's been deleted/moved since the list was
  fetched**: `ResolveFile` returns `ErrNotFound` → 404, frontend shows an
  inline error and re-fetches the listing.
- **Upload when a file with that name already exists and `overwrite`
  wasn't set**: 409, frontend prompts to confirm replace, retries with
  `overwrite=true` — no filesystem mutation attempted on the first,
  rejected call (check-then-act, an accepted narrow TOCTOU race given
  this is a single-owner tool, same acceptance Obsidian's rename/create
  already made).
- **Upload exceeding the 200MB cap**: `MaxBytesReader` trips, handler
  returns 413, frontend shows an inline error naming the limit.
- **Upload target folder doesn't exist** (e.g. deleted mid-navigation):
  `resolveDir` (shared with `List`) returns `ErrNotFound` → 404 — no
  folder auto-creation, matching the explicit "no folder creation"
  non-goal.
- **Very large directory listing**: no pagination in this iteration —
  Documents/Downloads are expected to be human-sized directories; add
  pagination later if it becomes a real problem (YAGNI, same posture as
  Obsidian's no-size-cap call).

## Testing

- `web-svc/filebrowser`: table tests for path validation (rejects `..`
  in any segment position, `/`/`\` inside a segment, empty segments,
  NTFS-colon segments) using a `t.TempDir()`-rooted fixture tree with
  nested subfolders — extends `obsidian_test.go`'s style to
  multi-segment paths. Separate tests for `Save`'s overwrite-vs-409
  behavior and `ResolveFile`'s not-found case.
- `web-svc/httpserver/files_handler_test.go`: handler-level tests for
  status codes and JSON shapes per route, including the 413 boundary
  (a body one byte over `maxUploadBytes`) — mirrors
  `obsidian_handler_test.go`.
- `web/src/components/*.test.tsx`: `FileRootList` (disabled state for a
  missing root), `FileBrowser` (breadcrumb navigation, folder drill-down,
  download triggers a click on a synthetic anchor, upload conflict →
  confirm → retry-with-overwrite flow) — mirrors
  `ObsidianFileList.test.tsx`'s use of a mocked `api.ts`.
- Manual: exercise the full flow against dev (`localhost:5190`) — browse
  into a nested subfolder, download a real file and confirm its bytes
  match, upload a file, upload the same name again and confirm the
  overwrite prompt, refresh and confirm the URL restores position —
  before treating this as done.
