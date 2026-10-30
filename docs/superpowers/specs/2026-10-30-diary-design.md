# Diary — Design

## Intent

Replace the standalone Diary app (`IdeaProjects/diary`: Node backend, React frontend, Android app) with a page inside the Soulman web dashboard. The owner is consolidating separate web services into Soulman one at a time; this is the first. The old app's navigation was disliked; its idea — plain text files in Google Drive, nothing else — is kept.

## Goals

- A "Diary" link on the dashboard, immediately to the right of "Projects".
- Accordion ("harmonica") navigation: year → month → day → all entries of that day. Opening a section closes its siblings.
- Write new entries and edit existing ones.
- Storage is plain files on the Google Drive for Desktop mount, accessed with ordinary filesystem calls from `web-svc`. No OAuth, no Google API client.
- Keep the 10 existing entries (all 2026-03) by migrating them to the new layout.
- Soulman's existing visual style; nothing carried over from the old Diary look.

## Non-goals

Delete, search, tags, Dropbox support, any change to the Android app, any change to the old repo, a separate service/port.

## Anti-goals

Re-introducing OAuth/session handling. Making the old app's `drive.file` scope a dependency. Importing the old frontend's look or code.

## Storage

Root (config `web.diary_root`): `G:\My Drive\diary`.

Layout: `<root>\<YYYY>\<MM>\<DD>\entry-<HH-MM-SS>.txt`, UTF-8 text, local time of the machine running `web-svc`. Colons are illegal in Windows filenames, hence `-` in the time. The entry's identity is its path; its displayed time is parsed from the filename.

Drive for Desktop may not be mounted or synced at the moment `web-svc` starts. A missing root must not stop startup (same posture as `claude_project_roots`); each request reports it as 503 instead.

### Migration

The old layout was `<year>\<month>\entry-<DD>-<HH>:<MM>:<SS>.txt`. Drive for Desktop currently displays these with spaces in place of colons (`entry-01-11 59 28.txt`); a migration must accept `:`, space or `-` as separators. A one-off PowerShell script (`scripts/migrate-diary-layout.ps1`, supports `-WhatIf`) moves each file to the new layout. Run once by the owner; not part of normal operation. The app does not read the old layout.

## Backend (`web-svc`)

New package `web-svc/diary`, in the style of `web-svc/obsidian`: every function validates its arguments (year/month/day are digits of fixed width, the filename matches `entry-HH-MM-SS.txt`) before touching the filesystem; traversal is impossible by construction. Typed sentinel errors (`ErrNotFound`, `ErrExists`, `ErrInvalidName`, `ErrRootUnavailable`) mapped to 404/409/400/503 by the handler.

New handler file `httpserver/diary_handler.go`, all routes behind the existing owner-JWT middleware:

| Route | Behaviour |
|---|---|
| `GET /api/diary/tree` | Years → months → days that have at least one entry, with an entry count per day. Sorted descending (newest first). Cheap because the whole diary is small; no pagination. |
| `GET /api/diary/entries?date=YYYY-MM-DD` | All entries of that day, ascending by time: `[{name, time:"HH:MM:SS", content}]`. |
| `POST /api/diary/entries` `{content}` | Creates `entry-<now>.txt` for the current date/time (server clock), creating year/month/day folders as needed. Exclusive create; on a same-second collision, advance to the next free second. Returns the created `{date, name, time}`. Empty or whitespace-only content is rejected (400). |
| `PUT /api/diary/entries` `{date, name, content}` | Overwrites an existing entry; 404 if it does not exist. The filename (and therefore timestamp) never changes. |

No size cap, matching `obsidian`'s posture.

Config: `WebConfig.DiaryRoot string \`json:"diary_root"\`` in `common/sharedconfig`, required non-empty in `web-svc/config.Load` (existence on disk is **not** checked at startup). Set in `config/prod.json`.

## Frontend (`web/`)

- `App.tsx`: new view `'diary'` (`?page=diary`), a `onOpenDiary` prop on `Dashboard`, and a "Diary" link rendered right of "Projects". Follows the pattern of the existing pages (`ProjectsPage`, `FilesPage`).
- `DiaryPage`: header with back-to-dashboard, a "New entry" textarea + Save at the top, then the accordion.
- Accordion: years, then months, then days, from `/api/diary/tree`. Exactly one section open per level — opening one closes its siblings; opening a parent resets its children. Each header shows its label and a count of entries beneath it. Newest first at each level. Opening a day fetches that day's entries and shows them all in full, in time order, each with its time and an Edit button that swaps the entry to a textarea with Save/Cancel.
- Open path is kept in the URL (`diaryYear`, `diaryMonth`, `diaryDay` via `urlState`), so reload and back behave as on other pages. The newest year/month/day is **not** auto-opened (my call: keeps the initial view compact; the owner can say otherwise).
- After saving a new entry, the tree refreshes and the accordion opens to today so the entry is visible.
- Month labels use the month name ("March"), not the number. Not-found/503 responses show an inline message ("Diary folder isn't reachable — is Google Drive running?") rather than a blank page.
- Styling uses existing `index.css` classes/variables. The accordion is a small reusable component (the app has none today); plain buttons with `aria-expanded`, no new dependency.

## Testing

- Go: table tests for `diary` (tree building, ordering, create/collision, update, traversal and bad-name rejection, missing root) on `t.TempDir()`; handler tests in the existing style; `config.Load` validation.
- Frontend: Vitest + Testing Library for the accordion behaviour (one open per level, children reset), new-entry save, edit save/cancel, error states — in the style of the existing component tests.
- Manual: run against the real `G:\` mount after migration.

## Docs

Update `CLAUDE.md` (web-svc and the repo table), `web-svc/NOTES.md`, and add a note on the Drive-for-Desktop dependency (the Drive mount must exist; entries sync to other devices by Drive itself, so a very recent write may take a moment to appear elsewhere).

## Left to the builder

File layout of the frontend components and CSS; exact JSON field casing (follow the existing handlers); the migration script's internals.
