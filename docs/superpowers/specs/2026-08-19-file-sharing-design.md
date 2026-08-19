# File Sharing (Time-Limited Links) Design

**Status:** Approved
**Date:** 2026-08-19

## Problem

The file browser (`docs/superpowers/specs/2026-08-19-file-browser-design.md`)
lets the owner download a file from the dashboard, but the download
endpoint is behind owner-only JWT auth — there's no way to hand a specific
file to someone else (or to another of the owner's own devices that isn't
logged into the dashboard) without them going through Supabase login. The
goal is a "Share" action next to "Download": generate a URL for one file,
copy it to the clipboard, and have it work when opened directly in a
browser (e.g. tapped from a chat app) — no login, time-limited, and
triggering the browser's save/download behavior rather than navigating to
a page.

Also in scope: the existing per-file Download button (and the new Share
button) get icons instead of text labels.

## Goals

- A "Share" button per file (next to Download) that creates a link scoped
  to that one file and copies it to the clipboard.
- The link works with no authentication — opening it is the only
  credential needed.
- The link stops working after a configurable time-to-live (default 1
  hour).
- Opening the link triggers the browser's download/save-as behavior
  directly — never a rendered page.
- Download and Share are icon buttons, not text.

## Non-Goals (this iteration)

- **Revocation before expiry.** The token is stateless (see Architecture)
  — there is no server-side record to delete. A link is live until it
  expires or the process that signed it restarts (see Signing Secret
  below).
- **Single-use enforcement.** A link can be opened more than once during
  its TTL. Enforcing single-use would require server-side state, which
  the stateless design deliberately avoids.
- **Rate limiting / brute-force protection on `/dl/{token}`.** The token
  carries a 256-bit HMAC-SHA256 signature — guessing a valid one is
  computationally infeasible, so no additional throttling is added.
- **Sharing a folder or multiple files at once (zip).** One file per link,
  matching Download's existing per-file scope.
- **Surviving a `web-svc` restart.** The signing secret is regenerated
  randomly every process start (see below) — a redeploy or a
  `start-everything.ps1` login run invalidates all outstanding links even
  if their TTL hasn't elapsed. Accepted tradeoff: this avoids a new
  `.env` secret, and restarts are infrequent relative to the default
  1-hour TTL.

## Architecture

```
web/src/components/FileBrowser.tsx
  ├─ [⬇] Download button  (unchanged behavior, now icon-only)
  └─ [🔗] Share button     (new)
       ▼ POST /api/files/share?root&path&file   (JWT-protected, same group as
         the rest of the file browser)
       ◀ { url: "/dl/<token>", expiresAt }
       navigator.clipboard.writeText(origin + url)

web-svc/httpserver/files_handler.go
  filesShare (JWT-protected)     → sharelink.Issue → returns relative /dl/<token>
  shareDownload (unauthenticated) → sharelink.Verify → filebrowser.ResolveFile
                                   → serveFileDownload (shared with filesDownload)

web-svc/sharelink  (new package — pure signing/verification, no filesystem
                     or HTTP dependency)

web/vite.config.ts — a second proxied path, `/dl`, alongside the existing
  `/api` (both `server.proxy` for dev and `preview.proxy` for prod), so a
  `/dl/<token>` link opened through the `cloudflared` tunnel at
  soulman.breynisson.org reaches web-svc instead of falling through to the
  SPA.
```

The unauthenticated route is a distinct path (`/dl/...`) rather than a
variant of `/api/files/download` — this keeps the two auth models (owner
JWT vs. self-verifying signed token) in separate routes rather than
branching inside one handler.

## Components

### `web-svc/sharelink` (new package)

Stateless token issuance and verification. No knowledge of the
filesystem — it only signs and verifies `(root label, relative path,
filename, expiry)` tuples as opaque strings; `filebrowser` resolves them
to an actual file, same as it does for the authenticated download path.

```go
package sharelink

import "time"

var (
    ErrExpired = errors.New("sharelink: token expired")
    ErrInvalid = errors.New("sharelink: invalid token")
)

// payload is the JSON structure embedded in every token. Field names are
// short since they travel in every URL.
type payload struct {
    Root string `json:"root"`
    Path string `json:"path"`
    File string `json:"file"`
    Exp  int64  `json:"exp"` // unix seconds
}

// Issue creates a token for one file, valid for ttl from now. The token is
// self-contained: base64url(JSON payload) + "." + base64url(HMAC-SHA256
// signature over the payload's base64url text, keyed by secret). JSON
// (rather than a hand-rolled delimited string) sidesteps any need to
// escape "/" or non-ASCII bytes in path/file — an Icelandic filename
// round-trips exactly like an ASCII one.
func Issue(secret []byte, root, path, file string, ttl time.Duration) (token string, expiresAt time.Time)

// Verify checks the signature (constant-time comparison via hmac.Equal)
// and expiry, and returns the embedded root/path/file. Returns ErrInvalid
// for a malformed token or a signature that doesn't match (wrong/rotated
// secret, tampering), ErrExpired for a validly-signed token past its exp.
func Verify(secret []byte, token string) (root, path, file string, err error)
```

**Signing secret:** 32 random bytes (`crypto/rand`) generated once in
`web-svc/main.go` at process startup, held only in memory, passed into
`httpserver.Config` as `ShareLinkSecret []byte`. Never written to disk or
`.env` — see the Non-Goals note on restart behavior.

### Config

`common/sharedconfig.WebConfig` gains one optional field:

```go
type WebConfig struct {
    // ...existing fields...
    // ShareLinkTTLMinutes is how long a generated share link stays valid.
    // Optional — zero or absent defaults to 60 in web-svc/config.Load,
    // the same loose-default posture as DoNotDisturb's Start/End (not a
    // fatal validation error).
    ShareLinkTTLMinutes int `json:"share_link_ttl_minutes"`
}
```

`web-svc/config.Load()`:

```go
shareLinkTTL := time.Duration(shared.Web.ShareLinkTTLMinutes) * time.Minute
if shareLinkTTL <= 0 {
    shareLinkTTL = 60 * time.Minute
}
```

`config/dev.json` and `config/prod.json` both add `"share_link_ttl_minutes": 60`
under `"web"` (explicit rather than relying on the default, for visibility).

### `web-svc/httpserver` additions

New routes in `web-svc/httpserver/server.go`. `filesShare` joins the
existing owner-JWT group; `shareDownload` is added directly on the router,
outside that group:

```go
r.Group(func(r chi.Router) {
    r.Use(s.verifier.Middleware)
    // ...existing routes...
    r.Post("/api/files/share", s.filesShare)
})

r.Get("/dl/{token}", s.shareDownload)
```

**`filesShare`** (in `files_handler.go`, alongside the other file-browser
handlers): validates `root`/`path`/`file` exactly like `filesDownload`
does today (`findFileBrowserRoot` → `filebrowser.ResolveFile`, same
`writeFileBrowserError` mapping) — a share link is never issued for a
file that doesn't currently exist. On success:

```go
token, expiresAt := sharelink.Issue(s.cfg.ShareLinkSecret, root.Label, path, filename, s.cfg.ShareLinkTTL)
writeJSON(w, http.StatusOK, map[string]any{
    "url":       "/dl/" + token,
    "expiresAt": expiresAt,
})
```

**`shareDownload`**: no JWT, no CORS concern (opened via top-level browser
navigation, not `fetch`).

```go
func (s *Server) shareDownload(w http.ResponseWriter, r *http.Request) {
    rootLabel, relPath, filename, err := sharelink.Verify(s.cfg.ShareLinkSecret, chi.URLParam(r, "token"))
    if errors.Is(err, sharelink.ErrExpired) {
        writeShareLinkError(w, http.StatusGone, "This link has expired.")
        return
    }
    if err != nil {
        writeShareLinkError(w, http.StatusNotFound, "This link is invalid or the file is no longer available.")
        return
    }
    root, ok := findFileBrowserRoot(s.cfg.FileBrowserRoots, rootLabel)
    if !ok {
        writeShareLinkError(w, http.StatusNotFound, "This link is invalid or the file is no longer available.")
        return
    }
    absPath, err := filebrowser.ResolveFile(root, relPath, filename)
    if err != nil {
        writeShareLinkError(w, http.StatusNotFound, "This link is invalid or the file is no longer available.")
        return
    }
    serveFileDownload(w, r, absPath, filename)
}
```

`writeShareLinkError` writes a minimal `text/html` page (not JSON — a
human may land here directly by tapping a stale link):

```go
func writeShareLinkError(w http.ResponseWriter, status int, message string) {
    w.Header().Set("Content-Type", "text/html; charset=utf-8")
    w.WriteHeader(status)
    fmt.Fprintf(w, "<!doctype html><meta charset=\"utf-8\"><p>%s</p>", html.EscapeString(message))
}
```

**Shared download logic:** `filesDownload`'s body (the
`Content-Disposition`/`Cache-Control: no-store`/BOM-detection sequence
added in the file-browser feature's Icelandic-encoding fix) is extracted
into:

```go
func serveFileDownload(w http.ResponseWriter, r *http.Request, absPath, filename string) {
    w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
    w.Header().Set("Cache-Control", "no-store")
    needsBOM, err := isBOMlessUTF8Text(absPath)
    if err != nil {
        writeJSONError(w, http.StatusInternalServerError, "internal error")
        return
    }
    if needsBOM {
        serveWithUTF8BOM(w, absPath)
        return
    }
    http.ServeFile(w, r, absPath)
}
```

`filesDownload` becomes a thin wrapper: resolve root/path/file (as today),
then call `serveFileDownload`. `shareDownload` calls the same function —
the UTF-8 BOM fix and cache-control behavior apply identically to shared
links, with no duplicated logic.

### Frontend (`web/src`)

**`api.ts`** — one new function. Neither `getJSON` (GET-only) nor
`mutateJSON` (always sends a JSON body and returns `Promise<void>`, never
the parsed response) fits: `filesShare` is a POST with no body, taking
its arguments as query params (matching `filesList`/`filesDownload`'s
existing convention) and returning a parsed JSON payload. Bespoke, same
posture the design doc for the file browser itself took for
`downloadFile`/`uploadFile`:

```ts
export interface ShareLinkResponse { url: string; expiresAt: string; }

export async function shareFile(
  token: string, root: string, path: string, file: string,
): Promise<ShareLinkResponse> {
  const response = await fetch(
    `/api/files/share?root=${encodeURIComponent(root)}&path=${encodeURIComponent(path)}&file=${encodeURIComponent(file)}`,
    { method: 'POST', headers: token ? { Authorization: `Bearer ${token}` } : {} },
  );
  if (!response.ok) {
    throw new ApiError(response.status, `share failed (${response.status})`);
  }
  return response.json();
}
```

**`components/icons.tsx`** (new) — two small inline SVG components, no new
npm dependency (the app has none today):

```tsx
export function DownloadIcon(props: React.SVGProps<SVGSVGElement>) {
  return (
    <svg viewBox="0 0 20 20" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="1.5" {...props}>
      <path d="M10 3v9m0 0-3.5-3.5M10 12l3.5-3.5M4 15.5h12" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

export function ShareIcon(props: React.SVGProps<SVGSVGElement>) {
  return (
    <svg viewBox="0 0 20 20" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="1.5" {...props}>
      <circle cx="15" cy="4.5" r="2" />
      <circle cx="5" cy="10" r="2" />
      <circle cx="15" cy="15.5" r="2" />
      <path d="M6.7 9 13.3 5.5M6.7 11 13.3 14.5" strokeLinecap="round" />
    </svg>
  );
}
```

**`components/FileBrowser.tsx`** — each file row's Download button becomes
icon-only (`<DownloadIcon />`, `aria-label="Download"`, `title="Download"`);
a new Share button (`<ShareIcon />`, `aria-label="Share"`, `title="Share"`)
sits next to it. Per-row share state:

```tsx
const [shareSuccessFile, setShareSuccessFile] = useState<string | null>(null);

async function handleShare(name: string) {
  const token = await getAccessToken();
  try {
    const { url } = await shareFile(token, root, currentPath, name);
    await navigator.clipboard.writeText(window.location.origin + url);
    setShareSuccessFile(name);
  } catch {
    setError('Failed to create share link');
  }
}
```

`shareSuccessFile === file.name` renders an inline "Link copied" success
line under that row, matching the existing upload-success message's
visual treatment (`text-green-600`); it clears whenever `handleDownload`,
`handleShare` (for a different file), or navigation happens — same
staleness rule the upload success message already follows.

## Data Flow (share example)

1. Owner is on the dashboard (already JWT-authenticated), browsing
   `Documents/Taxes`, and clicks Share on `2025-return.pdf`.
2. `POST /api/files/share?root=Documents&path=Taxes&file=2025-return.pdf`
   → `web-svc` validates the file exists, issues a token good for 60
   minutes → `{"url": "/dl/eyJyb290Ijoi...", "expiresAt": "..."}`.
3. Frontend copies `https://soulman.breynisson.org/dl/eyJyb290Ijoi...`
   (or the dev origin) to the clipboard; shows "Link copied" under that
   row.
4. Owner pastes the link into a chat app on another device.
5. That device opens the link → `GET /dl/eyJyb290Ijoi...` reaches
   `web-svc` through the `cloudflared` tunnel and the new `/dl` Vite
   proxy entry (no login prompt) → signature and expiry check pass →
   file streams with `Content-Disposition: attachment` → the browser
   triggers its save/download UI directly, no page render.
6. 90 minutes later, the same link is opened again → signature still
   valid, but `exp` has passed → 410 page: "This link has expired."

## Error Handling & Edge Cases

- **Expired token:** 410 Gone, plain-text-in-HTML message. Distinguished
  from "invalid" deliberately (honest UX, not a security-sensitive
  distinction — knowing a link *was* once valid isn't useful information).
- **Tampered/malformed token, or one signed by a previous process's
  secret** (e.g. after a restart): signature check fails →
  `sharelink.ErrInvalid` → 404, same generic message as any other
  not-found case, no distinction from a genuinely bad token.
- **Root removed from config since the token was issued:**
  `findFileBrowserRoot` fails → treated the same as an invalid token
  (404) — nothing in the response reveals whether the root or the file
  was the problem.
- **File deleted/moved/renamed since the token was issued:**
  `filebrowser.ResolveFile` returns `ErrNotFound` → 404, same message.
- **Share requested for a file that doesn't exist (e.g. deleted between
  page load and clicking Share):** `filesShare` returns the same 404 JSON
  error `filesDownload` already returns for this case — no token is
  issued.
- **`web-svc` restarts between issuing and opening a link:** covered
  under Non-Goals — the new secret can't verify tokens signed by the old
  one; opening the link produces the same "invalid" 404 as a tampered
  token. No special-cased message, since the server has no way to tell
  the difference.
- **Clipboard write fails** (e.g. `navigator.clipboard` unavailable over
  plain HTTP, or permission denied): `handleShare`'s catch sets the
  existing `error` state ("Failed to create share link"); the link is
  not shown to the user as text anywhere as a fallback — matching this
  iteration's scope (icons, not a manual-copy text field). If this proves
  annoying in practice, a visible fallback URL is a natural follow-up.

## Testing

- `web-svc/sharelink`: table tests for `Issue`/`Verify` round-tripping
  root/path/file (including a filename with non-ASCII characters, e.g.
  Icelandic, mirroring the download-encoding fix's test style),
  `ErrExpired` for a token issued with a negative/zero TTL, `ErrInvalid`
  for a tampered payload, a tampered signature, and a token signed with a
  different secret (simulating a restart).
- `web-svc/httpserver/files_handler_test.go`: `filesShare` returns a
  `/dl/...` URL and a 404 for a nonexistent file (same as
  `filesDownload`'s existing coverage); `shareDownload` end-to-end
  (issue via `sharelink.Issue` in the test, then hit `/dl/<token>` and
  assert the same `Content-Disposition`/`Cache-Control`/BOM behavior
  `filesDownload` already has tests for), plus expired-token → 410 and
  invalid-token → 404 cases.
- `web/src/components/FileBrowser.test.tsx`: clicking Share calls
  `shareFile`, writes the origin-prefixed URL to a mocked
  `navigator.clipboard.writeText`, and shows/clears the "Link copied"
  message per the same rules as the upload-success message's existing
  tests.
- Manual: exercise the full flow against prod — share a file from the
  dashboard, open the copied link on a different device with no
  dashboard login, confirm it triggers a save dialog (not a page) and
  the bytes match; wait past expiry (or use a short TTL override) and
  confirm the 410 page.
