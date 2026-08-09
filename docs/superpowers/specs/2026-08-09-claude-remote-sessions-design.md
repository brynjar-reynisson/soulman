# Claude Remote Sessions — Design

**Status:** Approved
**Date:** 2026-08-09

## Problem

The Soulman web dashboard currently exposes read/edit access to the Obsidian vault but has no way to start development work remotely. The user wants to launch a Claude Code session — configured for remote control (accessible later from mobile/tablet via `claude.ai/code`) — against one of their existing project folders, from the dashboard itself, without needing to be at the machine's terminal.

## Goals

- A "Claude" link in the web dashboard, to the left of the existing "Obsidian" link.
- A curated, grouped list of project folders drawn from three roots on the local filesystem:
  - `C:\Users\Lenovo\Documents\obsidian` (label: "Obsidian")
  - `C:\Users\Lenovo\IdeaProjects` (label: "IdeaProjects")
  - `C:\Users\Lenovo\misc_projects` (label: "Misc Projects")
- Picking a folder lets the user set a session name (defaulting to the folder name, editable) and launch a Claude Code remote-control session rooted in that folder.
- Launching runs `claude --remote-control --name "<session-name>"` with its working directory set to the selected folder, as a detached background process on the machine web-svc runs on.
- Works from mobile/tablet — the dashboard is already responsive; this feature adds no new device constraints.

## Non-Goals

- No tracking, listing, or stopping of launched sessions from the dashboard. Session lifecycle after launch is entirely owned by Claude Code's own remote-control mechanism (`claude.ai/code`) — soulman's job ends at successfully starting the process.
- No capture of the launched process's stdout/stderr. True fire-and-forget; if a launch fails silently after the initial `Start()` succeeds, there is nothing to inspect after the fact.
- No support for arbitrary/uncurated folder paths — only direct subfolders of the three configured roots are launchable, enforced server-side.
- No per-environment distinction — dev and prod `web-svc` both offer this feature against the same physical folders (mirrors how `obsidian_root` is already identical in both `config/dev.json` and `config/prod.json`).

## Architecture

```
Browser (mobile/tablet)
   │  GET  /api/claude/roots
   │  POST /api/claude/launch  {root, folder, sessionName}
   ▼
web-svc (httpserver, JWT + owner-email authed route group)
   ▼
claudesession package (pure functions, root passed explicitly)
   ├─ ListRoots(roots []Root) []RootListing
   └─ Launch(root Root, folder, sessionName string) error
          → exec.Command("claude", "--remote-control", "--name", sessionName)
              Dir: <root.Path>\<folder>
              Windows: SysProcAttr.CreationFlags = CREATE_NEW_PROCESS_GROUP
              Stdout/Stderr: discarded (nil)
              cmd.Process.Release() immediately after Start()
```

No new services, no NATS involvement, no database. Structurally mirrors the existing Obsidian file viewer feature (`web-svc/obsidian/`, `web-svc/httpserver/obsidian_handler.go`): a small pure Go package doing the real work, thin HTTP handlers mapping sentinel errors to status codes, and a React page reachable via a nav link.

## Components

### Config

New field on `sharedconfig.WebConfig` (`common/sharedconfig/config.go`):

```go
// ClaudeProjectRoots lists the curated project-folder roots the Claude
// remote-session launcher offers. A root missing from disk is reported
// as such in the API response, not a startup error.
ClaudeProjectRoots []ClaudeProjectRoot `json:"claude_project_roots"`

type ClaudeProjectRoot struct {
    Label string `json:"label"`
    Path  string `json:"path"`
}
```

Added identically to `config/dev.json` and `config/prod.json`'s `"web"` block:

```json
"claude_project_roots": [
  {"label": "Obsidian", "path": "C:\\Users\\Lenovo\\Documents\\obsidian"},
  {"label": "IdeaProjects", "path": "C:\\Users\\Lenovo\\IdeaProjects"},
  {"label": "Misc Projects", "path": "C:\\Users\\Lenovo\\misc_projects"}
]
```

Unlike `ObsidianRoot`, this field is **not** required to point at existing, reachable directories at startup — `web-svc/config/config.go`'s `Load()` only validates the field is present and non-empty (at least one root configured), not that each `Path` exists on disk. Existence is checked per-request by `ListRoots` so a temporarily-missing root degrades to "shown but empty" rather than failing config load or the whole endpoint.

### `web-svc/claudesession/claudesession.go`

Mirrors the style of `web-svc/obsidian/obsidian.go`.

```go
type Root struct {
    Label string
    Path  string
}

type RootListing struct {
    Label   string
    Path    string
    Exists  bool
    Folders []string // nil when Exists is false
}

func ListRoots(roots []Root) []RootListing
func Launch(root Root, folder, sessionName string) error
```

- `ListRoots`: for each configured root, `os.Stat`. Not found or not a directory → `{Exists: false, Folders: nil}`. Otherwise `os.ReadDir`, filtered to directory entries, sorted case-insensitively, → `{Exists: true, Folders: [...]}`.
- `Launch`: validates `folder` is a single path segment (no `..`, no path separators, no `:`, non-empty) using the same `validSegment`-style guard as `obsidian.resolveFolder`, rejecting path traversal. Resolves `root.Path` + `folder` to an absolute directory and confirms it exists and is a directory (`ErrNotFound` otherwise). Builds `exec.Command("claude", "--remote-control", "--name", sessionName)` with `Dir` set to that resolved path. On Windows, sets `SysProcAttr.CreationFlags = syscall.CREATE_NEW_PROCESS_GROUP` so the child process is not tied to web-svc's own process group and survives a web-svc restart. `Stdout`/`Stderr` are left `nil` (Go connects them to the OS null device). Calls `cmd.Start()`; a failure here (e.g. `claude` not found on `PATH`) is returned as `ErrLaunchFailed` wrapping the underlying error. On success, calls `cmd.Process.Release()` immediately so Go retains no handle and does not wait on the child.
- `sessionName` is passed as a literal argument via `exec.Command`, never through a shell, so there is no shell-injection risk regardless of its contents. No validation of `sessionName` is performed beyond non-empty — if the user manages to make Claude Code itself reject a bad name, that surfaces as a non-zero-but-unobserved exit, consistent with the fire-and-forget non-goal above.

### `web-svc/httpserver` additions

Two routes added to the existing authed group in `server.go`, following the `writeObsidianError`-style sentinel-to-status mapping:

| Method | Path | Request body | Response |
|---|---|---|---|
| GET | `/api/claude/roots` | — | `200 {roots: [{label, path, exists, folders: []string}]}` |
| POST | `/api/claude/launch` | `{root: string, folder: string, sessionName: string}` | `204` on successful spawn; `400` invalid root label / invalid folder segment / empty sessionName; `404` folder does not exist; `500` process failed to start |

`root` in the launch request is the configured `Label` (e.g. `"Obsidian"`), validated against `cfg.ClaudeProjectRoots` server-side — an unrecognized label is a `400`, not a lookup into arbitrary paths.

### Frontend (`web/src/`)

- `Dashboard.tsx`: new `<button onClick={onOpenClaude}>Claude</button>` placed immediately left of the existing Obsidian button in the header.
- `App.tsx`: `ViewState` gains `'claude'`; new `onOpenClaude`/`onBack` wiring identical in shape to the existing Obsidian view-switch.
- `urlState.ts`: persists `page=claude` and the currently-expanded root label in the query string (same rationale as the Obsidian page — a mobile reload must not lose your place).
- `ClaudePage.tsx`: fetches `GET /api/claude/roots` on mount. Renders three fixed-order accordion sections (Obsidian / IdeaProjects / Misc Projects, in configured order — not alphabetical). A section whose `exists` is `false` renders a greyed-out "not found" row and cannot be expanded. An existing, expanded section lists its `folders` as buttons.
- Clicking a folder opens an inline form beneath it: a text input for the session name (defaulted to the folder name, fully editable) and a "Launch" button.
- On submit: `POST /api/claude/launch`. Success shows a transient inline confirmation ("Session '<name>' launched"); failure shows the server's error message inline. No navigation away from `ClaudePage` — the user can launch another session immediately after.
- `web/src/api.ts`: typed `getClaudeRoots(): Promise<{roots: ClaudeRootListing[]}>` and `launchClaudeSession(root: string, folder: string, sessionName: string): Promise<void>`, following the existing relative-path (`/api/...`) convention proxied by Vite in dev.

## Data Flow

1. User opens the dashboard, clicks "Claude".
2. `ClaudePage` calls `GET /api/claude/roots`. web-svc's handler calls `claudesession.ListRoots(cfg.ClaudeProjectRoots)`, returns per-root existence + folder lists.
3. User expands "IdeaProjects", sees its subfolders, clicks one (e.g. `digital-me`).
4. Inline form appears, session name pre-filled `digital-me`. User optionally edits it, clicks "Launch".
5. `POST /api/claude/launch {root: "IdeaProjects", folder: "digital-me", sessionName: "digital-me"}`.
6. Handler calls `claudesession.Launch`, which validates the folder segment, resolves `C:\Users\Lenovo\IdeaProjects\digital-me`, and starts `claude --remote-control --name "digital-me"` detached with that working directory.
7. `204` returned on successful `Start()`. Dashboard shows confirmation. The Claude Code process registers itself for remote control independently — the user accesses it later via `claude.ai/code`, entirely outside soulman.

## Error Handling & Edge Cases

- **Root missing from disk** (e.g. `misc_projects` temporarily unmounted or renamed): `ListRoots` reports `exists: false`; UI shows it as present-but-unavailable, not hidden, not a page-level error.
- **Folder deleted between list and launch**: `Launch` re-checks existence at launch time and returns `ErrNotFound` → `404`, surfaced inline in the launch form.
- **Path traversal attempt** (`folder` containing `..`, separators, or `:`): rejected as `400` before any filesystem access, same guard style as the Obsidian package's NTFS-ADS protection.
- **`claude` not on `PATH`** for the account web-svc runs as: `cmd.Start()` fails, returned as `500` with the underlying OS error message. This is the only failure signal available given the fire-and-forget/no-log-capture decision — if remote control never shows up despite a `204` response, the user checks Task Manager or their own terminal, not soulman.
- **Duplicate session names / concurrent launches**: not deduplicated or rate-limited. Launching the same name twice, or many sessions at once, is allowed — Claude Code's own remote-control registration is responsible for handling name collisions, if any.
- **Empty `sessionName`**: rejected `400` before spawning.

## Testing

- `claudesession` package: unit tests for `ListRoots` (existing root with folders, missing root, root that's a file not a directory, empty root) and for `Launch`'s path-validation guard (traversal segments, absolute paths, `:` rejected) using `t.TempDir()` fixtures — the actual `exec.Command("claude", ...)` call is not exercised in these tests (no fake binary), only the validation and path-resolution logic ahead of it.
- `httpserver` handlers: table-driven tests asserting the sentinel-error → status-code mapping, using a fake `claudesession`-shaped interface or by pointing `ClaudeProjectRoots` at temp-dir fixtures.
- Frontend: no automated tests added, consistent with the existing Obsidian page's testing posture (manual verification against a running dev instance).
- End-to-end verification (manual, cannot be automated in CI): with dev `web-svc` running, open the dashboard, click Claude, launch a session against a real folder, confirm a `claude --remote-control` process appears in Task Manager with the correct working directory and process name argument.
