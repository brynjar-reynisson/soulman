# Projects Tool Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the Projects tool — a new isolated Go service (`projects-svc`) with its own Postgres schema, proxied through `web-svc` at `/api/projects/**`, plus a new dashboard page — that lets the user manage projects and prompts, where creating a prompt automatically launches a background Claude Code session and tracks it through a NOT_STARTED → CREATING_SPEC → IMPLEMENTING → DONE lifecycle, with at most one CREATING_SPEC session running at a time.

**Architecture:** New `projects-svc` Go module (own `go.mod`, own Postgres schema `projects_dev`/`projects_prod`, two HTTP listeners — a main port for CRUD, a loopback-only port for the session-notify callback). `web-svc` gains a thin authenticated proxy route group forwarding `/api/projects/**` to `projects-svc`'s main port. `web/` gains a new page reachable from the dashboard, sharing only the existing global Tailwind styling.

**Tech Stack:** Go 1.25, `github.com/go-chi/chi/v5` (routing), `github.com/jackc/pgx/v5` (Postgres), React + TypeScript + Tailwind (frontend, no new dependencies).

**Spec:** `docs/superpowers/specs/2026-09-04-projects-tool-design.md` — read it first if anything below is ambiguous.

## Global Constraints

- Git branch: `feature/projects-tool` (created before Task 1; per this repo's convention, checked out directly — no worktree).
- Commit messages follow this repo's `type(scope): summary` convention (see recent `git log` for examples), ending with the required attribution trailer (see below).
- Ports: `projects-svc` main port `9006` (prod) / `9016` (dev); notify port `9007` (prod) / `9017` (dev). These are read from `HTTP_PORT`/`NOTIFY_PORT` env vars (`projects-svc` itself, like every other Go service here, does not hardcode dev-vs-prod — the launcher script sets the env var per environment), defaulting to the prod values (`9006`/`9007`) when unset.
- Postgres: same physical instance as `memory-svc` (`DATABASE_URL` env var, default `postgres://postgres:postgres@localhost:54322/postgres`), schema selected via a `SCHEMA` env var (default `projects_dev`), interpolated into queries via `fmt.Sprintf` on the schema name (never as a bound parameter — Postgres doesn't allow parameterizing identifiers), exactly matching `memory-svc/storage/postgres.go`'s existing pattern.
- State constants are the four literal strings `NOT_STARTED`, `CREATING_SPEC`, `IMPLEMENTING`, `DONE` — defined once in `projects-svc/store` and referenced everywhere else, never re-declared as separate string literals in other packages.
- **Never write a test that lets `launcher.Launch` reach a valid project path with `claude` actually on `PATH` at the same time** — that combination calls `exec.Command("claude", "--remote-control", "--bg", ...).Start()` for real, spawning an actual remote-control session on whatever machine runs the test suite. Every `launcher` test must stop at an earlier validation error (missing/non-directory path). `dispatch` tests must use a fake `LaunchFunc`, never the real `launcher.Launch`.
- Every commit ends with:
  ```
  Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_0145DzrC2W5CLksBESBsJpaA
  ```
- Every new Go source file added: run `git add` for it explicitly (new files don't get staged by an existing `git add -u`-style habit).
- Deployment (creating `run-projects-svc.ps1` in `soulman-dev`/`soulman-prod`, applying the DDL, editing `start-everything.ps1`/`setup-firewall-rules.ps1`, restarting services) is **explicitly out of scope for this plan's tasks** — those directories aren't git repos and that work happens after this branch merges, done directly by the coordinating session, not by a task-implementer subagent.

---

## Task 1: SQL DDL and shared config wiring

**Files:**
- Create: `docs/superpowers/specs/sql/2026-09-04-projects-tables.sql`
- Modify: `common/sharedconfig/config.go`
- Modify: `common/sharedconfig/config_test.go`
- Modify: `config/dev.json`
- Modify: `config/prod.json`

**Interfaces:**
- Produces: `sharedconfig.WebConfig.ProjectsSvcURL string` (json tag `projects_svc_url`) — later tasks (Task 9) read this from `web-svc/config`.

- [ ] **Step 1: Write the DDL file**

```sql
-- project/prompt tables for projects-svc.
-- Apply by hand against each environment's Postgres (projects_dev /
-- projects_prod schema), matching how memory-svc's tables are provisioned
-- -- projects-svc itself never creates its own tables. See
-- docs/superpowers/specs/2026-09-04-projects-tool-design.md for context.
--
-- Usage:
--   psql "$DATABASE_URL" -v schema=projects_dev -f 2026-09-04-projects-tables.sql
--   psql "$DATABASE_URL" -v schema=projects_prod -f 2026-09-04-projects-tables.sql

CREATE SCHEMA IF NOT EXISTS :schema;

CREATE TABLE IF NOT EXISTS :schema.project (
    name text PRIMARY KEY,
    path text NOT NULL
);

CREATE TABLE IF NOT EXISTS :schema.prompt (
    id                 bigserial PRIMARY KEY,
    project_name       text NOT NULL REFERENCES :schema.project(name),
    task_name          text NOT NULL,
    prompt_text        text NOT NULL,
    state              text NOT NULL DEFAULT 'NOT_STARTED'
                         CHECK (state IN ('NOT_STARTED', 'CREATING_SPEC', 'IMPLEMENTING', 'DONE')),
    last_launch_error  text,
    created_at         timestamptz NOT NULL DEFAULT now()
);
```

- [ ] **Step 2: Add `ProjectsSvcURL` to `sharedconfig.WebConfig`**

Open `common/sharedconfig/config.go`, find the `WebConfig` struct (it already has `PerceptionSvcURL`, `MemorySvcURL`, `ThinkingSvcURL`, `ActionSvcURL` fields). Add, alongside those:

```go
ProjectsSvcURL string `json:"projects_svc_url"`
```

- [ ] **Step 3: Write the config test**

Open `common/sharedconfig/config_test.go`. Find the existing test(s) that assert `PerceptionSvcURL`/`MemorySvcURL` load correctly from a fixture JSON blob (look for a test function with `web` in its name, e.g. `TestLoad_WebFields` or similar — match its exact style). Add a case (or extend the existing fixture) asserting that a `"projects_svc_url": "http://localhost:9016"` entry under `"web"` loads into `ProjectsSvcURL`. Follow the exact structure of the neighboring assertion for `PerceptionSvcURL` in that same test — same fixture JSON shape, same assertion style.

- [ ] **Step 4: Run the test, verify it fails, then passes**

```
go test ./common/sharedconfig/... -run TestLoad -v
```
Expected: fails before Step 2 is complete (field doesn't exist / zero value), passes after.

- [ ] **Step 5: Wire the URLs into `config/dev.json` and `config/prod.json`**

In `config/dev.json`, inside the `"web"` object (alongside `"perception_svc_url"` etc.), add:
```json
"projects_svc_url": "http://localhost:9016"
```
In `config/prod.json`, in the same location:
```json
"projects_svc_url": "http://localhost:9006"
```

- [ ] **Step 6: Run the full `common` test suite**

```
go test ./common/...
```
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add docs/superpowers/specs/sql/2026-09-04-projects-tables.sql common/sharedconfig/config.go common/sharedconfig/config_test.go config/dev.json config/prod.json
git commit -m "feat: add projects tool DDL and shared config wiring

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0145DzrC2W5CLksBESBsJpaA"
```

---

## Task 2: `projects-svc` module scaffold and config package

**Files:**
- Create: `projects-svc/go.mod`
- Create: `projects-svc/config/config.go`
- Create: `projects-svc/config/config_test.go`

**Interfaces:**
- Produces: `config.Config{DatabaseURL, Schema, HTTPPort, NotifyPort string}` and `config.Load() (*Config, error)` — consumed by Task 8 (`main.go`).

- [ ] **Step 1: Create the module**

```
cd projects-svc
go mod init soulman/projects-svc
```
(Run from the vault root: `C:\Users\Lenovo\Documents\obsidian\soulman\projects-svc` must be created first — `mkdir projects-svc` if it doesn't exist, then `go mod init` inside it.)

Edit the generated `go.mod` to match this repo's convention (compare with `memory-svc/go.mod`):
```go
module soulman/projects-svc

go 1.25.0

require soulman/common v0.0.0

replace soulman/common => ../common
```

- [ ] **Step 2: Write the config test**

```go
package config_test

import (
	"os"
	"testing"

	"soulman/projects-svc/config"
)

func TestLoad_DefaultsWhenEnvUnset(t *testing.T) {
	os.Unsetenv("DATABASE_URL")
	os.Unsetenv("SCHEMA")
	os.Unsetenv("HTTP_PORT")
	os.Unsetenv("NOTIFY_PORT")

	cfg := config.Load()

	if cfg.DatabaseURL != "postgres://postgres:postgres@localhost:54322/postgres" {
		t.Errorf("DatabaseURL = %q, want the default local Postgres URL", cfg.DatabaseURL)
	}
	if cfg.Schema != "projects_dev" {
		t.Errorf("Schema = %q, want projects_dev", cfg.Schema)
	}
	if cfg.HTTPPort != "9006" {
		t.Errorf("HTTPPort = %q, want 9006", cfg.HTTPPort)
	}
	if cfg.NotifyPort != "9007" {
		t.Errorf("NotifyPort = %q, want 9007", cfg.NotifyPort)
	}
}

func TestLoad_ReadsFromEnv(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x/y")
	t.Setenv("SCHEMA", "projects_prod")
	t.Setenv("HTTP_PORT", "9106")
	t.Setenv("NOTIFY_PORT", "9107")

	cfg := config.Load()

	if cfg.DatabaseURL != "postgres://x/y" {
		t.Errorf("DatabaseURL = %q, want postgres://x/y", cfg.DatabaseURL)
	}
	if cfg.Schema != "projects_prod" {
		t.Errorf("Schema = %q, want projects_prod", cfg.Schema)
	}
	if cfg.HTTPPort != "9106" {
		t.Errorf("HTTPPort = %q, want 9106", cfg.HTTPPort)
	}
	if cfg.NotifyPort != "9107" {
		t.Errorf("NotifyPort = %q, want 9107", cfg.NotifyPort)
	}
}
```

- [ ] **Step 3: Run the test, verify it fails (package doesn't exist yet)**

```
go test ./projects-svc/config/... -v
```
Expected: FAIL (build error, no such package contents).

- [ ] **Step 4: Implement `config.go`**

```go
// Package config reads projects-svc's runtime configuration from
// environment variables only — unlike web-svc/memory-svc, projects-svc has
// no cross-service settings to read from sharedconfig, so it doesn't call
// sharedconfig.Load at all.
package config

import "os"

type Config struct {
	DatabaseURL string
	Schema      string
	HTTPPort    string
	NotifyPort  string
}

func Load() *Config {
	return &Config{
		DatabaseURL: env("DATABASE_URL", "postgres://postgres:postgres@localhost:54322/postgres"),
		Schema:      env("SCHEMA", "projects_dev"),
		HTTPPort:    env("HTTP_PORT", "9006"),
		NotifyPort:  env("NOTIFY_PORT", "9007"),
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
```

- [ ] **Step 5: Run the test, verify it passes**

```
go test ./projects-svc/config/... -v
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add projects-svc/go.mod projects-svc/config/config.go projects-svc/config/config_test.go
git commit -m "feat(projects-svc): scaffold module and config package

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0145DzrC2W5CLksBESBsJpaA"
```

---

## Task 3: `store` package — Postgres CRUD

**Files:**
- Create: `projects-svc/store/store.go`
- Create: `projects-svc/store/store_test.go`
- Modify: `projects-svc/go.mod` (add `github.com/jackc/pgx/v5` dependency)

**Interfaces:**
- Consumes: nothing new.
- Produces (consumed by Task 5 `dispatch`, Task 6/7 `httpserver`, Task 8 `main.go`):
  ```go
  const (
      StateNotStarted   = "NOT_STARTED"
      StateCreatingSpec = "CREATING_SPEC"
      StateImplementing = "IMPLEMENTING"
      StateDone         = "DONE"
  )
  var ErrNotFound, ErrAlreadyExists, ErrHasPrompts error

  type Project struct { Name, Path string }
  type Prompt struct {
      ID              int64
      ProjectName     string
      TaskName        string
      PromptText      string
      State           string
      LastLaunchError *string
      CreatedAt       time.Time
  }

  type Store struct { /* unexported */ }
  func New(ctx context.Context, connStr, schema string) (*Store, error)
  func (s *Store) Close()
  func (s *Store) ListProjects(ctx context.Context) ([]Project, error)
  func (s *Store) CreateProject(ctx context.Context, p Project) error
  func (s *Store) UpdateProject(ctx context.Context, name, path string) error
  func (s *Store) DeleteProject(ctx context.Context, name string) error
  func (s *Store) GetProject(ctx context.Context, name string) (Project, error)
  func (s *Store) ListPrompts(ctx context.Context) ([]Prompt, error)
  func (s *Store) CreatePrompt(ctx context.Context, projectName, taskName, promptText string) (Prompt, error)
  func (s *Store) UpdatePromptState(ctx context.Context, id int64, state string) error
  func (s *Store) SetLaunchError(ctx context.Context, id int64, msg string) error
  func (s *Store) MarkCreatingSpec(ctx context.Context, id int64) error
  func (s *Store) HasCreatingSpec(ctx context.Context) (bool, error)
  func (s *Store) OldestNotStarted(ctx context.Context) (*Prompt, error)
  func (s *Store) ExecCleanup(ctx context.Context, sql string, args ...interface{}) // test-only
  ```

**Note on `CreateProject`/`CreatePrompt` error mapping:** a duplicate `project.name` maps to `ErrAlreadyExists` (via `INSERT ... ON CONFLICT (name) DO NOTHING` + checking `RowsAffected()`) and a `prompt.project_name` referencing a nonexistent project maps to `ErrNotFound` (via the Postgres foreign-key-violation error code `23503`) — this exact error-mapping approach isn't spelled out in the spec but is a small, necessary addition: without it, either case would otherwise surface as an unhandled 500 with a raw Postgres error message, which is clearly not what "manage projects (add/delete/edit)" and "add new PROMPTs" are supposed to feel like from the UI.

- [ ] **Step 1: Add the pgx dependency**

```
cd projects-svc
go get github.com/jackc/pgx/v5@v5.10.0
```
(Match the version `memory-svc/go.mod` already uses.)

- [ ] **Step 2: Write `store_test.go`**

```go
package store_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"soulman/projects-svc/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@localhost:54322/postgres"
	}
	ctx := context.Background()
	s, err := store.New(ctx, dbURL, "projects_dev")
	if err != nil {
		t.Skipf("postgres not available (%v) — set DATABASE_URL to run DB tests", err)
	}
	t.Cleanup(s.Close)
	return s
}

func uniqueName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func cleanupProject(t *testing.T, s *store.Store, name string) {
	t.Cleanup(func() {
		s.ExecCleanup(context.Background(), "DELETE FROM projects_dev.prompt WHERE project_name = $1", name)
		s.ExecCleanup(context.Background(), "DELETE FROM projects_dev.project WHERE name = $1", name)
	})
}

func TestStore_CreateAndListProjects(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	name := uniqueName("proj")
	cleanupProject(t, s, name)

	if err := s.CreateProject(ctx, store.Project{Name: name, Path: `C:\tmp\` + name}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	projects, err := s.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	found := false
	for _, p := range projects {
		if p.Name == name {
			found = true
		}
	}
	if !found {
		t.Errorf("ListProjects did not include created project %q", name)
	}
}

func TestStore_CreateProject_DuplicateName_ReturnsErrAlreadyExists(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	name := uniqueName("dup")
	cleanupProject(t, s, name)

	if err := s.CreateProject(ctx, store.Project{Name: name, Path: `C:\tmp\a`}); err != nil {
		t.Fatalf("first CreateProject: %v", err)
	}
	err := s.CreateProject(ctx, store.Project{Name: name, Path: `C:\tmp\b`})
	if err != store.ErrAlreadyExists {
		t.Fatalf("second CreateProject error = %v, want ErrAlreadyExists", err)
	}
}

func TestStore_UpdateProject_NotFound_ReturnsErrNotFound(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	err := s.UpdateProject(ctx, uniqueName("missing"), `C:\tmp`)
	if err != store.ErrNotFound {
		t.Fatalf("UpdateProject error = %v, want ErrNotFound", err)
	}
}

func TestStore_DeleteProject_WithPrompts_ReturnsErrHasPrompts(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	name := uniqueName("hasprompts")
	cleanupProject(t, s, name)

	if err := s.CreateProject(ctx, store.Project{Name: name, Path: `C:\tmp`}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := s.CreatePrompt(ctx, name, "task", "do the thing"); err != nil {
		t.Fatalf("CreatePrompt: %v", err)
	}

	err := s.DeleteProject(ctx, name)
	if err != store.ErrHasPrompts {
		t.Fatalf("DeleteProject error = %v, want ErrHasPrompts", err)
	}
}

func TestStore_DeleteProject_NoPrompts_Succeeds(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	name := uniqueName("empty")

	if err := s.CreateProject(ctx, store.Project{Name: name, Path: `C:\tmp`}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := s.DeleteProject(ctx, name); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if _, err := s.GetProject(ctx, name); err != store.ErrNotFound {
		t.Fatalf("GetProject after delete = %v, want ErrNotFound", err)
	}
}

func TestStore_CreatePrompt_UnknownProject_ReturnsErrNotFound(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	_, err := s.CreatePrompt(ctx, uniqueName("noproject"), "task", "text")
	if err != store.ErrNotFound {
		t.Fatalf("CreatePrompt error = %v, want ErrNotFound", err)
	}
}

func TestStore_CreatePrompt_DefaultsToNotStarted(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	name := uniqueName("proj")
	cleanupProject(t, s, name)

	if err := s.CreateProject(ctx, store.Project{Name: name, Path: `C:\tmp`}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	p, err := s.CreatePrompt(ctx, name, "task", "text")
	if err != nil {
		t.Fatalf("CreatePrompt: %v", err)
	}
	if p.State != store.StateNotStarted {
		t.Errorf("State = %q, want %q", p.State, store.StateNotStarted)
	}
	if p.LastLaunchError != nil {
		t.Errorf("LastLaunchError = %v, want nil", p.LastLaunchError)
	}
}

func TestStore_MarkCreatingSpecAndHasCreatingSpec(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	name := uniqueName("proj")
	cleanupProject(t, s, name)

	if err := s.CreateProject(ctx, store.Project{Name: name, Path: `C:\tmp`}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	p, err := s.CreatePrompt(ctx, name, "task", "text")
	if err != nil {
		t.Fatalf("CreatePrompt: %v", err)
	}

	if has, err := s.HasCreatingSpec(ctx); err != nil || has {
		t.Fatalf("HasCreatingSpec before mark = (%v, %v), want (false, nil)", has, err)
	}

	if err := s.MarkCreatingSpec(ctx, p.ID); err != nil {
		t.Fatalf("MarkCreatingSpec: %v", err)
	}

	if has, err := s.HasCreatingSpec(ctx); err != nil || !has {
		t.Fatalf("HasCreatingSpec after mark = (%v, %v), want (true, nil)", has, err)
	}
}

func TestStore_OldestNotStarted_PicksLowestID(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	name := uniqueName("proj")
	cleanupProject(t, s, name)

	if err := s.CreateProject(ctx, store.Project{Name: name, Path: `C:\tmp`}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	first, err := s.CreatePrompt(ctx, name, "first", "text")
	if err != nil {
		t.Fatalf("CreatePrompt first: %v", err)
	}
	if _, err := s.CreatePrompt(ctx, name, "second", "text"); err != nil {
		t.Fatalf("CreatePrompt second: %v", err)
	}

	oldest, err := s.OldestNotStarted(ctx)
	if err != nil {
		t.Fatalf("OldestNotStarted: %v", err)
	}
	if oldest == nil || oldest.ID != first.ID {
		t.Fatalf("OldestNotStarted = %+v, want ID %d", oldest, first.ID)
	}
}

func TestStore_OldestNotStarted_NoneReturnsNil(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	name := uniqueName("proj")
	cleanupProject(t, s, name)

	if err := s.CreateProject(ctx, store.Project{Name: name, Path: `C:\tmp`}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	p, err := s.CreatePrompt(ctx, name, "task", "text")
	if err != nil {
		t.Fatalf("CreatePrompt: %v", err)
	}
	if err := s.UpdatePromptState(ctx, p.ID, store.StateDone); err != nil {
		t.Fatalf("UpdatePromptState: %v", err)
	}

	oldest, err := s.OldestNotStarted(ctx)
	if err != nil {
		t.Fatalf("OldestNotStarted: %v", err)
	}
	if oldest != nil {
		t.Errorf("OldestNotStarted = %+v, want nil", oldest)
	}
}

func TestStore_SetLaunchError_ThenMarkCreatingSpecClearsIt(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	name := uniqueName("proj")
	cleanupProject(t, s, name)

	if err := s.CreateProject(ctx, store.Project{Name: name, Path: `C:\tmp`}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	p, err := s.CreatePrompt(ctx, name, "task", "text")
	if err != nil {
		t.Fatalf("CreatePrompt: %v", err)
	}

	if err := s.SetLaunchError(ctx, p.ID, "boom"); err != nil {
		t.Fatalf("SetLaunchError: %v", err)
	}
	prompts, err := s.ListPrompts(ctx)
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	var got *store.Prompt
	for i := range prompts {
		if prompts[i].ID == p.ID {
			got = &prompts[i]
		}
	}
	if got == nil || got.LastLaunchError == nil || *got.LastLaunchError != "boom" {
		t.Fatalf("prompt after SetLaunchError = %+v, want LastLaunchError=boom", got)
	}

	if err := s.MarkCreatingSpec(ctx, p.ID); err != nil {
		t.Fatalf("MarkCreatingSpec: %v", err)
	}
	prompts, err = s.ListPrompts(ctx)
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	for i := range prompts {
		if prompts[i].ID == p.ID {
			got = &prompts[i]
		}
	}
	if got.LastLaunchError != nil {
		t.Errorf("LastLaunchError after MarkCreatingSpec = %v, want nil", got.LastLaunchError)
	}
	if got.State != store.StateCreatingSpec {
		t.Errorf("State = %q, want %q", got.State, store.StateCreatingSpec)
	}
}
```

- [ ] **Step 3: Run the test, verify it fails (package doesn't exist yet)**

```
go test ./projects-svc/store/... -v
```
Expected: FAIL (build error).

- [ ] **Step 4: Implement `store.go`**

```go
// Package store is projects-svc's Postgres access layer for the project
// and prompt tables. Mirrors memory-svc/storage's schema-interpolation
// pattern (see memory-svc/storage/postgres.go) — the schema name is
// selected via fmt.Sprintf, not a bound parameter, since Postgres cannot
// parameterize identifiers.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	StateNotStarted   = "NOT_STARTED"
	StateCreatingSpec = "CREATING_SPEC"
	StateImplementing = "IMPLEMENTING"
	StateDone         = "DONE"
)

var (
	ErrNotFound      = errors.New("store: not found")
	ErrAlreadyExists = errors.New("store: already exists")
	ErrHasPrompts    = errors.New("store: project has prompts")
)

const foreignKeyViolation = "23503"

type Project struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type Prompt struct {
	ID              int64     `json:"id"`
	ProjectName     string    `json:"project_name"`
	TaskName        string    `json:"task_name"`
	PromptText      string    `json:"prompt_text"`
	State           string    `json:"state"`
	LastLaunchError *string   `json:"last_launch_error,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type Store struct {
	pool   *pgxpool.Pool
	schema string
}

func New(ctx context.Context, connStr, schema string) (*Store, error) {
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		return nil, fmt.Errorf("store: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &Store{pool: pool, schema: schema}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT name, path FROM %s.project ORDER BY name`, s.schema))
	if err != nil {
		return nil, fmt.Errorf("store: list projects: %w", err)
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.Name, &p.Path); err != nil {
			return nil, fmt.Errorf("store: scan project: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) CreateProject(ctx context.Context, p Project) error {
	tag, err := s.pool.Exec(ctx, fmt.Sprintf(
		`INSERT INTO %s.project (name, path) VALUES ($1, $2) ON CONFLICT (name) DO NOTHING`, s.schema),
		p.Name, p.Path)
	if err != nil {
		return fmt.Errorf("store: create project: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrAlreadyExists
	}
	return nil
}

func (s *Store) UpdateProject(ctx context.Context, name, path string) error {
	tag, err := s.pool.Exec(ctx, fmt.Sprintf(
		`UPDATE %s.project SET path = $1 WHERE name = $2`, s.schema), path, name)
	if err != nil {
		return fmt.Errorf("store: update project: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteProject(ctx context.Context, name string) error {
	tag, err := s.pool.Exec(ctx, fmt.Sprintf(`DELETE FROM %s.project WHERE name = $1`, s.schema), name)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
			return ErrHasPrompts
		}
		return fmt.Errorf("store: delete project: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetProject(ctx context.Context, name string) (Project, error) {
	var p Project
	err := s.pool.QueryRow(ctx, fmt.Sprintf(`SELECT name, path FROM %s.project WHERE name = $1`, s.schema), name).
		Scan(&p.Name, &p.Path)
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, fmt.Errorf("store: get project: %w", err)
	}
	return p, nil
}

func (s *Store) ListPrompts(ctx context.Context) ([]Prompt, error) {
	rows, err := s.pool.Query(ctx, fmt.Sprintf(
		`SELECT id, project_name, task_name, prompt_text, state, last_launch_error, created_at
		 FROM %s.prompt ORDER BY id`, s.schema))
	if err != nil {
		return nil, fmt.Errorf("store: list prompts: %w", err)
	}
	defer rows.Close()
	var out []Prompt
	for rows.Next() {
		var p Prompt
		if err := rows.Scan(&p.ID, &p.ProjectName, &p.TaskName, &p.PromptText, &p.State, &p.LastLaunchError, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: scan prompt: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) CreatePrompt(ctx context.Context, projectName, taskName, promptText string) (Prompt, error) {
	var p Prompt
	err := s.pool.QueryRow(ctx, fmt.Sprintf(`
		INSERT INTO %s.prompt (project_name, task_name, prompt_text)
		VALUES ($1, $2, $3)
		RETURNING id, project_name, task_name, prompt_text, state, last_launch_error, created_at
	`, s.schema), projectName, taskName, promptText).
		Scan(&p.ID, &p.ProjectName, &p.TaskName, &p.PromptText, &p.State, &p.LastLaunchError, &p.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
			return Prompt{}, ErrNotFound
		}
		return Prompt{}, fmt.Errorf("store: create prompt: %w", err)
	}
	return p, nil
}

func (s *Store) UpdatePromptState(ctx context.Context, id int64, state string) error {
	tag, err := s.pool.Exec(ctx, fmt.Sprintf(
		`UPDATE %s.prompt SET state = $1 WHERE id = $2`, s.schema), state, id)
	if err != nil {
		return fmt.Errorf("store: update prompt state: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetLaunchError(ctx context.Context, id int64, msg string) error {
	_, err := s.pool.Exec(ctx, fmt.Sprintf(
		`UPDATE %s.prompt SET last_launch_error = $1 WHERE id = $2`, s.schema), msg, id)
	if err != nil {
		return fmt.Errorf("store: set launch error: %w", err)
	}
	return nil
}

func (s *Store) MarkCreatingSpec(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, fmt.Sprintf(
		`UPDATE %s.prompt SET state = $1, last_launch_error = NULL WHERE id = $2`, s.schema),
		StateCreatingSpec, id)
	if err != nil {
		return fmt.Errorf("store: mark creating_spec: %w", err)
	}
	return nil
}

func (s *Store) HasCreatingSpec(ctx context.Context) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, fmt.Sprintf(
		`SELECT EXISTS(SELECT 1 FROM %s.prompt WHERE state = $1)`, s.schema), StateCreatingSpec).
		Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("store: has creating_spec: %w", err)
	}
	return exists, nil
}

func (s *Store) OldestNotStarted(ctx context.Context) (*Prompt, error) {
	var p Prompt
	err := s.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT id, project_name, task_name, prompt_text, state, last_launch_error, created_at
		FROM %s.prompt WHERE state = $1 ORDER BY id LIMIT 1
	`, s.schema), StateNotStarted).
		Scan(&p.ID, &p.ProjectName, &p.TaskName, &p.PromptText, &p.State, &p.LastLaunchError, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: oldest not_started: %w", err)
	}
	return &p, nil
}

// ExecCleanup runs an arbitrary SQL statement — used only by tests for cleanup.
func (s *Store) ExecCleanup(ctx context.Context, sql string, args ...interface{}) {
	s.pool.Exec(ctx, sql, args...)
}
```

- [ ] **Step 5: Apply the DDL to the local dev Postgres so tests can run**

```
psql "postgres://postgres:postgres@localhost:54322/postgres" -v schema=projects_dev -f docs/superpowers/specs/sql/2026-09-04-projects-tables.sql
```
(If `psql` isn't on `PATH` or the local Postgres isn't reachable, the tests will `t.Skip` — that's expected and fine; Step 6 still needs to show a clean build.)

- [ ] **Step 6: Run the tests**

```
go test ./projects-svc/store/... -v
```
Expected: PASS (or all `SKIP` with the "postgres not available" message, if no local Postgres — either is acceptable to proceed, but prefer applying the DDL and getting real PASSes if a local Postgres is reachable).

- [ ] **Step 7: Commit**

```bash
git add projects-svc/store/store.go projects-svc/store/store_test.go projects-svc/go.mod projects-svc/go.sum
git commit -m "feat(projects-svc): add Postgres store package

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0145DzrC2W5CLksBESBsJpaA"
```

---

## Task 4: `launcher` package — spawns the Claude Code session

**Files:**
- Create: `projects-svc/launcher/launcher.go`
- Create: `projects-svc/launcher/launcher_windows.go`
- Create: `projects-svc/launcher/launcher_test.go`

**Interfaces:**
- Consumes: nothing new (works with its own small `Project`/`Prompt` structs, not `store`'s — see below).
- Produces (consumed by Task 5 `dispatch`, Task 8 `main.go`):
  ```go
  type Project struct { Name, Path string }
  type Prompt struct { ID int64; TaskName, PromptText string }
  var ErrProjectPathNotFound, ErrLaunchFailed error
  func Launch(project Project, prompt Prompt, notifyPort string) error
  ```

**Why `launcher` defines its own `Project`/`Prompt` instead of importing `store`'s:** keeps `launcher` free of any Postgres/store dependency — it only needs a name, a path, an ID, a task name, and prompt text, so it declares that shape itself. `dispatch` (Task 5) does the translation between `store.Project`/`store.Prompt` and `launcher.Project`/`launcher.Prompt`.

- [ ] **Step 1: Write `launcher_test.go`**

```go
package launcher_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"soulman/projects-svc/launcher"
)

func TestLaunch_MissingProjectPath_ReturnsErrProjectPathNotFound(t *testing.T) {
	project := launcher.Project{Name: "demo", Path: filepath.Join(t.TempDir(), "does-not-exist")}
	prompt := launcher.Prompt{ID: 1, TaskName: "task", PromptText: "do it"}

	err := launcher.Launch(project, prompt, "9017")
	if !errors.Is(err, launcher.ErrProjectPathNotFound) {
		t.Fatalf("Launch error = %v, want ErrProjectPathNotFound", err)
	}
}

func TestLaunch_ProjectPathIsAFile_ReturnsErrProjectPathNotFound(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	project := launcher.Project{Name: "demo", Path: file}
	prompt := launcher.Prompt{ID: 1, TaskName: "task", PromptText: "do it"}

	err := launcher.Launch(project, prompt, "9017")
	if !errors.Is(err, launcher.ErrProjectPathNotFound) {
		t.Fatalf("Launch error = %v, want ErrProjectPathNotFound", err)
	}
}
```

(These two tests stop at the path-validation guard, before `exec.Command` is ever reached — per the Global Constraints, no test here may reach a real `claude` process spawn.)

- [ ] **Step 2: Run the test, verify it fails (package doesn't exist yet)**

```
go test ./projects-svc/launcher/... -v
```
Expected: FAIL (build error).

- [ ] **Step 3: Implement `launcher.go`**

```go
// Package launcher starts a detached, remote-control-enabled Claude Code
// session for one prompt, rooted at its project's path. Mirrors
// web-svc/claudesession.Launch's detachment pattern (see that package's
// doc comment for why --bg plus a null-device stdin/stdout/stderr is
// required on Windows), but takes a free-form project.Path directly
// instead of resolving a folder under a curated root, and passes the
// prompt text (with the fixed spec/implement/notify directive appended)
// as Launch's trailing positional argument — see
// docs/superpowers/specs/2026-09-04-projects-tool-design.md's Data Flow
// section for the exact directive text this builds.
package launcher

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

var (
	ErrProjectPathNotFound = errors.New("launcher: project path not found")
	ErrLaunchFailed        = errors.New("launcher: launch failed")
)

type Project struct {
	Name string
	Path string
}

type Prompt struct {
	ID         int64
	TaskName   string
	PromptText string
}

// Launch starts `claude --remote-control --bg --name "<project> <task>"
// "<prompt>"` detached, with its working directory set to project.Path.
// notifyPort is substituted into the directive's curl commands so the
// spawned session calls back on the right loopback-only notify listener
// for this environment (dev vs prod use different ports).
func Launch(project Project, prompt Prompt, notifyPort string) error {
	info, err := os.Stat(project.Path)
	if err != nil || !info.IsDir() {
		return ErrProjectPathNotFound
	}

	sessionName := project.Name + " " + prompt.TaskName
	fullPrompt := prompt.PromptText + "\n\n" + directive(prompt.ID, notifyPort)

	cmd := exec.Command("claude", "--remote-control", "--bg", "--name", sessionName, fullPrompt)
	cmd.Dir = project.Path
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%w: %v", ErrLaunchFailed, err)
	}
	cmd.Process.Release()
	return nil
}

func directive(promptID int64, notifyPort string) string {
	notify := func(state string) string {
		return fmt.Sprintf(
			`curl -s -X POST http://localhost:%s/notify -H "Content-Type: application/json" -d '{"prompt_id": %d, "state": "%s"}'`,
			notifyPort, promptID, state)
	}
	return fmt.Sprintf(
		"Use Superpowers to figure out via questioning what is being requested, and to write the feature spec. "+
			"Once the feature spec has been accepted, run:\n\n%s\n\n"+
			"then proceed to creating an implementation plan and executing it as usual. "+
			"Once that is complete, run:\n\n%s",
		notify("IMPLEMENTING"), notify("DONE"))
}
```

- [ ] **Step 4: Implement the Windows detach helper**

```go
//go:build windows

package launcher

import (
	"os/exec"
	"syscall"
)

// detach ensures the spawned claude process is not tied to projects-svc's
// own process group, so it survives a projects-svc restart — identical to
// web-svc/claudesession_windows.go's detach.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
```

- [ ] **Step 5: Run the tests, verify they pass**

```
go test ./projects-svc/launcher/... -v
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add projects-svc/launcher/launcher.go projects-svc/launcher/launcher_windows.go projects-svc/launcher/launcher_test.go
git commit -m "feat(projects-svc): add launcher package

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0145DzrC2W5CLksBESBsJpaA"
```

---

## Task 5: `dispatch` package — one-at-a-time queue

**Files:**
- Create: `projects-svc/dispatch/dispatch.go`
- Create: `projects-svc/dispatch/dispatch_test.go`

**Interfaces:**
- Consumes: `store.Project`, `store.Prompt`, `store.StateNotStarted`/`StateCreatingSpec` (types/consts from Task 3).
- Produces (consumed by Task 6/7 `httpserver`, Task 8 `main.go`):
  ```go
  type LaunchFunc func(project store.Project, prompt store.Prompt) error
  type Store interface {
      HasCreatingSpec(ctx context.Context) (bool, error)
      OldestNotStarted(ctx context.Context) (*store.Prompt, error)
      GetProject(ctx context.Context, name string) (store.Project, error)
      MarkCreatingSpec(ctx context.Context, id int64) error
      SetLaunchError(ctx context.Context, id int64, msg string) error
  }
  type Dispatcher struct { /* unexported */ }
  func New(st Store, launch LaunchFunc) *Dispatcher
  func (d *Dispatcher) TryDispatchNext(ctx context.Context)
  ```
  `*store.Store` (from Task 3) satisfies the `Store` interface here structurally — no explicit assertion needed, but Task 8's `main.go` should compile-check this by passing it directly.

- [ ] **Step 1: Write `dispatch_test.go`**

```go
package dispatch_test

import (
	"context"
	"errors"
	"testing"

	"soulman/projects-svc/dispatch"
	"soulman/projects-svc/store"
)

type fakeStore struct {
	hasCreatingSpec bool
	oldest          *store.Prompt
	projects        map[string]store.Project
	markedID        int64
	launchErrorID   int64
	launchErrorMsg  string
	hasCreatingSpecErr, oldestErr, getProjectErr, markErr, setErrErr error
}

func (f *fakeStore) HasCreatingSpec(ctx context.Context) (bool, error) {
	return f.hasCreatingSpec, f.hasCreatingSpecErr
}
func (f *fakeStore) OldestNotStarted(ctx context.Context) (*store.Prompt, error) {
	return f.oldest, f.oldestErr
}
func (f *fakeStore) GetProject(ctx context.Context, name string) (store.Project, error) {
	if f.getProjectErr != nil {
		return store.Project{}, f.getProjectErr
	}
	return f.projects[name], nil
}
func (f *fakeStore) MarkCreatingSpec(ctx context.Context, id int64) error {
	f.markedID = id
	return f.markErr
}
func (f *fakeStore) SetLaunchError(ctx context.Context, id int64, msg string) error {
	f.launchErrorID = id
	f.launchErrorMsg = msg
	return f.setErrErr
}

func TestTryDispatchNext_NoOpWhenAlreadyCreatingSpec(t *testing.T) {
	fs := &fakeStore{hasCreatingSpec: true}
	launchCalled := false
	d := dispatch.New(fs, func(store.Project, store.Prompt) error {
		launchCalled = true
		return nil
	})

	d.TryDispatchNext(context.Background())

	if launchCalled {
		t.Error("launch should not be called when a prompt is already CREATING_SPEC")
	}
}

func TestTryDispatchNext_NoOpWhenNothingQueued(t *testing.T) {
	fs := &fakeStore{hasCreatingSpec: false, oldest: nil}
	launchCalled := false
	d := dispatch.New(fs, func(store.Project, store.Prompt) error {
		launchCalled = true
		return nil
	})

	d.TryDispatchNext(context.Background())

	if launchCalled {
		t.Error("launch should not be called when no prompt is NOT_STARTED")
	}
}

func TestTryDispatchNext_SuccessfulLaunch_MarksCreatingSpec(t *testing.T) {
	prompt := &store.Prompt{ID: 42, ProjectName: "demo", TaskName: "task", PromptText: "text", State: store.StateNotStarted}
	fs := &fakeStore{
		hasCreatingSpec: false,
		oldest:          prompt,
		projects:        map[string]store.Project{"demo": {Name: "demo", Path: `C:\demo`}},
	}
	var gotProject store.Project
	var gotPrompt store.Prompt
	d := dispatch.New(fs, func(p store.Project, pr store.Prompt) error {
		gotProject, gotPrompt = p, pr
		return nil
	})

	d.TryDispatchNext(context.Background())

	if gotProject.Name != "demo" || gotPrompt.ID != 42 {
		t.Fatalf("launch called with (%+v, %+v), want project demo / prompt 42", gotProject, gotPrompt)
	}
	if fs.markedID != 42 {
		t.Errorf("MarkCreatingSpec called with id=%d, want 42", fs.markedID)
	}
}

func TestTryDispatchNext_LaunchFails_RecordsErrorAndDoesNotMark(t *testing.T) {
	prompt := &store.Prompt{ID: 7, ProjectName: "demo", TaskName: "task", PromptText: "text", State: store.StateNotStarted}
	fs := &fakeStore{
		hasCreatingSpec: false,
		oldest:          prompt,
		projects:        map[string]store.Project{"demo": {Name: "demo", Path: `C:\demo`}},
	}
	d := dispatch.New(fs, func(store.Project, store.Prompt) error {
		return errors.New("path not found")
	})

	d.TryDispatchNext(context.Background())

	if fs.markedID != 0 {
		t.Errorf("MarkCreatingSpec should not be called on launch failure, got id=%d", fs.markedID)
	}
	if fs.launchErrorID != 7 || fs.launchErrorMsg != "path not found" {
		t.Errorf("SetLaunchError called with (%d, %q), want (7, \"path not found\")", fs.launchErrorID, fs.launchErrorMsg)
	}
}
```

- [ ] **Step 2: Run the test, verify it fails (package doesn't exist yet)**

```
go test ./projects-svc/dispatch/... -v
```
Expected: FAIL (build error).

- [ ] **Step 3: Implement `dispatch.go`**

```go
// Package dispatch enforces "at most one prompt CREATING_SPEC at a time,
// globally" — see docs/superpowers/specs/2026-09-04-projects-tool-design.md's
// "Backend API & queue orchestration" section. Safe for concurrent calls
// from multiple HTTP handlers because exactly one projects-svc process
// runs per environment and TryDispatchNext is guarded by a single
// in-process mutex — no DB-level locking is needed.
package dispatch

import (
	"context"
	"log/slog"
	"sync"

	"soulman/projects-svc/store"
)

// Store is the subset of *store.Store this package needs — kept as a
// small local interface (rather than depending on the concrete type) so
// tests can supply a fake without touching Postgres.
type Store interface {
	HasCreatingSpec(ctx context.Context) (bool, error)
	OldestNotStarted(ctx context.Context) (*store.Prompt, error)
	GetProject(ctx context.Context, name string) (store.Project, error)
	MarkCreatingSpec(ctx context.Context, id int64) error
	SetLaunchError(ctx context.Context, id int64, msg string) error
}

// LaunchFunc starts a Claude Code session for prompt, rooted at project's
// path. In production this is an adapter around launcher.Launch (see
// projects-svc/main.go); tests supply a fake.
type LaunchFunc func(project store.Project, prompt store.Prompt) error

type Dispatcher struct {
	mu     sync.Mutex
	store  Store
	launch LaunchFunc
}

func New(st Store, launch LaunchFunc) *Dispatcher {
	return &Dispatcher{store: st, launch: launch}
}

// TryDispatchNext launches the oldest NOT_STARTED prompt if, and only if,
// no prompt is currently CREATING_SPEC. Errors from the store are logged
// and otherwise swallowed — this is called opportunistically after
// several different events (see callers), and none of them should fail
// just because a dispatch attempt couldn't proceed.
func (d *Dispatcher) TryDispatchNext(ctx context.Context) {
	d.mu.Lock()
	defer d.mu.Unlock()

	busy, err := d.store.HasCreatingSpec(ctx)
	if err != nil {
		slog.Error("dispatch: check creating_spec failed", "error", err)
		return
	}
	if busy {
		return
	}

	prompt, err := d.store.OldestNotStarted(ctx)
	if err != nil {
		slog.Error("dispatch: query oldest not_started failed", "error", err)
		return
	}
	if prompt == nil {
		return
	}

	project, err := d.store.GetProject(ctx, prompt.ProjectName)
	if err != nil {
		slog.Error("dispatch: load project failed", "project", prompt.ProjectName, "error", err)
		return
	}

	if err := d.launch(project, *prompt); err != nil {
		slog.Warn("dispatch: launch failed, prompt stays NOT_STARTED", "prompt_id", prompt.ID, "error", err)
		if serr := d.store.SetLaunchError(ctx, prompt.ID, err.Error()); serr != nil {
			slog.Error("dispatch: recording launch error failed", "prompt_id", prompt.ID, "error", serr)
		}
		return
	}

	if err := d.store.MarkCreatingSpec(ctx, prompt.ID); err != nil {
		slog.Error("dispatch: marking CREATING_SPEC failed after successful launch", "prompt_id", prompt.ID, "error", err)
	}
}
```

- [ ] **Step 4: Run the tests, verify they pass**

```
go test ./projects-svc/dispatch/... -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add projects-svc/dispatch/dispatch.go projects-svc/dispatch/dispatch_test.go
git commit -m "feat(projects-svc): add dispatch package for the one-at-a-time queue

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0145DzrC2W5CLksBESBsJpaA"
```

---

## Task 6: `httpserver` — main-port CRUD routes

**Files:**
- Create: `projects-svc/httpserver/server.go`
- Create: `projects-svc/httpserver/server_test.go`
- Modify: `projects-svc/go.mod` (add `github.com/go-chi/chi/v5`)

**Interfaces:**
- Consumes: `store.Store` methods (Task 3), `dispatch.Dispatcher.TryDispatchNext` (Task 5).
- Produces (consumed by Task 8 `main.go`):
  ```go
  func New(st *store.Store, d *dispatch.Dispatcher) *Server
  func (s *Server) Handler() http.Handler
  ```

- [ ] **Step 1: Add the chi dependency**

```
cd projects-svc
go get github.com/go-chi/chi/v5@v5.3.1
```

- [ ] **Step 2: Write `server_test.go`**

```go
package httpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"soulman/projects-svc/dispatch"
	"soulman/projects-svc/httpserver"
	"soulman/projects-svc/store"
)

// fakeStore implements the subset of *store.Store methods the httpserver
// package calls directly (list/create/update/delete), independent of the
// dispatch.Store interface in Task 5.
type fakeStore struct {
	projects       []store.Project
	prompts        []store.Prompt
	createErr      error
	updateErr      error
	deleteErr      error
	createPromptErr error
	updateStateErr error
	nextID         int64
}

func (f *fakeStore) ListProjects(ctx context.Context) ([]store.Project, error) { return f.projects, nil }
func (f *fakeStore) CreateProject(ctx context.Context, p store.Project) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.projects = append(f.projects, p)
	return nil
}
func (f *fakeStore) UpdateProject(ctx context.Context, name, path string) error { return f.updateErr }
func (f *fakeStore) DeleteProject(ctx context.Context, name string) error       { return f.deleteErr }
func (f *fakeStore) ListPrompts(ctx context.Context) ([]store.Prompt, error)    { return f.prompts, nil }
func (f *fakeStore) CreatePrompt(ctx context.Context, projectName, taskName, promptText string) (store.Prompt, error) {
	if f.createPromptErr != nil {
		return store.Prompt{}, f.createPromptErr
	}
	f.nextID++
	p := store.Prompt{ID: f.nextID, ProjectName: projectName, TaskName: taskName, PromptText: promptText, State: store.StateNotStarted}
	f.prompts = append(f.prompts, p)
	return p, nil
}
func (f *fakeStore) UpdatePromptState(ctx context.Context, id int64, state string) error {
	return f.updateStateErr
}

// noopDispatchStore is a dispatch.Store that always reports "busy" (a
// prompt is CREATING_SPEC), so TryDispatchNext no-ops immediately without
// calling any other method. Used to build an inert *dispatch.Dispatcher
// for tests that don't exercise dispatch behavior — passing a nil Store
// interface instead (newNoopDispatcher()) is NOT safe: a nil-interface
// method call panics, and Task 7's /notify handler runs TryDispatchNext
// in an unrecovered goroutine, so that panic would crash the whole test
// binary rather than just fail one test. This type is defined once, here,
// and reused by Task 7's notify_test.go (same httpserver_test package —
// do not redefine it there).
type noopDispatchStore struct{}

func (noopDispatchStore) HasCreatingSpec(ctx context.Context) (bool, error) { return true, nil }
func (noopDispatchStore) OldestNotStarted(ctx context.Context) (*store.Prompt, error) {
	return nil, nil
}
func (noopDispatchStore) GetProject(ctx context.Context, name string) (store.Project, error) {
	return store.Project{}, nil
}
func (noopDispatchStore) MarkCreatingSpec(ctx context.Context, id int64) error { return nil }
func (noopDispatchStore) SetLaunchError(ctx context.Context, id int64, msg string) error {
	return nil
}

func newNoopDispatcher() *dispatch.Dispatcher {
	return dispatch.New(noopDispatchStore{}, func(store.Project, store.Prompt) error { return nil })
}

func TestListProjects_ReturnsJSON(t *testing.T) {
	fs := &fakeStore{projects: []store.Project{{Name: "demo", Path: `C:\demo`}}}
	srv := httpserver.New(fs, newNoopDispatcher())

	req := httptest.NewRequest(http.MethodGet, "/projects", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var got []store.Project
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].Name != "demo" {
		t.Errorf("got %+v, want one project named demo", got)
	}
}

func TestCreateProject_Success_Returns201(t *testing.T) {
	fs := &fakeStore{}
	srv := httpserver.New(fs, newNoopDispatcher())

	body, _ := json.Marshal(store.Project{Name: "demo", Path: `C:\demo`})
	req := httptest.NewRequest(http.MethodPost, "/projects", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateProject_AlreadyExists_Returns409(t *testing.T) {
	fs := &fakeStore{createErr: store.ErrAlreadyExists}
	srv := httpserver.New(fs, newNoopDispatcher())

	body, _ := json.Marshal(store.Project{Name: "demo", Path: `C:\demo`})
	req := httptest.NewRequest(http.MethodPost, "/projects", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestCreateProject_MissingFields_Returns400(t *testing.T) {
	fs := &fakeStore{}
	srv := httpserver.New(fs, newNoopDispatcher())

	body, _ := json.Marshal(store.Project{Name: "demo"})
	req := httptest.NewRequest(http.MethodPost, "/projects", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestDeleteProject_HasPrompts_Returns409(t *testing.T) {
	fs := &fakeStore{deleteErr: store.ErrHasPrompts}
	srv := httpserver.New(fs, newNoopDispatcher())

	req := httptest.NewRequest(http.MethodDelete, "/projects/demo", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeleteProject_NotFound_Returns404(t *testing.T) {
	fs := &fakeStore{deleteErr: store.ErrNotFound}
	srv := httpserver.New(fs, newNoopDispatcher())

	req := httptest.NewRequest(http.MethodDelete, "/projects/missing", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestCreatePrompt_Success_Returns201(t *testing.T) {
	fs := &fakeStore{}
	srv := httpserver.New(fs, newNoopDispatcher())

	body, _ := json.Marshal(map[string]string{"project_name": "demo", "task_name": "task", "prompt_text": "do it"})
	req := httptest.NewRequest(http.MethodPost, "/prompts", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreatePrompt_UnknownProject_Returns400(t *testing.T) {
	fs := &fakeStore{createPromptErr: store.ErrNotFound}
	srv := httpserver.New(fs, newNoopDispatcher())

	body, _ := json.Marshal(map[string]string{"project_name": "missing", "task_name": "task", "prompt_text": "do it"})
	req := httptest.NewRequest(http.MethodPost, "/prompts", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestUpdatePromptState_InvalidState_Returns400(t *testing.T) {
	fs := &fakeStore{}
	srv := httpserver.New(fs, newNoopDispatcher())

	body, _ := json.Marshal(map[string]string{"state": "NOT_A_REAL_STATE"})
	req := httptest.NewRequest(http.MethodPut, "/prompts/1", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestUpdatePromptState_ValidState_Returns204(t *testing.T) {
	fs := &fakeStore{}
	srv := httpserver.New(fs, newNoopDispatcher())

	body, _ := json.Marshal(map[string]string{"state": store.StateNotStarted})
	req := httptest.NewRequest(http.MethodPut, "/prompts/1", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 3: Run the test, verify it fails (package doesn't exist yet)**

```
go test ./projects-svc/httpserver/... -v
```
Expected: FAIL (build error).

- [ ] **Step 4: Implement `server.go`**

Note: `New` takes a small local `Store` interface (covering only the CRUD methods this package calls), not `*store.Store` directly, so `server_test.go`'s `fakeStore` can satisfy it. `*store.Store` (Task 3) satisfies it structurally.

```go
// Package httpserver serves projects-svc's main-port CRUD API for
// projects and prompts. The separate loopback-only /notify listener
// (Task 7) is a distinct http.Server, not part of this router — see
// docs/superpowers/specs/2026-09-04-projects-tool-design.md's
// "Backend API & queue orchestration" section for why they're split.
package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"soulman/projects-svc/dispatch"
	"soulman/projects-svc/store"
)

// Store is the subset of *store.Store this package calls directly.
type Store interface {
	ListProjects(ctx context.Context) ([]store.Project, error)
	CreateProject(ctx context.Context, p store.Project) error
	UpdateProject(ctx context.Context, name, path string) error
	DeleteProject(ctx context.Context, name string) error
	ListPrompts(ctx context.Context) ([]store.Prompt, error)
	CreatePrompt(ctx context.Context, projectName, taskName, promptText string) (store.Prompt, error)
	UpdatePromptState(ctx context.Context, id int64, state string) error
}

type Server struct {
	store      Store
	dispatcher *dispatch.Dispatcher
	router     chi.Router
}

func New(st Store, d *dispatch.Dispatcher) *Server {
	s := &Server{store: st, dispatcher: d}
	s.router = s.buildRouter()
	return s
}

func (s *Server) Handler() http.Handler { return s.router }

func (s *Server) buildRouter() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.Logger)
	r.Get("/health", s.health)
	r.Get("/projects", s.listProjects)
	r.Post("/projects", s.createProject)
	r.Put("/projects/{name}", s.updateProject)
	r.Delete("/projects/{name}", s.deleteProject)
	r.Get("/prompts", s.listPrompts)
	r.Post("/prompts", s.createPrompt)
	r.Put("/prompts/{id}", s.updatePromptState)
	return r
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.store.ListProjects(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, projects)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var body store.Project
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" || body.Path == "" {
		writeError(w, http.StatusBadRequest, "name and path are required")
		return
	}
	if err := s.store.CreateProject(r.Context(), body); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "project already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, body)
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	if err := s.store.UpdateProject(r.Context(), name, body.Path); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if err := s.store.DeleteProject(r.Context(), name); err != nil {
		if errors.Is(err, store.ErrHasPrompts) {
			writeError(w, http.StatusConflict, "project has prompts; delete them first")
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listPrompts(w http.ResponseWriter, r *http.Request) {
	prompts, err := s.store.ListPrompts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, prompts)
}

func (s *Server) createPrompt(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProjectName string `json:"project_name"`
		TaskName    string `json:"task_name"`
		PromptText  string `json:"prompt_text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
		body.ProjectName == "" || body.TaskName == "" || body.PromptText == "" {
		writeError(w, http.StatusBadRequest, "project_name, task_name and prompt_text are required")
		return
	}
	prompt, err := s.store.CreatePrompt(r.Context(), body.ProjectName, body.TaskName, body.PromptText)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusBadRequest, "project not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, prompt)
	if s.dispatcher != nil {
		s.dispatcher.TryDispatchNext(r.Context())
	}
}

func (s *Server) updatePromptState(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !validState(body.State) {
		writeError(w, http.StatusBadRequest, "invalid state")
		return
	}
	if err := s.store.UpdatePromptState(r.Context(), id, body.State); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "prompt not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validState(s string) bool {
	switch s {
	case store.StateNotStarted, store.StateCreatingSpec, store.StateImplementing, store.StateDone:
		return true
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
```

Note: `TestListProjects_ReturnsJSON` and similar tests pass `newNoopDispatcher()` rather than a `dispatch.New(nil, nil)` built from nil interfaces — `TryDispatchNext` is reached whenever `createPrompt` succeeds (every test above that hits `POST /prompts`), and calling a method on a genuinely nil `Store` interface panics. `newNoopDispatcher()`'s `noopDispatchStore` always reports "busy," so `TryDispatchNext` no-ops immediately and safely. Also guard against a nil `*Server.dispatcher` field itself by checking `s.dispatcher != nil` before calling it, as shown above (this makes the field genuinely optional for any future caller that doesn't need auto-dispatch).

- [ ] **Step 5: Run the tests, verify they pass**

```
go test ./projects-svc/httpserver/... -v
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add projects-svc/httpserver/server.go projects-svc/httpserver/server_test.go projects-svc/go.mod projects-svc/go.sum
git commit -m "feat(projects-svc): add main-port CRUD HTTP routes

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0145DzrC2W5CLksBESBsJpaA"
```

---

## Task 7: `httpserver` — loopback-only notify listener

**Files:**
- Create: `projects-svc/httpserver/notify.go`
- Create: `projects-svc/httpserver/notify_test.go`

**Interfaces:**
- Consumes: `store.UpdatePromptState`, `store.StateImplementing`/`StateDone` (Task 3), `dispatch.Dispatcher.TryDispatchNext` (Task 5), and — in `notify_test.go` only — the `noopDispatchStore` type and `newNoopDispatcher()` helper function already defined in `projects-svc/httpserver/server_test.go` (Task 6, same `httpserver_test` package). Do not redefine them; `notify_test.go` does not need to import `soulman/projects-svc/dispatch` at all, since it never names that package directly.
- Produces (consumed by Task 8 `main.go`):
  ```go
  func NewNotifyServer(st NotifyStore, d *dispatch.Dispatcher) *http.Server
  ```

- [ ] **Step 1: Write `notify_test.go`**

```go
package httpserver_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"soulman/projects-svc/httpserver"
	"soulman/projects-svc/store"
)

// newNoopDispatcher is defined in server_test.go (Task 6, same
// httpserver_test package) — reused here, not redefined.

type fakeNotifyStore struct {
	updateStateErr error
	gotID          int64
	gotState       string
}

func (f *fakeNotifyStore) UpdatePromptState(ctx context.Context, id int64, state string) error {
	f.gotID, f.gotState = id, state
	return f.updateStateErr
}

func TestNotify_LoopbackAddress_Implementing_Returns204(t *testing.T) {
	fs := &fakeNotifyStore{}
	srv := httpserver.NewNotifyServer(fs, newNoopDispatcher())

	body := bytes.NewReader([]byte(`{"prompt_id": 7, "state": "IMPLEMENTING"}`))
	req := httptest.NewRequest(http.MethodPost, "/notify", body)
	req.RemoteAddr = "127.0.0.1:54321"
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", rec.Code, rec.Body.String())
	}
	if fs.gotID != 7 || fs.gotState != store.StateImplementing {
		t.Errorf("UpdatePromptState called with (%d, %q), want (7, IMPLEMENTING)", fs.gotID, fs.gotState)
	}
}

func TestNotify_NonLoopbackAddress_Returns403(t *testing.T) {
	fs := &fakeNotifyStore{}
	srv := httpserver.NewNotifyServer(fs, newNoopDispatcher())

	body := bytes.NewReader([]byte(`{"prompt_id": 7, "state": "IMPLEMENTING"}`))
	req := httptest.NewRequest(http.MethodPost, "/notify", body)
	req.RemoteAddr = "203.0.113.5:54321" // TEST-NET-3, a real non-loopback address
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if fs.gotID != 0 {
		t.Error("UpdatePromptState should not be called for a rejected request")
	}
}

func TestNotify_InvalidState_Returns400(t *testing.T) {
	fs := &fakeNotifyStore{}
	srv := httpserver.NewNotifyServer(fs, newNoopDispatcher())

	body := bytes.NewReader([]byte(`{"prompt_id": 7, "state": "NOT_A_REAL_STATE"}`))
	req := httptest.NewRequest(http.MethodPost, "/notify", body)
	req.RemoteAddr = "127.0.0.1:54321"
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestNotify_NotFoundPromptID_Returns404(t *testing.T) {
	fs := &fakeNotifyStore{updateStateErr: store.ErrNotFound}
	srv := httpserver.NewNotifyServer(fs, newNoopDispatcher())

	body := bytes.NewReader([]byte(`{"prompt_id": 999, "state": "DONE"}`))
	req := httptest.NewRequest(http.MethodPost, "/notify", body)
	req.RemoteAddr = "127.0.0.1:54321"
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
```

- [ ] **Step 2: Run the test, verify it fails (`NewNotifyServer` doesn't exist yet)**

```
go test ./projects-svc/httpserver/... -run TestNotify -v
```
Expected: FAIL (build error).

- [ ] **Step 3: Implement `notify.go`**

```go
package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"

	"soulman/projects-svc/dispatch"
	"soulman/projects-svc/store"
)

// NotifyStore is the subset of *store.Store the notify listener calls.
type NotifyStore interface {
	UpdatePromptState(ctx context.Context, id int64, state string) error
}

// NewNotifyServer builds the loopback-only /notify listener as a
// standalone *http.Server, separate from the main CRUD router — projects-
// svc/main.go binds this one to "127.0.0.1:<NOTIFY_PORT>" specifically
// (not "0.0.0.0"), and the RemoteAddr check here is defense in depth on
// top of that binding. See
// docs/superpowers/specs/2026-09-04-projects-tool-design.md's "Backend
// API & queue orchestration" section.
func NewNotifyServer(st NotifyStore, d *dispatch.Dispatcher) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/notify", notifyHandler(st, d))
	return &http.Server{Handler: mux}
}

func notifyHandler(st NotifyStore, d *dispatch.Dispatcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r.RemoteAddr) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}

		var body struct {
			PromptID int64  `json:"prompt_id"`
			State    string `json:"state"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if body.State != store.StateImplementing && body.State != store.StateDone {
			writeError(w, http.StatusBadRequest, "state must be IMPLEMENTING or DONE")
			return
		}

		if err := st.UpdatePromptState(r.Context(), body.PromptID, body.State); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "prompt not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		w.WriteHeader(http.StatusNoContent)

		if body.State == store.StateImplementing && d != nil {
			go dispatchNextSafely(d)
		}
	}
}

// dispatchNextSafely runs TryDispatchNext in the background goroutine
// notifyHandler spawns, recovering any panic so a bug in the dispatch
// path can never crash the whole process from an unrecovered goroutine
// panic — chi's middleware.Recoverer (used by the main CRUD router) does
// NOT protect goroutines spawned from within a handler, only the
// synchronous request-handling goroutine itself.
func dispatchNextSafely(d *dispatch.Dispatcher) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("notify: TryDispatchNext panicked", "recovered", r)
		}
	}()
	d.TryDispatchNext(context.Background())
}

func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
```

- [ ] **Step 4: Run the tests, verify they pass**

```
go test ./projects-svc/httpserver/... -v
```
Expected: PASS (all of `server_test.go` and `notify_test.go`).

- [ ] **Step 5: Commit**

```bash
git add projects-svc/httpserver/notify.go projects-svc/httpserver/notify_test.go
git commit -m "feat(projects-svc): add loopback-only notify listener

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0145DzrC2W5CLksBESBsJpaA"
```

---

## Task 8: `main.go` — wire it all together

**Files:**
- Create: `projects-svc/main.go`

**Interfaces:**
- Consumes: everything from Tasks 2–7.

- [ ] **Step 1: Implement `main.go`**

```go
package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"soulman/projects-svc/config"
	"soulman/projects-svc/dispatch"
	"soulman/projects-svc/httpserver"
	"soulman/projects-svc/launcher"
	"soulman/projects-svc/store"
)

func main() {
	cfg := config.Load()

	ctx := context.Background()
	st, err := store.New(ctx, cfg.DatabaseURL, cfg.Schema)
	if err != nil {
		slog.Error("store init failed", "error", err)
		os.Exit(1)
	}
	defer st.Close()

	launchFunc := func(project store.Project, prompt store.Prompt) error {
		return launcher.Launch(
			launcher.Project{Name: project.Name, Path: project.Path},
			launcher.Prompt{ID: prompt.ID, TaskName: prompt.TaskName, PromptText: prompt.PromptText},
			cfg.NotifyPort,
		)
	}
	dispatcher := dispatch.New(st, launchFunc)

	mainSrv := httpserver.New(st, dispatcher)
	notifySrv := httpserver.NewNotifyServer(st, dispatcher)

	go func() {
		slog.Info("projects-svc main http listening", "port", cfg.HTTPPort)
		if err := http.ListenAndServe(":"+cfg.HTTPPort, mainSrv.Handler()); err != nil {
			slog.Error("main http server failed", "error", err)
		}
	}()

	go func() {
		listener, err := net.Listen("tcp", "127.0.0.1:"+cfg.NotifyPort)
		if err != nil {
			slog.Error("notify listener bind failed", "error", err)
			return
		}
		slog.Info("projects-svc notify listener bound to loopback only", "port", cfg.NotifyPort)
		if err := notifySrv.Serve(listener); err != nil {
			slog.Error("notify http server failed", "error", err)
		}
	}()

	// Resume dispatching any queue that built up while projects-svc was down.
	dispatcher.TryDispatchNext(ctx)

	slog.Info("projects-svc started", "schema", cfg.Schema, "http_port", cfg.HTTPPort, "notify_port", cfg.NotifyPort)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("projects-svc shutting down")
}
```

- [ ] **Step 2: Build to confirm it compiles**

```
go build ./projects-svc/...
```
Expected: builds with no errors. (`net.Listen("tcp", "127.0.0.1:"+cfg.NotifyPort)` is what actually enforces "only accessible from localhost" — binding to a loopback address, not `0.0.0.0`, means no other machine on the LAN can reach this port at all, regardless of firewall rules.)

- [ ] **Step 3: Run the full `projects-svc` test suite**

```
go test ./projects-svc/...
```
Expected: PASS across all packages.

- [ ] **Step 4: Commit**

```bash
git add projects-svc/main.go
git commit -m "feat(projects-svc): wire main.go — two listeners, startup dispatch

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0145DzrC2W5CLksBESBsJpaA"
```

---

## Task 9: `web-svc` config wiring for `ProjectsSvcURL`

**Files:**
- Modify: `web-svc/config/config.go`
- Modify: `web-svc/config/config_test.go`

**Interfaces:**
- Consumes: `sharedconfig.WebConfig.ProjectsSvcURL` (Task 1).
- Produces (consumed by Task 10): `config.Config.ProjectsSvcURL string`.

- [ ] **Step 1: Extend the shared fixture and add test cases**

Open `web-svc/config/config_test.go`. In the `validConfigJSON` constant, add `"projects_svc_url": "http://localhost:9016"` alongside the existing `"action_svc_url"` line:

```go
const validConfigJSON = `{
  "web": {
    "owner_email": "breynisson@gmail.com",
    "cors_allowed_origin": "http://localhost:5178",
    "perception_svc_url": "http://localhost:9011",
    "memory_svc_url": "http://localhost:9012",
    "thinking_svc_url": "http://localhost:9013",
    "action_svc_url": "http://localhost:9014",
    "projects_svc_url": "http://localhost:9016",
    "obsidian_root": "C:\\Users\\Lenovo\\Documents\\obsidian",
    "claude_project_roots": [
      {"label": "Obsidian", "path": "C:\\Users\\Lenovo\\Documents\\obsidian"}
    ],
    "file_browser_roots": [
      {"label": "Documents", "path": "C:\\Users\\Lenovo\\Documents"}
    ]
  }
}`
```

In `TestLoad_PopulatesDownstreamURLsAndCORSOrigin`, add, alongside the existing `ActionSvcURL` assertion:
```go
if cfg.ProjectsSvcURL != "http://localhost:9016" {
    t.Errorf("ProjectsSvcURL = %q", cfg.ProjectsSvcURL)
}
```

Add a new test, matching `TestLoad_MissingDownstreamURL_ReturnsError`'s exact shape (an inline incomplete JSON with every other required field present):
```go
func TestLoad_MissingProjectsSvcURL_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	incomplete := `{"web": {"owner_email": "breynisson@gmail.com", "cors_allowed_origin": "http://localhost:5178", "perception_svc_url": "http://localhost:9011", "memory_svc_url": "http://localhost:9012", "thinking_svc_url": "http://localhost:9013", "action_svc_url": "http://localhost:9014", "projects_svc_url": "", "obsidian_root": "C:\\Users\\Lenovo\\Documents\\obsidian", "claude_project_roots": [{"label": "Obsidian", "path": "C:\\Users\\Lenovo\\Documents\\obsidian"}], "file_browser_roots": [{"label": "Documents", "path": "C:\\Users\\Lenovo\\Documents"}]}}`
	path := writeConfigFile(t, dir, incomplete)
	os.Setenv("CONFIG_PATH", path)
	os.Setenv("SUPABASE_URL", "https://example.supabase.co")
	os.Setenv("SUPABASE_JWT_SECRET", "shh")
	defer os.Unsetenv("CONFIG_PATH")
	defer os.Unsetenv("SUPABASE_URL")
	defer os.Unsetenv("SUPABASE_JWT_SECRET")

	if _, err := config.Load(); err == nil {
		t.Fatal("Load() error = nil, want an error when web.projects_svc_url is blank")
	}
}
```

Note: `TestLoad_MissingDownstreamURL_ReturnsError`'s own inline `incomplete` JSON (existing code, not modified by this task) omits `obsidian_root`/`claude_project_roots`/`file_browser_roots` entirely and still expects an error — that's fine, since it's blank on `memory_svc_url` which is checked first in `Load()`. The new test above blanks only `projects_svc_url` and supplies every other required field, to isolate that this specific check fires.

- [ ] **Step 2: Run the tests, verify they fail**

```
go test ./web-svc/config/... -v
```
Expected: FAIL (field doesn't exist yet / fatal-if-empty check missing).

- [ ] **Step 3: Add the field and validation to `config.go`**

In the `Config` struct, add `ProjectsSvcURL string` alongside the existing `ActionSvcURL string`.

In `Load()`, add, alongside the existing `if shared.Web.ActionSvcURL == ""` block:
```go
if shared.Web.ProjectsSvcURL == "" {
    return nil, fmt.Errorf("shared config %s has no web.projects_svc_url configured", configPath)
}
```

In the returned `&Config{...}` literal, add:
```go
ProjectsSvcURL: shared.Web.ProjectsSvcURL,
```

- [ ] **Step 4: Run the tests, verify they pass**

```
go test ./web-svc/config/... -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web-svc/config/config.go web-svc/config/config_test.go
git commit -m "feat(web-svc): read projects_svc_url from shared config

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0145DzrC2W5CLksBESBsJpaA"
```

---

## Task 10: `web-svc` proxy routes for `/api/projects/**`

**Files:**
- Create: `web-svc/httpserver/projects_proxy.go`
- Create: `web-svc/httpserver/projects_proxy_test.go`
- Modify: `web-svc/httpserver/server.go`
- Modify: `web-svc/main.go`

**Interfaces:**
- Consumes: `Config.ProjectsSvcURL` (extend `httpserver.Config`, mirroring the existing `PerceptionSvcURL` field), `config.Config.ProjectsSvcURL` (Task 9).

- [ ] **Step 1: Write `projects_proxy_test.go`**

```go
package httpserver_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"soulman/web-svc/auth"
	"soulman/web-svc/httpserver"
)

func TestAPIProjects_ProxiesListToProjectsSvc(t *testing.T) {
	var gotMethod, gotPath string
	projectsSvc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	}))
	defer projectsSvc.Close()

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ProjectsSvcURL: projectsSvc.URL}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/projects", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if gotMethod != http.MethodGet || gotPath != "/projects" {
		t.Errorf("proxied request = %s %s, want GET /projects", gotMethod, gotPath)
	}
}

func TestAPIProjects_ProxiesCreateWithBodyAndMethod(t *testing.T) {
	var gotMethod string
	var gotBody []byte
	projectsSvc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer projectsSvc.Close()

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ProjectsSvcURL: projectsSvc.URL}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	wantBody := `{"name":"demo","path":"C:\\demo"}`
	req := httptest.NewRequest(http.MethodPost, "/api/projects/projects", strings.NewReader(wantBody))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	if gotMethod != http.MethodPost {
		t.Errorf("proxied method = %s, want POST", gotMethod)
	}
	if string(gotBody) != wantBody {
		t.Errorf("proxied body = %s, want %s", gotBody, wantBody)
	}
}

func TestAPIProjects_DeleteForwardsURLParam(t *testing.T) {
	var gotMethod, gotPath string
	projectsSvc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer projectsSvc.Close()

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ProjectsSvcURL: projectsSvc.URL}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodDelete, "/api/projects/projects/demo", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", rec.Code, rec.Body.String())
	}
	if gotMethod != http.MethodDelete || gotPath != "/projects/demo" {
		t.Errorf("proxied request = %s %s, want DELETE /projects/demo", gotMethod, gotPath)
	}
}

func TestAPIProjects_ProjectsSvcDown_Returns502(t *testing.T) {
	projectsSvc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	projectsSvc.Close() // closed immediately: connection refused, simulating "down"

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ProjectsSvcURL: projectsSvc.URL}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/projects", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestAPIProjects_NoToken_Returns401(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178"}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/projects", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
```

- [ ] **Step 2: Run the tests, verify they fail (routes/field don't exist yet)**

```
go test ./web-svc/httpserver/... -run TestAPIProjects -v
```
Expected: FAIL (build error — `Config.ProjectsSvcURL` doesn't exist).

- [ ] **Step 3: Add `ProjectsSvcURL` to `httpserver.Config`**

In `web-svc/httpserver/server.go`'s `Config` struct, add `ProjectsSvcURL string` alongside `ActionSvcURL`.

- [ ] **Step 4: Implement `projects_proxy.go`**

```go
// projects_proxy.go forwards /api/projects/** to projects-svc's main
// port, preserving method and body. Unlike proxyGet (proxy.go, GET-only,
// query-string-only), this generalized proxy is needed because the
// projects CRUD routes span GET/POST/PUT/DELETE with JSON request
// bodies and path parameters. See
// docs/superpowers/specs/2026-09-04-projects-tool-design.md's "Backend
// API & queue orchestration" section — the /notify endpoint is
// deliberately NOT proxied here at all; it's reached directly on
// projects-svc's loopback-only listener.
package httpserver

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// proxyProjects forwards the incoming request to
// cfg.ProjectsSvcURL+upstreamPath(r), preserving method and body, and
// streams the response back verbatim. A non-2xx/network-error upstream
// response becomes a 502, matching proxyGet's convention.
func (s *Server) proxyProjects(upstreamPath func(r *http.Request) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		url := s.cfg.ProjectsSvcURL + upstreamPath(r)

		req, err := http.NewRequestWithContext(ctx, r.Method, url, r.Body)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		req.Header.Set("Content-Type", "application/json")

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

func projectsPath(r *http.Request) string    { return "/projects" }
func projectByName(r *http.Request) string   { return "/projects/" + chi.URLParam(r, "name") }
func promptsPath(r *http.Request) string     { return "/prompts" }
func promptByID(r *http.Request) string      { return "/prompts/" + chi.URLParam(r, "id") }
```

- [ ] **Step 5: Wire the routes into `server.go`'s `buildRouter`**

Add, inside the existing `r.Group(func(r chi.Router) { r.Use(s.verifier.Middleware) ... })` block, alongside the other authed routes:
```go
r.Get("/api/projects/projects", s.proxyProjects(projectsPath))
r.Post("/api/projects/projects", s.proxyProjects(projectsPath))
r.Put("/api/projects/projects/{name}", s.proxyProjects(projectByName))
r.Delete("/api/projects/projects/{name}", s.proxyProjects(projectByName))
r.Get("/api/projects/prompts", s.proxyProjects(promptsPath))
r.Post("/api/projects/prompts", s.proxyProjects(promptsPath))
r.Put("/api/projects/prompts/{id}", s.proxyProjects(promptByID))
```

- [ ] **Step 6: Wire `ProjectsSvcURL` through `web-svc/main.go`**

In the `httpserver.New(cfg.HTTPPort, httpserver.Config{...}, verifier)` call, add, alongside `ActionSvcURL: cfg.ActionSvcURL,`:
```go
ProjectsSvcURL: cfg.ProjectsSvcURL,
```

- [ ] **Step 7: Run the tests, verify they pass**

```
go test ./web-svc/... -v
```
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add web-svc/httpserver/projects_proxy.go web-svc/httpserver/projects_proxy_test.go web-svc/httpserver/server.go web-svc/main.go
git commit -m "feat(web-svc): proxy /api/projects/** to projects-svc

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0145DzrC2W5CLksBESBsJpaA"
```

---

## Task 11: Frontend — `api.ts` additions

**Files:**
- Modify: `web/src/api.ts`

**Interfaces:**
- Produces (consumed by Task 12): typed `Project`, `Prompt` interfaces and `getProjects`, `createProject`, `updateProject`, `deleteProject`, `getPrompts`, `createPrompt`, `updatePromptState` functions.

- [ ] **Step 1: Extend `mutateJSON` to support DELETE (no body)**

`mutateJSON` currently only accepts `'POST' | 'PUT'` and always sends a JSON body. Add a small sibling for method-only requests (used by `deleteProject`, which needs no body):

```ts
async function deleteRequest(path: string, token: string | null): Promise<void> {
  const response = await fetch(path, {
    method: 'DELETE',
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  });
  if (!response.ok) {
    throw new ApiError(response.status, `${path} failed (${response.status})`);
  }
}
```

- [ ] **Step 2: Add the Projects types and functions at the end of the file**

```ts
export interface Project {
  name: string;
  path: string;
}

export interface Prompt {
  id: number;
  project_name: string;
  task_name: string;
  prompt_text: string;
  state: 'NOT_STARTED' | 'CREATING_SPEC' | 'IMPLEMENTING' | 'DONE';
  last_launch_error?: string;
  created_at: string;
}

export const getProjects = (token: string | null): Promise<Project[]> =>
  getJSON('/api/projects/projects', token);

export const createProject = (token: string | null, name: string, path: string): Promise<void> =>
  mutateJSON('POST', '/api/projects/projects', token, { name, path });

export const updateProject = (token: string | null, name: string, path: string): Promise<void> =>
  mutateJSON('PUT', `/api/projects/projects/${encodeURIComponent(name)}`, token, { path });

export const deleteProject = (token: string | null, name: string): Promise<void> =>
  deleteRequest(`/api/projects/projects/${encodeURIComponent(name)}`, token);

export const getPrompts = (token: string | null): Promise<Prompt[]> =>
  getJSON('/api/projects/prompts', token);

export const createPrompt = (
  token: string | null,
  projectName: string,
  taskName: string,
  promptText: string,
): Promise<void> =>
  mutateJSON('POST', '/api/projects/prompts', token, {
    project_name: projectName,
    task_name: taskName,
    prompt_text: promptText,
  });

export const updatePromptState = (
  token: string | null,
  id: number,
  state: Prompt['state'],
): Promise<void> => mutateJSON('PUT', `/api/projects/prompts/${id}`, token, { state });
```

- [ ] **Step 3: Type-check**

```
cd web
npx tsc --noEmit
```
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add web/src/api.ts
git commit -m "feat(web): add Projects API client functions

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0145DzrC2W5CLksBESBsJpaA"
```

---

## Task 12: Frontend — `ProjectsPage`, `ProjectsPanel`, `PromptsPanel`

**Files:**
- Create: `web/src/components/ProjectsPage.tsx`
- Create: `web/src/components/ProjectsPanel.tsx`
- Create: `web/src/components/PromptsPanel.tsx`

**Interfaces:**
- Consumes: `Project`, `Prompt`, and the CRUD functions from Task 11.
- Produces (consumed by Task 13): `ProjectsPage({ onBack: () => void })`.

- [ ] **Step 1: Implement `ProjectsPanel.tsx`**

```tsx
import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { getProjects, createProject, deleteProject, ApiError, type Project } from '../api';

export function ProjectsPanel() {
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [name, setName] = useState('');
  const [path, setPath] = useState('');

  async function refresh() {
    const token = await getAccessToken();
    try {
      const data = await getProjects(token);
      setProjects(data);
      setError(null);
    } catch {
      setError('Projects unavailable');
    }
  }

  useEffect(() => {
    refresh();
  }, []);

  async function handleAdd() {
    if (!name || !path) return;
    const token = await getAccessToken();
    try {
      await createProject(token, name, path);
      setName('');
      setPath('');
      await refresh();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to add project');
    }
  }

  async function handleDelete(projectName: string) {
    const token = await getAccessToken();
    try {
      await deleteProject(token, projectName);
      await refresh();
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        setError(`Cannot delete "${projectName}": it still has prompts`);
      } else {
        setError('Failed to delete project');
      }
    }
  }

  return (
    <div className="rounded border border-gray-200 bg-white p-4">
      <h2 className="mb-3 text-lg font-semibold">Projects</h2>
      {error && <p className="mb-2 text-sm text-red-600">{error}</p>}
      {projects === null && <p className="text-sm text-gray-500">Loading...</p>}
      {projects && (
        <table className="mb-4 w-full text-sm">
          <thead>
            <tr className="text-left text-gray-500">
              <th className="pb-1">Name</th>
              <th className="pb-1">Path</th>
              <th className="pb-1"></th>
            </tr>
          </thead>
          <tbody>
            {projects.map((p) => (
              <tr key={p.name} className="border-t border-gray-100">
                <td className="py-1 font-medium">{p.name}</td>
                <td className="py-1 text-gray-600">{p.path}</td>
                <td className="py-1 text-right">
                  <button onClick={() => handleDelete(p.name)} className="text-xs text-red-600 underline">
                    Delete
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <div className="flex gap-2">
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Project name"
          className="flex-1 rounded border border-gray-300 px-2 py-1 text-sm"
        />
        <input
          value={path}
          onChange={(e) => setPath(e.target.value)}
          placeholder="Path"
          className="flex-1 rounded border border-gray-300 px-2 py-1 text-sm"
        />
        <button onClick={handleAdd} className="rounded bg-gray-800 px-3 py-1 text-sm text-white">
          Add
        </button>
      </div>
    </div>
  );
}
```

- [ ] **Step 2: Implement `PromptsPanel.tsx`**

```tsx
import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import {
  getPrompts,
  createPrompt,
  updatePromptState,
  getProjects,
  ApiError,
  type Prompt,
  type Project,
} from '../api';

const STATES: Prompt['state'][] = ['NOT_STARTED', 'CREATING_SPEC', 'IMPLEMENTING', 'DONE'];

export function PromptsPanel() {
  const [prompts, setPrompts] = useState<Prompt[] | null>(null);
  const [projects, setProjects] = useState<Project[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [projectName, setProjectName] = useState('');
  const [taskName, setTaskName] = useState('');
  const [promptText, setPromptText] = useState('');

  async function refresh() {
    const token = await getAccessToken();
    try {
      const [promptData, projectData] = await Promise.all([getPrompts(token), getProjects(token)]);
      setPrompts(promptData);
      setProjects(projectData);
      setError(null);
    } catch {
      setError('Prompts unavailable');
    }
  }

  useEffect(() => {
    refresh();
  }, []);

  async function handleAdd() {
    if (!projectName || !taskName || !promptText) return;
    const token = await getAccessToken();
    try {
      await createPrompt(token, projectName, taskName, promptText);
      setTaskName('');
      setPromptText('');
      await refresh();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to add prompt');
    }
  }

  async function handleStateChange(id: number, state: Prompt['state']) {
    const token = await getAccessToken();
    try {
      await updatePromptState(token, id, state);
      await refresh();
    } catch {
      setError('Failed to update state');
    }
  }

  return (
    <div className="rounded border border-gray-200 bg-white p-4">
      <div className="mb-3 flex items-center justify-between">
        <h2 className="text-lg font-semibold">Prompts</h2>
        <button onClick={refresh} className="text-xs text-gray-500 underline">
          Refresh
        </button>
      </div>
      {error && <p className="mb-2 text-sm text-red-600">{error}</p>}
      {prompts === null && <p className="text-sm text-gray-500">Loading...</p>}
      {prompts && (
        <table className="mb-4 w-full text-sm">
          <thead>
            <tr className="text-left text-gray-500">
              <th className="pb-1">Project</th>
              <th className="pb-1">Task</th>
              <th className="pb-1">State</th>
              <th className="pb-1">Created</th>
            </tr>
          </thead>
          <tbody>
            {prompts.map((p) => (
              <tr key={p.id} className="border-t border-gray-100">
                <td className="py-1">{p.project_name}</td>
                <td className="py-1">{p.task_name}</td>
                <td className="py-1">
                  <select
                    value={p.state}
                    onChange={(e) => handleStateChange(p.id, e.target.value as Prompt['state'])}
                    className="rounded border border-gray-300 px-1 py-0.5 text-xs"
                  >
                    {STATES.map((s) => (
                      <option key={s} value={s}>
                        {s}
                      </option>
                    ))}
                  </select>
                  {p.last_launch_error && (
                    <p className="mt-1 text-xs text-red-600">{p.last_launch_error}</p>
                  )}
                </td>
                <td className="py-1 text-gray-500">{new Date(p.created_at).toLocaleString()}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <div className="flex flex-col gap-2">
        <select
          value={projectName}
          onChange={(e) => setProjectName(e.target.value)}
          className="rounded border border-gray-300 px-2 py-1 text-sm"
        >
          <option value="">Select a project</option>
          {projects.map((p) => (
            <option key={p.name} value={p.name}>
              {p.name}
            </option>
          ))}
        </select>
        <input
          value={taskName}
          onChange={(e) => setTaskName(e.target.value)}
          placeholder="Task name"
          className="rounded border border-gray-300 px-2 py-1 text-sm"
        />
        <textarea
          value={promptText}
          onChange={(e) => setPromptText(e.target.value)}
          placeholder="Prompt text"
          rows={3}
          className="rounded border border-gray-300 px-2 py-1 text-sm"
        />
        <button onClick={handleAdd} className="self-start rounded bg-gray-800 px-3 py-1 text-sm text-white">
          Add Prompt
        </button>
      </div>
    </div>
  );
}
```

- [ ] **Step 3: Implement `ProjectsPage.tsx`**

```tsx
import { ProjectsPanel } from './ProjectsPanel';
import { PromptsPanel } from './PromptsPanel';

export function ProjectsPage({ onBack }: { onBack: () => void }) {
  return (
    <div className="min-h-screen bg-gray-50 p-6">
      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Projects</h1>
        <button onClick={onBack} className="text-sm text-gray-500 underline">
          ← Soulman
        </button>
      </div>
      <div className="grid grid-cols-1 gap-6 md:grid-cols-2">
        <ProjectsPanel />
        <PromptsPanel />
      </div>
    </div>
  );
}
```

- [ ] **Step 4: Type-check**

```
cd web
npx tsc --noEmit
```
Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/ProjectsPage.tsx web/src/components/ProjectsPanel.tsx web/src/components/PromptsPanel.tsx
git commit -m "feat(web): add Projects page, projects panel, prompts panel

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0145DzrC2W5CLksBESBsJpaA"
```

---

## Task 13: Frontend — wire into `App.tsx` and `Dashboard.tsx`

**Files:**
- Modify: `web/src/App.tsx`
- Modify: `web/src/components/Dashboard.tsx`

**Interfaces:**
- Consumes: `ProjectsPage` (Task 12).

- [ ] **Step 1: Extend `App.tsx`**

In `web/src/App.tsx`:
- Add the import: `import { ProjectsPage } from './components/ProjectsPage';`
- Extend the `ViewState` union: `'loading' | 'login' | 'restricted' | 'dashboard' | 'obsidian' | 'claude' | 'files' | 'search' | 'projects'`
- In `viewFromPageParam()`, add: `if (page === 'projects') return 'projects';`
- After the existing `if (view === 'search') { ... }` block, add:
  ```tsx
  if (view === 'projects') {
    return (
      <ProjectsPage
        onBack={() => {
          setParams({ page: null });
          setView('dashboard');
        }}
      />
    );
  }
  ```
- In the `<Dashboard ... />` render, add a new prop:
  ```tsx
  onOpenProjects={() => {
    setParams({ page: 'projects' });
    setView('projects');
  }}
  ```

- [ ] **Step 2: Extend `Dashboard.tsx`**

In `web/src/components/Dashboard.tsx`:
- Add `onOpenProjects: () => void;` to the props destructuring and its type block, alongside `onOpenClaude`.
- Add a new nav button, placed immediately left of the existing Claude button:
  ```tsx
  <button onClick={onOpenProjects} className="text-sm text-gray-500 underline">
    Projects
  </button>
  ```

- [ ] **Step 3: Type-check**

```
cd web
npx tsc --noEmit
```
Expected: no errors.

- [ ] **Step 4: Manual verification**

With dev `web-svc`/`projects-svc`/`web` running (after this branch merges and is deployed — see the plan's Global Constraints on deployment being out of scope for these tasks), open the dashboard, click "Projects", confirm both panels load, add a project, add a prompt against it, and confirm a `claude --remote-control --bg` process starts (Task Manager) with the correct working directory, and that the prompt's state moves to `CREATING_SPEC`. This step is a note for the coordinating session's post-merge deployment/verification, not something a task-implementer subagent can do on its own (no running services in that context).

- [ ] **Step 5: Commit**

```bash
git add web/src/App.tsx web/src/components/Dashboard.tsx
git commit -m "feat(web): add Projects nav link to dashboard

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_0145DzrC2W5CLksBESBsJpaA"
```
