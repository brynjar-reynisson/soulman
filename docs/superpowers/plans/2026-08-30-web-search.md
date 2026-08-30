# Web Search Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a "Web-Search" utility to the soulman dashboard, backed by the Brave Search API, presenting results as a plain list of links with snippets (no aggregated/synthesized summary).

**Architecture:** Same shape as Files/Claude/Obsidian — no new service, an incremental extension of `web-svc`'s existing owner-only-JWT HTTP API plus one new React page in `web`. A new `web-svc/websearch` package wraps the Brave Search API; a new `GET /api/search?q=` handler calls it; a new `SearchPage.tsx` calls that endpoint and renders results.

**Tech Stack:** Go (`web-svc`, stdlib `net/http`, `log/slog`), React + TypeScript + Tailwind (`web`), Vitest + Testing Library for frontend tests, Go's `testing` + `httptest` for backend tests.

**Spec:** `docs/superpowers/specs/2026-08-30-web-search-design.md`

## Global Constraints

- Auth: `/api/search` sits behind `web-svc`'s existing owner-only JWT middleware (`s.verifier.Middleware`) — no new auth mechanism.
- Secret name: the API key env var is **`BRAVE_SEARCH_API_KEY`** (already set by the user in `.env` in both `soulman-dev\` and `soulman-prod\`) — non-fatal if blank at `web-svc` startup, but any search request made while blank returns `503`.
- Results: capped at **10** per query, no pagination in this iteration.
- No filter UI (safe search, region, freshness) — Brave's default moderate safe-search is used as-is.
- Brave's `<strong>`/`</strong>` highlight tags are stripped server-side before the snippet is returned as JSON — the frontend never renders HTML from a third party.
- Outbound calls to Brave are bounded by a 5-second timeout, matching `web-svc`'s existing `isHealthy` pattern.
- Frontend styling matches the existing minimal Tailwind style already used across `ReportsPanel.tsx`/`FileBrowser.tsx` — no new UI dependencies.
- This is a plan-level refinement over the spec's illustrative Go pseudocode (which sketched a plain `Search(ctx, apiKey, query)` function): `web-svc/websearch` instead exposes a `Client` constructed via `NewClient(apiKey, baseURL string)`, mirroring `thinking-svc/llm`'s `DeepSeekClient` — this is what makes `websearch_test.go` and `search_handler_test.go` able to point at an `httptest.Server` instead of the real Brave API. The behavior (10-result cap, tag stripping, 503/502 mapping) is unchanged from the spec.
- Frontend error banners use fixed, friendly strings dispatched on `ApiError.status` (`503` → "Web search is not configured", anything else → "Web search failed") rather than surfacing the raw `ApiError` message — this is a plan-level correction to match the actual established convention in `FileBrowser.tsx` (confirmed by reading its upload/share error handling), not the spec's "raw message" phrasing, which didn't match that convention.

---

### Task 1: `web-svc/websearch` — Brave Search API client

**Files:**
- Create: `web-svc/websearch/websearch.go`
- Test: `web-svc/websearch/websearch_test.go`

**Interfaces:**
- Produces: `websearch.Result{Title, URL, Snippet string}` (JSON tags `title`/`url`/`snippet`), `websearch.ErrNoAPIKey error`, `websearch.DefaultBaseURL string`, `websearch.NewClient(apiKey, baseURL string) *Client`, `(*Client).Search(ctx context.Context, query string) ([]Result, error)`.

- [ ] **Step 1: Write the failing tests**

Create `web-svc/websearch/websearch_test.go`:

```go
package websearch_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"soulman/web-svc/websearch"
)

func TestClient_Search_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/res/v1/web/search" {
			t.Errorf("path = %s, want /res/v1/web/search", r.URL.Path)
		}
		if got := r.URL.Query().Get("q"); got != "soulman ai agent" {
			t.Errorf("q = %q, want %q", got, "soulman ai agent")
		}
		if got := r.Header.Get("X-Subscription-Token"); got != "test-key" {
			t.Errorf("X-Subscription-Token = %q, want test-key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"web":{"results":[
			{"title":"Soulman","url":"https://example.com/soulman","description":"A <strong>personal</strong> AI agent."}
		]}}`))
	}))
	defer srv.Close()

	client := websearch.NewClient("test-key", srv.URL)
	results, err := client.Search(context.Background(), "soulman ai agent")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	want := websearch.Result{Title: "Soulman", URL: "https://example.com/soulman", Snippet: "A personal AI agent."}
	if results[0] != want {
		t.Errorf("results[0] = %+v, want %+v", results[0], want)
	}
}

func TestClient_Search_EmptyAPIKey_ReturnsErrNoAPIKeyWithoutHTTPCall(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Write([]byte(`{"web":{"results":[]}}`))
	}))
	defer srv.Close()

	client := websearch.NewClient("", srv.URL)
	_, err := client.Search(context.Background(), "query")
	if err != websearch.ErrNoAPIKey {
		t.Fatalf("err = %v, want ErrNoAPIKey", err)
	}
	if called {
		t.Error("expected no HTTP call when apiKey is empty")
	}
}

func TestClient_Search_NonOKStatus_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer srv.Close()

	client := websearch.NewClient("test-key", srv.URL)
	_, err := client.Search(context.Background(), "query")
	if err == nil {
		t.Fatal("expected error for non-200 response")
	}
}

func TestClient_Search_Timeout_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte(`{"web":{"results":[]}}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	client := websearch.NewClient("test-key", srv.URL)
	_, err := client.Search(ctx, "query")
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestClient_Search_TruncatesToTenResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		results := ""
		for i := 0; i < 15; i++ {
			if i > 0 {
				results += ","
			}
			results += `{"title":"r","url":"https://example.com","description":"d"}`
		}
		w.Write([]byte(`{"web":{"results":[` + results + `]}}`))
	}))
	defer srv.Close()

	client := websearch.NewClient("test-key", srv.URL)
	results, err := client.Search(context.Background(), "query")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 10 {
		t.Errorf("len(results) = %d, want 10", len(results))
	}
}

func TestClient_Search_EmptyResults_ReturnsEmptySlice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"web":{"results":[]}}`))
	}))
	defer srv.Close()

	client := websearch.NewClient("test-key", srv.URL)
	results, err := client.Search(context.Background(), "query")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("len(results) = %d, want 0", len(results))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run (from `web-svc/`): `go test ./websearch/...`
Expected: FAIL — `package soulman/web-svc/websearch is not in std` / no such package (the `websearch.go` file doesn't exist yet).

- [ ] **Step 3: Write the implementation**

Create `web-svc/websearch/websearch.go`:

```go
// Package websearch is a small client for the Brave Search API
// (https://api.search.brave.com), the backing search provider for
// soulman's Web-Search dashboard page. There is exactly one caller
// (web-svc/httpserver's search handler), so this is plain functions on a
// struct rather than an interface — same shape as thinking-svc/llm's
// DeepSeekClient, whose baseURL-injection approach to testability this
// package's tests mirror.
package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// DefaultBaseURL is the production Brave Search API host. Search appends
// the versioned web-search path to whatever baseURL NewClient is given —
// tests pass an httptest.Server URL instead.
const DefaultBaseURL = "https://api.search.brave.com"

// maxResults caps how many results Search returns, enforced on the
// parsed response regardless of what Brave sends back — correctness here
// matters more than trusting an upstream API to honor an implicit limit.
const maxResults = 10

// ErrNoAPIKey is returned by Search when the client was constructed with
// an empty API key. The httpserver handler maps this to a 503, distinct
// from a Brave-side failure (502).
var ErrNoAPIKey = errors.New("brave search api key not configured")

// Result is one Brave Search result, trimmed to what the dashboard shows.
type Result struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// Client calls the Brave Search API's web search endpoint.
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// NewClient constructs a Client. apiKey may be empty — Search then
// returns ErrNoAPIKey without making any HTTP call, matching the
// non-fatal-if-blank posture web-svc/config uses for BRAVE_SEARCH_API_KEY.
func NewClient(apiKey, baseURL string) *Client {
	return &Client{
		apiKey:     apiKey,
		baseURL:    baseURL,
		httpClient: &http.Client{},
	}
}

type braveSearchResponse struct {
	Web struct {
		Results []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
		} `json:"results"`
	} `json:"web"`
}

var highlightTagReplacer = strings.NewReplacer("<strong>", "", "</strong>", "")

// Search queries the Brave Search API and returns up to maxResults web
// results. ctx carries the caller's deadline — this package sets no
// timeout of its own beyond what ctx provides, matching how the rest of
// web-svc's outbound HTTP calls (see httpserver.Server.isHealthy) derive
// their timeout from a context the handler builds.
func (c *Client) Search(ctx context.Context, query string) ([]Result, error) {
	if c.apiKey == "" {
		return nil, ErrNoAPIKey
	}

	reqURL := c.baseURL + "/res/v1/web/search?q=" + url.QueryEscape(query)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("websearch: build request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("X-Subscription-Token", c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("websearch: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("websearch: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("websearch: brave search status %d", resp.StatusCode)
	}

	var parsed braveSearchResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("websearch: unmarshal response: %w", err)
	}

	results := make([]Result, 0, len(parsed.Web.Results))
	for _, r := range parsed.Web.Results {
		if len(results) >= maxResults {
			break
		}
		results = append(results, Result{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: highlightTagReplacer.Replace(r.Description),
		})
	}

	return results, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./websearch/...`
Expected: PASS (6 tests).

- [ ] **Step 5: Commit**

```bash
git add web-svc/websearch/websearch.go web-svc/websearch/websearch_test.go
git commit -m "feat(web-svc): add Brave Search API client"
```

---

### Task 2: `web-svc/config` — `BRAVE_SEARCH_API_KEY` env var

**Files:**
- Modify: `web-svc/config/config.go`
- Test: `web-svc/config/config_test.go`

**Interfaces:**
- Consumes: nothing from Task 1.
- Produces: `config.Config.BraveSearchAPIKey string` — read by Task 3's `main.go` wiring.

- [ ] **Step 1: Write the failing tests**

Add to `web-svc/config/config_test.go` (after `TestLoad_ShareLinkTTLMinutesOverride`):

```go
func TestLoad_BraveSearchAPIKeyDefaultsToEmptyString(t *testing.T) {
	dir := t.TempDir()
	path := writeConfigFile(t, dir, validConfigJSON)
	os.Setenv("CONFIG_PATH", path)
	os.Setenv("SUPABASE_URL", "https://example.supabase.co")
	os.Setenv("SUPABASE_JWT_SECRET", "shh")
	defer os.Unsetenv("CONFIG_PATH")
	defer os.Unsetenv("SUPABASE_URL")
	defer os.Unsetenv("SUPABASE_JWT_SECRET")
	os.Unsetenv("BRAVE_SEARCH_API_KEY")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want no error when BRAVE_SEARCH_API_KEY is unset", err)
	}
	if cfg.BraveSearchAPIKey != "" {
		t.Errorf("BraveSearchAPIKey = %q, want empty string", cfg.BraveSearchAPIKey)
	}
}

func TestLoad_BraveSearchAPIKeyEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := writeConfigFile(t, dir, validConfigJSON)
	os.Setenv("CONFIG_PATH", path)
	os.Setenv("SUPABASE_URL", "https://example.supabase.co")
	os.Setenv("SUPABASE_JWT_SECRET", "shh")
	os.Setenv("BRAVE_SEARCH_API_KEY", "brave-test-key")
	defer os.Unsetenv("CONFIG_PATH")
	defer os.Unsetenv("SUPABASE_URL")
	defer os.Unsetenv("SUPABASE_JWT_SECRET")
	defer os.Unsetenv("BRAVE_SEARCH_API_KEY")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.BraveSearchAPIKey != "brave-test-key" {
		t.Errorf("BraveSearchAPIKey = %q, want brave-test-key", cfg.BraveSearchAPIKey)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run (from `web-svc/`): `go test ./config/...`
Expected: FAIL — `cfg.BraveSearchAPIKey undefined (type *config.Config has no field or method BraveSearchAPIKey)`.

- [ ] **Step 3: Write the implementation**

In `web-svc/config/config.go`, add the field to the `Config` struct (after `ShareLinkTTL`):

```go
type Config struct {
	HTTPPort           string
	SupabaseURL        string
	SupabaseJWTSecret  string
	OwnerEmail         string
	CORSAllowedOrigin  string
	PerceptionSvcURL   string
	MemorySvcURL       string
	ThinkingSvcURL     string
	ActionSvcURL       string
	SoulmanRoot        string
	ObsidianRoot       string
	ClaudeProjectRoots []sharedconfig.ClaudeProjectRoot
	FileBrowserRoots   []sharedconfig.FileBrowserRoot
	ShareLinkTTL       time.Duration
	BraveSearchAPIKey  string
}
```

And add the field to the returned `&Config{...}` literal in `Load()` (after `ShareLinkTTL: shareLinkTTL,`):

```go
		ShareLinkTTL:       shareLinkTTL,
		BraveSearchAPIKey:  env("BRAVE_SEARCH_API_KEY", ""),
	}, nil
}
```

No new validation — this mirrors `SupabaseJWTSecret`'s non-fatal-if-blank treatment, not `ObsidianRoot`'s required-non-empty check, since search is a separate optional feature rather than a startup precondition.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./config/...`
Expected: PASS (all existing tests plus the 2 new ones).

- [ ] **Step 5: Commit**

```bash
git add web-svc/config/config.go web-svc/config/config_test.go
git commit -m "feat(web-svc): read BRAVE_SEARCH_API_KEY from the environment"
```

---

### Task 3: `web-svc/httpserver` — `GET /api/search` endpoint + `main.go` wiring

**Files:**
- Modify: `web-svc/httpserver/server.go`
- Create: `web-svc/httpserver/search_handler.go`
- Test: `web-svc/httpserver/search_handler_test.go`
- Modify: `web-svc/main.go`

**Interfaces:**
- Consumes: `websearch.NewClient`, `websearch.DefaultBaseURL`, `websearch.ErrNoAPIKey`, `websearch.Result` (Task 1); `config.Config.BraveSearchAPIKey` (Task 2); existing `writeJSON`/`writeJSONError` (already in `web-svc/httpserver/obsidian_handler.go`, same package).
- Produces: route `GET /api/search?q=` (owner-JWT-gated), response `{"results":[{"title","url","snippet"}]}`.

- [ ] **Step 1: Write the failing tests**

Create `web-svc/httpserver/search_handler_test.go`:

```go
package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"soulman/web-svc/auth"
	"soulman/web-svc/httpserver"
)

func TestAPISearch_ReturnsResults(t *testing.T) {
	brave := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"web":{"results":[{"title":"Soulman","url":"https://example.com","description":"desc"}]}}`))
	}))
	defer brave.Close()

	cfg := httpserver.Config{
		CORSAllowedOrigin:  "http://localhost:5178",
		BraveSearchAPIKey:  "test-key",
		BraveSearchBaseURL: brave.URL,
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/search?q=soulman", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Snippet string `json:"snippet"`
		} `json:"results"`
	}
	json.NewDecoder(rec.Body).Decode(&body)
	if len(body.Results) != 1 || body.Results[0].Title != "Soulman" {
		t.Errorf("results = %+v", body.Results)
	}
}

func TestAPISearch_NoToken_Returns401(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", BraveSearchAPIKey: "test-key"}
	srv := httpserver.New("9005", cfg, auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail))

	req := httptest.NewRequest(http.MethodGet, "/api/search?q=soulman", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestAPISearch_EmptyQuery_Returns400(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", BraveSearchAPIKey: "test-key"}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/search?q=", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPISearch_NoAPIKeyConfigured_Returns503(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178"}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/search?q=soulman", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPISearch_BraveError_Returns502(t *testing.T) {
	brave := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer brave.Close()

	cfg := httpserver.Config{
		CORSAllowedOrigin:  "http://localhost:5178",
		BraveSearchAPIKey:  "test-key",
		BraveSearchBaseURL: brave.URL,
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/search?q=soulman", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPISearch_EmptyResults_Returns200WithEmptyList(t *testing.T) {
	brave := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"web":{"results":[]}}`))
	}))
	defer brave.Close()

	cfg := httpserver.Config{
		CORSAllowedOrigin:  "http://localhost:5178",
		BraveSearchAPIKey:  "test-key",
		BraveSearchBaseURL: brave.URL,
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/search?q=soulman", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Results []struct{} `json:"results"`
	}
	json.NewDecoder(rec.Body).Decode(&body)
	if len(body.Results) != 0 {
		t.Errorf("results = %+v, want empty", body.Results)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run (from `web-svc/`): `go test ./httpserver/...`
Expected: FAIL — `unknown field BraveSearchAPIKey in struct literal of type httpserver.Config` (the `Config` struct doesn't have these fields yet, and `/api/search` isn't routed).

- [ ] **Step 3: Write the implementation**

In `web-svc/httpserver/server.go`, add the import:

```go
	"soulman/web-svc/claudesession"
	"soulman/web-svc/filebrowser"
	"soulman/web-svc/websearch"
```

Add two fields to `Config` (after `ShareLinkTTL time.Duration`):

```go
type Config struct {
	CORSAllowedOrigin  string
	PerceptionSvcURL   string
	MemorySvcURL       string
	ThinkingSvcURL     string
	ActionSvcURL       string
	ReportsRoot        string
	ObsidianRoot       string
	ClaudeProjectRoots []claudesession.Root
	FileBrowserRoots   []filebrowser.Root
	ShareLinkSecret    []byte
	ShareLinkTTL       time.Duration
	BraveSearchAPIKey  string
	// BraveSearchBaseURL overrides websearch.DefaultBaseURL when non-empty.
	// Production leaves this blank; tests set it to an httptest.Server URL.
	BraveSearchBaseURL string
}
```

Add a `searchClient` field to `Server` and construct it in `New`:

```go
type Server struct {
	port         string
	cfg          Config
	verifier     *auth.Verifier
	httpClient   *http.Client
	searchClient *websearch.Client
	router       chi.Router
}

func New(port string, cfg Config, verifier *auth.Verifier) *Server {
	baseURL := cfg.BraveSearchBaseURL
	if baseURL == "" {
		baseURL = websearch.DefaultBaseURL
	}
	s := &Server{
		port:         port,
		cfg:          cfg,
		verifier:     verifier,
		httpClient:   &http.Client{Timeout: 5 * time.Second},
		searchClient: websearch.NewClient(cfg.BraveSearchAPIKey, baseURL),
	}
	s.router = s.buildRouter()
	return s
}
```

Add the route inside the existing `r.Use(s.verifier.Middleware)` group in `buildRouter`, right after `r.Post("/api/files/share", s.filesShare)`:

```go
		r.Post("/api/files/share", s.filesShare)
		r.Get("/api/search", s.search)
```

Create `web-svc/httpserver/search_handler.go`:

```go
package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"soulman/web-svc/websearch"
)

// searchTimeout bounds how long a single Brave Search API call may take,
// derived from the incoming request's context the same way isHealthy
// bounds its own outbound calls.
const searchTimeout = 5 * time.Second

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		writeJSONError(w, http.StatusBadRequest, "q is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), searchTimeout)
	defer cancel()

	results, err := s.searchClient.Search(ctx, query)
	if err != nil {
		if errors.Is(err, websearch.ErrNoAPIKey) {
			writeJSONError(w, http.StatusServiceUnavailable, "web search is not configured")
			return
		}
		slog.Error("web search failed", "error", err)
		writeJSONError(w, http.StatusBadGateway, "web search failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string][]websearch.Result{"results": results})
}
```

`web-svc/main.go` needs no new import — `log/slog` is already imported, and `websearch` is only referenced inside `httpserver`, not directly here. After the `fileBrowserRoots` loop and before building `shareLinkSecret`, add the startup warning:

```go
	if cfg.BraveSearchAPIKey == "" {
		slog.Warn("BRAVE_SEARCH_API_KEY not set — web search requests will fail until it's configured")
	}
```

Add the field to the `httpserver.Config{...}` literal passed to `httpserver.New` (after `ShareLinkTTL: cfg.ShareLinkTTL,`):

```go
		ShareLinkTTL:      cfg.ShareLinkTTL,
		BraveSearchAPIKey: cfg.BraveSearchAPIKey,
	}, verifier)
```

(`BraveSearchBaseURL` is deliberately left unset here — zero value `""`, so `httpserver.New` defaults it to `websearch.DefaultBaseURL`.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./...` (from `web-svc/`)
Expected: PASS — every existing `web-svc` test plus the 6 new ones in `search_handler_test.go`.

- [ ] **Step 5: Commit**

```bash
git add web-svc/httpserver/server.go web-svc/httpserver/search_handler.go web-svc/httpserver/search_handler_test.go web-svc/main.go
git commit -m "feat(web-svc): add GET /api/search endpoint backed by Brave Search"
```

---

### Task 4: `web/src/api.ts` — `search()` client function

**Files:**
- Modify: `web/src/api.ts`
- Modify: `web/src/api.test.ts`

**Interfaces:**
- Produces: `SearchResult{title, url, snippet: string}`, `SearchResults{results: SearchResult[]}`, `search(token: string | null, query: string): Promise<SearchResults>` — consumed by Task 5's `SearchPage.tsx`.

- [ ] **Step 1: Write the failing tests**

In `web/src/api.test.ts`, add `search` to the import list at the top (after `shareFile,`):

```ts
  shareFile,
  search,
  ApiError,
} from './api';
```

Add at the end of the file:

```ts
describe('search', () => {
  it('passes the q query param and returns parsed results', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ results: [{ title: 'Soulman', url: 'https://example.com', snippet: 'desc' }] }),
    });

    const result = await search('tok-abc', 'soulman ai agent');

    expect(result.results).toHaveLength(1);
    expect(result.results[0].title).toBe('Soulman');
    const [url] = mockFetch.mock.calls[0];
    expect(url).toContain('/api/search');
    expect(url).toContain('q=soulman%20ai%20agent');
  });

  it('throws ApiError on a non-ok response', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: false, status: 503 });

    await expect(search('tok-abc', 'soulman')).rejects.toThrow(ApiError);
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run (from `web/`): `npx vitest run src/api.test.ts`
Expected: FAIL — `does not provide an export named 'search'`.

- [ ] **Step 3: Write the implementation**

At the end of `web/src/api.ts`, add:

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

- [ ] **Step 4: Run tests to verify they pass**

Run: `npx vitest run src/api.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/api.ts web/src/api.test.ts
git commit -m "feat(web): add search() API client function"
```

---

### Task 5: `web/src/components/SearchPage.tsx` — the search UI

**Files:**
- Create: `web/src/components/SearchPage.tsx`
- Test: `web/src/components/SearchPage.test.tsx`

**Interfaces:**
- Consumes: `search`, `SearchResult`, `ApiError` from `../api` (Task 4); `getAccessToken` from `../auth`; `getParam`/`setParams` from `../urlState`.
- Produces: `SearchPage({ onBack: () => void })` — consumed by Task 6's `App.tsx`.

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/SearchPage.test.tsx`:

```tsx
// web/src/components/SearchPage.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ApiError } from '../api';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockSearch = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return { ...actual, search: (...args: unknown[]) => mockSearch(...args) };
});

beforeEach(() => {
  vi.clearAllMocks();
  window.history.replaceState(null, '', '/');
});

describe('SearchPage', () => {
  it('shows the centered empty state before any search', async () => {
    const { SearchPage } = await import('./SearchPage');
    render(<SearchPage onBack={vi.fn()} />);

    expect(await screen.findByText('Soulman Search')).toBeInTheDocument();
    expect(screen.queryByText('No results found.')).not.toBeInTheDocument();
  });

  it('submits a query and renders results as links with snippets', async () => {
    mockSearch.mockResolvedValue({
      results: [{ title: 'Soulman', url: 'https://example.com/soulman', snippet: 'A personal AI agent.' }],
    });
    const { SearchPage } = await import('./SearchPage');
    render(<SearchPage onBack={vi.fn()} />);

    await userEvent.type(screen.getByLabelText('Search query'), 'soulman ai agent');
    await userEvent.click(screen.getByRole('button', { name: 'Search' }));

    expect(mockSearch).toHaveBeenCalledWith('tok-abc', 'soulman ai agent');
    const link = await screen.findByRole('link', { name: 'Soulman' });
    expect(link).toHaveAttribute('href', 'https://example.com/soulman');
    expect(link).toHaveAttribute('target', '_blank');
    expect(screen.getByText('A personal AI agent.')).toBeInTheDocument();
  });

  it('shows "No results found." for a successful search with zero results', async () => {
    mockSearch.mockResolvedValue({ results: [] });
    const { SearchPage } = await import('./SearchPage');
    render(<SearchPage onBack={vi.fn()} />);

    await userEvent.type(screen.getByLabelText('Search query'), 'nothing here');
    await userEvent.click(screen.getByRole('button', { name: 'Search' }));

    expect(await screen.findByText('No results found.')).toBeInTheDocument();
  });

  it('shows a "not configured" banner on a 503', async () => {
    mockSearch.mockRejectedValue(new ApiError(503, '/api/search?q=x failed (503)'));
    const { SearchPage } = await import('./SearchPage');
    render(<SearchPage onBack={vi.fn()} />);

    await userEvent.type(screen.getByLabelText('Search query'), 'soulman');
    await userEvent.click(screen.getByRole('button', { name: 'Search' }));

    expect(await screen.findByText('Web search is not configured')).toBeInTheDocument();
  });

  it('shows a generic failure banner on any other error', async () => {
    mockSearch.mockRejectedValue(new ApiError(502, '/api/search?q=x failed (502)'));
    const { SearchPage } = await import('./SearchPage');
    render(<SearchPage onBack={vi.fn()} />);

    await userEvent.type(screen.getByLabelText('Search query'), 'soulman');
    await userEvent.click(screen.getByRole('button', { name: 'Search' }));

    expect(await screen.findByText('Web search failed')).toBeInTheDocument();
  });

  it('does not call search when the query is empty', async () => {
    const { SearchPage } = await import('./SearchPage');
    render(<SearchPage onBack={vi.fn()} />);

    await userEvent.click(screen.getByRole('button', { name: 'Search' }));

    expect(mockSearch).not.toHaveBeenCalled();
  });

  it('restores and re-runs a search from a q URL param on mount', async () => {
    window.history.replaceState(null, '', '/?page=search&q=restored');
    mockSearch.mockResolvedValue({ results: [{ title: 'Restored', url: 'https://example.com', snippet: 's' }] });
    const { SearchPage } = await import('./SearchPage');
    render(<SearchPage onBack={vi.fn()} />);

    expect(await screen.findByRole('link', { name: 'Restored' })).toBeInTheDocument();
    expect(mockSearch).toHaveBeenCalledWith('tok-abc', 'restored');
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run (from `web/`): `npx vitest run src/components/SearchPage.test.tsx`
Expected: FAIL — `Failed to resolve import "./SearchPage"`.

- [ ] **Step 3: Write the implementation**

Create `web/src/components/SearchPage.tsx`:

```tsx
import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { search, ApiError, type SearchResult } from '../api';
import { getParam, setParams } from '../urlState';

export function SearchPage({ onBack }: { onBack: () => void }) {
  const [query, setQuery] = useState(getParam('q') ?? '');
  const [results, setResults] = useState<SearchResult[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const runSearch = async (q: string) => {
    if (!q.trim()) return;
    setLoading(true);
    setError(null);
    const token = await getAccessToken();
    try {
      const data = await search(token, q);
      setResults(data.results);
      setParams({ page: 'search', q });
    } catch (err) {
      if (err instanceof ApiError && err.status === 503) {
        setError('Web search is not configured');
      } else {
        setError('Web search failed');
      }
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (query) runSearch(query);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const searchBox = (
    <form
      className="flex gap-2"
      onSubmit={(e) => {
        e.preventDefault();
        runSearch(query);
      }}
    >
      <label htmlFor="search-query" className="sr-only">
        Search query
      </label>
      <input
        id="search-query"
        type="text"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        className="flex-1 rounded border px-3 py-2 text-sm"
        placeholder="Search the web..."
      />
      <button type="submit" className="rounded border bg-white px-4 py-2 text-sm">
        Search
      </button>
    </form>
  );

  if (results === null) {
    return (
      <div className="min-h-screen bg-gray-50 p-6">
        <div className="mb-6 flex items-center justify-between">
          <h1 className="text-2xl font-semibold">Web-Search</h1>
          <button onClick={onBack} className="text-sm text-gray-500 underline">
            ← Soulman
          </button>
        </div>
        <div className="mx-auto mt-24 max-w-xl">
          <h2 className="mb-6 text-center text-xl font-medium">Soulman Search</h2>
          {searchBox}
          {loading && <p className="mt-4 text-center text-sm text-gray-500">Searching…</p>}
          {error && <p className="mt-4 text-center text-sm text-red-600">{error}</p>}
        </div>
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-gray-50 p-6">
      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Web-Search</h1>
        <button onClick={onBack} className="text-sm text-gray-500 underline">
          ← Soulman
        </button>
      </div>
      <div className="mx-auto max-w-2xl">
        <div className="mb-4">{searchBox}</div>
        {loading && <p className="text-sm text-gray-500">Searching…</p>}
        {error && <p className="text-sm text-red-600">{error}</p>}
        {!loading && !error && results.length === 0 && (
          <p className="text-sm text-gray-500">No results found.</p>
        )}
        <ul className="space-y-4">
          {results.map((r, i) => (
            <li key={`${r.url}-${i}`}>
              <a href={r.url} target="_blank" rel="noopener noreferrer" className="text-blue-700 underline">
                {r.title}
              </a>
              <p className="text-xs text-gray-400">{r.url}</p>
              <p className="text-sm text-gray-700">{r.snippet}</p>
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npx vitest run src/components/SearchPage.test.tsx`
Expected: PASS (7 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/components/SearchPage.tsx web/src/components/SearchPage.test.tsx
git commit -m "feat(web): add SearchPage with search box and result list"
```

---

### Task 6: Nav wiring — "Web-Search" link in `Dashboard.tsx` + `App.tsx` view state

**Files:**
- Modify: `web/src/components/Dashboard.tsx`
- Modify: `web/src/App.tsx`
- Modify: `web/src/App.test.tsx`

**Interfaces:**
- Consumes: `SearchPage` from `./components/SearchPage` (Task 5).
- Produces: nothing further downstream — this is the final integration point.

- [ ] **Step 1: Write the failing tests**

Add to `web/src/App.test.tsx`, inside the `describe('App', ...)` block, after the `'switches to the files page and back via the header link'` test:

```tsx
  it('switches to the search page and back via the header link', async () => {
    mockUseAuth.mockReturnValue({ user: { email: 'breynisson@gmail.com' }, loading: false, signIn: vi.fn(), signOut: vi.fn() });
    mockGetStatus.mockResolvedValue({ 'memory-svc': 'up' });
    const { default: App } = await import('./App');
    render(<App />);
    await screen.findByText(/soulman dashboard/i);

    await userEvent.click(screen.getByRole('button', { name: /web-search/i }));

    expect(await screen.findByRole('heading', { name: /web-search/i })).toBeInTheDocument();

    await userEvent.click(screen.getByText(/soulman/i));

    expect(await screen.findByText(/soulman dashboard/i)).toBeInTheDocument();
  });

  it('restores the search page from a page=search URL param on mount', async () => {
    window.history.replaceState(null, '', '/?page=search');
    mockUseAuth.mockReturnValue({
      user: { email: 'breynisson@gmail.com' },
      loading: false,
      signIn: vi.fn(),
      signOut: vi.fn(),
    });
    mockGetStatus.mockResolvedValue({ 'memory-svc': 'up' });
    const { default: App } = await import('./App');
    render(<App />);

    expect(await screen.findByRole('heading', { name: /web-search/i })).toBeInTheDocument();
  });
```

- [ ] **Step 2: Run tests to verify they fail**

Run (from `web/`): `npx vitest run src/App.test.tsx`
Expected: FAIL — no button named `/web-search/i` exists yet in `Dashboard`.

- [ ] **Step 3: Write the implementation**

In `web/src/components/Dashboard.tsx`, add `onOpenSearch` to the props and a button between Files and Sign out:

```tsx
import type { ServiceStatus } from '../api';
import { StatusPanel } from './StatusPanel';
import { EpisodesPanel } from './EpisodesPanel';
import { RawInputsPanel } from './RawInputsPanel';
import { ReportsPanel } from './ReportsPanel';

export function Dashboard({
  initialStatus,
  onSignOut,
  onOpenObsidian,
  onOpenClaude,
  onOpenFiles,
  onOpenSearch,
}: {
  initialStatus: ServiceStatus | null;
  onSignOut: () => void;
  onOpenObsidian: () => void;
  onOpenClaude: () => void;
  onOpenFiles: () => void;
  onOpenSearch: () => void;
}) {
  return (
    <div className="min-h-screen bg-gray-50 p-6">
      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Soulman Dashboard</h1>
        <div className="flex items-center gap-4">
          <button onClick={onOpenClaude} className="text-sm text-gray-500 underline">
            Claude
          </button>
          <button onClick={onOpenObsidian} className="text-sm text-gray-500 underline">
            Obsidian
          </button>
          <button onClick={onOpenFiles} className="text-sm text-gray-500 underline">
            Files
          </button>
          <button onClick={onOpenSearch} className="text-sm text-gray-500 underline">
            Web-Search
          </button>
          <button onClick={onSignOut} className="text-sm text-gray-500 underline">
            Sign out
          </button>
        </div>
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

In `web/src/App.tsx`, add the import (after `import { FilesPage } from './components/FilesPage';`):

```tsx
import { FilesPage } from './components/FilesPage';
import { SearchPage } from './components/SearchPage';
```

Extend `ViewState` and `viewFromPageParam`:

```tsx
type ViewState = 'loading' | 'login' | 'restricted' | 'dashboard' | 'obsidian' | 'claude' | 'files' | 'search';

function viewFromPageParam(): ViewState {
  const page = getParam('page');
  if (page === 'obsidian') return 'obsidian';
  if (page === 'claude') return 'claude';
  if (page === 'files') return 'files';
  if (page === 'search') return 'search';
  return 'dashboard';
}
```

Add a `view === 'search'` branch (after the existing `if (view === 'files') { ... }` block):

```tsx
  if (view === 'search') {
    return (
      <SearchPage
        onBack={() => {
          setParams({ page: null, q: null });
          setView('dashboard');
        }}
      />
    );
  }
```

Wire the new prop on `<Dashboard>` (after `onOpenFiles={...}`):

```tsx
      onOpenFiles={() => {
        setParams({ page: 'files' });
        setView('files');
      }}
      onOpenSearch={() => {
        setParams({ page: 'search' });
        setView('search');
      }}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npx vitest run src/App.test.tsx`
Expected: PASS. Then run the full frontend suite: `npm test` (from `web/`) — expected: PASS, all files.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/Dashboard.tsx web/src/App.tsx web/src/App.test.tsx
git commit -m "feat(web): wire Web-Search into the dashboard nav and view routing"
```

---

### Task 7: Documentation — `CLAUDE.md` and `web-svc/NOTES.md`

**Files:**
- Modify: `CLAUDE.md`
- Modify: `web-svc/NOTES.md`

**Interfaces:** none — documentation only, no code.

- [ ] **Step 1: Update `CLAUDE.md`'s web-svc bullet**

In the root `CLAUDE.md`'s Services section, item 5 (`web-svc`), find this sentence fragment:

```
plus — added the same day, immediately after the file browser — `POST /api/files/share` and `GET /dl/{token}` (time-limited share links: the JWT-protected share endpoint issues an HMAC-signed token for one file, and `/dl/{token}` serves it. **`GET /dl/{token}` is the one route in this entire service that bypasses owner-JWT auth** — by design: the signed, time-limited token, default TTL 60 minutes via `web.share_link_ttl_minutes`, is the only credential). Does not touch NATS at all.
```

Replace it with (inserting the new endpoint before "Does not touch NATS at all"):

```
plus — added the same day, immediately after the file browser — `POST /api/files/share` and `GET /dl/{token}` (time-limited share links: the JWT-protected share endpoint issues an HMAC-signed token for one file, and `/dl/{token}` serves it. **`GET /dl/{token}` is the one route in this entire service that bypasses owner-JWT auth** — by design: the signed, time-limited token, default TTL 60 minutes via `web.share_link_ttl_minutes`, is the only credential), and — added 2026-08-30 — `GET /api/search?q=` (proxies the Brave Search API via `web-svc/websearch`, owner-JWT-gated, results capped at 10 with no pagination; needs the `BRAVE_SEARCH_API_KEY` env var — non-fatal if blank at startup, but a search request then returns 503). Does not touch NATS at all.
```

Then find the web-svc Specs line:

```
   - Specs: `2026-07-19-soulman-web-dashboard-design.md`, `2026-07-20-system-monitor-dashboard-panel-design.md`, `2026-07-20-dashboard-status-merge-and-raw-input-modal-design.md`, `2026-07-20-daily-report-importance-split-design.md`, `2026-08-07-obsidian-file-viewer-design.md`, `2026-08-09-claude-remote-sessions-design.md`, `2026-08-19-file-browser-design.md`, `2026-08-19-file-sharing-design.md`
```

Replace it with:

```
   - Specs: `2026-07-19-soulman-web-dashboard-design.md`, `2026-07-20-system-monitor-dashboard-panel-design.md`, `2026-07-20-dashboard-status-merge-and-raw-input-modal-design.md`, `2026-07-20-daily-report-importance-split-design.md`, `2026-08-07-obsidian-file-viewer-design.md`, `2026-08-09-claude-remote-sessions-design.md`, `2026-08-19-file-browser-design.md`, `2026-08-19-file-sharing-design.md`, `2026-08-30-web-search-design.md`
```

- [ ] **Step 2: Add a `web-svc/NOTES.md` section for the new env var**

In `web-svc/NOTES.md`, insert a new section immediately after the existing `## SUPABASE_URL / SUPABASE_JWT_SECRET are not in this repo` section (before `## Owner-email check, not a roles table`):

```markdown
## BRAVE_SEARCH_API_KEY is also env-only, non-fatal if blank

Same treatment as `DEEPSEEK_API_KEY` in `thinking-svc`, not `SUPABASE_URL`'s fatal-if-blank one: `web-svc` starts fine with it unset (a `slog.Warn` at startup), but any `GET /api/search` request made while it's blank returns `503`. Set via `.env` in `soulman-dev\` and `soulman-prod\`, same file `SUPABASE_URL` lives in. No shared-config (`config/dev.json`/`prod.json`) entry exists for this — it's a secret, not a `web.*` setting.
```

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md web-svc/NOTES.md
git commit -m "docs: document the web search feature"
```

---

## Final Verification

After Task 7's commit, run the full test suites once more from a clean state to confirm nothing regressed:

- `cd web-svc && go test ./...` — expected: PASS, all packages.
- `cd web && npm test` — expected: PASS, all files.

Then exercise the feature manually against dev (`localhost:5190`, `soulman-dev`'s `web-svc` on `9015`) per the spec's Testing section: click "Web-Search" from the dashboard, run a real query, confirm results render as links with snippets and open in a new tab, temporarily unset `BRAVE_SEARCH_API_KEY` in `soulman-dev\.env` and confirm the "Web search is not configured" banner appears (then restore the key and restart `web-svc` via `run-web-svc.ps1` — see the `deploy-soulman-services` skill), and confirm a reload with `?page=search&q=...` in the URL restores the search.
