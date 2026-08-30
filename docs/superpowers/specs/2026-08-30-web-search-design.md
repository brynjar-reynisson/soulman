# Web Search Design

**Status:** Approved
**Date:** 2026-08-30

## Problem

There's no way to search the web from the soulman dashboard — doing so
today means switching to a browser tab and a separate search engine. This
adds a "Web-Search" utility alongside Claude/Obsidian/Files, backed by the
Brave Search API, presenting results the way a traditional search engine
does (a list of links with snippets) rather than an LLM-style aggregated
answer.

## Goals

- A "Web-Search" link in the dashboard nav, next to Claude/Obsidian/Files.
- A dedicated search page: centered search box when empty (google.com-style),
  results as a list of `{title (link), URL, snippet}` — no synthesized
  summary.
- Reuse `web-svc`'s existing owner-only JWT auth — no new auth mechanism.
- Keep the Brave API key server-side; the browser never sees it.

## Non-Goals (this iteration)

- Pagination / "more results" — a single page of up to 10 results per
  query. Re-searching with different terms is the escape hatch (YAGNI,
  confirmed with the user).
- Any filter UI (safe search level, region, freshness, file type). Brave's
  default moderate safe-search setting is used; no query-string knobs
  exposed in the UI.
- Search history, saved searches, or query logging beyond the existing
  request logger's normal method/path/status line.
- Result favicons/thumbnails or any other visual enrichment beyond
  title/URL/snippet.
- Rate limiting beyond what Brave's API itself enforces — this is a
  single-owner tool, same posture as every other `web-svc` endpoint.

## Architecture

Same shape as Files/Claude/Obsidian: no new service, an incremental
extension of `web-svc`'s existing owner-only-JWT pattern.

```
web/src/components/SearchPage.tsx  (search box + results list)
  ▼ fetch (relative /api/search?q=..., same pattern as api.ts today)
web-svc/httpserver/search_handler.go
  ▼ calls
web-svc/websearch/websearch.go  (Brave Search API client)
  ▼ calls (server-side only — key never reaches the browser)
https://api.search.brave.com/res/v1/web/search
  ▲ authenticated via
env: BRAVE_SEARCH_API_KEY
```

## Components

### Config — `BRAVE_SEARCH_API_KEY` (env var, not shared config)

Follows the exact precedent `DEEPSEEK_API_KEY` set in `thinking-svc`: read
via `env("BRAVE_SEARCH_API_KEY", "")` in `web-svc/config/config.go`,
**non-fatal if blank** at startup (a warning is logged, same style as
`thinking-svc`'s DeepSeek warning) — but any search request made while
the key is blank returns `503` with a clear error body, since search is
this feature's entire purpose rather than an optional enhancement to
something else.

This is an env var, not a `web.*` shared-config field — it's a secret,
same treatment as `SUPABASE_JWT_SECRET` and (in `action-svc`)
`DISCORD_BOT_TOKEN`, not a non-secret setting like `file_browser_roots`.
The user has already added `BRAVE_SEARCH_API_KEY=<key>` to `.env` in both
`soulman-dev\` and `soulman-prod\` (the same file `SUPABASE_URL` lives
in, loaded by `load-env.ps1`) — no further action needed there.

`web-svc/config/config.go`'s `Config` struct gains:

```go
type Config struct {
    // ...existing fields...
    BraveSearchAPIKey string
}
```

```go
BraveSearchAPIKey: env("BRAVE_SEARCH_API_KEY", ""),
```

`web-svc/main.go` passes it through to `httpserver.Config` as
`BraveSearchAPIKey`.

### `web-svc/websearch` (new package)

A small, single-purpose Brave Search API client — no interface, no
mock-friendly abstraction layer, since there is exactly one caller and
one implementation (YAGNI, consistent with `web-svc/reports`'s and
`web-svc/obsidian`'s existing shape of plain functions over an
interface).

```go
package websearch

import "context"

// Result is one Brave Search result, trimmed to what the UI shows.
type Result struct {
    Title   string
    URL     string
    Snippet string
}

// ErrNoAPIKey is returned by Search when apiKey is empty — the handler
// maps this to a 503, distinguishing "not configured" from "Brave
// returned an error" (502) or "network/timeout failure" (502).
var ErrNoAPIKey = errors.New("brave search api key not configured")

// Search queries the Brave Search API and returns up to 10 web results.
// apiKey is passed explicitly (not read from the environment inside this
// package) so the caller's config-loading stays the single source of
// truth for where secrets come from — mirrors how thinking-svc/llm takes
// its DeepSeek key as a constructor argument rather than reading the env
// itself.
func Search(ctx context.Context, apiKey, query string) ([]Result, error)
```

`Search`'s implementation:

1. Returns `ErrNoAPIKey` immediately if `apiKey == ""` — no HTTP call
   attempted.
2. Builds `GET https://api.search.brave.com/res/v1/web/search?q=<query>`
   (query URL-encoded), with headers `Accept: application/json` and
   `X-Subscription-Token: <apiKey>`.
3. Uses a `context.Context`-aware request with a 5-second timeout via the
   caller-supplied `ctx` (the handler derives it from the incoming
   request the same way `isHealthy` does today), and a package-level
   `http.Client` (no connection-pooling concerns beyond what the default
   transport already gives every other `web-svc` HTTP call).
4. On a non-2xx response, returns a wrapped error including the status
   code (no response body included in the error — Brave error bodies may
   echo the query back, and this keeps the error message stable and
   short; the handler logs the full status server-side via `slog`).
5. Parses the response body's `web.results[]` array; each element's
   `title`, `url`, `description` map directly to `Result.Title`,
   `Result.URL`, `Result.Snippet`. Brave's `description` field contains
   basic HTML highlighting tags (`<strong>`) around matched terms — these
   are stripped with a small helper (`stripHighlightTags`, a targeted
   `strings.NewReplacer` for `<strong>`/`</strong>` only, not a general
   HTML sanitizer) so the frontend renders plain text without needing to
   trust and render HTML from a third party.
6. Truncates to the first 10 results if Brave returns more (it can, when
   `count` isn't explicitly capped in the request — instead of relying on
   a request parameter, this is enforced defensively on the parsed slice
   too, since correctness here matters more than trusting the upstream
   API to honor a query param).

### `web-svc/httpserver` additions

New file `web-svc/httpserver/search_handler.go`, following
`obsidian_handler.go`'s style: a thin handler calling into `websearch`,
inside the existing `r.Use(s.verifier.Middleware)` group:

| Method | Path | Query | Response |
|---|---|---|---|
| GET | `/api/search?q=<query>` | `q` (required) | `{"results": [{"title","url","snippet"}]}` |

- Missing/empty `q` → `400` with a JSON error body (`writeJSONError`,
  matching every other handler's error shape).
- `websearch.ErrNoAPIKey` → `503`, message "web search is not configured".
- Any other `websearch.Search` error (Brave non-2xx, network failure,
  timeout) → `502`, message "web search failed"; the underlying error is
  logged server-side via `slog.Error` with the status/error detail, never
  echoed to the client.
- Success, zero results → `200` with `{"results": []}` — an empty list is
  not an error; the frontend renders "No results found."

`httpserver.Config` gains `BraveSearchAPIKey string`; `Server` stores it
alongside the other config fields it already holds.

### Frontend (`web/src`)

**`api.ts`** — new typed function:

```ts
export interface SearchResult {
  title: string;
  url: string;
  snippet: string;
}

export interface SearchResults {
  results: SearchResult[];
}

export const search = (token: string | null, query: string): Promise<SearchResults> =>
  getJSON(`/api/search?q=${encodeURIComponent(query)}`, token);
```

Reuses the existing `getJSON` helper and `ApiError` — no new fetch
plumbing needed, this is a plain GET like `/api/episodes`.

**`App.tsx`** — `ViewState` gains `'search'`; `viewFromPageParam` gets a
`page === 'search'` branch. `Dashboard` gets a "Web-Search" button next to
"Claude"/"Obsidian"/"Files", wired to `setParams({ page: 'search' })` /
`setView('search')`, mirroring the other three exactly.

**`components/SearchPage.tsx`** (new) — top-level page, holds:
- `query` state (the text box's current value)
- `results` state (`SearchResult[] | null` — `null` before any search has
  run)
- `loading` and `error` state

Layout:
- Before any search (`results === null`): a large centered search box
  roughly mid-page (google.com-style empty state), with a "Soulman
  Search" heading above it and a back link ("← Soulman") in the corner
  matching every other page's back-navigation convention.
- After a search: the same search box moves to a slim bar pinned near the
  top (still editable — re-submitting runs a new search), results render
  as a plain vertical list below it. Each result: title rendered as an
  `<a>` with `target="_blank" rel="noopener noreferrer"` (title text,
  underlined, blue-ish — link styling, not a button), the URL shown
  underneath in small gray text, then the snippet as a normal paragraph
  underneath that. No cards, no borders, no icons — plain list, matching
  this dashboard's existing minimal Tailwind style (see `ReportsPanel.tsx`
  for the closest tonal match already in the codebase).
- Submitting the empty string is a no-op (button disabled / form
  `onSubmit` early-returns) — no request fired for a blank query.
- `loading`: the results area shows a simple "Searching…" text state — no
  spinner component exists elsewhere in this codebase to reuse, and
  adding one is out of scope.
- `error` (503/502/network failure): an inline red-text error banner
  above the (possibly stale) results area — same treatment
  `FileBrowser.tsx` and `ObsidianFileViewer.tsx` already give fetch
  failures — with the raw message from `ApiError` (already generic:
  "web search is not configured" / "web search failed", no secrets
  in it).
- Zero results (`results.length === 0` after a successful search): "No
  results found." text, no error styling.

The current query is persisted to the URL via the existing `urlState.ts`
helpers (`?page=search&q=...`), matching Files'/Claude's/Obsidian's
reload-preserves-place precedent — **only after a search has actually run**
(not on every keystroke), consistent with how `FileBrowser.tsx` updates
`filePath` on navigation, not on typing.

## Data Flow (search example)

1. User clicks "Web-Search" in the dashboard nav → `SearchPage` renders
   with the centered empty-state box, URL becomes `?page=search`.
2. Types "soulman project soulman ai agent" and submits → `loading` is
   set, `GET /api/search?q=soulman%20project%20soulman%20ai%20agent` →
   `web-svc` calls `websearch.Search` → Brave API → up to 10
   `{title, url, snippet}` results returned → `results` state set,
   `loading` cleared, URL becomes `?page=search&q=soulman...`.
3. Box moves to the top bar, results render as a list below it.
4. User clicks a result's title → opens the URL in a new browser tab
   (soulman dashboard tab stays open, unaffected).
5. User edits the query in the (now top-pinned) box and re-submits →
   same flow repeats, replacing `results`.

## Error Handling & Edge Cases

- **`BRAVE_SEARCH_API_KEY` not set**: `web-svc` starts normally (logged
  warning, non-fatal, same posture as a blank `DEEPSEEK_API_KEY`); any
  search request returns `503` with "web search is not configured",
  shown as the inline error banner.
- **Brave API returns a non-2xx status** (bad key, rate-limited, Brave
  outage): `502` "web search failed" to the client; full status/detail
  logged server-side via `slog.Error` for diagnosis — this repo's
  established split between what a client sees and what a log records
  (same pattern as the share-link token redaction in `web-svc`'s request
  logger).
- **Brave API times out** (network issue, slow response past 5s): same
  `502` path — `Search` treats a context-deadline error identically to
  any other transport error, no special-cased retry (YAGNI — a manual
  re-search is the retry).
- **Empty query submitted**: rejected client-side before any request
  (submit is a no-op); if somehow reached anyway, `web-svc` returns `400`
  server-side as defense in depth.
- **Zero results for a valid query**: `200` with an empty list, rendered
  as "No results found," not an error.
- **XSS via a malicious result title/snippet**: React's default JSX
  text-interpolation escapes all result fields — nothing is rendered via
  `dangerouslySetInnerHTML`, so a title/snippet containing `<script>` (or
  any markup) renders as inert literal text. This is why `websearch.Search`
  strips Brave's own `<strong>` highlight tags server-side rather than
  the frontend rendering Brave's HTML — the highlighting isn't worth
  introducing an HTML-rendering path for.

## Testing

- `web-svc/websearch`: unit tests against an `httptest.Server` standing in
  for Brave's API — covers the happy path (parses `web.results[]`
  correctly, strips `<strong>` tags, truncates to 10), `ErrNoAPIKey` when
  `apiKey == ""` (no HTTP call made — assert the test server sees zero
  requests), a non-2xx upstream response, and a timeout (server that
  never responds within a short test-configured deadline).
- `web-svc/httpserver/search_handler_test.go`: handler-level tests for
  status codes and JSON shapes — missing `q` → 400, `ErrNoAPIKey` → 503,
  other `websearch` errors → 502, empty results → 200 with `{"results":
  []}` — mirrors `obsidian_handler_test.go`'s style, using a fake/stub
  `websearch.Search` via a small function-value seam in the handler
  (matching how other handlers in this file are already tested without a
  real backing service).
- `web/src/components/SearchPage.test.tsx`: empty-state renders the
  centered box; submitting a query calls the mocked `api.search` and
  renders results as links with snippets; loading state shows while the
  promise is pending; a `503`/`502` `ApiError` renders the error banner;
  an empty `results: []` renders "No results found"; submitting an empty
  query fires no request — mirrors `FileBrowser.test.tsx`'s use of a
  mocked `api.ts`.
- Manual: exercise the full flow against dev (`localhost:5190`) — search
  a real query, confirm results look right and links open in a new tab,
  temporarily unset `BRAVE_SEARCH_API_KEY` and confirm the 503 banner
  appears, confirm reload with `?page=search&q=...` in the URL restores
  the search — before treating this as done.
