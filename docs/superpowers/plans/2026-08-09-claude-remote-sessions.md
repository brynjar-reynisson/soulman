# Claude Remote Sessions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a "Claude" link to the Soulman web dashboard that lists project folders from three curated roots and lets the user launch a `claude --remote-control --name "<session-name>"` session against one, as a detached background process on the machine `web-svc` runs on.

**Architecture:** A new `web-svc/claudesession` Go package (mirroring `web-svc/obsidian`) does folder listing and process launching against a configured list of `{label, path}` roots; thin `httpserver` handlers expose it as two JWT-authed routes; a new React page (`ClaudeRootList` / `ClaudeLaunchForm`) reachable via a nav button left of "Obsidian" drives it.

**Tech Stack:** Go 1.25 (`os/exec`, `syscall` on Windows) for `web-svc`; React 19 + TypeScript + Vitest/Testing Library for `web`.

## Global Constraints

- Spec: `docs/superpowers/specs/2026-08-09-claude-remote-sessions-design.md` — read it first if anything below is ambiguous.
- Three curated roots, exact labels and paths (identical in both `config/dev.json` and `config/prod.json`):
  - `{"label": "Obsidian", "path": "C:\\Users\\Lenovo\\Documents\\obsidian"}`
  - `{"label": "IdeaProjects", "path": "C:\\Users\\Lenovo\\IdeaProjects"}`
  - `{"label": "Misc Projects", "path": "C:\\Users\\Lenovo\\misc_projects"}`
- A root missing from disk is reported via `exists: false` in the API response — never a startup error, never hidden from the UI.
- No session tracking, no stdout/stderr capture, no stopping sessions from the dashboard. `Launch` calls `cmd.Process.Release()` immediately after a successful `Start()`.
- The client identifies a root by its `label` string; the server validates it against configured roots.
- **Never write an automated test that lets `claudesession.Launch` (or the `/api/claude/launch` handler) reach a valid folder + valid session name at the same time** — that combination calls `exec.Command("claude", "--remote-control", ...).Start()` for real, which would spawn an actual remote-control session on whatever machine runs the test suite. Every test must stop at an earlier validation error (empty name, invalid folder segment, or missing folder).
- Git branch prefix: `feature/`. Commit messages follow this repo's existing `type(scope): summary` convention (see recent commits for examples).

---

## Task 1: Shared config schema — `ClaudeProjectRoot` type and JSON config files

**Files:**
- Modify: `common/sharedconfig/config.go`
- Modify: `config/dev.json`
- Modify: `config/prod.json`

**Interfaces:**
- Produces: `sharedconfig.ClaudeProjectRoot{Label, Path string}` (JSON tags `label`, `path`); `sharedconfig.WebConfig.ClaudeProjectRoots []ClaudeProjectRoot` (JSON tag `claude_project_roots`).

This task has no Go tests of its own (a struct field + two JSON files) — Task 2 exercises it via `sharedconfig.Load`/`config.Load`.

- [ ] **Step 1: Add the `ClaudeProjectRoot` type and `WebConfig` field**

In `common/sharedconfig/config.go`, add the new type just above `WebConfig` and add the field to `WebConfig`:

```go
// ClaudeProjectRoot is one curated project-folder root the Claude
// remote-session launcher (web-svc/claudesession) offers: a
// human-readable label (matched against a launch request's "root"
// field) and the filesystem path it corresponds to. See
// docs/superpowers/specs/2026-08-09-claude-remote-sessions-design.md.
type ClaudeProjectRoot struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

// WebConfig holds web-svc's settings: the single owner email allowed full
// dashboard access, the frontend origin CORS must allow, and the base URLs
// of the four services web-svc calls into. Unlike GmailConfig/
// SystemMonitorConfig, every field here is required — web-svc has no
// degraded "partially configured" mode. ClaudeProjectRoots is required to
// be non-empty, but unlike ObsidianRoot its entries' Path values are not
// required to exist on disk at startup — a missing root is reported as
// such per-request instead (see web-svc/claudesession.ListRoots).
type WebConfig struct {
	OwnerEmail         string              `json:"owner_email"`
	CORSAllowedOrigin  string              `json:"cors_allowed_origin"`
	PerceptionSvcURL   string              `json:"perception_svc_url"`
	MemorySvcURL       string              `json:"memory_svc_url"`
	ThinkingSvcURL     string              `json:"thinking_svc_url"`
	ActionSvcURL       string              `json:"action_svc_url"`
	ObsidianRoot       string              `json:"obsidian_root"`
	ClaudeProjectRoots []ClaudeProjectRoot `json:"claude_project_roots"`
}
```

- [ ] **Step 2: Add `claude_project_roots` to `config/dev.json` and `config/prod.json`**

In both files, add this key to the existing `"web"` block, right after `"obsidian_root"`:

```json
    "claude_project_roots": [
      { "label": "Obsidian", "path": "C:\\Users\\Lenovo\\Documents\\obsidian" },
      { "label": "IdeaProjects", "path": "C:\\Users\\Lenovo\\IdeaProjects" },
      { "label": "Misc Projects", "path": "C:\\Users\\Lenovo\\misc_projects" }
    ]
```

So `config/dev.json`'s `"web"` block reads:

```json
  "web": {
    "owner_email": "breynisson@gmail.com",
    "cors_allowed_origin": "http://localhost:5190",
    "perception_svc_url": "http://localhost:9011",
    "memory_svc_url": "http://localhost:9012",
    "thinking_svc_url": "http://localhost:9013",
    "action_svc_url": "http://localhost:9014",
    "obsidian_root": "C:\\Users\\Lenovo\\Documents\\obsidian",
    "claude_project_roots": [
      { "label": "Obsidian", "path": "C:\\Users\\Lenovo\\Documents\\obsidian" },
      { "label": "IdeaProjects", "path": "C:\\Users\\Lenovo\\IdeaProjects" },
      { "label": "Misc Projects", "path": "C:\\Users\\Lenovo\\misc_projects" }
    ]
  }
```

and `config/prod.json`'s `"web"` block reads (same `claude_project_roots`, different `cors_allowed_origin`/service ports as already present):

```json
  "web": {
    "owner_email": "breynisson@gmail.com",
    "cors_allowed_origin": "https://soulman.breynisson.org",
    "perception_svc_url": "http://localhost:9001",
    "memory_svc_url": "http://localhost:9002",
    "thinking_svc_url": "http://localhost:9003",
    "action_svc_url": "http://localhost:9004",
    "obsidian_root": "C:\\Users\\Lenovo\\Documents\\obsidian",
    "claude_project_roots": [
      { "label": "Obsidian", "path": "C:\\Users\\Lenovo\\Documents\\obsidian" },
      { "label": "IdeaProjects", "path": "C:\\Users\\Lenovo\\IdeaProjects" },
      { "label": "Misc Projects", "path": "C:\\Users\\Lenovo\\misc_projects" }
    ]
  }
```

- [ ] **Step 3: Build to confirm the struct change compiles**

Run: `go build ./...` from the repo root.
Expected: succeeds with no errors (this only adds a field and a type; nothing consumes it yet).

- [ ] **Step 4: Commit**

```bash
git add common/sharedconfig/config.go config/dev.json config/prod.json
git commit -m "feat(common): add claude_project_roots to shared web config schema"
```

---

## Task 2: `web-svc/config` — load and validate `claude_project_roots`

**Files:**
- Modify: `web-svc/config/config.go`
- Modify: `web-svc/config/config_test.go`

**Interfaces:**
- Consumes: `sharedconfig.WebConfig.ClaudeProjectRoots []sharedconfig.ClaudeProjectRoot` (Task 1).
- Produces: `config.Config.ClaudeProjectRoots []sharedconfig.ClaudeProjectRoot`, populated by `config.Load()`; `config.Load()` now also returns an error when `web.claude_project_roots` is empty.

- [ ] **Step 1: Update `validConfigJSON` and add the new tests (failing first)**

In `web-svc/config/config_test.go`, update the shared `validConfigJSON` constant to include `claude_project_roots` (every existing passing-path test uses this constant, so this must be updated in the same step as the new tests, not separately):

```go
const validConfigJSON = `{
  "web": {
    "owner_email": "breynisson@gmail.com",
    "cors_allowed_origin": "http://localhost:5178",
    "perception_svc_url": "http://localhost:9011",
    "memory_svc_url": "http://localhost:9012",
    "thinking_svc_url": "http://localhost:9013",
    "action_svc_url": "http://localhost:9014",
    "obsidian_root": "C:\\Users\\Lenovo\\Documents\\obsidian",
    "claude_project_roots": [
      {"label": "Obsidian", "path": "C:\\Users\\Lenovo\\Documents\\obsidian"}
    ]
  }
}`
```

Add these two new test functions at the end of the file:

```go
func TestLoad_PopulatesClaudeProjectRoots(t *testing.T) {
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
	if len(cfg.ClaudeProjectRoots) != 1 {
		t.Fatalf("len(ClaudeProjectRoots) = %d, want 1", len(cfg.ClaudeProjectRoots))
	}
	if cfg.ClaudeProjectRoots[0].Label != "Obsidian" || cfg.ClaudeProjectRoots[0].Path != `C:\Users\Lenovo\Documents\obsidian` {
		t.Errorf("ClaudeProjectRoots[0] = %+v", cfg.ClaudeProjectRoots[0])
	}
}

func TestLoad_MissingClaudeProjectRoots_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	incomplete := `{"web": {"owner_email": "breynisson@gmail.com", "cors_allowed_origin": "http://localhost:5178", "perception_svc_url": "http://localhost:9011", "memory_svc_url": "http://localhost:9012", "thinking_svc_url": "http://localhost:9013", "action_svc_url": "http://localhost:9014", "obsidian_root": "C:\\Users\\Lenovo\\Documents\\obsidian", "claude_project_roots": []}}`
	path := writeConfigFile(t, dir, incomplete)
	os.Setenv("CONFIG_PATH", path)
	os.Setenv("SUPABASE_URL", "https://example.supabase.co")
	os.Setenv("SUPABASE_JWT_SECRET", "shh")
	defer os.Unsetenv("CONFIG_PATH")
	defer os.Unsetenv("SUPABASE_URL")
	defer os.Unsetenv("SUPABASE_JWT_SECRET")

	if _, err := config.Load(); err == nil {
		t.Fatal("Load() error = nil, want an error when web.claude_project_roots is empty")
	}
}
```

- [ ] **Step 2: Run the new tests to verify they fail**

Run: `go test ./web-svc/config/... -run 'TestLoad_PopulatesClaudeProjectRoots|TestLoad_MissingClaudeProjectRoots_ReturnsError' -v`
Expected: `TestLoad_PopulatesClaudeProjectRoots` fails (`cfg.ClaudeProjectRoots` doesn't exist yet — compile error) and/or `TestLoad_MissingClaudeProjectRoots_ReturnsError` fails (`Load()` returns no error today for a missing field it doesn't check).

- [ ] **Step 3: Implement the field and validation in `web-svc/config/config.go`**

Add the field to `Config`:

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
}
```

Add validation right after the existing `ObsidianRoot` check in `Load()`:

```go
	if shared.Web.ObsidianRoot == "" {
		return nil, fmt.Errorf("shared config %s has no web.obsidian_root configured", configPath)
	}
	if len(shared.Web.ClaudeProjectRoots) == 0 {
		return nil, fmt.Errorf("shared config %s has no web.claude_project_roots configured", configPath)
	}
```

Add the field to the returned `&Config{...}` literal, right after `ObsidianRoot`:

```go
		ObsidianRoot:       shared.Web.ObsidianRoot,
		ClaudeProjectRoots: shared.Web.ClaudeProjectRoots,
```

- [ ] **Step 4: Run all `config` package tests to verify they pass**

Run: `go test ./web-svc/config/... -v`
Expected: PASS for all tests, including the two new ones and every existing test that uses `validConfigJSON`.

- [ ] **Step 5: Commit**

```bash
git add web-svc/config/config.go web-svc/config/config_test.go
git commit -m "feat(web-svc): load and validate web.claude_project_roots"
```

---

## Task 3: `web-svc/claudesession` — `Root`, `RootListing`, `ListRoots`

**Files:**
- Create: `web-svc/claudesession/claudesession.go`
- Create: `web-svc/claudesession/claudesession_test.go`

**Interfaces:**
- Produces: `claudesession.Root{Label, Path string}`; `claudesession.RootListing{Label, Path string; Exists bool; Folders []string}`; `func ListRoots(roots []Root) []RootListing`.

- [ ] **Step 1: Write the failing tests**

Create `web-svc/claudesession/claudesession_test.go`:

```go
package claudesession

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListRoots_ExistingRootReturnsSortedFoldersOnly(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "zeta"), 0o755)
	os.Mkdir(filepath.Join(dir, "alpha"), 0o755)
	os.WriteFile(filepath.Join(dir, "not-a-folder.txt"), []byte("x"), 0o644)

	listings := ListRoots([]Root{{Label: "Test", Path: dir}})

	if len(listings) != 1 {
		t.Fatalf("len(listings) = %d, want 1", len(listings))
	}
	got := listings[0]
	if !got.Exists {
		t.Fatalf("Exists = false, want true")
	}
	want := []string{"alpha", "zeta"}
	if len(got.Folders) != len(want) || got.Folders[0] != want[0] || got.Folders[1] != want[1] {
		t.Errorf("Folders = %v, want %v", got.Folders, want)
	}
}

func TestListRoots_MissingRootReportsNotExists(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	listings := ListRoots([]Root{{Label: "Missing", Path: missing}})

	if len(listings) != 1 {
		t.Fatalf("len(listings) = %d, want 1", len(listings))
	}
	if listings[0].Exists {
		t.Errorf("Exists = true, want false")
	}
	if listings[0].Folders != nil {
		t.Errorf("Folders = %v, want nil", listings[0].Folders)
	}
}

func TestListRoots_PathIsAFile_ReportsNotExists(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "not-a-directory")
	os.WriteFile(filePath, []byte("x"), 0o644)

	listings := ListRoots([]Root{{Label: "Test", Path: filePath}})

	if listings[0].Exists {
		t.Errorf("Exists = true, want false for a root path that is a file")
	}
}

func TestListRoots_PreservesInputOrder(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()

	listings := ListRoots([]Root{{Label: "B", Path: b}, {Label: "A", Path: a}})

	if listings[0].Label != "B" || listings[1].Label != "A" {
		t.Errorf("listings = %+v, want order [B, A]", listings)
	}
}

func TestListRoots_EmptyRootDirectory_ReturnsEmptyNotNilFolders(t *testing.T) {
	dir := t.TempDir()

	listings := ListRoots([]Root{{Label: "Test", Path: dir}})

	if listings[0].Folders == nil {
		t.Fatalf("Folders = nil, want non-nil empty slice")
	}
	if len(listings[0].Folders) != 0 {
		t.Fatalf("Folders = %v, want empty", listings[0].Folders)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./web-svc/claudesession/... -v`
Expected: FAIL — the `claudesession` package doesn't exist yet (build error: `Root`/`ListRoots` undefined).

- [ ] **Step 3: Implement `web-svc/claudesession/claudesession.go`**

```go
// Package claudesession launches Claude Code "--remote-control" sessions
// rooted in a curated set of project folders (web.claude_project_roots).
// Fire-and-forget: once the process starts, this package no longer
// tracks it — session lifecycle is owned entirely by Claude Code's own
// remote-control registration (claude.ai/code). See
// docs/superpowers/specs/2026-08-09-claude-remote-sessions-design.md.
package claudesession

import (
	"errors"
	"os"
	"sort"
)

var (
	ErrNotFound     = errors.New("claudesession: not found")
	ErrInvalidName  = errors.New("claudesession: invalid name")
	ErrLaunchFailed = errors.New("claudesession: launch failed")
)

// Root identifies one curated project-folder root: a human-readable
// label (matched against a launch request's "root" field) and the
// filesystem path it corresponds to.
type Root struct {
	Label string
	Path  string
}

// RootListing is a Root plus its current filesystem state.
type RootListing struct {
	Label   string
	Path    string
	Exists  bool
	Folders []string
}

// ListRoots reports the current state of each configured root. A root
// whose Path doesn't exist (or isn't a directory) is reported with
// Exists: false and a nil Folders slice, rather than being omitted or
// treated as an error — a temporarily missing root should not take
// down the whole roots listing.
func ListRoots(roots []Root) []RootListing {
	listings := make([]RootListing, len(roots))
	for i, root := range roots {
		info, err := os.Stat(root.Path)
		if err != nil || !info.IsDir() {
			listings[i] = RootListing{Label: root.Label, Path: root.Path, Exists: false}
			continue
		}
		listings[i] = RootListing{Label: root.Label, Path: root.Path, Exists: true, Folders: listFolders(root.Path)}
	}
	return listings
}

func listFolders(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return []string{}
	}
	folders := []string{}
	for _, e := range entries {
		if e.IsDir() {
			folders = append(folders, e.Name())
		}
	}
	sort.Strings(folders)
	return folders
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./web-svc/claudesession/... -v`
Expected: PASS for all five tests.

- [ ] **Step 5: Commit**

```bash
git add web-svc/claudesession/claudesession.go web-svc/claudesession/claudesession_test.go
git commit -m "feat(web-svc): add claudesession.ListRoots"
```

---

## Task 4: `web-svc/claudesession` — `Launch` (path validation + detached spawn)

**Files:**
- Modify: `web-svc/claudesession/claudesession.go`
- Create: `web-svc/claudesession/claudesession_windows.go`
- Modify: `web-svc/claudesession/claudesession_test.go`

**Interfaces:**
- Consumes: `Root`, `ErrNotFound`, `ErrInvalidName`, `ErrLaunchFailed` (Task 3).
- Produces: `func Launch(root Root, folder, sessionName string) error`.

**Read the Global Constraints section above before writing this task's tests** — no test here may reach a successful `cmd.Start()`.

- [ ] **Step 1: Write the failing tests**

Append to `web-svc/claudesession/claudesession_test.go` (add `"errors"` to the existing `import` block):

```go
func TestResolveDir_InvalidFolderName_ReturnsErrInvalidName(t *testing.T) {
	root := Root{Label: "Test", Path: t.TempDir()}

	for _, folder := range []string{"..", "../etc", `a\b`, "a/b", ""} {
		_, err := resolveDir(root, folder)
		if !errors.Is(err, ErrInvalidName) {
			t.Errorf("resolveDir(%q) error = %v, want ErrInvalidName", folder, err)
		}
	}
}

func TestResolveDir_MissingFolder_ReturnsErrNotFound(t *testing.T) {
	root := Root{Label: "Test", Path: t.TempDir()}

	_, err := resolveDir(root, "does-not-exist")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("resolveDir() error = %v, want ErrNotFound", err)
	}
}

func TestResolveDir_FolderIsAFile_ReturnsErrNotFound(t *testing.T) {
	rootPath := t.TempDir()
	os.WriteFile(filepath.Join(rootPath, "not-a-folder"), []byte("x"), 0o644)
	root := Root{Label: "Test", Path: rootPath}

	_, err := resolveDir(root, "not-a-folder")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("resolveDir() error = %v, want ErrNotFound", err)
	}
}

func TestResolveDir_ValidFolder_ReturnsJoinedAbsolutePath(t *testing.T) {
	rootPath := t.TempDir()
	os.Mkdir(filepath.Join(rootPath, "myproject"), 0o755)
	root := Root{Label: "Test", Path: rootPath}

	dir, err := resolveDir(root, "myproject")
	if err != nil {
		t.Fatalf("resolveDir() error = %v", err)
	}
	want := filepath.Join(rootPath, "myproject")
	if dir != want {
		t.Errorf("dir = %q, want %q", dir, want)
	}
}

// Launch tests below deliberately stop at a validation error (empty
// name, invalid folder segment, or missing folder) — every one of these
// returns before Launch reaches exec.Command. A test that supplied a
// valid folder AND a valid sessionName together would actually spawn a
// real `claude --remote-control` process; that combination must never
// be exercised by an automated test. See this plan's Global Constraints.

func TestLaunch_EmptySessionName_ReturnsErrInvalidName(t *testing.T) {
	root := Root{Label: "Test", Path: t.TempDir()}

	err := Launch(root, "anything", "")
	if !errors.Is(err, ErrInvalidName) {
		t.Fatalf("Launch() error = %v, want ErrInvalidName", err)
	}
}

func TestLaunch_InvalidFolder_ReturnsErrInvalidName(t *testing.T) {
	root := Root{Label: "Test", Path: t.TempDir()}

	err := Launch(root, "../etc", "my-session")
	if !errors.Is(err, ErrInvalidName) {
		t.Fatalf("Launch() error = %v, want ErrInvalidName", err)
	}
}

func TestLaunch_MissingFolder_ReturnsErrNotFound(t *testing.T) {
	root := Root{Label: "Test", Path: t.TempDir()}

	err := Launch(root, "does-not-exist", "my-session")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Launch() error = %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./web-svc/claudesession/... -v`
Expected: FAIL — `resolveDir` and `Launch` are undefined.

- [ ] **Step 3: Implement `resolveDir` and `Launch` in `claudesession.go`**

Add these imports to the existing `import` block (now: `errors`, `fmt`, `os`, `os/exec`, `path/filepath`, `sort`, `strings`):

```go
import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)
```

Append to `claudesession.go`:

```go
// validSegment rejects anything that isn't a single, plain path
// component — see web-svc/obsidian's identical guard for the full
// rationale (path traversal and NTFS alternate-data-stream protection).
func validSegment(name string) bool {
	return name != "" && !strings.ContainsAny(name, `/\`) && name != "." && name != ".." && filepath.IsLocal(name)
}

func isWithin(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveDir validates folder as a single path segment directly under
// root.Path, confirms the joined path stays within root.Path, and
// confirms it exists and is a directory.
func resolveDir(root Root, folder string) (string, error) {
	if !validSegment(folder) {
		return "", ErrInvalidName
	}
	cleanRoot := filepath.Clean(root.Path)
	dir := filepath.Join(cleanRoot, folder)
	if !isWithin(cleanRoot, dir) {
		return "", ErrInvalidName
	}
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("claudesession: stat %s: %w", dir, err)
	}
	if !info.IsDir() {
		return "", ErrNotFound
	}
	return dir, nil
}

// Launch starts `claude --remote-control --name sessionName` detached,
// with its working directory set to root.Path/folder. It does not wait
// for the process, capture its output, or track it after Start()
// succeeds. sessionName is passed as a literal exec.Command argument
// (never through a shell), so it carries no injection risk regardless
// of its contents.
func Launch(root Root, folder, sessionName string) error {
	if sessionName == "" {
		return ErrInvalidName
	}
	dir, err := resolveDir(root, folder)
	if err != nil {
		return err
	}
	cmd := exec.Command("claude", "--remote-control", "--name", sessionName)
	cmd.Dir = dir
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%w: %v", ErrLaunchFailed, err)
	}
	cmd.Process.Release()
	return nil
}
```

- [ ] **Step 4: Create `web-svc/claudesession/claudesession_windows.go`**

```go
//go:build windows

package claudesession

import (
	"os/exec"
	"syscall"
)

// detach sets Windows-specific process creation flags so the spawned
// claude process is not tied to web-svc's own process group and
// survives a web-svc restart. Mirrors perception-svc/sysmonitor's
// existing convention of a *_windows.go file for OS-specific code —
// this repo's services only ever run on Windows, so no non-Windows
// counterpart is needed (see perception-svc/sysmonitor/stats_windows.go).
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./web-svc/claudesession/... -v`
Expected: PASS for all tests. Confirm with Task Manager (or `Get-Process claude -ErrorAction SilentlyContinue`) that no stray `claude` process was spawned by the test run.

- [ ] **Step 6: Commit**

```bash
git add web-svc/claudesession/claudesession.go web-svc/claudesession/claudesession_windows.go web-svc/claudesession/claudesession_test.go
git commit -m "feat(web-svc): add claudesession.Launch"
```

---

## Task 5: `httpserver` — `Config` field and `GET /api/claude/roots`

**Files:**
- Modify: `web-svc/httpserver/server.go`
- Create: `web-svc/httpserver/claude_handler.go`
- Create: `web-svc/httpserver/claude_handler_test.go`

**Interfaces:**
- Consumes: `claudesession.Root`, `claudesession.ListRoots` (Task 3); `writeJSON`, `writeJSONError` (existing, `web-svc/httpserver/server.go` and `obsidian_handler.go`).
- Produces: `httpserver.Config.ClaudeProjectRoots []claudesession.Root`; route `GET /api/claude/roots`.

- [ ] **Step 1: Write the failing tests**

Create `web-svc/httpserver/claude_handler_test.go`:

```go
package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"soulman/web-svc/auth"
	"soulman/web-svc/claudesession"
	"soulman/web-svc/httpserver"
)

func TestAPIClaudeRoots_ReturnsRootsWithExistenceAndFolders(t *testing.T) {
	existing := t.TempDir()
	os.Mkdir(filepath.Join(existing, "soulman"), 0o755)
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		ClaudeProjectRoots: []claudesession.Root{
			{Label: "Obsidian", Path: existing},
			{Label: "Misc Projects", Path: missing},
		},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/claude/roots", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Roots []struct {
			Label   string   `json:"label"`
			Exists  bool     `json:"exists"`
			Folders []string `json:"folders"`
		} `json:"roots"`
	}
	json.NewDecoder(rec.Body).Decode(&body)
	if len(body.Roots) != 2 {
		t.Fatalf("len(roots) = %d, want 2", len(body.Roots))
	}
	if body.Roots[0].Label != "Obsidian" || !body.Roots[0].Exists || len(body.Roots[0].Folders) != 1 || body.Roots[0].Folders[0] != "soulman" {
		t.Errorf("roots[0] = %+v, want Obsidian/exists/[soulman]", body.Roots[0])
	}
	if body.Roots[1].Label != "Misc Projects" || body.Roots[1].Exists {
		t.Errorf("roots[1] = %+v, want Misc Projects/not exists", body.Roots[1])
	}
}

func TestAPIClaudeRoots_NoToken_Returns401(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178"}
	srv := httpserver.New("9005", cfg, auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail))

	req := httptest.NewRequest(http.MethodGet, "/api/claude/roots", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./web-svc/httpserver/... -run TestAPIClaudeRoots -v`
Expected: FAIL — `httpserver.Config` has no `ClaudeProjectRoots` field, route doesn't exist (404/build error).

- [ ] **Step 3: Add the `Config` field and route in `server.go`**

Add the import and field:

```go
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
	"soulman/web-svc/claudesession"
)

type Config struct {
	CORSAllowedOrigin  string
	PerceptionSvcURL   string
	MemorySvcURL       string
	ThinkingSvcURL     string
	ActionSvcURL       string
	ReportsRoot        string
	ObsidianRoot       string
	ClaudeProjectRoots []claudesession.Root
}
```

Add the route inside the authed `r.Group`, right after the Obsidian routes:

```go
		r.Post("/api/obsidian/file/rename", s.obsidianFileRename)
		r.Get("/api/claude/roots", s.claudeRoots)
```

- [ ] **Step 4: Create `web-svc/httpserver/claude_handler.go`**

```go
package httpserver

import (
	"net/http"

	"soulman/web-svc/claudesession"
)

type claudeRootResponse struct {
	Label   string   `json:"label"`
	Path    string   `json:"path"`
	Exists  bool     `json:"exists"`
	Folders []string `json:"folders"`
}

func (s *Server) claudeRoots(w http.ResponseWriter, r *http.Request) {
	listings := claudesession.ListRoots(s.cfg.ClaudeProjectRoots)
	resp := make([]claudeRootResponse, len(listings))
	for i, l := range listings {
		resp[i] = claudeRootResponse{Label: l.Label, Path: l.Path, Exists: l.Exists, Folders: l.Folders}
	}
	writeJSON(w, http.StatusOK, map[string][]claudeRootResponse{"roots": resp})
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./web-svc/httpserver/... -v`
Expected: PASS for all tests, including the pre-existing ones (confirms the new `Config` field and route didn't break anything).

- [ ] **Step 6: Commit**

```bash
git add web-svc/httpserver/server.go web-svc/httpserver/claude_handler.go web-svc/httpserver/claude_handler_test.go
git commit -m "feat(web-svc): add GET /api/claude/roots"
```

---

## Task 6: `httpserver` — `POST /api/claude/launch`

**Files:**
- Modify: `web-svc/httpserver/server.go`
- Modify: `web-svc/httpserver/claude_handler.go`
- Modify: `web-svc/httpserver/claude_handler_test.go`

**Interfaces:**
- Consumes: `claudesession.Launch`, `claudesession.ErrNotFound`, `claudesession.ErrInvalidName`, `claudesession.ErrLaunchFailed` (Task 4).
- Produces: route `POST /api/claude/launch`.

**Every test in this task must stop at a validation error before `claudesession.Launch` reaches `exec.Command` — see the Global Constraints section.**

- [ ] **Step 1: Write the failing tests**

Append to `web-svc/httpserver/claude_handler_test.go` (add `"bytes"` to the `import` block):

```go
func TestAPIClaudeLaunch_UnknownRoot_Returns400(t *testing.T) {
	cfg := httpserver.Config{
		CORSAllowedOrigin:  "http://localhost:5178",
		ClaudeProjectRoots: []claudesession.Root{{Label: "Obsidian", Path: t.TempDir()}},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	reqBody, _ := json.Marshal(map[string]string{"root": "NotConfigured", "folder": "x", "sessionName": "x"})
	req := httptest.NewRequest(http.MethodPost, "/api/claude/launch", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPIClaudeLaunch_InvalidFolder_Returns400(t *testing.T) {
	cfg := httpserver.Config{
		CORSAllowedOrigin:  "http://localhost:5178",
		ClaudeProjectRoots: []claudesession.Root{{Label: "Obsidian", Path: t.TempDir()}},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	reqBody, _ := json.Marshal(map[string]string{"root": "Obsidian", "folder": "../etc", "sessionName": "x"})
	req := httptest.NewRequest(http.MethodPost, "/api/claude/launch", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPIClaudeLaunch_MissingFolder_Returns404(t *testing.T) {
	cfg := httpserver.Config{
		CORSAllowedOrigin:  "http://localhost:5178",
		ClaudeProjectRoots: []claudesession.Root{{Label: "Obsidian", Path: t.TempDir()}},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	reqBody, _ := json.Marshal(map[string]string{"root": "Obsidian", "folder": "does-not-exist", "sessionName": "x"})
	req := httptest.NewRequest(http.MethodPost, "/api/claude/launch", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPIClaudeLaunch_InvalidBody_Returns400(t *testing.T) {
	cfg := httpserver.Config{
		CORSAllowedOrigin:  "http://localhost:5178",
		ClaudeProjectRoots: []claudesession.Root{{Label: "Obsidian", Path: t.TempDir()}},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodPost, "/api/claude/launch", bytes.NewReader([]byte("not json")))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPIClaudeLaunch_NoToken_Returns401(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178"}
	srv := httpserver.New("9005", cfg, auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail))

	req := httptest.NewRequest(http.MethodPost, "/api/claude/launch", bytes.NewReader([]byte("{}")))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./web-svc/httpserver/... -run TestAPIClaudeLaunch -v`
Expected: FAIL — route doesn't exist yet (404 instead of the expected status codes).

- [ ] **Step 3: Add the route in `server.go`**

```go
		r.Get("/api/claude/roots", s.claudeRoots)
		r.Post("/api/claude/launch", s.claudeLaunch)
```

- [ ] **Step 4: Implement the handler in `claude_handler.go`**

Add these to `claude_handler.go` (add `"encoding/json"` and `"errors"` to the `import` block):

```go
import (
	"encoding/json"
	"errors"
	"net/http"

	"soulman/web-svc/claudesession"
)
```

```go
type claudeLaunchRequest struct {
	Root        string `json:"root"`
	Folder      string `json:"folder"`
	SessionName string `json:"sessionName"`
}

func (s *Server) claudeLaunch(w http.ResponseWriter, r *http.Request) {
	var req claudeLaunchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	root, ok := findClaudeRoot(s.cfg.ClaudeProjectRoots, req.Root)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "unknown root")
		return
	}
	if err := claudesession.Launch(root, req.Folder, req.SessionName); err != nil {
		writeClaudeSessionError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func findClaudeRoot(roots []claudesession.Root, label string) (claudesession.Root, bool) {
	for _, r := range roots {
		if r.Label == label {
			return r, true
		}
	}
	return claudesession.Root{}, false
}

func writeClaudeSessionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, claudesession.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "not found")
	case errors.Is(err, claudesession.ErrInvalidName):
		writeJSONError(w, http.StatusBadRequest, "invalid name")
	case errors.Is(err, claudesession.ErrLaunchFailed):
		writeJSONError(w, http.StatusInternalServerError, "launch failed")
	default:
		writeJSONError(w, http.StatusInternalServerError, "internal error")
	}
}
```

- [ ] **Step 5: Run all `httpserver` tests to verify they pass**

Run: `go test ./web-svc/httpserver/... -v`
Expected: PASS for every test. Confirm with Task Manager that no stray `claude` process appeared during the run.

- [ ] **Step 6: Commit**

```bash
git add web-svc/httpserver/server.go web-svc/httpserver/claude_handler.go web-svc/httpserver/claude_handler_test.go
git commit -m "feat(web-svc): add POST /api/claude/launch"
```

---

## Task 7: `main.go` wiring

**Files:**
- Modify: `web-svc/main.go`

**Interfaces:**
- Consumes: `config.Config.ClaudeProjectRoots []sharedconfig.ClaudeProjectRoot` (Task 2); `httpserver.Config.ClaudeProjectRoots []claudesession.Root` (Task 5); `claudesession.Root` (Task 3).

No new automated test — this is pure wiring between two already-tested layers, verified by a manual startup check in Step 3.

- [ ] **Step 1: Add the conversion and wire it into `httpserver.Config`**

```go
package main

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"soulman/web-svc/auth"
	"soulman/web-svc/claudesession"
	"soulman/web-svc/config"
	"soulman/web-svc/httpserver"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}

	verifier := auth.NewVerifier(cfg.SupabaseURL, cfg.SupabaseJWTSecret, cfg.OwnerEmail)

	claudeRoots := make([]claudesession.Root, len(cfg.ClaudeProjectRoots))
	for i, r := range cfg.ClaudeProjectRoots {
		claudeRoots[i] = claudesession.Root{Label: r.Label, Path: r.Path}
	}

	srv := httpserver.New(cfg.HTTPPort, httpserver.Config{
		CORSAllowedOrigin:  cfg.CORSAllowedOrigin,
		PerceptionSvcURL:   cfg.PerceptionSvcURL,
		MemorySvcURL:       cfg.MemorySvcURL,
		ThinkingSvcURL:     cfg.ThinkingSvcURL,
		ActionSvcURL:       cfg.ActionSvcURL,
		ReportsRoot:        cfg.SoulmanRoot,
		ObsidianRoot:       cfg.ObsidianRoot,
		ClaudeProjectRoots: claudeRoots,
	}, verifier)
```

(the rest of `main.go` — the goroutine starting `srv.Start()`, the `slog.Info` calls, and the signal-wait block — is unchanged)

- [ ] **Step 2: Build**

Run: `go build ./...` from the repo root.
Expected: succeeds with no errors.

- [ ] **Step 3: Manual smoke check**

This wiring can't be exercised by `go test` (it's `main`), so verify it by hand once dev `web-svc` is next rebuilt/restarted (via `start-everything.ps1` or `run-web-svc.ps1` in `~/soulman-dev/`): confirm `GET http://localhost:9015/api/claude/roots` (with a valid bearer token) returns the three configured roots. This can be deferred until Task 11 is also done and the frontend can be checked end-to-end in one pass — note it here so it isn't forgotten, but it does not block committing this task.

- [ ] **Step 4: Commit**

```bash
git add web-svc/main.go
git commit -m "feat(web-svc): wire claude_project_roots into httpserver"
```

---

## Task 8: Frontend `api.ts` — types and functions

**Files:**
- Modify: `web/src/api.ts`
- Modify: `web/src/api.test.ts`

**Interfaces:**
- Produces: `ClaudeRootListing{label, path, exists, folders}`; `ClaudeRoots{roots: ClaudeRootListing[]}`; `getClaudeRoots(token): Promise<ClaudeRoots>`; `launchClaudeSession(token, root, folder, sessionName): Promise<void>`.

- [ ] **Step 1: Write the failing tests**

In `web/src/api.test.ts`, add `getClaudeRoots` and `launchClaudeSession` to the existing top-of-file import list:

```ts
import {
  getStatus,
  getEpisodes,
  getRawInputs,
  getLatestReport,
  getReportByDate,
  getObsidianFolders,
  getObsidianFiles,
  getObsidianFile,
  saveObsidianFile,
  createObsidianFile,
  renameObsidianFile,
  getClaudeRoots,
  launchClaudeSession,
  ApiError,
} from './api';
```

Append these two `describe` blocks at the end of the file:

```ts
describe('getClaudeRoots', () => {
  it('calls the claude/roots endpoint', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: true, status: 200, json: async () => ({ roots: [] }) });

    const result = await getClaudeRoots('tok-abc');

    expect(result.roots).toEqual([]);
    const [url] = mockFetch.mock.calls[0];
    expect(url).toContain('/api/claude/roots');
  });
});

describe('launchClaudeSession', () => {
  it('sends a POST with root/folder/sessionName body', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: true, status: 204, json: async () => ({}) });

    await launchClaudeSession('tok-abc', 'Obsidian', 'soulman', 'soulman');

    const [url, options] = mockFetch.mock.calls[0];
    expect(url).toBe('/api/claude/launch');
    expect(options.method).toBe('POST');
    expect(JSON.parse(options.body)).toEqual({ root: 'Obsidian', folder: 'soulman', sessionName: 'soulman' });
  });

  it('throws ApiError on a non-2xx response', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: false, status: 500, json: async () => ({}) });

    await expect(launchClaudeSession('tok-abc', 'Obsidian', 'soulman', 'x')).rejects.toThrow(ApiError);
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npm --prefix web test -- api.test.ts`
Expected: FAIL — `getClaudeRoots`/`launchClaudeSession` are not exported by `./api` yet (TypeScript import error).

- [ ] **Step 3: Implement in `web/src/api.ts`**

Append after the existing `renameObsidianFile` export:

```ts
export interface ClaudeRootListing {
  label: string;
  path: string;
  exists: boolean;
  folders: string[];
}

export interface ClaudeRoots {
  roots: ClaudeRootListing[];
}

export const getClaudeRoots = (token: string | null): Promise<ClaudeRoots> =>
  getJSON('/api/claude/roots', token);

export const launchClaudeSession = (
  token: string | null,
  root: string,
  folder: string,
  sessionName: string,
): Promise<void> => mutateJSON('POST', '/api/claude/launch', token, { root, folder, sessionName });
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npm --prefix web test -- api.test.ts`
Expected: PASS for all tests in the file, including the two new ones.

- [ ] **Step 5: Commit**

```bash
git add web/src/api.ts web/src/api.test.ts
git commit -m "feat(web): add getClaudeRoots and launchClaudeSession API functions"
```

---

## Task 9: Frontend `ClaudeLaunchForm` component

**Files:**
- Create: `web/src/components/ClaudeLaunchForm.tsx`
- Create: `web/src/components/ClaudeLaunchForm.test.tsx`

**Interfaces:**
- Consumes: `launchClaudeSession`, `ApiError` (Task 8); `getAccessToken` from `web/src/auth.ts` (existing).
- Produces: `ClaudeLaunchForm({ root, folder }: { root: string; folder: string })` — a text input defaulted to `folder`, a "Launch" button, and a success/error message.

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/ClaudeLaunchForm.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockLaunchClaudeSession = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return { ...actual, launchClaudeSession: (...args: unknown[]) => mockLaunchClaudeSession(...args) };
});

beforeEach(() => vi.clearAllMocks());

describe('ClaudeLaunchForm', () => {
  it('defaults the session name input to the folder name', async () => {
    const { ClaudeLaunchForm } = await import('./ClaudeLaunchForm');
    render(<ClaudeLaunchForm root="Obsidian" folder="soulman" />);

    expect(screen.getByRole('textbox')).toHaveValue('soulman');
  });

  it('launches with the edited session name and shows a success message', async () => {
    mockLaunchClaudeSession.mockResolvedValue(undefined);
    const { ClaudeLaunchForm } = await import('./ClaudeLaunchForm');
    render(<ClaudeLaunchForm root="Obsidian" folder="soulman" />);

    await userEvent.clear(screen.getByRole('textbox'));
    await userEvent.type(screen.getByRole('textbox'), 'my-session');
    await userEvent.click(screen.getByRole('button', { name: /launch/i }));

    expect(mockLaunchClaudeSession).toHaveBeenCalledWith('tok-abc', 'Obsidian', 'soulman', 'my-session');
    expect(await screen.findByText(/session 'my-session' launched/i)).toBeInTheDocument();
  });

  it('shows an error message when launch fails', async () => {
    const { ApiError } = await import('../api');
    mockLaunchClaudeSession.mockRejectedValue(new ApiError(500, 'launch failed'));
    const { ClaudeLaunchForm } = await import('./ClaudeLaunchForm');
    render(<ClaudeLaunchForm root="Obsidian" folder="soulman" />);

    await userEvent.click(screen.getByRole('button', { name: /launch/i }));

    expect(await screen.findByText(/launch failed/i)).toBeInTheDocument();
  });

  it('resets the input to the new folder name when the folder prop changes', async () => {
    const { ClaudeLaunchForm } = await import('./ClaudeLaunchForm');
    const { rerender } = render(<ClaudeLaunchForm root="Obsidian" folder="soulman" />);

    rerender(<ClaudeLaunchForm root="Obsidian" folder="other-project" />);

    expect(screen.getByRole('textbox')).toHaveValue('other-project');
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npm --prefix web test -- ClaudeLaunchForm.test.tsx`
Expected: FAIL — `./ClaudeLaunchForm` doesn't exist yet.

- [ ] **Step 3: Implement `web/src/components/ClaudeLaunchForm.tsx`**

```tsx
import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { launchClaudeSession, ApiError } from '../api';

export function ClaudeLaunchForm({ root, folder }: { root: string; folder: string }) {
  const [sessionName, setSessionName] = useState(folder);
  const [status, setStatus] = useState<'idle' | 'launching' | 'success' | 'error'>('idle');
  const [message, setMessage] = useState<string | null>(null);

  useEffect(() => {
    setSessionName(folder);
    setStatus('idle');
    setMessage(null);
  }, [folder]);

  const handleLaunch = async () => {
    setStatus('launching');
    setMessage(null);
    const token = await getAccessToken();
    try {
      await launchClaudeSession(token, root, folder, sessionName);
      setStatus('success');
      setMessage(`Session '${sessionName}' launched`);
    } catch (err) {
      setStatus('error');
      setMessage(err instanceof ApiError ? `Launch failed (${err.status})` : 'Launch failed');
    }
  };

  return (
    <div className="ml-4 mt-1 flex items-center gap-2">
      <input
        type="text"
        value={sessionName}
        onChange={(e) => setSessionName(e.target.value)}
        className="rounded border border-gray-300 px-2 py-1 text-sm"
      />
      <button
        onClick={handleLaunch}
        disabled={status === 'launching'}
        className="rounded bg-blue-600 px-3 py-1 text-sm text-white disabled:opacity-50"
      >
        Launch
      </button>
      {message && (
        <span className={`text-sm ${status === 'error' ? 'text-red-600' : 'text-green-600'}`}>{message}</span>
      )}
    </div>
  );
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npm --prefix web test -- ClaudeLaunchForm.test.tsx`
Expected: PASS for all four tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/ClaudeLaunchForm.tsx web/src/components/ClaudeLaunchForm.test.tsx
git commit -m "feat(web): add ClaudeLaunchForm component"
```

---

## Task 10: Frontend `ClaudeRootList` component

**Files:**
- Create: `web/src/components/ClaudeRootList.tsx`
- Create: `web/src/components/ClaudeRootList.test.tsx`

**Interfaces:**
- Consumes: `getClaudeRoots`, `ClaudeRootListing` (Task 8); `ClaudeLaunchForm` (Task 9); `getParam`, `setParams` from `web/src/urlState.ts` (existing); `getAccessToken` (existing).
- Produces: `ClaudeRootList()` — fetches roots on mount, renders three accordion sections in the order returned by the API, each expandable (if `exists`) to show folder buttons that open a `ClaudeLaunchForm`.

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/ClaudeRootList.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockGetClaudeRoots = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return { ...actual, getClaudeRoots: (...args: unknown[]) => mockGetClaudeRoots(...args) };
});

beforeEach(() => {
  vi.clearAllMocks();
  window.history.replaceState(null, '', '/');
});

describe('ClaudeRootList', () => {
  it('lists existing roots and expands one to show its folders', async () => {
    mockGetClaudeRoots.mockResolvedValue({
      roots: [
        { label: 'Obsidian', path: 'C:\\obsidian', exists: true, folders: ['soulman'] },
        { label: 'IdeaProjects', path: 'C:\\IdeaProjects', exists: true, folders: ['digital-me'] },
      ],
    });
    const { ClaudeRootList } = await import('./ClaudeRootList');
    render(<ClaudeRootList />);

    await userEvent.click(await screen.findByText('Obsidian'));

    expect(await screen.findByText('soulman')).toBeInTheDocument();
    expect(screen.queryByText('digital-me')).not.toBeInTheDocument();
  });

  it('shows a missing root as unavailable and does not let it expand', async () => {
    mockGetClaudeRoots.mockResolvedValue({
      roots: [{ label: 'Misc Projects', path: 'C:\\misc_projects', exists: false, folders: [] }],
    });
    const { ClaudeRootList } = await import('./ClaudeRootList');
    render(<ClaudeRootList />);

    expect(await screen.findByText(/misc projects.*not found/i)).toBeInTheDocument();
  });

  it('opens the launch form for a selected folder, defaulted to the folder name', async () => {
    mockGetClaudeRoots.mockResolvedValue({
      roots: [{ label: 'Obsidian', path: 'C:\\obsidian', exists: true, folders: ['soulman'] }],
    });
    const { ClaudeRootList } = await import('./ClaudeRootList');
    render(<ClaudeRootList />);

    await userEvent.click(await screen.findByText('Obsidian'));
    await userEvent.click(await screen.findByText('soulman'));

    expect(screen.getByRole('textbox')).toHaveValue('soulman');
  });

  it('shows an error banner when roots fail to load', async () => {
    mockGetClaudeRoots.mockRejectedValue(new Error('network error'));
    const { ClaudeRootList } = await import('./ClaudeRootList');
    render(<ClaudeRootList />);

    expect(await screen.findByText(/roots unavailable/i)).toBeInTheDocument();
  });

  it('restores the previously expanded root from the URL on mount', async () => {
    window.history.replaceState(null, '', '/?claudeRoot=Obsidian');
    mockGetClaudeRoots.mockResolvedValue({
      roots: [{ label: 'Obsidian', path: 'C:\\obsidian', exists: true, folders: ['soulman'] }],
    });
    const { ClaudeRootList } = await import('./ClaudeRootList');
    render(<ClaudeRootList />);

    expect(await screen.findByText('soulman')).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npm --prefix web test -- ClaudeRootList.test.tsx`
Expected: FAIL — `./ClaudeRootList` doesn't exist yet.

- [ ] **Step 3: Implement `web/src/components/ClaudeRootList.tsx`**

```tsx
import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { getClaudeRoots, type ClaudeRootListing } from '../api';
import { ClaudeLaunchForm } from './ClaudeLaunchForm';
import { getParam, setParams } from '../urlState';

export function ClaudeRootList() {
  const [roots, setRoots] = useState<ClaudeRootListing[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [expanded, setExpanded] = useState<string | null>(() => getParam('claudeRoot'));
  const [selectedFolder, setSelectedFolder] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    (async () => {
      const token = await getAccessToken();
      try {
        const data = await getClaudeRoots(token);
        if (active) setRoots(data.roots ?? []);
      } catch {
        if (active) setError('Roots unavailable');
      }
    })();
    return () => {
      active = false;
    };
  }, []);

  return (
    <div>
      {error && <p className="text-sm text-red-600">{error}</p>}
      {!error && roots === null && <p className="text-sm text-gray-500">Loading...</p>}
      {!error && roots && (
        <ul className="space-y-2">
          {roots.map((root) => (
            <li key={root.label}>
              {!root.exists && (
                <span className="text-sm font-medium text-gray-400">{root.label} (not found)</span>
              )}
              {root.exists && (
                <>
                  <button
                    onClick={() => {
                      const next = expanded === root.label ? null : root.label;
                      setExpanded(next);
                      setSelectedFolder(null);
                      setParams({ claudeRoot: next });
                    }}
                    className="text-sm font-medium underline"
                  >
                    {root.label}
                  </button>
                  {expanded === root.label && (
                    <ul className="ml-4 mt-1 space-y-1">
                      {(root.folders ?? []).map((folder) => (
                        <li key={folder}>
                          <button
                            onClick={() => setSelectedFolder(selectedFolder === folder ? null : folder)}
                            className="text-sm underline"
                          >
                            {folder}
                          </button>
                          {selectedFolder === folder && <ClaudeLaunchForm root={root.label} folder={folder} />}
                        </li>
                      ))}
                    </ul>
                  )}
                </>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npm --prefix web test -- ClaudeRootList.test.tsx`
Expected: PASS for all five tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/ClaudeRootList.tsx web/src/components/ClaudeRootList.test.tsx
git commit -m "feat(web): add ClaudeRootList component"
```

---

## Task 11: Frontend `ClaudePage`, `Dashboard`, and `App` wiring

**Files:**
- Create: `web/src/components/ClaudePage.tsx`
- Create: `web/src/components/ClaudePage.test.tsx`
- Modify: `web/src/components/Dashboard.tsx`
- Modify: `web/src/App.tsx`
- Modify: `web/src/App.test.tsx`

**Interfaces:**
- Consumes: `ClaudeRootList` (Task 10).
- Produces: `ClaudePage({ onBack })`; `Dashboard`'s new `onOpenClaude: () => void` prop; `App`'s `'claude'` view state, reachable via a "Claude" button left of "Obsidian" and via `?page=claude` in the URL.

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/ClaudePage.test.tsx`:

```tsx
import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return { ...actual, getClaudeRoots: vi.fn().mockResolvedValue({ roots: [] }) };
});

describe('ClaudePage', () => {
  it('calls onBack when the back link is clicked', async () => {
    const onBack = vi.fn();
    const { ClaudePage } = await import('./ClaudePage');
    render(<ClaudePage onBack={onBack} />);

    await userEvent.click(screen.getByText(/soulman/i));

    expect(onBack).toHaveBeenCalled();
  });
});
```

Append these two tests to `web/src/App.test.tsx` (inside the existing `describe('App', ...)` block, after the Obsidian-equivalent tests):

```tsx
  it('switches to the claude page and back via the header links', async () => {
    mockUseAuth.mockReturnValue({
      user: { email: 'breynisson@gmail.com' },
      loading: false,
      signIn: vi.fn(),
      signOut: vi.fn(),
    });
    mockGetStatus.mockResolvedValue({ 'memory-svc': 'up' });
    const { default: App } = await import('./App');
    render(<App />);

    await screen.findByText(/soulman dashboard/i);
    await userEvent.click(screen.getByRole('button', { name: /claude/i }));

    expect(await screen.findByRole('heading', { name: /claude/i })).toBeInTheDocument();

    await userEvent.click(screen.getByText(/soulman/i));

    expect(await screen.findByText(/soulman dashboard/i)).toBeInTheDocument();
  });

  it('restores the claude page from a page=claude URL param on mount', async () => {
    window.history.replaceState(null, '', '/?page=claude');
    mockUseAuth.mockReturnValue({
      user: { email: 'breynisson@gmail.com' },
      loading: false,
      signIn: vi.fn(),
      signOut: vi.fn(),
    });
    mockGetStatus.mockResolvedValue({ 'memory-svc': 'up' });
    const { default: App } = await import('./App');
    render(<App />);

    expect(await screen.findByRole('heading', { name: /claude/i })).toBeInTheDocument();
  });
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npm --prefix web test -- ClaudePage.test.tsx App.test.tsx`
Expected: FAIL — `./ClaudePage` doesn't exist, `Dashboard` has no "Claude" button, `App` has no `'claude'` view state.

- [ ] **Step 3: Implement `web/src/components/ClaudePage.tsx`**

```tsx
import { ClaudeRootList } from './ClaudeRootList';

export function ClaudePage({ onBack }: { onBack: () => void }) {
  return (
    <div className="min-h-screen bg-gray-50 p-6">
      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Claude</h1>
        <button onClick={onBack} className="text-sm text-gray-500 underline">
          ← Soulman
        </button>
      </div>
      <ClaudeRootList />
    </div>
  );
}
```

- [ ] **Step 4: Update `web/src/components/Dashboard.tsx`**

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
}: {
  initialStatus: ServiceStatus | null;
  onSignOut: () => void;
  onOpenObsidian: () => void;
  onOpenClaude: () => void;
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

- [ ] **Step 5: Update `web/src/App.tsx`**

```tsx
import { useEffect, useState } from 'react';
import { useAuth, getAccessToken } from './auth';
import { getStatus, ApiError, type ServiceStatus } from './api';
import { LoginScreen } from './components/LoginScreen';
import { RestrictedScreen } from './components/RestrictedScreen';
import { Dashboard } from './components/Dashboard';
import { ObsidianPage } from './components/ObsidianPage';
import { ClaudePage } from './components/ClaudePage';
import { getParam, setParams } from './urlState';

type ViewState = 'loading' | 'login' | 'restricted' | 'dashboard' | 'obsidian' | 'claude';

function viewFromPageParam(): ViewState {
  const page = getParam('page');
  if (page === 'obsidian') return 'obsidian';
  if (page === 'claude') return 'claude';
  return 'dashboard';
}

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
        setView(viewFromPageParam());
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
  if (view === 'obsidian') {
    return (
      <ObsidianPage
        onBack={() => {
          setParams({ page: null, folder: null, file: null, mode: null });
          setView('dashboard');
        }}
      />
    );
  }
  if (view === 'claude') {
    return (
      <ClaudePage
        onBack={() => {
          setParams({ page: null, claudeRoot: null });
          setView('dashboard');
        }}
      />
    );
  }

  return (
    <Dashboard
      initialStatus={status}
      onSignOut={signOut}
      onOpenObsidian={() => {
        setParams({ page: 'obsidian' });
        setView('obsidian');
      }}
      onOpenClaude={() => {
        setParams({ page: 'claude' });
        setView('claude');
      }}
    />
  );
}

export default App;
```

- [ ] **Step 6: Run all frontend tests to verify they pass**

Run: `npm --prefix web test`
Expected: PASS for the entire suite, including every pre-existing test file (confirms the `Dashboard` prop addition and `App` view-state change didn't break the Obsidian flow).

- [ ] **Step 7: Commit**

```bash
git add web/src/components/ClaudePage.tsx web/src/components/ClaudePage.test.tsx web/src/components/Dashboard.tsx web/src/App.tsx web/src/App.test.tsx
git commit -m "feat(web): add Claude dashboard page and nav link"
```

---

## Task 12: Manual end-to-end verification and docs

**Files:**
- Modify: `CLAUDE.md`
- Modify: `web-svc/NOTES.md`

No new automated tests — this task is the manual verification called for by the design spec's Testing section, plus the doc updates the user's global CLAUDE.md instructions require before a feature branch is considered done.

- [ ] **Step 1: Rebuild and restart dev `web-svc` and `web`**

In `~/soulman-dev/`, pull this branch's changes and let the next `start-everything.ps1` run (or a manual `run-web-svc.ps1` / `run-web.ps1` invocation) rebuild both. Confirm `web-svc`'s dev process starts cleanly (no fatal config-load error — this is the first real check that `claude_project_roots` parses correctly from `config/dev.json`).

- [ ] **Step 2: Manual browser walkthrough**

Open the dev dashboard, sign in, click "Claude" (confirm it's left of "Obsidian"). Confirm all three sections render (Obsidian, IdeaProjects, Misc Projects) with correct exists/not-found state. Expand a section with folders, click a folder, confirm the launch form appears pre-filled with the folder name. Click "Launch" and confirm:
- A success message appears in the dashboard.
- A `claude` process appears in Task Manager with the correct working directory (right-click the process → Properties, or use `Get-Process claude | Select-Object Id, Path` in PowerShell) and is running detached (survives if `web-svc` is restarted).
- Optionally, confirm the session shows up as available for remote control via `claude.ai/code`.

Also confirm the not-found-root case: temporarily rename `C:\Users\Lenovo\misc_projects` (or check whichever of the three roots is safe to test with) and confirm its section shows "(not found)" instead of erroring the whole page, then rename it back.

- [ ] **Step 3: Update `CLAUDE.md`**

In the `## Services` section's `web-svc` bullet (item 5), extend the sentence listing `web-svc`'s routes to mention the new ones, and add the new spec to that bullet's Specs list. Find this text:

```
5. **`web-svc`** — the only Soulman service reachable from a browser: CORS-enabled, verifies Supabase-issued JWTs (reusing `agent-suite`'s existing hosted Supabase project and Google OAuth client), and authorizes a single configured owner email (`web.owner_email` in shared config) — no roles table. Serves `GET /api/status` (aggregates `/health` from the other four services), `GET /api/episodes` and `GET /api/raw-inputs/recent` (proxy `memory-svc`), `GET /api/system-monitor` (proxies `perception-svc`'s System Monitor check status), `GET /api/reports/latest` / `GET /api/reports?date=` (reads `$SOULMAN_ROOT/reports/*.txt` directly), and — as of 2026-08-07 — `GET/PUT/POST /api/obsidian/file`, `GET /api/obsidian/folders`, `GET /api/obsidian/files`, and `POST /api/obsidian/file/rename` (view/edit/create/rename `.txt`/`.md` files one level deep under `web.obsidian_root`, a new required shared-config field pointing at the Obsidian vault root). Does not touch NATS at all. Override/control dispatch (PAUSE/STOP/RESUME) is explicitly not implemented here — blocked on a Guard Agent design that doesn't exist yet.
   - Specs: `2026-07-19-soulman-web-dashboard-design.md`, `2026-07-20-system-monitor-dashboard-panel-design.md`, `2026-07-20-dashboard-status-merge-and-raw-input-modal-design.md`, `2026-07-20-daily-report-importance-split-design.md`, `2026-08-07-obsidian-file-viewer-design.md`
   - Notes: `web-svc/NOTES.md`
```

Replace with:

```
5. **`web-svc`** — the only Soulman service reachable from a browser: CORS-enabled, verifies Supabase-issued JWTs (reusing `agent-suite`'s existing hosted Supabase project and Google OAuth client), and authorizes a single configured owner email (`web.owner_email` in shared config) — no roles table. Serves `GET /api/status` (aggregates `/health` from the other four services), `GET /api/episodes` and `GET /api/raw-inputs/recent` (proxy `memory-svc`), `GET /api/system-monitor` (proxies `perception-svc`'s System Monitor check status), `GET /api/reports/latest` / `GET /api/reports?date=` (reads `$SOULMAN_ROOT/reports/*.txt` directly), `GET/PUT/POST /api/obsidian/file`, `GET /api/obsidian/folders`, `GET /api/obsidian/files`, and `POST /api/obsidian/file/rename` (added 2026-08-07 — view/edit/create/rename `.txt`/`.md` files one level deep under `web.obsidian_root`), and — as of 2026-08-09 — `GET /api/claude/roots` and `POST /api/claude/launch` (spawns a detached `claude --remote-control --name "<session>"` process rooted in a folder from one of three curated roots, `web.claude_project_roots`, for starting remote-controlled sessions from the dashboard). Does not touch NATS at all. Override/control dispatch (PAUSE/STOP/RESUME) is explicitly not implemented here — blocked on a Guard Agent design that doesn't exist yet.
   - Specs: `2026-07-19-soulman-web-dashboard-design.md`, `2026-07-20-system-monitor-dashboard-panel-design.md`, `2026-07-20-dashboard-status-merge-and-raw-input-modal-design.md`, `2026-07-20-daily-report-importance-split-design.md`, `2026-08-07-obsidian-file-viewer-design.md`, `2026-08-09-claude-remote-sessions-design.md`
   - Notes: `web-svc/NOTES.md`
```

- [ ] **Step 4: Add a section to `web-svc/NOTES.md`**

Append this section at the end of `web-svc/NOTES.md`:

```markdown

## Claude remote-session launcher (added 2026-08-09)

See `docs/superpowers/specs/2026-08-09-claude-remote-sessions-design.md`. The new `web-svc/claudesession` package is deliberately fire-and-forget: `Launch` calls `cmd.Process.Release()` immediately after a successful `Start()`, so web-svc holds no handle on the spawned `claude --remote-control` process and cannot report anything about it after the initial launch — session lifecycle is entirely owned by Claude Code's own remote-control registration (`claude.ai/code`). `web.claude_project_roots` (three curated `{label, path}` entries: Obsidian, IdeaProjects, Misc Projects) is required to be non-empty at config load, unlike `obsidian_root` its individual `path` values are *not* required to exist on disk — `claudesession.ListRoots` reports a missing root as `exists: false` per-request instead, since these are ordinary project folders that can come and go, unlike the Obsidian vault.

**Automated tests never exercise the actual process-spawn success path.** `claudesession_test.go` and `claude_handler_test.go` only cover validation errors (empty session name, invalid folder segment, missing folder) — every one of those returns before `Launch` reaches `exec.Command(...).Start()`. A test that combined a valid folder with a valid session name would genuinely spawn a `claude --remote-control` process on whatever machine runs `go test`, which is never acceptable in an automated suite. If this package is extended later, preserve that boundary rather than adding a "happy path" test.

Windows process detachment (`claudesession_windows.go`, `//go:build windows`, `syscall.CREATE_NEW_PROCESS_GROUP`) follows the same filename-suffix convention as `perception-svc/sysmonitor/stats_windows.go` — no non-Windows counterpart exists because every Soulman service only ever runs on Windows.
```

- [ ] **Step 5: Commit**

```bash
git add CLAUDE.md web-svc/NOTES.md
git commit -m "docs: document the Claude remote-session launcher feature"
```

---

## Self-Review Notes

- **Spec coverage:** every Goal in the design spec maps to a task — nav link (Task 11), curated three-root listing (Tasks 1–3, 5, 10), session-name-defaulted-to-folder launch (Tasks 4, 6, 9), detached process spawn (Task 4). Every Non-Goal is respected: no tracking/stop UI was added anywhere, no output capture appears in `Launch`, folder access is server-validated against configured roots only (Task 4's `resolveDir`), and both `config/dev.json`/`config/prod.json` get identical `claude_project_roots` (Task 1).
- **Frontend testing correction:** the design spec's Testing section stated "no automated tests added" for the frontend, extrapolating from an earlier, inaccurate read of the Obsidian feature's test coverage. `web/src/components/Obsidian*.test.tsx` and `web/src/App.test.tsx` in fact cover the Obsidian page thoroughly with Vitest + Testing Library — this plan follows that same real pattern for every new component (Tasks 9–11) and for `api.ts` (Task 8) instead of skipping frontend tests.
- **Type consistency check:** `claudesession.Root{Label, Path}` (Task 3) is the type threaded through `httpserver.Config.ClaudeProjectRoots` (Task 5) and `main.go`'s conversion loop (Task 7) unchanged. `ClaudeRootListing` (frontend, Task 8) field names (`label`, `path`, `exists`, `folders`) match `claudeRootResponse`'s JSON tags (Task 5) exactly. `launchClaudeSession`'s parameter order (`token, root, folder, sessionName`) matches every call site (`ClaudeLaunchForm`, Task 9; the corresponding test in Task 8).
