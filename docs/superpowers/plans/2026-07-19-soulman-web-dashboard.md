# Soulman Web Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `web-svc` (a new 5th Go service) and `web/` (a new React dashboard) so Soulman has a read-only, Supabase-authenticated web UI showing episodes, raw inputs, daily reports, and service health.

**Architecture:** `web/` (React 19 + Vite + TS + Tailwind 4) authenticates via Supabase Google OAuth (reusing `agent-suite`'s existing hosted project) and calls `web-svc` (a new Go service, chi router) with a Bearer JWT on every request. `web-svc` is the only Soulman component with CORS and JWT verification enabled: it checks the token's `email` claim against a configured owner email, then proxies reads to `memory-svc`'s existing HTTP API, reads daily report files directly off disk, and aggregates `/health` from all four existing services. `perception-svc`, `thinking-svc`, and `action-svc` are not modified.

**Tech Stack:** Go 1.25 (`go-chi/chi/v5`, `go-chi/cors`, `golang-jwt/jwt/v5`), React 19 + Vite 8 + TypeScript + Tailwind CSS 4, Vitest + React Testing Library for frontend tests, `@supabase/supabase-js`.

## Global Constraints

- Reuse `agent-suite`'s existing hosted Supabase project (ref `grgspbzqzjblsoxmmojy`) and its existing Google OAuth client — no new Supabase project, no new Google Cloud OAuth client.
- Authorization is a single owner-email check (`breynisson@gmail.com`) — no roles table.
- `perception-svc`, `thinking-svc`, `action-svc` HTTP surfaces are not modified in any task.
- `web-svc` is the only new backend surface with CORS + auth. It calls the other four services over plain HTTP (no NATS involvement at all).
- Override/control dispatch (PAUSE/STOP/RESUME) is out of scope — not implemented in any task here.
- Follow existing repo conventions exactly: one Go module per service, `sharedconfig` for non-secret per-environment JSON config, env vars for secrets, dev ports = prod ports + 10000... actually dev = prod + 10 offset per existing services (`900X` prod / `901X` dev) — `web-svc` uses `9005` prod / `9015` dev.
- Every code file created must be `git add`ed (per user's global CLAUDE.md instruction), and each task ends in a commit.

---

## File Structure

**`web-svc/` (new Go module):**
- `web-svc/go.mod`, `web-svc/main.go` — module def, entrypoint
- `web-svc/config/config.go` (+ `config_test.go`) — env vars + sharedconfig loading, fatal validation
- `web-svc/auth/verifier.go` (+ `verifier_test.go`) — JWT verification (HS256/ES256), owner-email check, HTTP middleware
- `web-svc/httpserver/server.go` (+ `server_test.go`) — chi router, CORS, `/health`, `/api/status`
- `web-svc/httpserver/proxy.go` (+ `proxy_test.go`) — generic upstream-JSON-proxy handler used by `/api/episodes` and `/api/raw-inputs/recent`
- `web-svc/reports/reports.go` (+ `reports_test.go`) — daily report file path/read logic
- `web-svc/httpserver/reports_handler.go` (+ `reports_handler_test.go`) — `/api/reports/latest`, `/api/reports`
- `web-svc/NOTES.md`

**`web/` (new frontend, Vite scaffold + these hand-written files):**
- `web/src/auth.ts` (+ `auth.test.ts`) — Supabase client, `useAuth()`, `getAccessToken()`
- `web/src/api.ts` (+ `api.test.ts`) — typed fetch wrappers for `web-svc`
- `web/src/App.tsx` (+ `App.test.tsx`) — login/restricted/dashboard state routing
- `web/src/components/LoginScreen.tsx`, `RestrictedScreen.tsx`, `Dashboard.tsx`, `StatusPanel.tsx` (+ test), `EpisodesPanel.tsx` (+ test), `RawInputsPanel.tsx`, `ReportsPanel.tsx` (+ test)
- `web/src/setupTests.ts`, `web/.env`, `web/.env.production`, `web/.env.test`

**Existing files modified:**
- `common/sharedconfig/config.go` — add `WebConfig`
- `config/dev.json`, `config/prod.json` — add `web` block
- `C:\Users\Lenovo\soulman-dev\run-web-svc.ps1`, `C:\Users\Lenovo\soulman-prod\run-web-svc.ps1` — new launch scripts (outside the vault repo, not git-tracked)
- `C:\Users\Lenovo\start-everything.ps1` — add `"web-svc"` to the service list
- `CLAUDE.md` — document the new service and frontend

---

### Task 1: `web-svc` module scaffold, shared config, and `/health`

**Files:**
- Create: `web-svc/go.mod`
- Create: `web-svc/config/config.go`
- Create: `web-svc/config/config_test.go`
- Create: `web-svc/httpserver/server.go`
- Create: `web-svc/httpserver/server_test.go`
- Create: `web-svc/main.go`
- Modify: `common/sharedconfig/config.go`
- Modify: `config/dev.json`
- Modify: `config/prod.json`

**Interfaces:**
- Produces: `config.Config{HTTPPort string}`, `config.Load() (*config.Config, error)`; `httpserver.Server` with `New(port string) *Server`, `(*Server).Handler() http.Handler`, `(*Server).Start() error`.

- [ ] **Step 1: Add `WebConfig` to `common/sharedconfig/config.go`**

Add this struct and wire it into `Config`:

```go
// WebConfig holds web-svc's settings: the single owner email allowed full
// dashboard access, the frontend origin CORS must allow, and the base URLs
// of the four services web-svc calls into. Unlike GmailConfig/
// SystemMonitorConfig, every field here is required — web-svc has no
// degraded "partially configured" mode.
type WebConfig struct {
	OwnerEmail        string `json:"owner_email"`
	CORSAllowedOrigin string `json:"cors_allowed_origin"`
	PerceptionSvcURL  string `json:"perception_svc_url"`
	MemorySvcURL      string `json:"memory_svc_url"`
	ThinkingSvcURL    string `json:"thinking_svc_url"`
	ActionSvcURL      string `json:"action_svc_url"`
}
```

Add `Web WebConfig \`json:"web"\`` as a new field on `Config`.

- [ ] **Step 2: Add the `web` block to `config/dev.json` and `config/prod.json`**

In `config/dev.json`, add (alongside the existing `gmail`/`system_monitor` blocks):

```json
"web": {
  "owner_email": "breynisson@gmail.com",
  "cors_allowed_origin": "http://localhost:5178",
  "perception_svc_url": "http://localhost:9011",
  "memory_svc_url": "http://localhost:9012",
  "thinking_svc_url": "http://localhost:9013",
  "action_svc_url": "http://localhost:9014"
}
```

In `config/prod.json`, add:

```json
"web": {
  "owner_email": "breynisson@gmail.com",
  "cors_allowed_origin": "http://localhost:4173",
  "perception_svc_url": "http://localhost:9001",
  "memory_svc_url": "http://localhost:9002",
  "thinking_svc_url": "http://localhost:9003",
  "action_svc_url": "http://localhost:9004"
}
```

(The prod `cors_allowed_origin` is a placeholder until Cloudflare exposure is set up later — out of scope here.)

- [ ] **Step 3: Write `web-svc/go.mod`**

```go
module soulman/web-svc

go 1.25.0

require soulman/common v0.0.0

replace soulman/common => ../common
```

- [ ] **Step 4: Write the failing test for `config.Load`**

`web-svc/config/config_test.go`:

```go
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"soulman/web-svc/config"
)

func writeConfigFile(t *testing.T, dir string, contents string) string {
	t.Helper()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("writing config file: %v", err)
	}
	return path
}

const validConfigJSON = `{
  "web": {
    "owner_email": "breynisson@gmail.com",
    "cors_allowed_origin": "http://localhost:5178",
    "perception_svc_url": "http://localhost:9011",
    "memory_svc_url": "http://localhost:9012",
    "thinking_svc_url": "http://localhost:9013",
    "action_svc_url": "http://localhost:9014"
  }
}`

func TestLoad_DefaultsHTTPPortTo9005(t *testing.T) {
	dir := t.TempDir()
	path := writeConfigFile(t, dir, validConfigJSON)
	os.Setenv("CONFIG_PATH", path)
	defer os.Unsetenv("CONFIG_PATH")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPPort != "9005" {
		t.Errorf("HTTPPort = %q, want 9005", cfg.HTTPPort)
	}
}

func TestLoad_HTTPPortEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := writeConfigFile(t, dir, validConfigJSON)
	os.Setenv("CONFIG_PATH", path)
	os.Setenv("HTTP_PORT", "9015")
	defer os.Unsetenv("CONFIG_PATH")
	defer os.Unsetenv("HTTP_PORT")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPPort != "9015" {
		t.Errorf("HTTPPort = %q, want 9015", cfg.HTTPPort)
	}
}

func TestLoad_MissingConfigFile_ReturnsError(t *testing.T) {
	os.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "does-not-exist.json"))
	defer os.Unsetenv("CONFIG_PATH")

	if _, err := config.Load(); err == nil {
		t.Fatal("Load() error = nil, want an error for a missing config file")
	}
}
```

- [ ] **Step 5: Run the test to verify it fails**

Run: `go -C web-svc test ./config/...`
Expected: FAIL — `package soulman/web-svc/config: no Go files` or similar, since `config.go` doesn't exist yet.

- [ ] **Step 6: Implement `web-svc/config/config.go`**

```go
package config

import (
	"fmt"
	"os"

	"soulman/common/sharedconfig"
)

type Config struct {
	HTTPPort string
}

func Load() (*Config, error) {
	configPath := env("CONFIG_PATH", "./config.json")

	if _, err := sharedconfig.Load(configPath); err != nil {
		return nil, fmt.Errorf("loading shared config: %w", err)
	}

	return &Config{
		HTTPPort: env("HTTP_PORT", "9005"),
	}, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
```

- [ ] **Step 7: Run the test to verify it passes**

Run: `go -C web-svc test ./config/...`
Expected: PASS (3 tests)

- [ ] **Step 8: Write the failing test for the HTTP server's `/health`**

`web-svc/httpserver/server_test.go`:

```go
package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"soulman/web-svc/httpserver"
)

func TestHealth_ReturnsOK(t *testing.T) {
	srv := httpserver.New("9005")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status field = %q, want ok", body["status"])
	}
}
```

- [ ] **Step 9: Run the test to verify it fails**

Run: `go -C web-svc test ./httpserver/...`
Expected: FAIL — package doesn't exist yet.

- [ ] **Step 10: Implement `web-svc/httpserver/server.go`**

```go
package httpserver

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Server struct {
	port   string
	router chi.Router
}

func New(port string) *Server {
	s := &Server{port: port}
	s.router = s.buildRouter()
	return s
}

func (s *Server) Handler() http.Handler { return s.router }

func (s *Server) Start() error {
	return http.ListenAndServe(":"+s.port, s.router)
}

func (s *Server) buildRouter() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Get("/health", s.health)
	return r
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
```

- [ ] **Step 11: Add `go-chi/chi` dependency and run tests**

Run:
```
go -C web-svc get github.com/go-chi/chi/v5@v5.3.1
go -C web-svc mod tidy
go -C web-svc test ./...
```
Expected: PASS (config tests + `TestHealth_ReturnsOK`)

- [ ] **Step 12: Write `web-svc/main.go`**

```go
package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"soulman/web-svc/config"
	"soulman/web-svc/httpserver"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	srv := httpserver.New(cfg.HTTPPort)
	go func() {
		log.Printf("HTTP listening on :%s", cfg.HTTPPort)
		if err := srv.Start(); err != nil {
			log.Printf("http: %v", err)
		}
	}()

	log.Printf("web-svc started (HTTP=:%s)", cfg.HTTPPort)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Printf("web-svc shutting down")
}
```

- [ ] **Step 13: Verify the whole module builds**

Run: `go -C web-svc build ./...`
Expected: exits 0, no output

- [ ] **Step 14: Commit**

```bash
git add web-svc/go.mod web-svc/go.sum web-svc/config web-svc/httpserver web-svc/main.go common/sharedconfig/config.go config/dev.json config/prod.json
git commit -m "feat(web-svc): scaffold module, shared config, and /health"
```

---

### Task 2: JWT verification (auth package)

**Files:**
- Create: `web-svc/auth/verifier.go`
- Create: `web-svc/auth/verifier_test.go`
- Modify: `web-svc/config/config.go`
- Modify: `web-svc/config/config_test.go`

**Interfaces:**
- Consumes: nothing from Task 1 beyond the module scaffold.
- Produces: `auth.NewVerifier(supabaseURL, jwtSecret, ownerEmail string) *auth.Verifier`; `(*Verifier).Verify(r *http.Request) (auth.Result, string)` where `Result` is one of `auth.Unauthorized`, `auth.Forbidden`, `auth.OK`; `(*Verifier).Middleware(next http.Handler) http.Handler`. `config.Config` gains `SupabaseURL`, `SupabaseJWTSecret`, `OwnerEmail` fields, all fatal-if-blank.

- [ ] **Step 1: Update Task 1's two existing tests to set the new required env vars**

`TestLoad_DefaultsHTTPPortTo9005` and `TestLoad_HTTPPortEnvOverride` (written in Task 1) don't set `SUPABASE_URL`/`SUPABASE_JWT_SECRET`. Once this task makes `config.Load()` fatally require both, those two tests would start failing if left unchanged. Update both in `web-svc/config/config_test.go` to set (and defer-unset) both env vars, e.g.:

```go
func TestLoad_DefaultsHTTPPortTo9005(t *testing.T) {
	dir := t.TempDir()
	path := writeConfigFile(t, dir, validConfigJSON)
	os.Setenv("CONFIG_PATH", path)
	os.Setenv("SUPABASE_URL", "https://example.supabase.co")
	os.Setenv("SUPABASE_JWT_SECRET", "shh")
	defer os.Unsetenv("CONFIG_PATH")
	defer os.Unsetenv("SUPABASE_URL")
	defer os.Unsetenv("SUPABASE_JWT_SECRET")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPPort != "9005" {
		t.Errorf("HTTPPort = %q, want 9005", cfg.HTTPPort)
	}
}
```

Apply the same three added lines (`os.Setenv("SUPABASE_URL", ...)`, `os.Setenv("SUPABASE_JWT_SECRET", ...)`, and their matching `defer os.Unsetenv(...)`) to `TestLoad_HTTPPortEnvOverride`.

- [ ] **Step 2: Write the failing tests for `config.Load`'s new fatal fields**

Add to `web-svc/config/config_test.go`:

```go
func TestLoad_MissingSupabaseURL_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := writeConfigFile(t, dir, validConfigJSON)
	os.Setenv("CONFIG_PATH", path)
	os.Setenv("SUPABASE_JWT_SECRET", "shh")
	defer os.Unsetenv("CONFIG_PATH")
	defer os.Unsetenv("SUPABASE_JWT_SECRET")
	os.Unsetenv("SUPABASE_URL")

	if _, err := config.Load(); err == nil {
		t.Fatal("Load() error = nil, want an error when SUPABASE_URL is unset")
	}
}

func TestLoad_MissingSupabaseJWTSecret_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := writeConfigFile(t, dir, validConfigJSON)
	os.Setenv("CONFIG_PATH", path)
	os.Setenv("SUPABASE_URL", "https://example.supabase.co")
	defer os.Unsetenv("CONFIG_PATH")
	defer os.Unsetenv("SUPABASE_URL")
	os.Unsetenv("SUPABASE_JWT_SECRET")

	if _, err := config.Load(); err == nil {
		t.Fatal("Load() error = nil, want an error when SUPABASE_JWT_SECRET is unset")
	}
}

func TestLoad_PopulatesSupabaseAndOwnerFields(t *testing.T) {
	dir := t.TempDir()
	path := writeConfigFile(t, dir, validConfigJSON)
	os.Setenv("CONFIG_PATH", path)
	os.Setenv("SUPABASE_URL", "https://example.supabase.co")
	os.Setenv("SUPABASE_JWT_SECRET", "shh")
	defer os.Unsetenv("CONFIG_PATH")
	defer os.Unsetenv("SUPABASE_URL")
	defer os.Unsetenv("SUPABASE_JWT_SECRET")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SupabaseURL != "https://example.supabase.co" {
		t.Errorf("SupabaseURL = %q", cfg.SupabaseURL)
	}
	if cfg.SupabaseJWTSecret != "shh" {
		t.Errorf("SupabaseJWTSecret = %q", cfg.SupabaseJWTSecret)
	}
	if cfg.OwnerEmail != "breynisson@gmail.com" {
		t.Errorf("OwnerEmail = %q, want breynisson@gmail.com", cfg.OwnerEmail)
	}
}
```

- [ ] **Step 3: Run tests, verify the new ones fail**

Run: `go -C web-svc test ./config/...`
Expected: FAIL on the three new tests (fields don't exist / no validation yet)

- [ ] **Step 4: Update `web-svc/config/config.go`**

```go
package config

import (
	"fmt"
	"os"

	"soulman/common/sharedconfig"
)

type Config struct {
	HTTPPort          string
	SupabaseURL       string
	SupabaseJWTSecret string
	OwnerEmail        string
}

func Load() (*Config, error) {
	configPath := env("CONFIG_PATH", "./config.json")

	shared, err := sharedconfig.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("loading shared config: %w", err)
	}
	if shared.Web.OwnerEmail == "" {
		return nil, fmt.Errorf("shared config %s has no web.owner_email configured", configPath)
	}

	supabaseURL := os.Getenv("SUPABASE_URL")
	if supabaseURL == "" {
		return nil, fmt.Errorf("SUPABASE_URL environment variable is required")
	}
	jwtSecret := os.Getenv("SUPABASE_JWT_SECRET")
	if jwtSecret == "" {
		return nil, fmt.Errorf("SUPABASE_JWT_SECRET environment variable is required")
	}

	return &Config{
		HTTPPort:          env("HTTP_PORT", "9005"),
		SupabaseURL:       supabaseURL,
		SupabaseJWTSecret: jwtSecret,
		OwnerEmail:        shared.Web.OwnerEmail,
	}, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
```

- [ ] **Step 5: Run tests, verify they pass**

Run: `go -C web-svc test ./config/...`
Expected: PASS (6 tests total)

- [ ] **Step 6: Write the failing tests for `auth.Verifier`**

`web-svc/auth/verifier_test.go`:

```go
package auth_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"soulman/web-svc/auth"
)

const (
	testSupabaseURL = "https://example.supabase.co"
	testSecret      = "test-jwt-secret"
	testOwnerEmail  = "breynisson@gmail.com"
)

func hsToken(t *testing.T, secret, issuer, audience, email string, exp time.Time) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss":   issuer,
		"aud":   audience,
		"email": email,
		"sub":   "test-user",
		"exp":   exp.Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("signing HS256 token: %v", err)
	}
	return signed
}

func requestWithToken(token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

func TestVerify_ValidOwnerToken_ReturnsOK(t *testing.T) {
	v := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	token := hsToken(t, testSecret, testSupabaseURL+"/auth/v1", "authenticated", testOwnerEmail, time.Now().Add(time.Hour))

	result, email := v.Verify(requestWithToken(token))

	if result != auth.OK {
		t.Fatalf("result = %v, want OK", result)
	}
	if email != testOwnerEmail {
		t.Errorf("email = %q, want %q", email, testOwnerEmail)
	}
}

func TestVerify_ValidNonOwnerToken_ReturnsForbidden(t *testing.T) {
	v := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	token := hsToken(t, testSecret, testSupabaseURL+"/auth/v1", "authenticated", "someone-else@example.com", time.Now().Add(time.Hour))

	result, email := v.Verify(requestWithToken(token))

	if result != auth.Forbidden {
		t.Fatalf("result = %v, want Forbidden", result)
	}
	if email != "someone-else@example.com" {
		t.Errorf("email = %q, want someone-else@example.com", email)
	}
}

func TestVerify_NoAuthorizationHeader_ReturnsUnauthorized(t *testing.T) {
	v := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)

	result, _ := v.Verify(httptest.NewRequest(http.MethodGet, "/api/status", nil))

	if result != auth.Unauthorized {
		t.Fatalf("result = %v, want Unauthorized", result)
	}
}

func TestVerify_ExpiredToken_ReturnsUnauthorized(t *testing.T) {
	v := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	token := hsToken(t, testSecret, testSupabaseURL+"/auth/v1", "authenticated", testOwnerEmail, time.Now().Add(-time.Hour))

	result, _ := v.Verify(requestWithToken(token))

	if result != auth.Unauthorized {
		t.Fatalf("result = %v, want Unauthorized", result)
	}
}

func TestVerify_WrongIssuer_ReturnsUnauthorized(t *testing.T) {
	v := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	token := hsToken(t, testSecret, "https://not-the-right-issuer.example/auth/v1", "authenticated", testOwnerEmail, time.Now().Add(time.Hour))

	result, _ := v.Verify(requestWithToken(token))

	if result != auth.Unauthorized {
		t.Fatalf("result = %v, want Unauthorized", result)
	}
}

func TestVerify_WrongAudience_ReturnsUnauthorized(t *testing.T) {
	v := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	token := hsToken(t, testSecret, testSupabaseURL+"/auth/v1", "anon", testOwnerEmail, time.Now().Add(time.Hour))

	result, _ := v.Verify(requestWithToken(token))

	if result != auth.Unauthorized {
		t.Fatalf("result = %v, want Unauthorized", result)
	}
}

func TestVerify_WrongSigningSecret_ReturnsUnauthorized(t *testing.T) {
	v := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	token := hsToken(t, "wrong-secret", testSupabaseURL+"/auth/v1", "authenticated", testOwnerEmail, time.Now().Add(time.Hour))

	result, _ := v.Verify(requestWithToken(token))

	if result != auth.Unauthorized {
		t.Fatalf("result = %v, want Unauthorized", result)
	}
}

func encodeCoord(b *big.Int) string {
	buf := make([]byte, 32)
	b.FillBytes(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}

func TestVerify_ValidES256TokenViaJWKS_ReturnsOK(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating EC key: %v", err)
	}

	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"keys": []map[string]string{
				{
					"kty": "EC",
					"crv": "P-256",
					"x":   encodeCoord(privateKey.X),
					"y":   encodeCoord(privateKey.Y),
				},
			},
		})
	}))
	defer jwks.Close()

	v := auth.NewVerifier(jwks.URL, testSecret, testOwnerEmail)

	claims := jwt.MapClaims{
		"iss":   jwks.URL + "/auth/v1",
		"aud":   "authenticated",
		"email": testOwnerEmail,
		"sub":   "test-user",
		"exp":   time.Now().Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	signed, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("signing ES256 token: %v", err)
	}

	result, email := v.Verify(requestWithToken(signed))

	if result != auth.OK {
		t.Fatalf("result = %v, want OK", result)
	}
	if email != testOwnerEmail {
		t.Errorf("email = %q, want %q", email, testOwnerEmail)
	}
}

func TestMiddleware_OK_CallsNext(t *testing.T) {
	v := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	token := hsToken(t, testSecret, testSupabaseURL+"/auth/v1", "authenticated", testOwnerEmail, time.Now().Add(time.Hour))

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	rec := httptest.NewRecorder()
	v.Middleware(next).ServeHTTP(rec, requestWithToken(token))

	if !called {
		t.Fatal("next handler was not called")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (default recorder code before handler writes)", rec.Code)
	}
}

func TestMiddleware_Forbidden_Returns403AndDoesNotCallNext(t *testing.T) {
	v := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	token := hsToken(t, testSecret, testSupabaseURL+"/auth/v1", "authenticated", "someone-else@example.com", time.Now().Add(time.Hour))

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	rec := httptest.NewRecorder()
	v.Middleware(next).ServeHTTP(rec, requestWithToken(token))

	if called {
		t.Fatal("next handler should not have been called")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestMiddleware_Unauthorized_Returns401AndDoesNotCallNext(t *testing.T) {
	v := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	rec := httptest.NewRecorder()
	v.Middleware(next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/status", nil))

	if called {
		t.Fatal("next handler should not have been called")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}
```

- [ ] **Step 6: Run tests, verify they fail**

Run: `go -C web-svc test ./auth/...`
Expected: FAIL — package `auth` doesn't exist yet.

- [ ] **Step 7: Implement `web-svc/auth/verifier.go`**

```go
// Package auth verifies Supabase-issued JWTs the same way agent-suite's
// UserResolverFilter does (pinned to HS256 shared-secret or ES256
// JWKS-fetched-and-cached, requiring iss=<supabaseURL>/auth/v1 and
// aud="authenticated"), but skips agent-suite's DB-backed user resolution
// entirely: Soulman has exactly one real user, so the email claim itself
// *is* the authorization decision.
package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Result int

const (
	Unauthorized Result = iota
	Forbidden
	OK
)

type Verifier struct {
	supabaseURL string
	jwtSecret   []byte
	ownerEmail  string
	httpClient  *http.Client

	mu        sync.Mutex
	cachedKey *ecdsa.PublicKey
}

func NewVerifier(supabaseURL, jwtSecret, ownerEmail string) *Verifier {
	return &Verifier{
		supabaseURL: strings.TrimRight(supabaseURL, "/"),
		jwtSecret:   []byte(jwtSecret),
		ownerEmail:  ownerEmail,
		httpClient:  &http.Client{Timeout: 5 * time.Second},
	}
}

type supabaseClaims struct {
	jwt.RegisteredClaims
	Email string `json:"email"`
}

// Verify checks the request's Bearer token and returns the authorization
// outcome plus the token's email claim (empty if the token itself couldn't
// be verified). Result is Unauthorized for any missing/invalid/expired/
// wrong-issuer/wrong-audience token, Forbidden for a valid token whose
// email doesn't match ownerEmail, OK otherwise.
func (v *Verifier) Verify(r *http.Request) (Result, string) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return Unauthorized, ""
	}
	tokenString := strings.TrimPrefix(header, "Bearer ")

	expectedIssuer := v.supabaseURL + "/auth/v1"
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"HS256", "ES256"}),
		jwt.WithIssuer(expectedIssuer),
		jwt.WithAudience("authenticated"),
	)

	token, err := parser.ParseWithClaims(tokenString, &supabaseClaims{}, func(t *jwt.Token) (interface{}, error) {
		switch t.Method.Alg() {
		case "HS256":
			return v.jwtSecret, nil
		case "ES256":
			return v.getOrFetchPublicKey()
		default:
			return nil, fmt.Errorf("unsupported algorithm: %s", t.Method.Alg())
		}
	})
	if err != nil || !token.Valid {
		return Unauthorized, ""
	}

	claims, ok := token.Claims.(*supabaseClaims)
	if !ok {
		return Unauthorized, ""
	}

	if claims.Email != v.ownerEmail {
		return Forbidden, claims.Email
	}
	return OK, claims.Email
}

// Middleware gates next behind Verify: 401 on Unauthorized, 403 on
// Forbidden, otherwise calls next.
func (v *Verifier) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, _ := v.Verify(r)
		switch result {
		case OK:
			next.ServeHTTP(w, r)
		case Forbidden:
			writeJSONError(w, http.StatusForbidden, "forbidden")
		default:
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		}
	})
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (v *Verifier) getOrFetchPublicKey() (*ecdsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.cachedKey != nil {
		return v.cachedKey, nil
	}
	key, err := v.fetchPublicKeyFromJWKS()
	if err != nil {
		return nil, err
	}
	v.cachedKey = key
	return key, nil
}

type jwksResponse struct {
	Keys []jwkKey `json:"keys"`
}

type jwkKey struct {
	Kty string `json:"kty"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func (v *Verifier) fetchPublicKeyFromJWKS() (*ecdsa.PublicKey, error) {
	url := v.supabaseURL + "/auth/v1/.well-known/jwks.json"
	resp, err := v.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetching JWKS from %s: %w", url, err)
	}
	defer resp.Body.Close()

	var set jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return nil, fmt.Errorf("parsing JWKS from %s: %w", url, err)
	}

	for _, k := range set.Keys {
		if k.Kty != "EC" {
			continue
		}
		xBytes, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil {
			return nil, fmt.Errorf("decoding JWKS x coordinate: %w", err)
		}
		yBytes, err := base64.RawURLEncoding.DecodeString(k.Y)
		if err != nil {
			return nil, fmt.Errorf("decoding JWKS y coordinate: %w", err)
		}
		return &ecdsa.PublicKey{
			Curve: elliptic.P256(),
			X:     new(big.Int).SetBytes(xBytes),
			Y:     new(big.Int).SetBytes(yBytes),
		}, nil
	}
	return nil, fmt.Errorf("no EC key found in JWKS at %s", url)
}
```

- [ ] **Step 8: Add the JWT dependency and run tests**

Run:
```
go -C web-svc get github.com/golang-jwt/jwt/v5
go -C web-svc mod tidy
go -C web-svc test ./...
```
Expected: PASS (all `config` and `auth` tests)

- [ ] **Step 9: Commit**

```bash
git add web-svc/auth web-svc/config web-svc/go.mod web-svc/go.sum
git commit -m "feat(web-svc): add JWT verification and owner-email authorization"
```

---

### Task 3: Router wiring, CORS, and `/api/status`

**Files:**
- Modify: `web-svc/httpserver/server.go`
- Modify: `web-svc/httpserver/server_test.go`
- Modify: `web-svc/config/config.go`
- Modify: `web-svc/config/config_test.go`
- Modify: `web-svc/main.go`

**Interfaces:**
- Consumes: `auth.Verifier` from Task 2 (`NewVerifier`, `Middleware`).
- Produces: `httpserver.New(cfg httpserver.Config, verifier *auth.Verifier) *Server` where `httpserver.Config{CORSAllowedOrigin, PerceptionSvcURL, MemorySvcURL, ThinkingSvcURL, ActionSvcURL string}`. Later tasks add more routes/fields to this same `Server`.

- [ ] **Step 1: Add the four downstream URL fields + CORS origin to `config.Config`, with tests**

Add to `web-svc/config/config_test.go` (append to `validConfigJSON`-based tests):

```go
func TestLoad_PopulatesDownstreamURLsAndCORSOrigin(t *testing.T) {
	dir := t.TempDir()
	path := writeConfigFile(t, dir, validConfigJSON)
	os.Setenv("CONFIG_PATH", path)
	os.Setenv("SUPABASE_URL", "https://example.supabase.co")
	os.Setenv("SUPABASE_JWT_SECRET", "shh")
	defer os.Unsetenv("CONFIG_PATH")
	defer os.Unsetenv("SUPABASE_URL")
	defer os.Unsetenv("SUPABASE_JWT_SECRET")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.CORSAllowedOrigin != "http://localhost:5178" {
		t.Errorf("CORSAllowedOrigin = %q", cfg.CORSAllowedOrigin)
	}
	if cfg.PerceptionSvcURL != "http://localhost:9011" {
		t.Errorf("PerceptionSvcURL = %q", cfg.PerceptionSvcURL)
	}
	if cfg.MemorySvcURL != "http://localhost:9012" {
		t.Errorf("MemorySvcURL = %q", cfg.MemorySvcURL)
	}
	if cfg.ThinkingSvcURL != "http://localhost:9013" {
		t.Errorf("ThinkingSvcURL = %q", cfg.ThinkingSvcURL)
	}
	if cfg.ActionSvcURL != "http://localhost:9014" {
		t.Errorf("ActionSvcURL = %q", cfg.ActionSvcURL)
	}
}

func TestLoad_MissingDownstreamURL_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	incomplete := `{"web": {"owner_email": "breynisson@gmail.com", "cors_allowed_origin": "http://localhost:5178", "perception_svc_url": "http://localhost:9011", "memory_svc_url": "", "thinking_svc_url": "http://localhost:9013", "action_svc_url": "http://localhost:9014"}}`
	path := writeConfigFile(t, dir, incomplete)
	os.Setenv("CONFIG_PATH", path)
	os.Setenv("SUPABASE_URL", "https://example.supabase.co")
	os.Setenv("SUPABASE_JWT_SECRET", "shh")
	defer os.Unsetenv("CONFIG_PATH")
	defer os.Unsetenv("SUPABASE_URL")
	defer os.Unsetenv("SUPABASE_JWT_SECRET")

	if _, err := config.Load(); err == nil {
		t.Fatal("Load() error = nil, want an error when a downstream URL is blank")
	}
}
```

- [ ] **Step 2: Run tests, verify the new ones fail**

Run: `go -C web-svc test ./config/...`
Expected: FAIL (fields don't exist yet)

- [ ] **Step 3: Update `web-svc/config/config.go`**

```go
package config

import (
	"fmt"
	"os"

	"soulman/common/sharedconfig"
)

type Config struct {
	HTTPPort          string
	SupabaseURL       string
	SupabaseJWTSecret string
	OwnerEmail        string
	CORSAllowedOrigin string
	PerceptionSvcURL  string
	MemorySvcURL      string
	ThinkingSvcURL    string
	ActionSvcURL      string
}

func Load() (*Config, error) {
	configPath := env("CONFIG_PATH", "./config.json")

	shared, err := sharedconfig.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("loading shared config: %w", err)
	}
	if shared.Web.OwnerEmail == "" {
		return nil, fmt.Errorf("shared config %s has no web.owner_email configured", configPath)
	}
	if shared.Web.CORSAllowedOrigin == "" {
		return nil, fmt.Errorf("shared config %s has no web.cors_allowed_origin configured", configPath)
	}
	if shared.Web.PerceptionSvcURL == "" {
		return nil, fmt.Errorf("shared config %s has no web.perception_svc_url configured", configPath)
	}
	if shared.Web.MemorySvcURL == "" {
		return nil, fmt.Errorf("shared config %s has no web.memory_svc_url configured", configPath)
	}
	if shared.Web.ThinkingSvcURL == "" {
		return nil, fmt.Errorf("shared config %s has no web.thinking_svc_url configured", configPath)
	}
	if shared.Web.ActionSvcURL == "" {
		return nil, fmt.Errorf("shared config %s has no web.action_svc_url configured", configPath)
	}

	supabaseURL := os.Getenv("SUPABASE_URL")
	if supabaseURL == "" {
		return nil, fmt.Errorf("SUPABASE_URL environment variable is required")
	}
	jwtSecret := os.Getenv("SUPABASE_JWT_SECRET")
	if jwtSecret == "" {
		return nil, fmt.Errorf("SUPABASE_JWT_SECRET environment variable is required")
	}

	return &Config{
		HTTPPort:          env("HTTP_PORT", "9005"),
		SupabaseURL:       supabaseURL,
		SupabaseJWTSecret: jwtSecret,
		OwnerEmail:        shared.Web.OwnerEmail,
		CORSAllowedOrigin: shared.Web.CORSAllowedOrigin,
		PerceptionSvcURL:  shared.Web.PerceptionSvcURL,
		MemorySvcURL:      shared.Web.MemorySvcURL,
		ThinkingSvcURL:    shared.Web.ThinkingSvcURL,
		ActionSvcURL:      shared.Web.ActionSvcURL,
	}, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
```

- [ ] **Step 4: Run tests, verify they pass**

Run: `go -C web-svc test ./config/...`
Expected: PASS (10 tests total)

- [ ] **Step 5: Write the failing tests for the new router shape and `/api/status`**

Replace `web-svc/httpserver/server_test.go` with:

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

const (
	testSupabaseURL = "https://example.supabase.co"
	testSecret      = "test-secret"
	testOwnerEmail  = "breynisson@gmail.com"
)

func newTestUpstream(t *testing.T, healthy bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if healthy {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok"}`))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
}

func TestHealth_ReturnsOK(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178"}
	srv := httpserver.New("9005", cfg, auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestAPIStatus_NoToken_Returns401(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178"}
	srv := httpserver.New("9005", cfg, auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestAPIStatus_AllUpstreamsHealthy_ReportsUp(t *testing.T) {
	perception := newTestUpstream(t, true)
	defer perception.Close()
	memory := newTestUpstream(t, true)
	defer memory.Close()
	thinking := newTestUpstream(t, true)
	defer thinking.Close()
	action := newTestUpstream(t, true)
	defer action.Close()

	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		PerceptionSvcURL:  perception.URL,
		MemorySvcURL:      memory.URL,
		ThinkingSvcURL:    thinking.URL,
		ActionSvcURL:      action.URL,
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, name := range []string{"perception-svc", "memory-svc", "thinking-svc", "action-svc"} {
		if body[name] != "up" {
			t.Errorf("%s = %q, want up", name, body[name])
		}
	}
}

func TestAPIStatus_OneUpstreamDown_ReportsDownWithout500(t *testing.T) {
	perception := newTestUpstream(t, true)
	defer perception.Close()
	memory := newTestUpstream(t, false)
	defer memory.Close()
	thinking := newTestUpstream(t, true)
	defer thinking.Close()
	action := newTestUpstream(t, true)
	defer action.Close()

	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		PerceptionSvcURL:  perception.URL,
		MemorySvcURL:      memory.URL,
		ThinkingSvcURL:    thinking.URL,
		ActionSvcURL:      action.URL,
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even with a downed service", rec.Code)
	}
	var body map[string]string
	json.NewDecoder(rec.Body).Decode(&body)
	if body["memory-svc"] != "down" {
		t.Errorf("memory-svc = %q, want down", body["memory-svc"])
	}
	if body["perception-svc"] != "up" {
		t.Errorf("perception-svc = %q, want up", body["perception-svc"])
	}
}
```

Add this shared helper at the bottom of the same file (used by the two status tests above):

```go
func ownerToken(t *testing.T) string {
	t.Helper()
	// Mirrors auth_test.go's hsToken helper — duplicated here (rather than
	// exported from the auth package) since it's test-only fixture code,
	// consistent with how each package's tests build their own fixtures.
	return signHS256(t, testSecret, testSupabaseURL+"/auth/v1", "authenticated", testOwnerEmail)
}
```

And add the signing helper (needs `github.com/golang-jwt/jwt/v5` and `time`):

```go
func signHS256(t *testing.T, secret, issuer, audience, email string) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss":   issuer,
		"aud":   audience,
		"email": email,
		"sub":   "test-user",
		"exp":   time.Now().Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("signing token: %v", err)
	}
	return signed
}
```

Add the two needed imports (`"github.com/golang-jwt/jwt/v5"` and `"time"`) to the test file's import block.

- [ ] **Step 6: Run tests, verify they fail**

Run: `go -C web-svc test ./httpserver/...`
Expected: FAIL — `httpserver.Config` and the new `New` signature don't exist yet.

- [ ] **Step 7: Implement the updated `web-svc/httpserver/server.go`**

```go
package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"soulman/web-svc/auth"
)

// Config holds the values httpserver needs beyond the port and verifier —
// kept as its own struct (rather than depending on web-svc/config
// directly) so tests can construct it without going through config.Load.
type Config struct {
	CORSAllowedOrigin string
	PerceptionSvcURL  string
	MemorySvcURL      string
	ThinkingSvcURL    string
	ActionSvcURL      string
}

type Server struct {
	port       string
	cfg        Config
	verifier   *auth.Verifier
	httpClient *http.Client
	router     chi.Router
}

func New(port string, cfg Config, verifier *auth.Verifier) *Server {
	s := &Server{
		port:       port,
		cfg:        cfg,
		verifier:   verifier,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
	s.router = s.buildRouter()
	return s
}

func (s *Server) Handler() http.Handler { return s.router }

func (s *Server) Start() error {
	return http.ListenAndServe(":"+s.port, s.router)
}

func (s *Server) buildRouter() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{s.cfg.CORSAllowedOrigin},
		AllowedMethods: []string{"GET", "OPTIONS"},
		AllowedHeaders: []string{"Authorization", "Content-Type"},
		MaxAge:         300,
	}))

	r.Get("/health", s.health)

	r.Group(func(r chi.Router) {
		r.Use(s.verifier.Middleware)
		r.Get("/api/status", s.apiStatus)
	})

	return r
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) apiStatus(w http.ResponseWriter, r *http.Request) {
	checks := map[string]string{
		"perception-svc": s.cfg.PerceptionSvcURL,
		"memory-svc":     s.cfg.MemorySvcURL,
		"thinking-svc":   s.cfg.ThinkingSvcURL,
		"action-svc":     s.cfg.ActionSvcURL,
	}

	result := make(map[string]string, len(checks))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for name, url := range checks {
		wg.Add(1)
		go func(name, url string) {
			defer wg.Done()
			status := "down"
			if s.isHealthy(url) {
				status = "up"
			}
			mu.Lock()
			result[name] = status
			mu.Unlock()
		}(name, url)
	}
	wg.Wait()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) isHealthy(baseURL string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
```

- [ ] **Step 8: Add the CORS dependency and run tests**

Run:
```
go -C web-svc get github.com/go-chi/cors@v1.2.1
go -C web-svc mod tidy
go -C web-svc test ./...
```
Expected: PASS (all config, auth, httpserver tests)

- [ ] **Step 9: Update `web-svc/main.go` to wire config → verifier → server**

```go
package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"soulman/web-svc/auth"
	"soulman/web-svc/config"
	"soulman/web-svc/httpserver"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	verifier := auth.NewVerifier(cfg.SupabaseURL, cfg.SupabaseJWTSecret, cfg.OwnerEmail)

	srv := httpserver.New(cfg.HTTPPort, httpserver.Config{
		CORSAllowedOrigin: cfg.CORSAllowedOrigin,
		PerceptionSvcURL:  cfg.PerceptionSvcURL,
		MemorySvcURL:      cfg.MemorySvcURL,
		ThinkingSvcURL:    cfg.ThinkingSvcURL,
		ActionSvcURL:      cfg.ActionSvcURL,
	}, verifier)

	go func() {
		log.Printf("HTTP listening on :%s", cfg.HTTPPort)
		if err := srv.Start(); err != nil {
			log.Printf("http: %v", err)
		}
	}()

	log.Printf("web-svc started (HTTP=:%s, owner=%s)", cfg.HTTPPort, cfg.OwnerEmail)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Printf("web-svc shutting down")
}
```

- [ ] **Step 10: Verify the module builds**

Run: `go -C web-svc build ./...`
Expected: exits 0

- [ ] **Step 11: Commit**

```bash
git add web-svc/httpserver web-svc/config web-svc/main.go web-svc/go.mod web-svc/go.sum
git commit -m "feat(web-svc): CORS-enabled router, owner-gated /api/status"
```

---

### Task 4: `memory-svc` proxy endpoints

**Files:**
- Create: `web-svc/httpserver/proxy.go`
- Create: `web-svc/httpserver/proxy_test.go`
- Modify: `web-svc/httpserver/server.go`

**Interfaces:**
- Consumes: `s.cfg.MemorySvcURL`, `s.httpClient` from Task 3.
- Produces: routes `GET /api/episodes` and `GET /api/raw-inputs/recent`, both proxying to `memory-svc`.

- [ ] **Step 1: Write the failing tests**

`web-svc/httpserver/proxy_test.go`:

```go
package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"soulman/web-svc/auth"
	"soulman/web-svc/httpserver"
)

func TestAPIEpisodes_ProxiesMemorySvcAndPassesLimit(t *testing.T) {
	var gotPath, gotQuery string
	memory := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"id":1,"summary":"test episode"}]`))
	}))
	defer memory.Close()

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", MemorySvcURL: memory.URL}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/episodes?limit=5", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if gotPath != "/memory/episodes" {
		t.Errorf("proxied path = %q, want /memory/episodes", gotPath)
	}
	if gotQuery != "limit=5" {
		t.Errorf("proxied query = %q, want limit=5", gotQuery)
	}
	if rec.Body.String() != `[{"id":1,"summary":"test episode"}]` {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func TestAPIRawInputs_ProxiesMemorySvc(t *testing.T) {
	memory := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	}))
	defer memory.Close()

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", MemorySvcURL: memory.URL}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/raw-inputs/recent", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestAPIEpisodes_MemorySvcDown_Returns502(t *testing.T) {
	memory := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	memory.Close() // closed immediately: connection refused, simulating "down"

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", MemorySvcURL: memory.URL}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/episodes", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestAPIEpisodes_NoToken_Returns401(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178"}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/episodes", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**

Run: `go -C web-svc test ./httpserver/... -run "TestAPIEpisodes|TestAPIRawInputs"`
Expected: FAIL — routes don't exist (404) yet.

- [ ] **Step 3: Implement `web-svc/httpserver/proxy.go`**

```go
package httpserver

import (
	"context"
	"io"
	"net/http"
	"time"
)

// proxyGet forwards the incoming request's query string to
// upstreamBaseURL+upstreamPath and streams the response back verbatim. A
// non-2xx/network-error upstream response becomes a 502 from web-svc
// rather than a hang or a crash — the frontend's per-panel error handling
// depends on getting a clear status code back quickly.
func (s *Server) proxyGet(upstreamBaseURL, upstreamPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		url := upstreamBaseURL + upstreamPath
		if r.URL.RawQuery != "" {
			url += "?" + r.URL.RawQuery
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}

		resp, err := s.httpClient.Do(req)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, "upstream unavailable")
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 500 {
			writeJSONError(w, http.StatusBadGateway, "upstream unavailable")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}
}
```

Add `writeJSONError` isn't duplicated — it already exists in `verifier.go`'s package `auth`, but `httpserver` needs its own copy since it's a different package. Add this small helper to `web-svc/httpserver/server.go` (near `health`):

```go
func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
```

Register the two new routes inside the existing authenticated `r.Group` in `buildRouter` (in `server.go`):

```go
	r.Group(func(r chi.Router) {
		r.Use(s.verifier.Middleware)
		r.Get("/api/status", s.apiStatus)
		r.Get("/api/episodes", s.proxyGet(s.cfg.MemorySvcURL, "/memory/episodes"))
		r.Get("/api/raw-inputs/recent", s.proxyGet(s.cfg.MemorySvcURL, "/raw-inputs/recent"))
	})
```

- [ ] **Step 4: Run tests, verify they pass**

Run: `go -C web-svc test ./...`
Expected: PASS (all tests)

- [ ] **Step 5: Commit**

```bash
git add web-svc/httpserver
git commit -m "feat(web-svc): proxy /api/episodes and /api/raw-inputs/recent to memory-svc"
```

---

### Task 5: Daily report reading

**Files:**
- Create: `web-svc/reports/reports.go`
- Create: `web-svc/reports/reports_test.go`
- Create: `web-svc/httpserver/reports_handler.go`
- Create: `web-svc/httpserver/reports_handler_test.go`
- Modify: `web-svc/httpserver/server.go`
- Modify: `web-svc/config/config.go`
- Modify: `web-svc/config/config_test.go`
- Modify: `web-svc/main.go`

**Interfaces:**
- Produces: `reports.PathForDate(root string, date time.Time) string`, `reports.Read(root string, date time.Time) (content string, found bool, err error)`. Routes `GET /api/reports/latest` and `GET /api/reports?date=YYYY-MM-DD`. `config.Config` gains `SoulmanRoot string` (env `SOULMAN_ROOT`, defaulting to `C:\Users\Lenovo\soulman-dev` — same non-fatal default convention as `action-svc/config`).

- [ ] **Step 1: Write the failing tests for the `reports` package**

`web-svc/reports/reports_test.go`:

```go
package reports_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"soulman/web-svc/reports"
)

func TestPathForDate_UsesDailyReportNamingConvention(t *testing.T) {
	date := time.Date(2026, 7, 19, 0, 0, 0, 0, time.Local)
	got := reports.PathForDate(`C:\root`, date)
	want := filepath.Join(`C:\root`, "reports", "daily-report-2026-07-19.txt")
	if got != want {
		t.Errorf("PathForDate = %q, want %q", got, want)
	}
}

func TestRead_ExistingFile_ReturnsContentAndFound(t *testing.T) {
	root := t.TempDir()
	date := time.Date(2026, 7, 19, 0, 0, 0, 0, time.Local)
	dir := filepath.Join(root, "reports")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "daily-report-2026-07-19.txt"), []byte("2026-07-19 09:00  [test]  hello"), 0o644)

	content, found, err := reports.Read(root, date)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !found {
		t.Fatal("found = false, want true")
	}
	if content != "2026-07-19 09:00  [test]  hello" {
		t.Errorf("content = %q", content)
	}
}

func TestRead_MissingFile_ReturnsNotFoundNoError(t *testing.T) {
	root := t.TempDir()
	date := time.Date(2026, 7, 19, 0, 0, 0, 0, time.Local)

	content, found, err := reports.Read(root, date)
	if err != nil {
		t.Fatalf("Read() error = %v, want nil", err)
	}
	if found {
		t.Fatal("found = true, want false")
	}
	if content != "" {
		t.Errorf("content = %q, want empty", content)
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**

Run: `go -C web-svc test ./reports/...`
Expected: FAIL — package doesn't exist yet.

- [ ] **Step 3: Implement `web-svc/reports/reports.go`**

```go
// Package reports reads action-svc's daily report files
// ($SOULMAN_ROOT/reports/daily-report-YYYY-MM-DD.txt). This duplicates
// action-svc/report's PathForDate/Read logic rather than adding a
// cross-module Go dependency, consistent with this codebase's convention
// of keeping each service an independent module.
package reports

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func PathForDate(root string, date time.Time) string {
	filename := fmt.Sprintf("daily-report-%s.txt", date.Format("2006-01-02"))
	return filepath.Join(root, "reports", filename)
}

// Read returns the report file's content for the given date. found is
// false (with a nil error) if the file doesn't exist yet — that's an
// expected, non-error condition, not a failure.
func Read(root string, date time.Time) (content string, found bool, err error) {
	path := PathForDate(root, date)
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("reports: read %s: %w", path, err)
	}
	return string(b), true, nil
}
```

- [ ] **Step 4: Run tests, verify they pass**

Run: `go -C web-svc test ./reports/...`
Expected: PASS (3 tests)

- [ ] **Step 5: Add `SoulmanRoot` to config, with tests**

Add to `web-svc/config/config_test.go`:

```go
func TestLoad_SoulmanRootDefaultsToDevPath(t *testing.T) {
	dir := t.TempDir()
	path := writeConfigFile(t, dir, validConfigJSON)
	os.Setenv("CONFIG_PATH", path)
	os.Setenv("SUPABASE_URL", "https://example.supabase.co")
	os.Setenv("SUPABASE_JWT_SECRET", "shh")
	defer os.Unsetenv("CONFIG_PATH")
	defer os.Unsetenv("SUPABASE_URL")
	defer os.Unsetenv("SUPABASE_JWT_SECRET")
	os.Unsetenv("SOULMAN_ROOT")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SoulmanRoot != `C:\Users\Lenovo\soulman-dev` {
		t.Errorf("SoulmanRoot = %q", cfg.SoulmanRoot)
	}
}

func TestLoad_SoulmanRootEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := writeConfigFile(t, dir, validConfigJSON)
	os.Setenv("CONFIG_PATH", path)
	os.Setenv("SUPABASE_URL", "https://example.supabase.co")
	os.Setenv("SUPABASE_JWT_SECRET", "shh")
	os.Setenv("SOULMAN_ROOT", `C:\Users\Lenovo\soulman-prod`)
	defer os.Unsetenv("CONFIG_PATH")
	defer os.Unsetenv("SUPABASE_URL")
	defer os.Unsetenv("SUPABASE_JWT_SECRET")
	defer os.Unsetenv("SOULMAN_ROOT")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SoulmanRoot != `C:\Users\Lenovo\soulman-prod` {
		t.Errorf("SoulmanRoot = %q", cfg.SoulmanRoot)
	}
}
```

- [ ] **Step 6: Run tests, verify the new ones fail**

Run: `go -C web-svc test ./config/...`
Expected: FAIL (field doesn't exist)

- [ ] **Step 7: Add `SoulmanRoot` to `web-svc/config/config.go`**

Add `SoulmanRoot string` to the `Config` struct, and add this line inside the returned `&Config{...}` literal:

```go
		SoulmanRoot: env("SOULMAN_ROOT", `C:\Users\Lenovo\soulman-dev`),
```

- [ ] **Step 8: Run tests, verify they pass**

Run: `go -C web-svc test ./config/...`
Expected: PASS (12 tests total)

- [ ] **Step 9: Write the failing tests for the reports HTTP handlers**

`web-svc/httpserver/reports_handler_test.go`:

```go
package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"soulman/web-svc/auth"
	"soulman/web-svc/httpserver"
)

func writeReportFile(t *testing.T, root string, date time.Time, content string) {
	t.Helper()
	dir := filepath.Join(root, "reports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	filename := "daily-report-" + date.Format("2006-01-02") + ".txt"
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0o644); err != nil {
		t.Fatalf("writing report file: %v", err)
	}
}

func TestAPIReportsLatest_ReturnsTodaysReport(t *testing.T) {
	root := t.TempDir()
	writeReportFile(t, root, time.Now(), "today's report")

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ReportsRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/reports/latest", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	json.NewDecoder(rec.Body).Decode(&body)
	if body["content"] != "today's report" {
		t.Errorf("content = %q", body["content"])
	}
}

func TestAPIReportsLatest_FallsBackToMostRecentWithinAWeek(t *testing.T) {
	root := t.TempDir()
	writeReportFile(t, root, time.Now().AddDate(0, 0, -3), "three days ago")

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ReportsRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/reports/latest", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]string
	json.NewDecoder(rec.Body).Decode(&body)
	if body["content"] != "three days ago" {
		t.Errorf("content = %q", body["content"])
	}
}

func TestAPIReportsLatest_NoReportInLastWeek_Returns404(t *testing.T) {
	root := t.TempDir()

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ReportsRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/reports/latest", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAPIReportsByDate_ExistingDate_ReturnsContent(t *testing.T) {
	root := t.TempDir()
	date := time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local)
	writeReportFile(t, root, date, "june first report")

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ReportsRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/reports?date=2026-06-01", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	json.NewDecoder(rec.Body).Decode(&body)
	if body["content"] != "june first report" {
		t.Errorf("content = %q", body["content"])
	}
	if body["date"] != "2026-06-01" {
		t.Errorf("date = %q", body["date"])
	}
}

func TestAPIReportsByDate_MissingDate_Returns404(t *testing.T) {
	root := t.TempDir()

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ReportsRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/reports?date=2020-01-01", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAPIReportsByDate_InvalidDateFormat_Returns400(t *testing.T) {
	root := t.TempDir()

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ReportsRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/reports?date=not-a-date", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
```

- [ ] **Step 10: Run tests, verify they fail**

Run: `go -C web-svc test ./httpserver/... -run TestAPIReports`
Expected: FAIL — `ReportsRoot` field and routes don't exist yet.

- [ ] **Step 11: Add `ReportsRoot` to `httpserver.Config` and implement handlers**

In `web-svc/httpserver/server.go`, add `ReportsRoot string` to the `Config` struct.

`web-svc/httpserver/reports_handler.go`:

```go
package httpserver

import (
	"encoding/json"
	"net/http"
	"time"

	"soulman/web-svc/reports"
)

func (s *Server) reportsLatest(w http.ResponseWriter, r *http.Request) {
	for i := 0; i < 7; i++ {
		date := time.Now().AddDate(0, 0, -i)
		content, found, err := reports.Read(s.cfg.ReportsRoot, date)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if found {
			writeReportJSON(w, date, content)
			return
		}
	}
	writeJSONError(w, http.StatusNotFound, "no report found in the last 7 days")
}

func (s *Server) reportsByDate(w http.ResponseWriter, r *http.Request) {
	dateStr := r.URL.Query().Get("date")
	date, err := time.ParseInLocation("2006-01-02", dateStr, time.Local)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid or missing date, expected YYYY-MM-DD")
		return
	}

	content, found, err := reports.Read(s.cfg.ReportsRoot, date)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !found {
		writeJSONError(w, http.StatusNotFound, "no report for this date")
		return
	}
	writeReportJSON(w, date, content)
}

func writeReportJSON(w http.ResponseWriter, date time.Time, content string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"date":    date.Format("2006-01-02"),
		"content": content,
	})
}
```

Register the two routes in `buildRouter`'s authenticated group (in `server.go`):

```go
		r.Get("/api/reports/latest", s.reportsLatest)
		r.Get("/api/reports", s.reportsByDate)
```

- [ ] **Step 12: Run tests, verify they pass**

Run: `go -C web-svc test ./...`
Expected: PASS (all tests)

- [ ] **Step 13: Wire `ReportsRoot` through `main.go`**

In `web-svc/main.go`, add `ReportsRoot: cfg.SoulmanRoot,` to the `httpserver.Config{...}` literal passed to `httpserver.New`.

- [ ] **Step 14: Verify the module builds**

Run: `go -C web-svc build ./...`
Expected: exits 0

- [ ] **Step 15: Commit**

```bash
git add web-svc/reports web-svc/httpserver web-svc/config web-svc/main.go
git commit -m "feat(web-svc): add /api/reports/latest and /api/reports?date="
```

---

### Task 6: Ops wiring — launch scripts, `start-everything.ps1`, `NOTES.md`

**Files:**
- Create: `C:\Users\Lenovo\soulman-dev\run-web-svc.ps1` (outside vault repo — not git-tracked)
- Create: `C:\Users\Lenovo\soulman-prod\run-web-svc.ps1` (outside vault repo — not git-tracked)
- Modify: `C:\Users\Lenovo\start-everything.ps1` (outside vault repo — not git-tracked)
- Create: `web-svc/NOTES.md`

**Interfaces:** none (pure ops/documentation task, no code interfaces).

- [ ] **Step 1: Write `C:\Users\Lenovo\soulman-dev\run-web-svc.ps1`**

```powershell
# Builds web-svc from the vault source and runs it in this (dev) environment.
# SUPABASE_URL / SUPABASE_JWT_SECRET come from .env in this directory via load-env.ps1.

$ErrorActionPreference = "Stop"

& "$PSScriptRoot\load-env.ps1"

$repoSrc = "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\web-svc"
$binDir  = Join-Path $PSScriptRoot "bin"
$exe     = Join-Path $binDir "web-svc.exe"

New-Item -ItemType Directory -Force $binDir | Out-Null

Push-Location $repoSrc
try {
    go build -o $exe .
} finally {
    Pop-Location
}

$configSrc = "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\config\dev.json"
$configDst = Join-Path $PSScriptRoot "config.json"
Copy-Item $configSrc $configDst -Force
$env:CONFIG_PATH = $configDst

if (-not $env:SUPABASE_URL -or -not $env:SUPABASE_JWT_SECRET) {
    Write-Warning "SUPABASE_URL / SUPABASE_JWT_SECRET not set in .env - web-svc will fail to start until both are configured."
}

# Dev uses its own port so it can run alongside prod without colliding.
$env:HTTP_PORT = "9015"

& $exe
```

- [ ] **Step 2: Write `C:\Users\Lenovo\soulman-prod\run-web-svc.ps1`**

```powershell
# Builds web-svc from the vault source and runs it in this (prod) environment.
# SUPABASE_URL / SUPABASE_JWT_SECRET come from .env in this directory via load-env.ps1.

$ErrorActionPreference = "Stop"

& "$PSScriptRoot\load-env.ps1"

$repoSrc = "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\web-svc"
$binDir  = Join-Path $PSScriptRoot "bin"
$exe     = Join-Path $binDir "web-svc.exe"

New-Item -ItemType Directory -Force $binDir | Out-Null

Push-Location $repoSrc
try {
    go build -o $exe .
} finally {
    Pop-Location
}

# config.go's SOULMAN_ROOT default points at soulman-dev - must override
# explicitly here or prod would read reports from the dev tree.
$env:SOULMAN_ROOT = $PSScriptRoot

$configSrc = "C:\Users\Lenovo\Documents\obsidian\brynjar-obsidian\config\prod.json"
$configDst = Join-Path $PSScriptRoot "config.json"
Copy-Item $configSrc $configDst -Force
$env:CONFIG_PATH = $configDst

if (-not $env:SUPABASE_URL -or -not $env:SUPABASE_JWT_SECRET) {
    Write-Warning "SUPABASE_URL / SUPABASE_JWT_SECRET not set in .env - web-svc will fail to start until both are configured."
}

& $exe
```

- [ ] **Step 3: Add `web-svc` to `start-everything.ps1`'s service list**

In `C:\Users\Lenovo\start-everything.ps1`, change:

```powershell
foreach ($svc in @("memory-svc", "perception-svc", "thinking-svc", "action-svc")) {
```

to:

```powershell
foreach ($svc in @("memory-svc", "perception-svc", "thinking-svc", "action-svc", "web-svc")) {
```

- [ ] **Step 4: Manually verify both launch scripts build and start (without a real Supabase secret yet, confirming the fatal-config-error path works)**

Run: `powershell -NoProfile -File "C:\Users\Lenovo\soulman-dev\run-web-svc.ps1"`
Expected: builds successfully, then either logs the `SUPABASE_URL`/`SUPABASE_JWT_SECRET` warning and exits via `config.Load()`'s fatal error (if `.env` isn't populated yet), or starts and logs `web-svc started (HTTP=:9015, ...)` if it is. Either outcome confirms the script itself is correct — populating real secrets is a manual step outside this plan (see `NOTES.md`).

- [ ] **Step 5: Write `web-svc/NOTES.md`**

```markdown
# web-svc — Operational Notes

Incidents, gotchas, and decisions learned running this service — not captured in the design specs themselves (see `CLAUDE.md`'s Services section for spec links).

## SUPABASE_URL / SUPABASE_JWT_SECRET are not in this repo

Both are required environment variables (fatal startup error if either is blank), set via `.env` in each of `soulman-dev\` and `soulman-prod\` (loaded by `load-env.ps1`, same as `action-svc`'s Discord token). They must be filled in by hand before `web-svc` will start — the JWT secret is the same Supabase project secret `agent-suite`'s backend already uses (`supabase.jwt-secret` in its `application.yml`), since both apps verify tokens from the same hosted Supabase project.

## Owner-email check, not a roles table

Unlike `agent-suite`'s `UserResolverFilter` (DB-backed `suite_user`/`user_role` lookup), `web-svc`'s `auth.Verifier` does no database lookup at all — it just compares the JWT's `email` claim against `web.owner_email` in `config/dev.json`/`prod.json`. This is deliberate: Soulman has exactly one real user. If this ever needs multiple authorized users, that's a real design change, not a config tweak.

## `/api/status` never fails even when a downstream service is down

Each of the four downstream `/health` checks in `apiStatus` has its own 5s timeout and failures are captured per-service (`"down"` in the response map) rather than failing the whole request — a single service being down (e.g. during a rebuild) shouldn't take down the dashboard's status panel along with it.
```

- [ ] **Step 6: Commit**

```bash
git add web-svc/NOTES.md
git commit -m "docs(web-svc): add operational notes; wire up dev/prod launch scripts"
```

(The two `run-web-svc.ps1` files and the `start-everything.ps1` edit live outside this git repo, per the existing convention for all other services' launch scripts — nothing to `git add` for those.)

---

### Task 7: `CLAUDE.md` documentation update

**Files:**
- Modify: `CLAUDE.md`

**Interfaces:** none (documentation only).

- [ ] **Step 1: Add `web-svc` to the Repository Structure table**

In the table under "## Repository Structure", add a row after the `action-svc` row:

```markdown
| `web-svc/`                      | Go service — Web dashboard backend runtime (`:9004`... |
```

Actually use the real port: add this row (matching the existing table's column format exactly):

```markdown
| `web-svc/`                      | Go service — Web dashboard backend runtime (`:9005`). See `web-svc/NOTES.md`. |
```

And add a row for the frontend:

```markdown
| `web/`                          | React + Vite frontend — Soulman's web dashboard. See `web/README.md` (if present) or `web-svc/NOTES.md` for the auth flow. |
```

- [ ] **Step 2: Add a numbered entry to the "## Services" section**

After entry 4 (`action-svc`), add:

```markdown
5. **`web-svc`** — the only Soulman service reachable from a browser: CORS-enabled, verifies Supabase-issued JWTs (reusing `agent-suite`'s existing hosted Supabase project and Google OAuth client), and authorizes a single configured owner email (`web.owner_email` in shared config) — no roles table. Serves `GET /api/status` (aggregates `/health` from the other four services), `GET /api/episodes` and `GET /api/raw-inputs/recent` (proxy `memory-svc`), and `GET /api/reports/latest` / `GET /api/reports?date=` (reads `$SOULMAN_ROOT/reports/*.txt` directly). Does not touch NATS at all. Override/control dispatch (PAUSE/STOP/RESUME) is explicitly not implemented here — blocked on a Guard Agent design that doesn't exist yet.
   - Specs: `2026-07-19-soulman-web-dashboard-design.md`
   - Notes: `web-svc/NOTES.md`
```

- [ ] **Step 3: Add a line to "### Running dev and prod simultaneously"**

After the existing paragraph about ports, add:

```markdown
`web-svc` follows the same port convention (`9005` prod / `9015` dev) but has no JetStream consumer and no NATS subscription at all — it only makes outbound HTTP calls to the other four services and reads report files directly off disk, so it needs no `consumer_names` entry and isn't part of the STIMULUS/THINKING_REQUEST/MEMORY_WRITE stream discussion above.
```

- [ ] **Step 4: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: document web-svc and the web dashboard frontend in CLAUDE.md"
```

---

### Task 8: Frontend scaffold (Vite + React + TS + Tailwind + Vitest)

**Files:**
- Create: `web/` (via `npm create vite@latest`) — `package.json`, `vite.config.ts`, `tsconfig*.json`, `index.html`, `src/main.tsx`, `src/index.css`, etc.
- Create: `web/postcss.config.js`
- Create: `web/src/setupTests.ts`
- Create: `web/.env`, `web/.env.production`, `web/.env.test`
- Modify: `web/package.json` (add Tailwind, Supabase, Vitest, RTL deps)
- Modify: `web/vite.config.ts` (ports, test config)

**Interfaces:** none yet — this task produces a buildable, empty shell; behavior arrives in later tasks.

- [ ] **Step 1: Scaffold the Vite project**

Run (from the vault root):
```
npm create vite@latest web -- --template react-ts
```
Expected: creates `web/` with the standard Vite React-TS template.

- [ ] **Step 2: Install base dependencies plus Tailwind, Supabase, and test tooling**

Run:
```
cd web
npm install
npm install @supabase/supabase-js@^2.107.0
npm install -D tailwindcss@^4.2.4 @tailwindcss/postcss@^4.2.4 autoprefixer@^10.5.0 postcss@^8.5.13
npm install -D vitest @testing-library/react @testing-library/jest-dom @testing-library/user-event jsdom
cd ..
```

- [ ] **Step 3: Add the `test` script to `web/package.json`**

In the `"scripts"` block, add:

```json
"test": "vitest run",
```

- [ ] **Step 4: Write `web/postcss.config.js`**

```js
export default {
  plugins: {
    '@tailwindcss/postcss': {},
    autoprefixer: {},
  },
}
```

- [ ] **Step 5: Replace `web/src/index.css`**

```css
@import "tailwindcss";
```

- [ ] **Step 6: Write `web/src/setupTests.ts`**

```ts
import '@testing-library/jest-dom/vitest';
```

- [ ] **Step 7: Update `web/vite.config.ts`**

```ts
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5178,
    host: '0.0.0.0',
  },
  preview: {
    port: 4173,
    host: '0.0.0.0',
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/setupTests.ts'],
  },
})
```

- [ ] **Step 8: Write the three env files**

`web/.env` (gitignored — dev points at local values; populate the real anon key by hand, matching `agent-suite/frontend/.env`'s local-dev convention):

```
VITE_SUPABASE_URL=https://grgspbzqzjblsoxmmojy.supabase.co
VITE_SUPABASE_ANON_KEY=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZSIsInJlZiI6ImdyZ3NwYnpxempibHNveG1tb2p5Iiwicm9sZSI6ImFub24iLCJpYXQiOjE3ODA3NTY4MTYsImV4cCI6MjA5NjMzMjgxNn0.1lVGjeZOGEcDMESw6W8tM7XdlVWEfHBozSZsPDiRfLs
VITE_AUTH_REDIRECT_URL=http://localhost:5178
VITE_WEB_SVC_URL=http://localhost:9015
```

`web/.env.production` (committed — anon key is public/safe, same as `agent-suite`'s):

```
VITE_SUPABASE_URL=https://grgspbzqzjblsoxmmojy.supabase.co
VITE_SUPABASE_ANON_KEY=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZSIsInJlZiI6ImdyZ3NwYnpxempibHNveG1tb2p5Iiwicm9sZSI6ImFub24iLCJpYXQiOjE3ODA3NTY4MTYsImV4cCI6MjA5NjMzMjgxNn0.1lVGjeZOGEcDMESw6W8tM7XdlVWEfHBozSZsPDiRfLs
VITE_AUTH_REDIRECT_URL=http://localhost:4173
VITE_WEB_SVC_URL=http://localhost:9005
```

`web/.env.test` (committed — used only by Vitest, points at a URL no real server needs to answer since tests mock `fetch`):

```
VITE_SUPABASE_URL=https://example.supabase.co
VITE_SUPABASE_ANON_KEY=test-anon-key
VITE_AUTH_REDIRECT_URL=http://localhost:5178
VITE_WEB_SVC_URL=http://localhost:9999
```

- [ ] **Step 9: Verify the project builds and the (currently empty) test suite runs cleanly**

Run:
```
cd web
npm run build
npm run test
cd ..
```
Expected: `npm run build` exits 0. `npm run test` reports "No test files found" (exit code may be non-zero for zero tests in some Vitest versions — that's expected and resolved once Task 9 adds the first real test file; do not treat this as a failure to fix here).

- [ ] **Step 10: Commit**

```bash
git add web/package.json web/package-lock.json web/vite.config.ts web/postcss.config.js web/src/index.css web/src/setupTests.ts web/.env.production web/.env.test web/tsconfig.json web/tsconfig.app.json web/tsconfig.node.json web/index.html web/src/main.tsx web/src/vite-env.d.ts web/.gitignore web/eslint.config.js
git commit -m "feat(web): scaffold Vite + React + TS + Tailwind + Vitest project"
```

(`web/.env` is dev-local and gitignored — do not add it. Confirm `web/.gitignore` — generated by the Vite scaffold — already excludes `.env`; if it doesn't, add `.env` to it before committing.)

---

### Task 9: `auth.ts` — Supabase client and `useAuth` hook

**Files:**
- Create: `web/src/auth.ts`
- Create: `web/src/auth.test.ts`

**Interfaces:**
- Produces: `useAuth(): { user: User | null; loading: boolean; signIn: () => Promise<void>; signOut: () => Promise<void> }`; `getAccessToken(): Promise<string | null>`.

- [ ] **Step 1: Write the failing tests**

`web/src/auth.test.ts`:

```ts
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';

const mockGetSession = vi.fn();
const mockOnAuthStateChange = vi.fn();
const mockSignInWithOAuth = vi.fn();
const mockSignOut = vi.fn();

vi.mock('@supabase/supabase-js', () => ({
  createClient: () => ({
    auth: {
      getSession: mockGetSession,
      onAuthStateChange: mockOnAuthStateChange,
      signInWithOAuth: mockSignInWithOAuth,
      signOut: mockSignOut,
    },
  }),
}));

beforeEach(() => {
  vi.clearAllMocks();
  mockOnAuthStateChange.mockReturnValue({ data: { subscription: { unsubscribe: vi.fn() } } });
});

describe('useAuth', () => {
  it('starts loading, then resolves to the session user', async () => {
    mockGetSession.mockResolvedValue({
      data: { session: { user: { id: 'u1', email: 'breynisson@gmail.com' } } },
    });
    const { useAuth } = await import('./auth');
    const { result } = renderHook(() => useAuth());

    expect(result.current.loading).toBe(true);
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.user?.email).toBe('breynisson@gmail.com');
  });

  it('resolves to no user when there is no session', async () => {
    mockGetSession.mockResolvedValue({ data: { session: null } });
    const { useAuth } = await import('./auth');
    const { result } = renderHook(() => useAuth());

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.user).toBeNull();
  });

  it('signIn calls signInWithOAuth with the google provider', async () => {
    mockGetSession.mockResolvedValue({ data: { session: null } });
    const { useAuth } = await import('./auth');
    const { result } = renderHook(() => useAuth());
    await waitFor(() => expect(result.current.loading).toBe(false));

    await result.current.signIn();

    expect(mockSignInWithOAuth).toHaveBeenCalledWith(
      expect.objectContaining({ provider: 'google' }),
    );
  });
});

describe('getAccessToken', () => {
  it('returns the session access token when present', async () => {
    mockGetSession.mockResolvedValue({ data: { session: { access_token: 'tok-123' } } });
    const { getAccessToken } = await import('./auth');
    await expect(getAccessToken()).resolves.toBe('tok-123');
  });

  it('returns null when there is no session', async () => {
    mockGetSession.mockResolvedValue({ data: { session: null } });
    const { getAccessToken } = await import('./auth');
    await expect(getAccessToken()).resolves.toBeNull();
  });
});
```

- [ ] **Step 2: Run the tests, verify they fail**

Run: `cd web && npm run test -- auth.test.ts`
Expected: FAIL — `./auth` module doesn't exist yet.

- [ ] **Step 3: Implement `web/src/auth.ts`**

Ported near-verbatim from `agent-suite/frontend/src/auth.ts`:

```ts
import { createClient } from '@supabase/supabase-js';
import { useEffect, useState } from 'react';
import type { User } from '@supabase/supabase-js';

const supabase = createClient(
  import.meta.env.VITE_SUPABASE_URL as string,
  import.meta.env.VITE_SUPABASE_ANON_KEY as string,
);

export async function getAccessToken(): Promise<string | null> {
  const { data: { session } } = await supabase.auth.getSession();
  return session?.access_token ?? null;
}

export function useAuth(): {
  user: User | null;
  loading: boolean;
  signIn: () => Promise<void>;
  signOut: () => Promise<void>;
} {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let active = true;
    supabase.auth.getSession().then(({ data: { session } }) => {
      if (!active) return;
      setUser(session?.user ?? null);
      setLoading(false);
    }).catch(() => {
      if (!active) return;
      setLoading(false);
    });

    const {
      data: { subscription },
    } = supabase.auth.onAuthStateChange((_event, session) => {
      setUser(session?.user ?? null);
    });

    return () => {
      active = false;
      subscription.unsubscribe();
    };
  }, []);

  const signIn = async () => {
    await supabase.auth.signInWithOAuth({
      provider: 'google',
      options: { redirectTo: import.meta.env.VITE_AUTH_REDIRECT_URL as string },
    });
  };

  const signOut = async () => {
    await supabase.auth.signOut();
  };

  return { user, loading, signIn, signOut };
}
```

- [ ] **Step 4: Run the tests, verify they pass**

Run: `cd web && npm run test -- auth.test.ts`
Expected: PASS (5 tests)

- [ ] **Step 5: Commit**

```bash
git add web/src/auth.ts web/src/auth.test.ts
git commit -m "feat(web): add Supabase auth client and useAuth hook"
```

---

### Task 10: `api.ts` — typed `web-svc` client

**Files:**
- Create: `web/src/api.ts`
- Create: `web/src/api.test.ts`

**Interfaces:**
- Consumes: nothing from earlier frontend tasks (imports `import.meta.env.VITE_WEB_SVC_URL` directly).
- Produces: `ApiError` (extends `Error`, has `.status`), `ServiceStatus`, `Episode`, `RawInput`, `Report` types; `getStatus`, `getEpisodes`, `getRawInputs`, `getLatestReport`, `getReportByDate` functions, each `(token: string | null, ...) => Promise<T>`.

- [ ] **Step 1: Write the failing tests**

`web/src/api.test.ts`:

```ts
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { getStatus, getEpisodes, getRawInputs, getLatestReport, getReportByDate, ApiError } from './api';

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn());
});

describe('getStatus', () => {
  it('attaches the bearer token and returns parsed JSON on success', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ 'memory-svc': 'up', 'action-svc': 'down' }),
    });

    const result = await getStatus('tok-abc');

    expect(result).toEqual({ 'memory-svc': 'up', 'action-svc': 'down' });
    const [url, options] = mockFetch.mock.calls[0];
    expect(url).toContain('/api/status');
    expect(options.headers.Authorization).toBe('Bearer tok-abc');
  });

  it('omits the Authorization header when token is null', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: true, status: 200, json: async () => ({}) });

    await getStatus(null);

    const [, options] = mockFetch.mock.calls[0];
    expect(options.headers.Authorization).toBeUndefined();
  });

  it('throws ApiError with the response status on a non-2xx response', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: false, status: 403, json: async () => ({}) });

    await expect(getStatus('tok-abc')).rejects.toThrow(ApiError);
    await expect(getStatus('tok-abc')).rejects.toMatchObject({ status: 403 });
  });
});

describe('getEpisodes', () => {
  it('passes the limit query param', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: true, status: 200, json: async () => [] });

    await getEpisodes('tok-abc', 5);

    const [url] = mockFetch.mock.calls[0];
    expect(url).toContain('/api/episodes');
    expect(url).toContain('limit=5');
  });
});

describe('getRawInputs', () => {
  it('calls the raw-inputs/recent endpoint', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: true, status: 200, json: async () => [] });

    await getRawInputs('tok-abc');

    const [url] = mockFetch.mock.calls[0];
    expect(url).toContain('/api/raw-inputs/recent');
  });
});

describe('getLatestReport', () => {
  it('calls the reports/latest endpoint', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: true, status: 200, json: async () => ({ date: '2026-07-19', content: 'x' }) });

    const result = await getLatestReport('tok-abc');

    expect(result.content).toBe('x');
    const [url] = mockFetch.mock.calls[0];
    expect(url).toContain('/api/reports/latest');
  });
});

describe('getReportByDate', () => {
  it('passes the date query param', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: true, status: 200, json: async () => ({ date: '2026-06-01', content: 'y' }) });

    await getReportByDate('tok-abc', '2026-06-01');

    const [url] = mockFetch.mock.calls[0];
    expect(url).toContain('date=2026-06-01');
  });
});
```

- [ ] **Step 2: Run the tests, verify they fail**

Run: `cd web && npm run test -- api.test.ts`
Expected: FAIL — `./api` module doesn't exist yet.

- [ ] **Step 3: Implement `web/src/api.ts`**

```ts
export interface ServiceStatus {
  [service: string]: 'up' | 'down';
}

export interface Episode {
  id: number;
  stream_seq: number;
  occurred_at: string;
  received_at: string;
  source: string;
  action_type: string;
  status: string;
  task_id?: string;
  summary: string;
  decision: string;
  outcome: string;
  tags: string[];
}

export interface RawInput {
  stimulus_id: string;
  received_at: string;
  channel: string;
  normalized_text?: string;
  raw_payload: unknown;
  override_cmd?: string;
}

export interface Report {
  date: string;
  content: string;
}

const WEB_SVC_URL = import.meta.env.VITE_WEB_SVC_URL as string;

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
  }
}

async function getJSON<T>(path: string, token: string | null): Promise<T> {
  const response = await fetch(`${WEB_SVC_URL}${path}`, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  });
  if (!response.ok) {
    throw new ApiError(response.status, `${path} failed (${response.status})`);
  }
  return response.json();
}

export const getStatus = (token: string | null): Promise<ServiceStatus> =>
  getJSON('/api/status', token);

export const getEpisodes = (token: string | null, limit = 20): Promise<Episode[]> =>
  getJSON(`/api/episodes?limit=${limit}`, token);

export const getRawInputs = (token: string | null, limit = 20): Promise<RawInput[]> =>
  getJSON(`/api/raw-inputs/recent?limit=${limit}`, token);

export const getLatestReport = (token: string | null): Promise<Report> =>
  getJSON('/api/reports/latest', token);

export const getReportByDate = (token: string | null, date: string): Promise<Report> =>
  getJSON(`/api/reports?date=${date}`, token);
```

- [ ] **Step 4: Run the tests, verify they pass**

Run: `cd web && npm run test -- api.test.ts`
Expected: PASS (7 tests)

- [ ] **Step 5: Commit**

```bash
git add web/src/api.ts web/src/api.test.ts
git commit -m "feat(web): add typed web-svc API client"
```

---

### Task 11: `App.tsx` — login / restricted / dashboard state routing

**Files:**
- Create: `web/src/components/LoginScreen.tsx`
- Create: `web/src/components/RestrictedScreen.tsx`
- Create: `web/src/App.tsx` (replaces the scaffold's default)
- Create: `web/src/App.test.tsx`
- Modify: `web/src/main.tsx` (no logic change needed — already imports `App` from `./App.tsx`; verify the import path still matches)

**Interfaces:**
- Consumes: `useAuth`, `getAccessToken` from `./auth` (Task 9); `getStatus`, `ApiError`, `ServiceStatus` from `./api` (Task 10).
- Produces: `App` default export. `LoginScreen({ onSignIn: () => void })`, `RestrictedScreen({ onSignOut: () => void })` — consumed by `App` here and available to later tasks.

- [ ] **Step 1: Write the failing tests**

`web/src/App.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';

const mockUseAuth = vi.fn();
vi.mock('./auth', () => ({
  useAuth: () => mockUseAuth(),
  getAccessToken: vi.fn().mockResolvedValue('tok-abc'),
}));

const mockGetStatus = vi.fn();
vi.mock('./api', async () => {
  const actual = await vi.importActual<typeof import('./api')>('./api');
  return { ...actual, getStatus: (...args: unknown[]) => mockGetStatus(...(args as [string | null])) };
});

beforeEach(() => {
  vi.clearAllMocks();
});

describe('App', () => {
  it('shows the login screen when there is no user', async () => {
    mockUseAuth.mockReturnValue({ user: null, loading: false, signIn: vi.fn(), signOut: vi.fn() });
    const { default: App } = await import('./App');
    render(<App />);

    expect(await screen.findByRole('button', { name: /sign in/i })).toBeInTheDocument();
  });

  it('shows the restricted page when /api/status returns 403', async () => {
    mockUseAuth.mockReturnValue({
      user: { email: 'someone-else@example.com' },
      loading: false,
      signIn: vi.fn(),
      signOut: vi.fn(),
    });
    const { ApiError } = await import('./api');
    mockGetStatus.mockRejectedValue(new ApiError(403, 'forbidden'));
    const { default: App } = await import('./App');
    render(<App />);

    expect(await screen.findByText(/private system/i)).toBeInTheDocument();
  });

  it('shows the dashboard when /api/status succeeds', async () => {
    mockUseAuth.mockReturnValue({
      user: { email: 'breynisson@gmail.com' },
      loading: false,
      signIn: vi.fn(),
      signOut: vi.fn(),
    });
    mockGetStatus.mockResolvedValue({ 'memory-svc': 'up' });
    const { default: App } = await import('./App');
    render(<App />);

    expect(await screen.findByText(/soulman dashboard/i)).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run the tests, verify they fail**

Run: `cd web && npm run test -- App.test.tsx`
Expected: FAIL — `./App` doesn't have this shape yet (still the Vite scaffold default).

- [ ] **Step 3: Write `web/src/components/LoginScreen.tsx`**

```tsx
export function LoginScreen({ onSignIn }: { onSignIn: () => void }) {
  return (
    <div className="flex h-screen items-center justify-center bg-gray-50">
      <button
        onClick={onSignIn}
        className="rounded bg-blue-600 px-6 py-3 text-white hover:bg-blue-700"
      >
        Sign in with Google
      </button>
    </div>
  );
}
```

- [ ] **Step 4: Write `web/src/components/RestrictedScreen.tsx`**

```tsx
export function RestrictedScreen({ onSignOut }: { onSignOut: () => void }) {
  return (
    <div className="flex h-screen flex-col items-center justify-center gap-4 bg-gray-50">
      <p className="text-lg text-gray-700">This is a private system.</p>
      <button onClick={onSignOut} className="text-sm text-gray-500 underline">
        Sign out
      </button>
    </div>
  );
}
```

- [ ] **Step 5: Write `web/src/App.tsx`**

Since `Dashboard` doesn't exist yet (arrives in Task 12), this task renders a minimal inline placeholder heading directly in `App.tsx` so the dashboard-reached test can pass without depending on a task that hasn't run yet — Task 12 replaces this inline block with the real `<Dashboard>` import.

```tsx
import { useEffect, useState } from 'react';
import { useAuth, getAccessToken } from './auth';
import { getStatus, ApiError, type ServiceStatus } from './api';
import { LoginScreen } from './components/LoginScreen';
import { RestrictedScreen } from './components/RestrictedScreen';

type ViewState = 'loading' | 'login' | 'restricted' | 'dashboard';

function App() {
  const { user, loading: authLoading, signIn, signOut } = useAuth();
  const [view, setView] = useState<ViewState>('loading');
  const [status, setStatus] = useState<ServiceStatus | null>(null);

  useEffect(() => {
    if (authLoading) return;
    if (!user) {
      setView('login');
      return;
    }
    let active = true;
    (async () => {
      const token = await getAccessToken();
      try {
        const s = await getStatus(token);
        if (!active) return;
        setStatus(s);
        setView('dashboard');
      } catch (err) {
        if (!active) return;
        if (err instanceof ApiError && err.status === 403) {
          setView('restricted');
        } else {
          setView('login');
        }
      }
    })();
    return () => {
      active = false;
    };
  }, [user, authLoading]);

  if (view === 'loading') return <div className="p-8 text-center">Loading...</div>;
  if (view === 'login') return <LoginScreen onSignIn={signIn} />;
  if (view === 'restricted') return <RestrictedScreen onSignOut={signOut} />;

  return (
    <div className="min-h-screen bg-gray-50 p-6">
      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Soulman Dashboard</h1>
        <button onClick={signOut} className="text-sm text-gray-500 underline">
          Sign out
        </button>
      </div>
      <p className="text-sm text-gray-500">
        {status ? `${Object.keys(status).length} services reporting` : 'Loading status...'}
      </p>
    </div>
  );
}

export default App;
```

- [ ] **Step 6: Delete the scaffold's leftover `App.css` import if present**

If `web/src/App.tsx`'s original scaffold content imported `./App.css`, that import is gone in the rewrite above — delete the now-unused `web/src/App.css` file.

- [ ] **Step 7: Run the tests, verify they pass**

Run: `cd web && npm run test -- App.test.tsx`
Expected: PASS (3 tests)

- [ ] **Step 8: Verify the full test suite and build still pass**

Run:
```
cd web
npm run test
npm run build
cd ..
```
Expected: all tests PASS, build exits 0

- [ ] **Step 9: Commit**

```bash
git add web/src/App.tsx web/src/App.test.tsx web/src/components/LoginScreen.tsx web/src/components/RestrictedScreen.tsx
git rm --cached web/src/App.css 2>/dev/null || true
git commit -m "feat(web): add login/restricted/dashboard state routing in App"
```

---

### Task 12: Dashboard panels

**Files:**
- Create: `web/src/components/StatusPanel.tsx`
- Create: `web/src/components/StatusPanel.test.tsx`
- Create: `web/src/components/EpisodesPanel.tsx`
- Create: `web/src/components/EpisodesPanel.test.tsx`
- Create: `web/src/components/RawInputsPanel.tsx`
- Create: `web/src/components/ReportsPanel.tsx`
- Create: `web/src/components/ReportsPanel.test.tsx`
- Create: `web/src/components/Dashboard.tsx`
- Modify: `web/src/App.tsx` (replace the inline placeholder block with `<Dashboard>`)

**Interfaces:**
- Consumes: `getAccessToken` from `./auth`; `getEpisodes`, `getRawInputs`, `getLatestReport`, `getReportByDate`, `ServiceStatus`, `Episode`, `RawInput`, `Report` from `./api`.
- Produces: `Dashboard({ initialStatus: ServiceStatus | null; onSignOut: () => void })`, consumed by `App.tsx`.

- [ ] **Step 1: Write the failing test for `StatusPanel`**

`web/src/components/StatusPanel.test.tsx`:

```tsx
import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { StatusPanel } from './StatusPanel';

describe('StatusPanel', () => {
  it('renders each service and its up/down state', () => {
    render(<StatusPanel initialStatus={{ 'memory-svc': 'up', 'action-svc': 'down' }} />);
    expect(screen.getByText('memory-svc')).toBeInTheDocument();
    expect(screen.getByText('up')).toBeInTheDocument();
    expect(screen.getByText('action-svc')).toBeInTheDocument();
    expect(screen.getByText('down')).toBeInTheDocument();
  });

  it('shows a placeholder when there is no status data', () => {
    render(<StatusPanel initialStatus={null} />);
    expect(screen.getByText(/no status data/i)).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run the test, verify it fails**

Run: `cd web && npm run test -- StatusPanel.test.tsx`
Expected: FAIL — component doesn't exist yet.

- [ ] **Step 3: Implement `web/src/components/StatusPanel.tsx`**

```tsx
import type { ServiceStatus } from '../api';

export function StatusPanel({ initialStatus }: { initialStatus: ServiceStatus | null }) {
  const services = initialStatus ? Object.entries(initialStatus) : [];
  return (
    <div className="rounded border bg-white p-4">
      <h2 className="mb-2 font-medium">System Status</h2>
      {services.length === 0 ? (
        <p className="text-sm text-gray-500">No status data</p>
      ) : (
        <ul className="space-y-1">
          {services.map(([name, state]) => (
            <li key={name} className="flex justify-between text-sm">
              <span>{name}</span>
              <span className={state === 'up' ? 'text-green-600' : 'text-red-600'}>{state}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
```

- [ ] **Step 4: Run the test, verify it passes**

Run: `cd web && npm run test -- StatusPanel.test.tsx`
Expected: PASS (2 tests)

- [ ] **Step 5: Write the failing test for `EpisodesPanel`**

`web/src/components/EpisodesPanel.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockGetEpisodes = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return { ...actual, getEpisodes: (...args: unknown[]) => mockGetEpisodes(...args) };
});

beforeEach(() => vi.clearAllMocks());

describe('EpisodesPanel', () => {
  it('shows episodes once loaded', async () => {
    mockGetEpisodes.mockResolvedValue([
      { id: 1, occurred_at: '2026-07-19T09:00:00Z', summary: 'Disk space critical' },
    ]);
    const { EpisodesPanel } = await import('./EpisodesPanel');
    render(<EpisodesPanel />);

    expect(await screen.findByText(/disk space critical/i)).toBeInTheDocument();
  });

  it('shows an error banner without throwing when the fetch fails', async () => {
    mockGetEpisodes.mockRejectedValue(new Error('network error'));
    const { EpisodesPanel } = await import('./EpisodesPanel');
    render(<EpisodesPanel />);

    expect(await screen.findByText(/episodes unavailable/i)).toBeInTheDocument();
  });

  it('shows an empty state when there are no episodes', async () => {
    mockGetEpisodes.mockResolvedValue([]);
    const { EpisodesPanel } = await import('./EpisodesPanel');
    render(<EpisodesPanel />);

    expect(await screen.findByText(/no episodes yet/i)).toBeInTheDocument();
  });
});
```

- [ ] **Step 6: Run the test, verify it fails**

Run: `cd web && npm run test -- EpisodesPanel.test.tsx`
Expected: FAIL — component doesn't exist yet.

- [ ] **Step 7: Implement `web/src/components/EpisodesPanel.tsx`**

```tsx
import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { getEpisodes, type Episode } from '../api';

export function EpisodesPanel() {
  const [episodes, setEpisodes] = useState<Episode[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    (async () => {
      const token = await getAccessToken();
      try {
        const data = await getEpisodes(token);
        if (active) setEpisodes(data);
      } catch {
        if (active) setError('Episodes unavailable');
      }
    })();
    return () => {
      active = false;
    };
  }, []);

  return (
    <div className="rounded border bg-white p-4">
      <h2 className="mb-2 font-medium">Recent Episodes</h2>
      {error && <p className="text-sm text-red-600">{error}</p>}
      {!error && episodes === null && <p className="text-sm text-gray-500">Loading...</p>}
      {!error && episodes?.length === 0 && <p className="text-sm text-gray-500">No episodes yet</p>}
      {!error && episodes && episodes.length > 0 && (
        <ul className="space-y-2">
          {episodes.map((e) => (
            <li key={e.id} className="text-sm">
              <span className="text-gray-400">{e.occurred_at}</span> — {e.summary}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
```

- [ ] **Step 8: Run the test, verify it passes**

Run: `cd web && npm run test -- EpisodesPanel.test.tsx`
Expected: PASS (3 tests)

- [ ] **Step 9: Implement `web/src/components/RawInputsPanel.tsx` (same fetch/loading/error/empty pattern as `EpisodesPanel`, not independently retested — the pattern is already proven by `EpisodesPanel.test.tsx`)**

```tsx
import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { getRawInputs, type RawInput } from '../api';

export function RawInputsPanel() {
  const [inputs, setInputs] = useState<RawInput[] | null>(null);
  const [error, setError] = useState<string | null>(null);

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
            <li key={i.stimulus_id} className="text-sm">
              <span className="text-gray-400">{i.received_at}</span> [{i.channel}]{' '}
              {i.normalized_text ?? '(no text)'}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
```

- [ ] **Step 10: Write the failing test for `ReportsPanel`**

`web/src/components/ReportsPanel.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockGetLatestReport = vi.fn();
const mockGetReportByDate = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    getLatestReport: (...args: unknown[]) => mockGetLatestReport(...args),
    getReportByDate: (...args: unknown[]) => mockGetReportByDate(...args),
  };
});

beforeEach(() => vi.clearAllMocks());

describe('ReportsPanel', () => {
  it('shows the latest report on load', async () => {
    mockGetLatestReport.mockResolvedValue({ date: '2026-07-19', content: 'latest report content' });
    const { ReportsPanel } = await import('./ReportsPanel');
    render(<ReportsPanel />);

    expect(await screen.findByText(/latest report content/i)).toBeInTheDocument();
  });

  it('loads a specific date report when the date picker changes', async () => {
    mockGetLatestReport.mockResolvedValue({ date: '2026-07-19', content: 'latest' });
    mockGetReportByDate.mockResolvedValue({ date: '2026-06-01', content: 'june first content' });
    const { ReportsPanel } = await import('./ReportsPanel');
    render(<ReportsPanel />);
    await screen.findByText(/latest/i);

    const input = screen.getByLabelText(/report date/i, { selector: 'input' }) as HTMLInputElement;
    fireEvent.change(input, { target: { value: '2026-06-01' } });

    expect(await screen.findByText(/june first content/i)).toBeInTheDocument();
    expect(mockGetReportByDate).toHaveBeenCalledWith('tok-abc', '2026-06-01');
  });

  it('shows an error message when no report exists for the selected date', async () => {
    mockGetLatestReport.mockResolvedValue({ date: '2026-07-19', content: 'latest' });
    mockGetReportByDate.mockRejectedValue(new Error('not found'));
    const { ReportsPanel } = await import('./ReportsPanel');
    render(<ReportsPanel />);
    await screen.findByText(/latest/i);

    const input = screen.getByLabelText(/report date/i, { selector: 'input' });
    await userEvent.type(input, '2020-01-01');

    expect(await screen.findByText(/no report for this date/i)).toBeInTheDocument();
  });
});
```

- [ ] **Step 11: Run the test, verify it fails**

Run: `cd web && npm run test -- ReportsPanel.test.tsx`
Expected: FAIL — component doesn't exist yet.

- [ ] **Step 12: Implement `web/src/components/ReportsPanel.tsx`**

```tsx
import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { getLatestReport, getReportByDate, type Report } from '../api';

export function ReportsPanel() {
  const [report, setReport] = useState<Report | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [date, setDate] = useState('');

  const load = async (selectedDate: string) => {
    setError(null);
    setReport(null);
    const token = await getAccessToken();
    try {
      const data = selectedDate
        ? await getReportByDate(token, selectedDate)
        : await getLatestReport(token);
      setReport(data);
    } catch {
      setError(selectedDate ? 'No report for this date' : 'Reports unavailable');
    }
  };

  useEffect(() => {
    load('');
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div className="rounded border bg-white p-4">
      <div className="mb-2 flex items-center justify-between">
        <h2 className="font-medium">Daily Report</h2>
        <label htmlFor="report-date" className="sr-only">
          Report date
        </label>
        <input
          id="report-date"
          type="date"
          aria-label="Report date"
          value={date}
          onChange={(e) => {
            setDate(e.target.value);
            load(e.target.value);
          }}
          className="rounded border px-2 py-1 text-sm"
        />
      </div>
      {error && <p className="text-sm text-red-600">{error}</p>}
      {!error && !report && <p className="text-sm text-gray-500">Loading...</p>}
      {!error && report && (
        <>
          <p className="mb-1 text-xs text-gray-400">{report.date}</p>
          <pre className="whitespace-pre-wrap text-sm">{report.content}</pre>
        </>
      )}
    </div>
  );
}
```

- [ ] **Step 13: Run the test, verify it passes**

Run: `cd web && npm run test -- ReportsPanel.test.tsx`
Expected: PASS (3 tests)

- [ ] **Step 14: Implement `web/src/components/Dashboard.tsx`**

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

- [ ] **Step 15: Replace `App.tsx`'s inline dashboard placeholder with the real `Dashboard`**

In `web/src/App.tsx`, add the import:

```tsx
import { Dashboard } from './components/Dashboard';
```

And replace the final inline `<div className="min-h-screen ...">...</div>` block (the placeholder written in Task 11 Step 5) with:

```tsx
  return <Dashboard initialStatus={status} onSignOut={signOut} />;
```

- [ ] **Step 16: Update `App.test.tsx`'s dashboard assertion to match the real Dashboard's heading**

The existing test in `web/src/App.test.tsx` (`shows the dashboard when /api/status succeeds`) already asserts `findByText(/soulman dashboard/i)`, which the real `Dashboard` component also renders — no change needed. Run it to confirm.

- [ ] **Step 17: Run the full frontend test suite and build**

Run:
```
cd web
npm run test
npm run build
cd ..
```
Expected: all tests PASS, build exits 0

- [ ] **Step 18: Commit**

```bash
git add web/src/components/StatusPanel.tsx web/src/components/StatusPanel.test.tsx web/src/components/EpisodesPanel.tsx web/src/components/EpisodesPanel.test.tsx web/src/components/RawInputsPanel.tsx web/src/components/ReportsPanel.tsx web/src/components/ReportsPanel.test.tsx web/src/components/Dashboard.tsx web/src/App.tsx
git commit -m "feat(web): add dashboard panels (status, episodes, raw inputs, reports)"
```

---

### Task 13: End-to-end manual verification

**Files:** none created/modified — this task only verifies the two services work together.

**Interfaces:** none.

- [ ] **Step 1: Fill in real secrets**

In `C:\Users\Lenovo\soulman-dev\.env`, ensure `SUPABASE_URL=https://grgspbzqzjblsoxmmojy.supabase.co` and `SUPABASE_JWT_SECRET=<the real Supabase project JWT secret, same value agent-suite's backend uses>` are both present. Repeat in `C:\Users\Lenovo\soulman-prod\.env` if prod is being verified too.

- [ ] **Step 2: Start `web-svc` (dev)**

Run: `powershell -NoProfile -File "C:\Users\Lenovo\soulman-dev\run-web-svc.ps1"`
Expected: logs `web-svc started (HTTP=:9015, owner=breynisson@gmail.com)`

- [ ] **Step 3: Start the frontend dev server**

Run: `cd web && npm run dev`
Expected: Vite dev server starts on `http://localhost:5178`

- [ ] **Step 4: Manually verify the three states in a browser**

1. Open `http://localhost:5178` in a private/incognito window (no existing Supabase session) — confirm the **Sign in with Google** button renders and nothing else.
2. Click it, complete the Google OAuth flow signed in as `breynisson@gmail.com` — confirm it redirects back and the **Soulman Dashboard** renders with System Status, Recent Episodes, Recent Raw Inputs, and Daily Report panels.
3. Confirm the System Status panel shows `perception-svc`/`memory-svc`/`thinking-svc`/`action-svc` (whichever of these are actually running in `soulman-dev` at the time — panels for services that aren't running should show `down`, not crash the page).
4. Sign out, then sign back in with a different Google account (or manually inspect the network tab to confirm a 403 from `/api/status` when testing with a non-owner token) — confirm the **"This is a private system."** static page renders instead of the dashboard.

- [ ] **Step 5: Report the outcome**

If all four checks in Step 4 pass, the feature is complete and working end-to-end. If any check fails, use `superpowers:systematic-debugging` before making further changes — do not patch symptoms without understanding root cause.

---
