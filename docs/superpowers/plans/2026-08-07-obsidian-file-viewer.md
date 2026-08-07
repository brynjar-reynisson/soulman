# Obsidian File Viewer/Editor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a dashboard page that browses the folders directly under the obsidian vault root, lists `.txt`/`.md` files inside a selected folder, and lets the owner view, edit, create, and rename those files from the browser.

**Architecture:** Extend `web-svc` (no new service) with a new `obsidian` package (path-validated filesystem operations) and six new HTTP routes behind the existing owner-only JWT middleware; extend the `web` React app with a new page reachable from the dashboard header, composed of a folder accordion, a file list, and a viewer/editor pair.

**Tech Stack:** Go (chi router, existing `web-svc` stack), React 19 + TypeScript + Tailwind (existing `web` stack), `react-markdown` (new frontend dependency for Markdown rendering).

## Global Constraints

- Every new HTTP route lives behind `web-svc`'s existing `verifier.Middleware` (owner-only JWT auth) — no new auth mechanism.
- Folder/file browsing is exactly one level deep: top-level folders under `web.obsidian_root`, and `.txt`/`.md` files directly inside a selected folder. No recursion into subfolders.
- No delete of files or folders, no create/rename of folders — this iteration is view, edit, create-file, rename-file only.
- Every path built from user input (`folder`, `file`, `new_name`) must be validated against traversal (`..`, `/`, `\`) and, for files, restricted to `.txt`/`.md` extensions, before touching the filesystem — this matters because prod's dashboard is reachable through a public `cloudflared` tunnel.
- `config/dev.json` and `config/prod.json` both set `web.obsidian_root` to the same literal path, `C:\Users\Lenovo\Documents\obsidian` — this is a real Windows path (JSON-escaped as `C:\\Users\\Lenovo\\Documents\\obsidian`), not a per-environment value.
- Follow existing code conventions exactly: Go table-style tests using `t.TempDir()` fixtures (see `web-svc/reports/reports_test.go`), thin chi handlers that delegate to a plain-function package (see `web-svc/httpserver/reports_handler.go`), React components using the `useEffect` + `useState` + `getAccessToken` fetch pattern with inline error banners (see `web/src/components/EpisodesPanel.tsx`), and component tests that mock `../api` via `vi.mock` with `importActual` (see `web/src/components/EpisodesPanel.test.tsx`).
- Run Go commands as `go -C web-svc <command>` and npm commands as `npm --prefix web <command>` from the repo root, rather than `cd`-ing into the subdirectory.

---

## Task 1: Config plumbing — `web.obsidian_root`

**Files:**
- Modify: `common/sharedconfig/config.go` (`WebConfig` struct, ~line 126)
- Modify: `web-svc/config/config.go` (`Config` struct and `Load()`)
- Test: `web-svc/config/config_test.go`

**Interfaces:**
- Produces: `sharedconfig.WebConfig.ObsidianRoot string` (JSON key `obsidian_root`); `config.Config.ObsidianRoot string`, populated by `config.Load()` and required (fatal error if blank) — later tasks (Task 4, Task 6) read `cfg.ObsidianRoot`.

- [ ] **Step 1: Write the failing tests**

Edit `web-svc/config/config_test.go`: update the shared `validConfigJSON` constant to include `obsidian_root` (every other test in this file uses this constant, so they'll all still pass once the field is required), and add two new tests.

Replace the `validConfigJSON` constant:

```go
const validConfigJSON = `{
  "web": {
    "owner_email": "breynisson@gmail.com",
    "cors_allowed_origin": "http://localhost:5178",
    "perception_svc_url": "http://localhost:9011",
    "memory_svc_url": "http://localhost:9012",
    "thinking_svc_url": "http://localhost:9013",
    "action_svc_url": "http://localhost:9014",
    "obsidian_root": "C:\\Users\\Lenovo\\Documents\\obsidian"
  }
}`
```

Add these tests at the end of the file:

```go
func TestLoad_PopulatesObsidianRoot(t *testing.T) {
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
	if cfg.ObsidianRoot != `C:\Users\Lenovo\Documents\obsidian` {
		t.Errorf("ObsidianRoot = %q", cfg.ObsidianRoot)
	}
}

func TestLoad_MissingObsidianRoot_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	incomplete := `{"web": {"owner_email": "breynisson@gmail.com", "cors_allowed_origin": "http://localhost:5178", "perception_svc_url": "http://localhost:9011", "memory_svc_url": "http://localhost:9012", "thinking_svc_url": "http://localhost:9013", "action_svc_url": "http://localhost:9014", "obsidian_root": ""}}`
	path := writeConfigFile(t, dir, incomplete)
	os.Setenv("CONFIG_PATH", path)
	os.Setenv("SUPABASE_URL", "https://example.supabase.co")
	os.Setenv("SUPABASE_JWT_SECRET", "shh")
	defer os.Unsetenv("CONFIG_PATH")
	defer os.Unsetenv("SUPABASE_URL")
	defer os.Unsetenv("SUPABASE_JWT_SECRET")

	if _, err := config.Load(); err == nil {
		t.Fatal("Load() error = nil, want an error when web.obsidian_root is blank")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go -C web-svc test ./config/... -run TestLoad_PopulatesObsidianRoot -v`
Expected: FAIL — `cfg.ObsidianRoot` doesn't exist yet (compile error).

- [ ] **Step 3: Add the field to `sharedconfig.WebConfig`**

In `common/sharedconfig/config.go`, change `WebConfig`:

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

- [ ] **Step 4: Add the field and validation to `web-svc/config`**

In `web-svc/config/config.go`, add `ObsidianRoot` to the `Config` struct:

```go
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
	SoulmanRoot       string
	ObsidianRoot      string
}
```

Add a required-field check in `Load()`, right after the existing `ActionSvcURL` check:

```go
	if shared.Web.ActionSvcURL == "" {
		return nil, fmt.Errorf("shared config %s has no web.action_svc_url configured", configPath)
	}
	if shared.Web.ObsidianRoot == "" {
		return nil, fmt.Errorf("shared config %s has no web.obsidian_root configured", configPath)
	}
```

Add `ObsidianRoot: shared.Web.ObsidianRoot,` to the returned `&Config{...}` literal, alongside the other `shared.Web.*` fields.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go -C web-svc test ./config/... -v`
Expected: PASS (all tests, including the pre-existing ones — the updated `validConfigJSON` keeps them green).

- [ ] **Step 6: Commit**

```bash
git -C . add common/sharedconfig/config.go web-svc/config/config.go web-svc/config/config_test.go
git -C . commit -m "feat(web-svc): add required web.obsidian_root config field"
```

---

## Task 2: `web-svc/obsidian` package — path validation, `ListFolders`, `ListFiles`

**Files:**
- Create: `web-svc/obsidian/obsidian.go`
- Test: `web-svc/obsidian/obsidian_test.go`

**Interfaces:**
- Consumes: nothing (pure filesystem package, no dependency on other web-svc packages).
- Produces: `obsidian.ErrNotFound`, `obsidian.ErrExists`, `obsidian.ErrInvalidName` (sentinel errors, all reused in Task 3); `obsidian.ListFolders(root string) ([]string, error)`; `obsidian.ListFiles(root, folder string) ([]string, error)` — both consumed by `web-svc/httpserver` in Task 4.

- [ ] **Step 1: Write the failing tests**

Create `web-svc/obsidian/obsidian_test.go`:

```go
package obsidian_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"soulman/web-svc/obsidian"
)

func TestListFolders_ReturnsSortedDirectoriesOnly(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "zeta"), 0o755)
	os.Mkdir(filepath.Join(root, "alpha"), 0o755)
	os.WriteFile(filepath.Join(root, "not-a-folder.txt"), []byte("x"), 0o644)

	folders, err := obsidian.ListFolders(root)
	if err != nil {
		t.Fatalf("ListFolders() error = %v", err)
	}
	want := []string{"alpha", "zeta"}
	if len(folders) != len(want) || folders[0] != want[0] || folders[1] != want[1] {
		t.Errorf("ListFolders() = %v, want %v", folders, want)
	}
}

func TestListFiles_ReturnsSortedTxtAndMdOnly(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "vault")
	os.Mkdir(folder, 0o755)
	os.WriteFile(filepath.Join(folder, "zeta.md"), []byte("z"), 0o644)
	os.WriteFile(filepath.Join(folder, "alpha.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(folder, "image.png"), []byte("p"), 0o644)
	os.Mkdir(filepath.Join(folder, "subdir"), 0o755)

	files, err := obsidian.ListFiles(root, "vault")
	if err != nil {
		t.Fatalf("ListFiles() error = %v", err)
	}
	want := []string{"alpha.txt", "zeta.md"}
	if len(files) != len(want) || files[0] != want[0] || files[1] != want[1] {
		t.Errorf("ListFiles() = %v, want %v", files, want)
	}
}

func TestListFiles_MissingFolder_ReturnsErrNotFound(t *testing.T) {
	root := t.TempDir()

	_, err := obsidian.ListFiles(root, "does-not-exist")
	if !errors.Is(err, obsidian.ErrNotFound) {
		t.Fatalf("ListFiles() error = %v, want ErrNotFound", err)
	}
}

func TestListFiles_InvalidFolderName_ReturnsErrInvalidName(t *testing.T) {
	root := t.TempDir()

	for _, folder := range []string{"..", "../etc", `a\b`, "a/b", ""} {
		_, err := obsidian.ListFiles(root, folder)
		if !errors.Is(err, obsidian.ErrInvalidName) {
			t.Errorf("ListFiles(%q) error = %v, want ErrInvalidName", folder, err)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go -C web-svc test ./obsidian/... -v`
Expected: FAIL — the `obsidian` package doesn't exist yet (compile error, no such package).

- [ ] **Step 3: Write the implementation**

Create `web-svc/obsidian/obsidian.go`:

```go
// Package obsidian provides validated read/write access to .txt/.md files
// under a vault root directory (web.obsidian_root), one level deep:
// top-level folders, and the .txt/.md files directly inside them. Every
// function validates its folder/file arguments before touching the
// filesystem — see resolveFolder/resolveFile — since this is reachable
// from the internet-tunneled prod dashboard (owner-JWT-gated, but
// path-traversal protection matters regardless).
package obsidian

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var (
	ErrNotFound    = errors.New("obsidian: not found")
	ErrExists      = errors.New("obsidian: already exists")
	ErrInvalidName = errors.New("obsidian: invalid name")
)

// ListFolders returns the names of directories directly under root, sorted.
func ListFolders(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("obsidian: reading root %s: %w", root, err)
	}
	var folders []string
	for _, e := range entries {
		if e.IsDir() {
			folders = append(folders, e.Name())
		}
	}
	sort.Strings(folders)
	return folders, nil
}

// ListFiles returns the .txt/.md filenames directly inside root/folder,
// sorted. Subdirectories and any other extension are skipped.
func ListFiles(root, folder string) ([]string, error) {
	dir, err := resolveFolder(root, folder)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("obsidian: reading folder %s: %w", dir, err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if hasValidExtension(e.Name()) {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	return files, nil
}

func hasValidExtension(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".txt" || ext == ".md"
}

// validSegment rejects anything that isn't a single, plain path component:
// empty, containing a path separator, or "." / "..".
func validSegment(name string) bool {
	return name != "" && !strings.ContainsAny(name, `/\`) && name != "." && name != ".."
}

// resolveFolder validates folder and returns root/folder, after confirming
// the joined path is still contained within root (defense in depth on top
// of validSegment's rejection of "..").
func resolveFolder(root, folder string) (string, error) {
	if !validSegment(folder) {
		return "", ErrInvalidName
	}
	cleanRoot := filepath.Clean(root)
	dir := filepath.Join(cleanRoot, folder)
	if !isWithin(cleanRoot, dir) {
		return "", ErrInvalidName
	}
	return dir, nil
}

// resolveFile validates folder and file (file must be a single path
// segment with a .txt/.md extension) and returns root/folder/file, after
// confirming the joined path is still contained within root/folder.
func resolveFile(root, folder, file string) (string, error) {
	dir, err := resolveFolder(root, folder)
	if err != nil {
		return "", err
	}
	if !validSegment(file) || !hasValidExtension(file) {
		return "", ErrInvalidName
	}
	path := filepath.Join(dir, file)
	if !isWithin(dir, path) {
		return "", ErrInvalidName
	}
	return path, nil
}

func isWithin(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go -C web-svc test ./obsidian/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git -C . add web-svc/obsidian/obsidian.go web-svc/obsidian/obsidian_test.go
git -C . commit -m "feat(web-svc): add obsidian package with ListFolders/ListFiles"
```

---

## Task 3: `web-svc/obsidian` package — `ReadFile`, `WriteFile`, `CreateFile`, `RenameFile`

**Files:**
- Modify: `web-svc/obsidian/obsidian.go`
- Test: `web-svc/obsidian/obsidian_test.go`

**Interfaces:**
- Consumes: `resolveFile` from Task 2 (unexported, same package).
- Produces: `obsidian.ReadFile(root, folder, file string) (string, error)`; `obsidian.WriteFile(root, folder, file, content string) error`; `obsidian.CreateFile(root, folder, file, content string) error`; `obsidian.RenameFile(root, folder, oldName, newName string) error` — all consumed by `web-svc/httpserver` in Tasks 4-5.

- [ ] **Step 1: Write the failing tests**

Append to `web-svc/obsidian/obsidian_test.go`:

```go
func TestReadFile_ReturnsContent(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "vault")
	os.Mkdir(folder, 0o755)
	os.WriteFile(filepath.Join(folder, "note.md"), []byte("hello"), 0o644)

	content, err := obsidian.ReadFile(root, "vault", "note.md")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if content != "hello" {
		t.Errorf("content = %q, want hello", content)
	}
}

func TestReadFile_Missing_ReturnsErrNotFound(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "vault"), 0o755)

	_, err := obsidian.ReadFile(root, "vault", "missing.md")
	if !errors.Is(err, obsidian.ErrNotFound) {
		t.Fatalf("ReadFile() error = %v, want ErrNotFound", err)
	}
}

func TestReadFile_InvalidFileName_ReturnsErrInvalidName(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "vault"), 0o755)

	for _, file := range []string{"../secrets.md", `a\b.md`, "a/b.md", "note.png", ""} {
		_, err := obsidian.ReadFile(root, "vault", file)
		if !errors.Is(err, obsidian.ErrInvalidName) {
			t.Errorf("ReadFile(%q) error = %v, want ErrInvalidName", file, err)
		}
	}
}

func TestWriteFile_OverwritesExistingContent(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "vault")
	os.Mkdir(folder, 0o755)
	os.WriteFile(filepath.Join(folder, "note.md"), []byte("old"), 0o644)

	if err := obsidian.WriteFile(root, "vault", "note.md", "new"); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(folder, "note.md"))
	if string(b) != "new" {
		t.Errorf("file content = %q, want new", string(b))
	}
}

func TestWriteFile_MissingFile_ReturnsErrNotFound(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "vault"), 0o755)

	err := obsidian.WriteFile(root, "vault", "missing.md", "content")
	if !errors.Is(err, obsidian.ErrNotFound) {
		t.Fatalf("WriteFile() error = %v, want ErrNotFound", err)
	}
}

func TestCreateFile_WritesNewFile(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "vault")
	os.Mkdir(folder, 0o755)

	if err := obsidian.CreateFile(root, "vault", "new.md", "hello"); err != nil {
		t.Fatalf("CreateFile() error = %v", err)
	}
	b, err := os.ReadFile(filepath.Join(folder, "new.md"))
	if err != nil {
		t.Fatalf("expected file to exist: %v", err)
	}
	if string(b) != "hello" {
		t.Errorf("content = %q, want hello", string(b))
	}
}

func TestCreateFile_AlreadyExists_ReturnsErrExists(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "vault")
	os.Mkdir(folder, 0o755)
	os.WriteFile(filepath.Join(folder, "existing.md"), []byte("old"), 0o644)

	err := obsidian.CreateFile(root, "vault", "existing.md", "new")
	if !errors.Is(err, obsidian.ErrExists) {
		t.Fatalf("CreateFile() error = %v, want ErrExists", err)
	}
	b, _ := os.ReadFile(filepath.Join(folder, "existing.md"))
	if string(b) != "old" {
		t.Errorf("existing file was overwritten: %q", string(b))
	}
}

func TestRenameFile_RenamesToNewName(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "vault")
	os.Mkdir(folder, 0o755)
	os.WriteFile(filepath.Join(folder, "old.md"), []byte("content"), 0o644)

	if err := obsidian.RenameFile(root, "vault", "old.md", "new.md"); err != nil {
		t.Fatalf("RenameFile() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(folder, "old.md")); !os.IsNotExist(err) {
		t.Error("old.md still exists")
	}
	b, err := os.ReadFile(filepath.Join(folder, "new.md"))
	if err != nil || string(b) != "content" {
		t.Errorf("new.md content = %q, err = %v", string(b), err)
	}
}

func TestRenameFile_SourceMissing_ReturnsErrNotFound(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "vault"), 0o755)

	err := obsidian.RenameFile(root, "vault", "missing.md", "new.md")
	if !errors.Is(err, obsidian.ErrNotFound) {
		t.Fatalf("RenameFile() error = %v, want ErrNotFound", err)
	}
}

func TestRenameFile_DestinationExists_ReturnsErrExists(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "vault")
	os.Mkdir(folder, 0o755)
	os.WriteFile(filepath.Join(folder, "a.md"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(folder, "b.md"), []byte("b"), 0o644)

	err := obsidian.RenameFile(root, "vault", "a.md", "b.md")
	if !errors.Is(err, obsidian.ErrExists) {
		t.Fatalf("RenameFile() error = %v, want ErrExists", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go -C web-svc test ./obsidian/... -v`
Expected: FAIL — `obsidian.ReadFile`/`WriteFile`/`CreateFile`/`RenameFile` don't exist yet (compile error).

- [ ] **Step 3: Write the implementation**

Append to `web-svc/obsidian/obsidian.go`:

```go
// ReadFile returns the content of root/folder/file.
func ReadFile(root, folder, file string) (string, error) {
	path, err := resolveFile(root, folder, file)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("obsidian: reading file %s: %w", path, err)
	}
	return string(b), nil
}

// WriteFile overwrites an existing file's content. Returns ErrNotFound if
// it doesn't already exist — this is the "edit" path, not "create".
func WriteFile(root, folder, file, content string) error {
	path, err := resolveFile(root, folder, file)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return ErrNotFound
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("obsidian: writing file %s: %w", path, err)
	}
	return nil
}

// CreateFile creates a new file. Returns ErrExists if it already exists.
func CreateFile(root, folder, file, content string) error {
	path, err := resolveFile(root, folder, file)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return ErrExists
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("obsidian: creating file %s: %w", path, err)
	}
	return nil
}

// RenameFile renames a file within the same folder. Returns ErrNotFound if
// the source doesn't exist, ErrExists if the destination does. This is a
// check-then-act sequence (not atomic against a concurrent writer) — an
// accepted narrow race given this is a single-owner tool.
func RenameFile(root, folder, oldName, newName string) error {
	oldPath, err := resolveFile(root, folder, oldName)
	if err != nil {
		return err
	}
	newPath, err := resolveFile(root, folder, newName)
	if err != nil {
		return err
	}
	if _, err := os.Stat(oldPath); os.IsNotExist(err) {
		return ErrNotFound
	}
	if _, err := os.Stat(newPath); err == nil {
		return ErrExists
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		return fmt.Errorf("obsidian: renaming %s to %s: %w", oldPath, newPath, err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go -C web-svc test ./obsidian/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git -C . add web-svc/obsidian/obsidian.go web-svc/obsidian/obsidian_test.go
git -C . commit -m "feat(web-svc): add ReadFile/WriteFile/CreateFile/RenameFile to obsidian package"
```

---

## Task 4: `web-svc/httpserver` — GET routes (`folders`, `files`, `file`)

**Files:**
- Modify: `web-svc/httpserver/server.go` (`Config` struct, `buildRouter`)
- Create: `web-svc/httpserver/obsidian_handler.go`
- Test: `web-svc/httpserver/obsidian_handler_test.go`

**Interfaces:**
- Consumes: `obsidian.ListFolders`, `obsidian.ListFiles`, `obsidian.ReadFile`, `obsidian.ErrNotFound`, `obsidian.ErrInvalidName` from Tasks 2-3.
- Produces: `httpserver.Config.ObsidianRoot string` field (consumed by Task 6's `main.go` wiring); routes `GET /api/obsidian/folders`, `GET /api/obsidian/files`, `GET /api/obsidian/file`.

- [ ] **Step 1: Write the failing tests**

Create `web-svc/httpserver/obsidian_handler_test.go`:

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
	"soulman/web-svc/httpserver"
)

func TestAPIObsidianFolders_ReturnsSortedDirectoryNames(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "zeta"), 0o755)
	os.Mkdir(filepath.Join(root, "alpha"), 0o755)

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ObsidianRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/obsidian/folders", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body map[string][]string
	json.NewDecoder(rec.Body).Decode(&body)
	want := []string{"alpha", "zeta"}
	if len(body["folders"]) != 2 || body["folders"][0] != want[0] || body["folders"][1] != want[1] {
		t.Errorf("folders = %v, want %v", body["folders"], want)
	}
}

func TestAPIObsidianFolders_NoToken_Returns401(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ObsidianRoot: t.TempDir()}
	srv := httpserver.New("9005", cfg, auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail))

	req := httptest.NewRequest(http.MethodGet, "/api/obsidian/folders", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestAPIObsidianFiles_ReturnsFilesInFolder(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "vault")
	os.Mkdir(folder, 0o755)
	os.WriteFile(filepath.Join(folder, "note.md"), []byte("x"), 0o644)

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ObsidianRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/obsidian/files?folder=vault", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body map[string][]string
	json.NewDecoder(rec.Body).Decode(&body)
	if len(body["files"]) != 1 || body["files"][0] != "note.md" {
		t.Errorf("files = %v, want [note.md]", body["files"])
	}
}

func TestAPIObsidianFiles_PathTraversalFolder_Returns400(t *testing.T) {
	root := t.TempDir()

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ObsidianRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/obsidian/files?folder=..%2F..%2Fwindows", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPIObsidianFile_ReturnsContent(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "vault")
	os.Mkdir(folder, 0o755)
	os.WriteFile(filepath.Join(folder, "note.md"), []byte("hello"), 0o644)

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ObsidianRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/obsidian/file?folder=vault&file=note.md", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	json.NewDecoder(rec.Body).Decode(&body)
	if body["content"] != "hello" {
		t.Errorf("content = %q, want hello", body["content"])
	}
}

func TestAPIObsidianFile_Missing_Returns404(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "vault"), 0o755)

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ObsidianRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/obsidian/file?folder=vault&file=missing.md", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go -C web-svc test ./httpserver/... -run TestAPIObsidian -v`
Expected: FAIL — `httpserver.Config` has no `ObsidianRoot` field, and the routes don't exist (compile error / 404s).

- [ ] **Step 3: Add `ObsidianRoot` to `Config` and wire the routes**

In `web-svc/httpserver/server.go`, add to the `Config` struct:

```go
type Config struct {
	CORSAllowedOrigin string
	PerceptionSvcURL  string
	MemorySvcURL      string
	ThinkingSvcURL    string
	ActionSvcURL      string
	ReportsRoot       string
	ObsidianRoot      string
}
```

Add the three new routes inside the existing authenticated `r.Group`, after `r.Get("/api/reports", s.reportsByDate)`:

```go
		r.Get("/api/obsidian/folders", s.obsidianFolders)
		r.Get("/api/obsidian/files", s.obsidianFiles)
		r.Get("/api/obsidian/file", s.obsidianFileGet)
```

- [ ] **Step 4: Write the handlers**

Create `web-svc/httpserver/obsidian_handler.go`:

```go
package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"soulman/web-svc/obsidian"
)

func (s *Server) obsidianFolders(w http.ResponseWriter, r *http.Request) {
	folders, err := obsidian.ListFolders(s.cfg.ObsidianRoot)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string][]string{"folders": folders})
}

func (s *Server) obsidianFiles(w http.ResponseWriter, r *http.Request) {
	folder := r.URL.Query().Get("folder")
	files, err := obsidian.ListFiles(s.cfg.ObsidianRoot, folder)
	if err != nil {
		writeObsidianError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]string{"files": files})
}

func (s *Server) obsidianFileGet(w http.ResponseWriter, r *http.Request) {
	folder := r.URL.Query().Get("folder")
	file := r.URL.Query().Get("file")
	content, err := obsidian.ReadFile(s.cfg.ObsidianRoot, folder, file)
	if err != nil {
		writeObsidianError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"content": content})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func writeObsidianError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, obsidian.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "not found")
	case errors.Is(err, obsidian.ErrExists):
		writeJSONError(w, http.StatusConflict, "already exists")
	case errors.Is(err, obsidian.ErrInvalidName):
		writeJSONError(w, http.StatusBadRequest, "invalid name")
	default:
		writeJSONError(w, http.StatusInternalServerError, "internal error")
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go -C web-svc test ./httpserver/... -v`
Expected: PASS (all tests, including pre-existing ones).

- [ ] **Step 6: Commit**

```bash
git -C . add web-svc/httpserver/server.go web-svc/httpserver/obsidian_handler.go web-svc/httpserver/obsidian_handler_test.go
git -C . commit -m "feat(web-svc): add GET /api/obsidian/{folders,files,file} routes"
```

---

## Task 5: `web-svc/httpserver` — PUT/POST routes (`file` write, `file` create, `file/rename`)

**Files:**
- Modify: `web-svc/httpserver/server.go` (CORS `AllowedMethods`, route wiring)
- Modify: `web-svc/httpserver/obsidian_handler.go`
- Modify: `web-svc/httpserver/obsidian_handler_test.go`

**Interfaces:**
- Consumes: `obsidian.WriteFile`, `obsidian.CreateFile`, `obsidian.RenameFile` from Task 3; `writeObsidianError` from Task 4 (same package).
- Produces: routes `PUT /api/obsidian/file`, `POST /api/obsidian/file`, `POST /api/obsidian/file/rename` — consumed by `web/src/api.ts` in Task 7.

- [ ] **Step 1: Write the failing tests**

Append to `web-svc/httpserver/obsidian_handler_test.go`. This requires adding `"bytes"` to the import block at the top of the file — update it to:

```go
import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"soulman/web-svc/auth"
	"soulman/web-svc/httpserver"
)
```

Then append these tests:

```go
func TestAPIObsidianFilePut_OverwritesExistingFile(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "vault")
	os.Mkdir(folder, 0o755)
	os.WriteFile(filepath.Join(folder, "note.md"), []byte("old"), 0o644)

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ObsidianRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	reqBody, _ := json.Marshal(map[string]string{"folder": "vault", "file": "note.md", "content": "new"})
	req := httptest.NewRequest(http.MethodPut, "/api/obsidian/file", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	b, _ := os.ReadFile(filepath.Join(folder, "note.md"))
	if string(b) != "new" {
		t.Errorf("file content = %q, want new", string(b))
	}
}

func TestAPIObsidianFilePut_MissingFile_Returns404(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "vault"), 0o755)

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ObsidianRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	reqBody, _ := json.Marshal(map[string]string{"folder": "vault", "file": "missing.md", "content": "x"})
	req := httptest.NewRequest(http.MethodPut, "/api/obsidian/file", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAPIObsidianFilePost_CreatesNewFile(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "vault")
	os.Mkdir(folder, 0o755)

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ObsidianRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	reqBody, _ := json.Marshal(map[string]string{"folder": "vault", "file": "new.md", "content": "hello"})
	req := httptest.NewRequest(http.MethodPost, "/api/obsidian/file", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	b, err := os.ReadFile(filepath.Join(folder, "new.md"))
	if err != nil || string(b) != "hello" {
		t.Errorf("file content = %q, err = %v", string(b), err)
	}
}

func TestAPIObsidianFilePost_AlreadyExists_Returns409(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "vault")
	os.Mkdir(folder, 0o755)
	os.WriteFile(filepath.Join(folder, "existing.md"), []byte("old"), 0o644)

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ObsidianRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	reqBody, _ := json.Marshal(map[string]string{"folder": "vault", "file": "existing.md", "content": "new"})
	req := httptest.NewRequest(http.MethodPost, "/api/obsidian/file", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestAPIObsidianFileRename_RenamesFile(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "vault")
	os.Mkdir(folder, 0o755)
	os.WriteFile(filepath.Join(folder, "old.md"), []byte("content"), 0o644)

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ObsidianRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	reqBody, _ := json.Marshal(map[string]string{"folder": "vault", "file": "old.md", "new_name": "new.md"})
	req := httptest.NewRequest(http.MethodPost, "/api/obsidian/file/rename", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(folder, "new.md")); err != nil {
		t.Errorf("new.md not found: %v", err)
	}
}

func TestAPIObsidianFileRename_DestinationExists_Returns409(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "vault")
	os.Mkdir(folder, 0o755)
	os.WriteFile(filepath.Join(folder, "a.md"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(folder, "b.md"), []byte("b"), 0o644)

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ObsidianRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	reqBody, _ := json.Marshal(map[string]string{"folder": "vault", "file": "a.md", "new_name": "b.md"})
	req := httptest.NewRequest(http.MethodPost, "/api/obsidian/file/rename", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go -C web-svc test ./httpserver/... -run TestAPIObsidianFile -v`
Expected: FAIL — PUT/POST to `/api/obsidian/file` and `/api/obsidian/file/rename` aren't routed yet (405/404), and CORS doesn't yet allow those methods.

- [ ] **Step 3: Update CORS and add the routes**

In `web-svc/httpserver/server.go`, update `AllowedMethods` in the `cors.Handler` call:

```go
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{s.cfg.CORSAllowedOrigin},
		AllowedMethods: []string{"GET", "POST", "PUT", "OPTIONS"},
		AllowedHeaders: []string{"Authorization", "Content-Type"},
		MaxAge:         300,
	}))
```

Add the three new routes after `r.Get("/api/obsidian/file", s.obsidianFileGet)`:

```go
		r.Put("/api/obsidian/file", s.obsidianFilePut)
		r.Post("/api/obsidian/file", s.obsidianFilePost)
		r.Post("/api/obsidian/file/rename", s.obsidianFileRename)
```

- [ ] **Step 4: Write the handlers**

Append to `web-svc/httpserver/obsidian_handler.go`:

```go
type obsidianFileRequest struct {
	Folder  string `json:"folder"`
	File    string `json:"file"`
	Content string `json:"content"`
}

func (s *Server) obsidianFilePut(w http.ResponseWriter, r *http.Request) {
	var req obsidianFileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := obsidian.WriteFile(s.cfg.ObsidianRoot, req.Folder, req.File, req.Content); err != nil {
		writeObsidianError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) obsidianFilePost(w http.ResponseWriter, r *http.Request) {
	var req obsidianFileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := obsidian.CreateFile(s.cfg.ObsidianRoot, req.Folder, req.File, req.Content); err != nil {
		writeObsidianError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type obsidianRenameRequest struct {
	Folder  string `json:"folder"`
	File    string `json:"file"`
	NewName string `json:"new_name"`
}

func (s *Server) obsidianFileRename(w http.ResponseWriter, r *http.Request) {
	var req obsidianRenameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := obsidian.RenameFile(s.cfg.ObsidianRoot, req.Folder, req.File, req.NewName); err != nil {
		writeObsidianError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go -C web-svc test ./httpserver/... -v`
Expected: PASS (all tests, including pre-existing ones).

- [ ] **Step 6: Commit**

```bash
git -C . add web-svc/httpserver/server.go web-svc/httpserver/obsidian_handler.go web-svc/httpserver/obsidian_handler_test.go
git -C . commit -m "feat(web-svc): add write/create/rename routes for obsidian files"
```

---

## Task 6: Wire `main.go` and config files

**Files:**
- Modify: `web-svc/main.go`
- Modify: `config/dev.json`
- Modify: `config/prod.json`

**Interfaces:**
- Consumes: `config.Config.ObsidianRoot` (Task 1), `httpserver.Config.ObsidianRoot` (Task 4).
- Produces: nothing further downstream — this is the last backend task, wiring everything built in Tasks 1-5 into the running service.

- [ ] **Step 1: Wire `ObsidianRoot` through `main.go`**

In `web-svc/main.go`, add `ObsidianRoot: cfg.ObsidianRoot,` to the `httpserver.Config{...}` literal:

```go
	srv := httpserver.New(cfg.HTTPPort, httpserver.Config{
		CORSAllowedOrigin: cfg.CORSAllowedOrigin,
		PerceptionSvcURL:  cfg.PerceptionSvcURL,
		MemorySvcURL:      cfg.MemorySvcURL,
		ThinkingSvcURL:    cfg.ThinkingSvcURL,
		ActionSvcURL:      cfg.ActionSvcURL,
		ReportsRoot:       cfg.SoulmanRoot,
		ObsidianRoot:      cfg.ObsidianRoot,
	}, verifier)
```

- [ ] **Step 2: Add `obsidian_root` to both environment config files**

In `config/dev.json`, add `"obsidian_root"` to the `"web"` block:

```json
  "web": {
    "owner_email": "breynisson@gmail.com",
    "cors_allowed_origin": "http://localhost:5190",
    "perception_svc_url": "http://localhost:9011",
    "memory_svc_url": "http://localhost:9012",
    "thinking_svc_url": "http://localhost:9013",
    "action_svc_url": "http://localhost:9014",
    "obsidian_root": "C:\\Users\\Lenovo\\Documents\\obsidian"
  }
```

In `config/prod.json`, add the same field to its `"web"` block:

```json
  "web": {
    "owner_email": "breynisson@gmail.com",
    "cors_allowed_origin": "https://soulman.breynisson.org",
    "perception_svc_url": "http://localhost:9001",
    "memory_svc_url": "http://localhost:9002",
    "thinking_svc_url": "http://localhost:9003",
    "action_svc_url": "http://localhost:9004",
    "obsidian_root": "C:\\Users\\Lenovo\\Documents\\obsidian"
  }
```

- [ ] **Step 3: Verify the whole module builds and all tests pass**

Run: `go -C web-svc build ./...`
Expected: exits 0, no errors.

Run: `go -C web-svc test ./...`
Expected: PASS across `auth`, `config`, `httpserver`, `obsidian`, `reports`.

- [ ] **Step 4: Commit**

```bash
git -C . add web-svc/main.go config/dev.json config/prod.json
git -C . commit -m "feat(web-svc): wire obsidian_root into main.go and env configs"
```

---

## Task 7: `web/src/api.ts` — Obsidian API client functions

**Files:**
- Modify: `web/src/api.ts`
- Modify: `web/src/api.test.ts`

**Interfaces:**
- Consumes: `getJSON` (existing, same file), `ApiError` (existing, same file).
- Produces: `getObsidianFolders(token)`, `getObsidianFiles(token, folder)`, `getObsidianFile(token, folder, file)`, `saveObsidianFile(token, folder, file, content)`, `createObsidianFile(token, folder, file, content)`, `renameObsidianFile(token, folder, file, newName)` — all consumed by components in Tasks 8-15.

- [ ] **Step 1: Write the failing tests**

Append to `web/src/api.test.ts`. First update the import line at the top to include the new functions:

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
  ApiError,
} from './api';
```

Then append these test blocks at the end of the file:

```ts
describe('getObsidianFolders', () => {
  it('calls the obsidian/folders endpoint', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: true, status: 200, json: async () => ({ folders: ['soulman'] }) });

    const result = await getObsidianFolders('tok-abc');

    expect(result.folders).toEqual(['soulman']);
    const [url] = mockFetch.mock.calls[0];
    expect(url).toContain('/api/obsidian/folders');
  });
});

describe('getObsidianFiles', () => {
  it('passes the folder query param', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: true, status: 200, json: async () => ({ files: [] }) });

    await getObsidianFiles('tok-abc', 'soulman');

    const [url] = mockFetch.mock.calls[0];
    expect(url).toContain('/api/obsidian/files');
    expect(url).toContain('folder=soulman');
  });
});

describe('getObsidianFile', () => {
  it('passes folder and file query params', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: true, status: 200, json: async () => ({ content: 'hi' }) });

    const result = await getObsidianFile('tok-abc', 'soulman', 'NOTES.md');

    expect(result.content).toBe('hi');
    const [url] = mockFetch.mock.calls[0];
    expect(url).toContain('folder=soulman');
    expect(url).toContain('file=NOTES.md');
  });
});

describe('saveObsidianFile', () => {
  it('sends a PUT with the folder/file/content body', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: true, status: 200, json: async () => ({}) });

    await saveObsidianFile('tok-abc', 'soulman', 'NOTES.md', 'new content');

    const [url, options] = mockFetch.mock.calls[0];
    expect(url).toBe('/api/obsidian/file');
    expect(options.method).toBe('PUT');
    expect(JSON.parse(options.body)).toEqual({ folder: 'soulman', file: 'NOTES.md', content: 'new content' });
  });

  it('throws ApiError on a non-2xx response', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: false, status: 404, json: async () => ({}) });

    await expect(saveObsidianFile('tok-abc', 'soulman', 'missing.md', 'x')).rejects.toThrow(ApiError);
  });
});

describe('createObsidianFile', () => {
  it('sends a POST with the folder/file/content body', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: true, status: 200, json: async () => ({}) });

    await createObsidianFile('tok-abc', 'soulman', 'new.md', '');

    const [url, options] = mockFetch.mock.calls[0];
    expect(url).toBe('/api/obsidian/file');
    expect(options.method).toBe('POST');
    expect(JSON.parse(options.body)).toEqual({ folder: 'soulman', file: 'new.md', content: '' });
  });
});

describe('renameObsidianFile', () => {
  it('sends a POST to the rename endpoint with new_name', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: true, status: 200, json: async () => ({}) });

    await renameObsidianFile('tok-abc', 'soulman', 'old.md', 'new.md');

    const [url, options] = mockFetch.mock.calls[0];
    expect(url).toBe('/api/obsidian/file/rename');
    expect(options.method).toBe('POST');
    expect(JSON.parse(options.body)).toEqual({ folder: 'soulman', file: 'old.md', new_name: 'new.md' });
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npm --prefix web run test -- api.test.ts`
Expected: FAIL — the new exports don't exist yet in `api.ts` (import error).

- [ ] **Step 3: Write the implementation**

Append to `web/src/api.ts`:

```ts
export interface ObsidianFolders {
  folders: string[];
}

export interface ObsidianFiles {
  files: string[];
}

export interface ObsidianFileContent {
  content: string;
}

async function mutateJSON(
  method: 'POST' | 'PUT',
  path: string,
  token: string | null,
  body: unknown,
): Promise<void> {
  const response = await fetch(path, {
    method,
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    body: JSON.stringify(body),
  });
  if (!response.ok) {
    throw new ApiError(response.status, `${path} failed (${response.status})`);
  }
}

export const getObsidianFolders = (token: string | null): Promise<ObsidianFolders> =>
  getJSON('/api/obsidian/folders', token);

export const getObsidianFiles = (token: string | null, folder: string): Promise<ObsidianFiles> =>
  getJSON(`/api/obsidian/files?folder=${encodeURIComponent(folder)}`, token);

export const getObsidianFile = (
  token: string | null,
  folder: string,
  file: string,
): Promise<ObsidianFileContent> =>
  getJSON(`/api/obsidian/file?folder=${encodeURIComponent(folder)}&file=${encodeURIComponent(file)}`, token);

export const saveObsidianFile = (
  token: string | null,
  folder: string,
  file: string,
  content: string,
): Promise<void> => mutateJSON('PUT', '/api/obsidian/file', token, { folder, file, content });

export const createObsidianFile = (
  token: string | null,
  folder: string,
  file: string,
  content: string,
): Promise<void> => mutateJSON('POST', '/api/obsidian/file', token, { folder, file, content });

export const renameObsidianFile = (
  token: string | null,
  folder: string,
  file: string,
  newName: string,
): Promise<void> =>
  mutateJSON('POST', '/api/obsidian/file/rename', token, { folder, file, new_name: newName });
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npm --prefix web run test -- api.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git -C . add web/src/api.ts web/src/api.test.ts
git -C . commit -m "feat(web): add obsidian API client functions"
```

---

## Task 8: `ObsidianFileEditor.tsx` — plain-text editor with Save/discard

**Files:**
- Create: `web/src/components/ObsidianFileEditor.tsx`
- Test: `web/src/components/ObsidianFileEditor.test.tsx`

**Interfaces:**
- Consumes: `getAccessToken` (`web/src/auth.ts`), `saveObsidianFile` (Task 7).
- Produces: `ObsidianFileEditor({folder, file, initialContent, onSaved, onCancel})` React component — `onSaved: (content: string) => void` fires after a successful save with the new content; `onCancel: () => void` fires on discard, no save attempted. Consumed by `ObsidianFileViewer` in Task 9.

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/ObsidianFileEditor.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockSaveObsidianFile = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return { ...actual, saveObsidianFile: (...args: unknown[]) => mockSaveObsidianFile(...args) };
});

beforeEach(() => vi.clearAllMocks());

describe('ObsidianFileEditor', () => {
  it('saves edited content and calls onSaved with the new value', async () => {
    mockSaveObsidianFile.mockResolvedValue(undefined);
    const onSaved = vi.fn();
    const { ObsidianFileEditor } = await import('./ObsidianFileEditor');
    render(
      <ObsidianFileEditor folder="soulman" file="NOTES.md" initialContent="old" onSaved={onSaved} onCancel={vi.fn()} />,
    );

    const textarea = screen.getByRole('textbox');
    await userEvent.clear(textarea);
    await userEvent.type(textarea, 'new content');
    await userEvent.click(screen.getByRole('button', { name: /save/i }));

    expect(mockSaveObsidianFile).toHaveBeenCalledWith('tok-abc', 'soulman', 'NOTES.md', 'new content');
    expect(onSaved).toHaveBeenCalledWith('new content');
  });

  it('calls onCancel without saving when the close button is clicked', async () => {
    const onCancel = vi.fn();
    const { ObsidianFileEditor } = await import('./ObsidianFileEditor');
    render(
      <ObsidianFileEditor folder="soulman" file="NOTES.md" initialContent="old" onSaved={vi.fn()} onCancel={onCancel} />,
    );

    await userEvent.click(screen.getByRole('button', { name: /close without saving/i }));

    expect(mockSaveObsidianFile).not.toHaveBeenCalled();
    expect(onCancel).toHaveBeenCalled();
  });

  it('shows an error and stays in edit mode when save fails', async () => {
    mockSaveObsidianFile.mockRejectedValue(new Error('network error'));
    const onSaved = vi.fn();
    const { ObsidianFileEditor } = await import('./ObsidianFileEditor');
    render(
      <ObsidianFileEditor folder="soulman" file="NOTES.md" initialContent="old" onSaved={onSaved} onCancel={vi.fn()} />,
    );

    await userEvent.click(screen.getByRole('button', { name: /save/i }));

    expect(await screen.findByText(/save failed/i)).toBeInTheDocument();
    expect(onSaved).not.toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npm --prefix web run test -- ObsidianFileEditor`
Expected: FAIL — `./ObsidianFileEditor` doesn't exist yet.

- [ ] **Step 3: Write the implementation**

Create `web/src/components/ObsidianFileEditor.tsx`:

```tsx
import { useState } from 'react';
import { getAccessToken } from '../auth';
import { saveObsidianFile } from '../api';

export function ObsidianFileEditor({
  folder,
  file,
  initialContent,
  onSaved,
  onCancel,
}: {
  folder: string;
  file: string;
  initialContent: string;
  onSaved: (content: string) => void;
  onCancel: () => void;
}) {
  const [value, setValue] = useState(initialContent);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const handleSave = async () => {
    setSaving(true);
    setError(null);
    const token = await getAccessToken();
    try {
      await saveObsidianFile(token, folder, file, value);
      onSaved(value);
    } catch {
      setError('Save failed');
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="rounded border bg-white p-4">
      <div className="mb-2 flex items-center justify-between">
        <h3 className="font-medium">{file}</h3>
        <div className="flex items-center gap-2">
          <button onClick={handleSave} disabled={saving} title="Save" aria-label="Save">
            <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2Z" />
              <path d="M17 21v-8H7v8" />
              <path d="M7 3v5h8" />
            </svg>
          </button>
          <button onClick={onCancel} title="Close without saving" aria-label="Close without saving">
            <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M18 6 6 18" />
              <path d="M6 6l12 12" />
            </svg>
          </button>
        </div>
      </div>
      {error && <p className="mb-2 text-sm text-red-600">{error}</p>}
      <textarea
        value={value}
        onChange={(e) => setValue(e.target.value)}
        className="h-96 w-full rounded border p-2 font-mono text-sm"
      />
    </div>
  );
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npm --prefix web run test -- ObsidianFileEditor`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git -C . add web/src/components/ObsidianFileEditor.tsx web/src/components/ObsidianFileEditor.test.tsx
git -C . commit -m "feat(web): add ObsidianFileEditor component"
```

---

## Task 9: `ObsidianFileViewer.tsx` — view content, pen "Edit" icon, mode toggle

**Files:**
- Create: `web/src/components/ObsidianFileViewer.tsx`
- Test: `web/src/components/ObsidianFileViewer.test.tsx`
- Modify: `web/package.json` (new dependency)

**Interfaces:**
- Consumes: `getAccessToken` (`web/src/auth.ts`), `getObsidianFile` (Task 7), `ObsidianFileEditor` (Task 8), `react-markdown` (new dependency).
- Produces: `ObsidianFileViewer({folder, file})` React component. Consumed by `ObsidianFileList` in Task 10.

- [ ] **Step 1: Install `react-markdown`**

Run: `npm --prefix web install react-markdown`
Expected: `web/package.json`'s `dependencies` gains `react-markdown`, `web/package-lock.json` updates.

- [ ] **Step 2: Write the failing tests**

Create `web/src/components/ObsidianFileViewer.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockGetObsidianFile = vi.fn();
const mockSaveObsidianFile = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    getObsidianFile: (...args: unknown[]) => mockGetObsidianFile(...args),
    saveObsidianFile: (...args: unknown[]) => mockSaveObsidianFile(...args),
  };
});

beforeEach(() => vi.clearAllMocks());

describe('ObsidianFileViewer', () => {
  it('renders markdown content for a .md file', async () => {
    mockGetObsidianFile.mockResolvedValue({ content: '# Heading' });
    const { ObsidianFileViewer } = await import('./ObsidianFileViewer');
    render(<ObsidianFileViewer folder="soulman" file="NOTES.md" />);

    expect(await screen.findByRole('heading', { name: 'Heading' })).toBeInTheDocument();
  });

  it('renders plain text content for a .txt file', async () => {
    mockGetObsidianFile.mockResolvedValue({ content: 'plain text here' });
    const { ObsidianFileViewer } = await import('./ObsidianFileViewer');
    render(<ObsidianFileViewer folder="soulman" file="todo.txt" />);

    expect(await screen.findByText('plain text here')).toBeInTheDocument();
  });

  it('shows an error banner when the fetch fails', async () => {
    mockGetObsidianFile.mockRejectedValue(new Error('network error'));
    const { ObsidianFileViewer } = await import('./ObsidianFileViewer');
    render(<ObsidianFileViewer folder="soulman" file="NOTES.md" />);

    expect(await screen.findByText(/file unavailable/i)).toBeInTheDocument();
  });

  it('switches to the editor when the edit button is clicked, and back on cancel', async () => {
    mockGetObsidianFile.mockResolvedValue({ content: 'hello' });
    const { ObsidianFileViewer } = await import('./ObsidianFileViewer');
    render(<ObsidianFileViewer folder="soulman" file="todo.txt" />);

    await screen.findByText('hello');
    await userEvent.click(screen.getByRole('button', { name: /edit/i }));

    expect(screen.getByRole('textbox')).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: /close without saving/i }));

    expect(await screen.findByText('hello')).toBeInTheDocument();
  });
});
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `npm --prefix web run test -- ObsidianFileViewer`
Expected: FAIL — `./ObsidianFileViewer` doesn't exist yet.

- [ ] **Step 4: Write the implementation**

Create `web/src/components/ObsidianFileViewer.tsx`:

```tsx
import { useEffect, useState } from 'react';
import ReactMarkdown from 'react-markdown';
import { getAccessToken } from '../auth';
import { getObsidianFile } from '../api';
import { ObsidianFileEditor } from './ObsidianFileEditor';

export function ObsidianFileViewer({ folder, file }: { folder: string; file: string }) {
  const [content, setContent] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [mode, setMode] = useState<'view' | 'edit'>('view');

  useEffect(() => {
    let active = true;
    setContent(null);
    setError(null);
    setMode('view');
    (async () => {
      const token = await getAccessToken();
      try {
        const data = await getObsidianFile(token, folder, file);
        if (active) setContent(data.content);
      } catch {
        if (active) setError('File unavailable');
      }
    })();
    return () => {
      active = false;
    };
  }, [folder, file]);

  if (mode === 'edit' && content !== null) {
    return (
      <ObsidianFileEditor
        folder={folder}
        file={file}
        initialContent={content}
        onSaved={(newContent) => {
          setContent(newContent);
          setMode('view');
        }}
        onCancel={() => setMode('view')}
      />
    );
  }

  const isMarkdown = file.toLowerCase().endsWith('.md');

  return (
    <div className="rounded border bg-white p-4">
      <div className="mb-2 flex items-center justify-between">
        <h3 className="font-medium">{file}</h3>
        {content !== null && (
          <button onClick={() => setMode('edit')} title="Edit" aria-label="Edit">
            <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M17 3a2.85 2.83 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z" />
            </svg>
          </button>
        )}
      </div>
      {error && <p className="text-sm text-red-600">{error}</p>}
      {!error && content === null && <p className="text-sm text-gray-500">Loading...</p>}
      {!error && content !== null && isMarkdown && (
        <div className="text-sm">
          <ReactMarkdown>{content}</ReactMarkdown>
        </div>
      )}
      {!error && content !== null && !isMarkdown && (
        <pre className="whitespace-pre-wrap text-sm">{content}</pre>
      )}
    </div>
  );
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `npm --prefix web run test -- ObsidianFileViewer`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git -C . add web/package.json web/package-lock.json web/src/components/ObsidianFileViewer.tsx web/src/components/ObsidianFileViewer.test.tsx
git -C . commit -m "feat(web): add ObsidianFileViewer component with react-markdown"
```

---

## Task 10: `ObsidianFileList.tsx` — list files, select to view

**Files:**
- Create: `web/src/components/ObsidianFileList.tsx`
- Test: `web/src/components/ObsidianFileList.test.tsx`

**Interfaces:**
- Consumes: `getAccessToken` (`web/src/auth.ts`), `getObsidianFiles` (Task 7), `ObsidianFileViewer` (Task 9).
- Produces: `ObsidianFileList({folder})` React component. Consumed by `ObsidianFolderList` in Task 13 (this task's own version has no create/rename yet — those are added in Tasks 11-12 on top of this same file).

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/ObsidianFileList.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockGetObsidianFiles = vi.fn();
const mockGetObsidianFile = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    getObsidianFiles: (...args: unknown[]) => mockGetObsidianFiles(...args),
    getObsidianFile: (...args: unknown[]) => mockGetObsidianFile(...args),
  };
});

beforeEach(() => vi.clearAllMocks());

describe('ObsidianFileList', () => {
  it('lists files for the given folder', async () => {
    mockGetObsidianFiles.mockResolvedValue({ files: ['NOTES.md', 'todo.txt'] });
    const { ObsidianFileList } = await import('./ObsidianFileList');
    render(<ObsidianFileList folder="soulman" />);

    expect(await screen.findByText('NOTES.md')).toBeInTheDocument();
    expect(screen.getByText('todo.txt')).toBeInTheDocument();
  });

  it('shows the file viewer when a file is selected', async () => {
    mockGetObsidianFiles.mockResolvedValue({ files: ['NOTES.md'] });
    mockGetObsidianFile.mockResolvedValue({ content: 'hello' });
    const { ObsidianFileList } = await import('./ObsidianFileList');
    render(<ObsidianFileList folder="soulman" />);

    await userEvent.click(await screen.findByText('NOTES.md'));

    expect(await screen.findByText('hello')).toBeInTheDocument();
  });

  it('shows an empty state when the folder has no files', async () => {
    mockGetObsidianFiles.mockResolvedValue({ files: [] });
    const { ObsidianFileList } = await import('./ObsidianFileList');
    render(<ObsidianFileList folder="soulman" />);

    expect(await screen.findByText(/no files/i)).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npm --prefix web run test -- ObsidianFileList`
Expected: FAIL — `./ObsidianFileList` doesn't exist yet.

- [ ] **Step 3: Write the implementation**

Create `web/src/components/ObsidianFileList.tsx`:

```tsx
import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { getObsidianFiles } from '../api';
import { ObsidianFileViewer } from './ObsidianFileViewer';

export function ObsidianFileList({ folder }: { folder: string }) {
  const [files, setFiles] = useState<string[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    setFiles(null);
    setError(null);
    setSelected(null);
    (async () => {
      const token = await getAccessToken();
      try {
        const data = await getObsidianFiles(token, folder);
        if (active) setFiles(data.files);
      } catch {
        if (active) setError('Files unavailable');
      }
    })();
    return () => {
      active = false;
    };
  }, [folder]);

  return (
    <div className="ml-4 mt-2">
      {error && <p className="text-sm text-red-600">{error}</p>}
      {!error && files === null && <p className="text-sm text-gray-500">Loading...</p>}
      {!error && files?.length === 0 && <p className="text-sm text-gray-500">No files</p>}
      {!error && files && files.length > 0 && (
        <ul className="space-y-1">
          {files.map((f) => (
            <li key={f}>
              <button
                onClick={() => setSelected(f)}
                className={`text-sm underline ${selected === f ? 'font-semibold' : ''}`}
              >
                {f}
              </button>
            </li>
          ))}
        </ul>
      )}
      {selected && (
        <div className="mt-2">
          <ObsidianFileViewer folder={folder} file={selected} />
        </div>
      )}
    </div>
  );
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npm --prefix web run test -- ObsidianFileList`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git -C . add web/src/components/ObsidianFileList.tsx web/src/components/ObsidianFileList.test.tsx
git -C . commit -m "feat(web): add ObsidianFileList component"
```

---

## Task 11: `ObsidianFileList.tsx` — create new file

**Files:**
- Modify: `web/src/components/ObsidianFileList.tsx`
- Modify: `web/src/components/ObsidianFileList.test.tsx`

**Interfaces:**
- Consumes: `createObsidianFile` (Task 7), added to this file's imports.
- Produces: no new exports — extends the existing `ObsidianFileList` component with a "+ New file" control.

- [ ] **Step 1: Write the failing tests**

In `web/src/components/ObsidianFileList.test.tsx`, add `createObsidianFile` to the mocked functions. Replace the `vi.mock('../api', ...)` block with:

```tsx
const mockGetObsidianFiles = vi.fn();
const mockGetObsidianFile = vi.fn();
const mockCreateObsidianFile = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    getObsidianFiles: (...args: unknown[]) => mockGetObsidianFiles(...args),
    getObsidianFile: (...args: unknown[]) => mockGetObsidianFile(...args),
    createObsidianFile: (...args: unknown[]) => mockCreateObsidianFile(...args),
  };
});
```

Append these tests:

```tsx
describe('ObsidianFileList create', () => {
  it('creates a new file and selects it', async () => {
    mockGetObsidianFiles.mockResolvedValueOnce({ files: [] }).mockResolvedValueOnce({ files: ['new.md'] });
    mockCreateObsidianFile.mockResolvedValue(undefined);
    mockGetObsidianFile.mockResolvedValue({ content: '' });
    const { ObsidianFileList } = await import('./ObsidianFileList');
    render(<ObsidianFileList folder="soulman" />);

    await userEvent.click(await screen.findByText(/new file/i));
    await userEvent.type(screen.getByPlaceholderText('filename.md'), 'new.md');
    await userEvent.click(screen.getByRole('button', { name: /^create$/i }));

    expect(mockCreateObsidianFile).toHaveBeenCalledWith('tok-abc', 'soulman', 'new.md', '');
    expect(await screen.findByText('new.md')).toBeInTheDocument();
  });

  it('shows an error when create fails', async () => {
    mockGetObsidianFiles.mockResolvedValue({ files: [] });
    mockCreateObsidianFile.mockRejectedValue(new Error('conflict'));
    const { ObsidianFileList } = await import('./ObsidianFileList');
    render(<ObsidianFileList folder="soulman" />);

    await userEvent.click(await screen.findByText(/new file/i));
    await userEvent.type(screen.getByPlaceholderText('filename.md'), 'dup.md');
    await userEvent.click(screen.getByRole('button', { name: /^create$/i }));

    expect(await screen.findByText(/could not create file/i)).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npm --prefix web run test -- ObsidianFileList`
Expected: FAIL — there's no "+ New file" control yet.

- [ ] **Step 3: Extend the implementation**

In `web/src/components/ObsidianFileList.tsx`, update the import line to add `createObsidianFile`:

```tsx
import { getObsidianFiles, createObsidianFile } from '../api';
```

Add state and a handler inside the component, after the existing `useEffect`:

```tsx
  const [creating, setCreating] = useState(false);
  const [newFileName, setNewFileName] = useState('');
  const [createError, setCreateError] = useState<string | null>(null);

  const handleCreate = async () => {
    setCreateError(null);
    const token = await getAccessToken();
    try {
      await createObsidianFile(token, folder, newFileName, '');
      const data = await getObsidianFiles(token, folder);
      setFiles(data.files);
      setSelected(newFileName);
      setCreating(false);
      setNewFileName('');
    } catch {
      setCreateError('Could not create file');
    }
  };
```

Add the create UI at the end of the returned JSX, right before the closing `</div>` (after the `{selected && ...}` block):

```tsx
      {creating ? (
        <div className="mt-2 flex items-center gap-2">
          <input
            value={newFileName}
            onChange={(e) => setNewFileName(e.target.value)}
            placeholder="filename.md"
            className="rounded border px-2 py-1 text-sm"
          />
          <button onClick={handleCreate} className="text-sm underline">
            Create
          </button>
          <button
            onClick={() => {
              setCreating(false);
              setNewFileName('');
              setCreateError(null);
            }}
            className="text-sm underline"
          >
            Cancel
          </button>
        </div>
      ) : (
        <button onClick={() => setCreating(true)} className="mt-2 text-sm underline">
          + New file
        </button>
      )}
      {createError && <p className="text-sm text-red-600">{createError}</p>}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npm --prefix web run test -- ObsidianFileList`
Expected: PASS (all `ObsidianFileList` tests, including the ones from Task 10).

- [ ] **Step 5: Commit**

```bash
git -C . add web/src/components/ObsidianFileList.tsx web/src/components/ObsidianFileList.test.tsx
git -C . commit -m "feat(web): add create-file support to ObsidianFileList"
```

---

## Task 12: `ObsidianFileList.tsx` — rename file

**Files:**
- Modify: `web/src/components/ObsidianFileList.tsx`
- Modify: `web/src/components/ObsidianFileList.test.tsx`

**Interfaces:**
- Consumes: `renameObsidianFile` (Task 7), added to this file's imports.
- Produces: no new exports — extends the existing `ObsidianFileList` component with a per-file rename affordance.

- [ ] **Step 1: Write the failing tests**

In `web/src/components/ObsidianFileList.test.tsx`, add `renameObsidianFile` to the mock block:

```tsx
const mockGetObsidianFiles = vi.fn();
const mockGetObsidianFile = vi.fn();
const mockCreateObsidianFile = vi.fn();
const mockRenameObsidianFile = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    getObsidianFiles: (...args: unknown[]) => mockGetObsidianFiles(...args),
    getObsidianFile: (...args: unknown[]) => mockGetObsidianFile(...args),
    createObsidianFile: (...args: unknown[]) => mockCreateObsidianFile(...args),
    renameObsidianFile: (...args: unknown[]) => mockRenameObsidianFile(...args),
  };
});
```

Append these tests:

```tsx
describe('ObsidianFileList rename', () => {
  it('renames a file', async () => {
    mockGetObsidianFiles.mockResolvedValueOnce({ files: ['old.md'] }).mockResolvedValueOnce({ files: ['new.md'] });
    mockRenameObsidianFile.mockResolvedValue(undefined);
    const { ObsidianFileList } = await import('./ObsidianFileList');
    render(<ObsidianFileList folder="soulman" />);

    await userEvent.click(await screen.findByLabelText('Rename old.md'));
    const input = screen.getByDisplayValue('old.md');
    await userEvent.clear(input);
    await userEvent.type(input, 'new.md');
    await userEvent.click(screen.getByRole('button', { name: /confirm/i }));

    expect(mockRenameObsidianFile).toHaveBeenCalledWith('tok-abc', 'soulman', 'old.md', 'new.md');
    expect(await screen.findByText('new.md')).toBeInTheDocument();
  });

  it('shows an error when rename fails', async () => {
    mockGetObsidianFiles.mockResolvedValue({ files: ['old.md'] });
    mockRenameObsidianFile.mockRejectedValue(new Error('conflict'));
    const { ObsidianFileList } = await import('./ObsidianFileList');
    render(<ObsidianFileList folder="soulman" />);

    await userEvent.click(await screen.findByLabelText('Rename old.md'));
    await userEvent.click(screen.getByRole('button', { name: /confirm/i }));

    expect(await screen.findByText(/could not rename file/i)).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npm --prefix web run test -- ObsidianFileList`
Expected: FAIL — there's no rename affordance yet.

- [ ] **Step 3: Extend the implementation**

In `web/src/components/ObsidianFileList.tsx`, update the import to add `renameObsidianFile`:

```tsx
import { getObsidianFiles, createObsidianFile, renameObsidianFile } from '../api';
```

Add state and a handler, after the `handleCreate` function:

```tsx
  const [renamingFile, setRenamingFile] = useState<string | null>(null);
  const [renameValue, setRenameValue] = useState('');
  const [renameError, setRenameError] = useState<string | null>(null);

  const handleRename = async (oldName: string) => {
    setRenameError(null);
    const token = await getAccessToken();
    try {
      await renameObsidianFile(token, folder, oldName, renameValue);
      const data = await getObsidianFiles(token, folder);
      setFiles(data.files);
      if (selected === oldName) setSelected(renameValue);
      setRenamingFile(null);
    } catch {
      setRenameError('Could not rename file');
    }
  };
```

Replace the file-list `<ul>` block with a version that supports inline renaming per row:

```tsx
        <ul className="space-y-1">
          {files.map((f) => (
            <li key={f} className="flex items-center gap-2">
              {renamingFile === f ? (
                <>
                  <input
                    value={renameValue}
                    onChange={(e) => setRenameValue(e.target.value)}
                    className="rounded border px-1 py-0.5 text-sm"
                  />
                  <button onClick={() => handleRename(f)} className="text-xs underline">
                    Confirm
                  </button>
                  <button onClick={() => setRenamingFile(null)} className="text-xs underline">
                    Cancel
                  </button>
                </>
              ) : (
                <>
                  <button
                    onClick={() => setSelected(f)}
                    className={`text-sm underline ${selected === f ? 'font-semibold' : ''}`}
                  >
                    {f}
                  </button>
                  <button
                    onClick={() => {
                      setRenamingFile(f);
                      setRenameValue(f);
                    }}
                    title="Rename"
                    aria-label={`Rename ${f}`}
                    className="text-xs text-gray-400"
                  >
                    ✎
                  </button>
                </>
              )}
            </li>
          ))}
        </ul>
```

Add `{renameError && <p className="text-sm text-red-600">{renameError}</p>}` right after the `{createError && ...}` line.

- [ ] **Step 4: Run tests to verify they pass**

Run: `npm --prefix web run test -- ObsidianFileList`
Expected: PASS (all `ObsidianFileList` tests from Tasks 10-12).

- [ ] **Step 5: Commit**

```bash
git -C . add web/src/components/ObsidianFileList.tsx web/src/components/ObsidianFileList.test.tsx
git -C . commit -m "feat(web): add rename support to ObsidianFileList"
```

---

## Task 13: `ObsidianFolderList.tsx` — folder accordion

**Files:**
- Create: `web/src/components/ObsidianFolderList.tsx`
- Test: `web/src/components/ObsidianFolderList.test.tsx`

**Interfaces:**
- Consumes: `getAccessToken` (`web/src/auth.ts`), `getObsidianFolders` (Task 7), `ObsidianFileList` (Tasks 10-12).
- Produces: `ObsidianFolderList()` React component (no props). Consumed by `ObsidianPage` in Task 14.

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/ObsidianFolderList.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockGetObsidianFolders = vi.fn();
const mockGetObsidianFiles = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    getObsidianFolders: (...args: unknown[]) => mockGetObsidianFolders(...args),
    getObsidianFiles: (...args: unknown[]) => mockGetObsidianFiles(...args),
  };
});

beforeEach(() => vi.clearAllMocks());

describe('ObsidianFolderList', () => {
  it('lists folders and expands one to show its files', async () => {
    mockGetObsidianFolders.mockResolvedValue({ folders: ['brynjar-obsidian', 'soulman'] });
    mockGetObsidianFiles.mockResolvedValue({ files: ['NOTES.md'] });
    const { ObsidianFolderList } = await import('./ObsidianFolderList');
    render(<ObsidianFolderList />);

    await userEvent.click(await screen.findByText('soulman'));

    expect(await screen.findByText('NOTES.md')).toBeInTheDocument();
  });

  it('collapses the previously open folder when a different one is selected', async () => {
    mockGetObsidianFolders.mockResolvedValue({ folders: ['brynjar-obsidian', 'soulman'] });
    mockGetObsidianFiles.mockResolvedValue({ files: ['NOTES.md'] });
    const { ObsidianFolderList } = await import('./ObsidianFolderList');
    render(<ObsidianFolderList />);

    await userEvent.click(await screen.findByText('soulman'));
    await screen.findByText('NOTES.md');
    mockGetObsidianFiles.mockResolvedValue({ files: ['diary.md'] });
    await userEvent.click(screen.getByText('brynjar-obsidian'));

    expect(await screen.findByText('diary.md')).toBeInTheDocument();
    expect(mockGetObsidianFiles).toHaveBeenCalledTimes(2);
  });

  it('shows an error banner when folders fail to load', async () => {
    mockGetObsidianFolders.mockRejectedValue(new Error('network error'));
    const { ObsidianFolderList } = await import('./ObsidianFolderList');
    render(<ObsidianFolderList />);

    expect(await screen.findByText(/folders unavailable/i)).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npm --prefix web run test -- ObsidianFolderList`
Expected: FAIL — `./ObsidianFolderList` doesn't exist yet.

- [ ] **Step 3: Write the implementation**

Create `web/src/components/ObsidianFolderList.tsx`:

```tsx
import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { getObsidianFolders } from '../api';
import { ObsidianFileList } from './ObsidianFileList';

export function ObsidianFolderList() {
  const [folders, setFolders] = useState<string[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [expanded, setExpanded] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    (async () => {
      const token = await getAccessToken();
      try {
        const data = await getObsidianFolders(token);
        if (active) setFolders(data.folders);
      } catch {
        if (active) setError('Folders unavailable');
      }
    })();
    return () => {
      active = false;
    };
  }, []);

  return (
    <div>
      {error && <p className="text-sm text-red-600">{error}</p>}
      {!error && folders === null && <p className="text-sm text-gray-500">Loading...</p>}
      {!error && folders && (
        <ul className="space-y-1">
          {folders.map((f) => (
            <li key={f}>
              <button
                onClick={() => setExpanded(expanded === f ? null : f)}
                className="text-sm font-medium underline"
              >
                {f}
              </button>
              {expanded === f && <ObsidianFileList folder={f} />}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npm --prefix web run test -- ObsidianFolderList`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git -C . add web/src/components/ObsidianFolderList.tsx web/src/components/ObsidianFolderList.test.tsx
git -C . commit -m "feat(web): add ObsidianFolderList accordion component"
```

---

## Task 14: `ObsidianPage.tsx` — top-level page

**Files:**
- Create: `web/src/components/ObsidianPage.tsx`
- Test: `web/src/components/ObsidianPage.test.tsx`

**Interfaces:**
- Consumes: `ObsidianFolderList` (Task 13).
- Produces: `ObsidianPage({onBack})` React component, `onBack: () => void`. Consumed by `App.tsx` in Task 15.

- [ ] **Step 1: Write the failing test**

Create `web/src/components/ObsidianPage.test.tsx`:

```tsx
import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return { ...actual, getObsidianFolders: vi.fn().mockResolvedValue({ folders: [] }) };
});

describe('ObsidianPage', () => {
  it('calls onBack when the back link is clicked', async () => {
    const onBack = vi.fn();
    const { ObsidianPage } = await import('./ObsidianPage');
    render(<ObsidianPage onBack={onBack} />);

    await userEvent.click(screen.getByText(/soulman/i));

    expect(onBack).toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm --prefix web run test -- ObsidianPage`
Expected: FAIL — `./ObsidianPage` doesn't exist yet.

- [ ] **Step 3: Write the implementation**

Create `web/src/components/ObsidianPage.tsx`:

```tsx
import { ObsidianFolderList } from './ObsidianFolderList';

export function ObsidianPage({ onBack }: { onBack: () => void }) {
  return (
    <div className="min-h-screen bg-gray-50 p-6">
      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Obsidian</h1>
        <button onClick={onBack} className="text-sm text-gray-500 underline">
          ← Soulman
        </button>
      </div>
      <ObsidianFolderList />
    </div>
  );
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npm --prefix web run test -- ObsidianPage`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git -C . add web/src/components/ObsidianPage.tsx web/src/components/ObsidianPage.test.tsx
git -C . commit -m "feat(web): add ObsidianPage component"
```

---

## Task 15: Wire the Obsidian page into `App.tsx` and `Dashboard.tsx`

**Files:**
- Modify: `web/src/App.tsx`
- Modify: `web/src/components/Dashboard.tsx`
- Modify: `web/src/App.test.tsx`

**Interfaces:**
- Consumes: `ObsidianPage` (Task 14).
- Produces: nothing further downstream — this is the last task, completing the feature end-to-end.

- [ ] **Step 1: Write the failing test**

In `web/src/App.test.tsx`, add `import userEvent from '@testing-library/user-event';` to the top imports, then append this test inside the existing `describe('App', ...)` block (after the "shows the dashboard" test):

```tsx
  it('switches to the obsidian page and back via the header links', async () => {
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
    await userEvent.click(screen.getByRole('button', { name: /obsidian/i }));

    expect(await screen.findByRole('heading', { name: /obsidian/i })).toBeInTheDocument();

    await userEvent.click(screen.getByText(/soulman/i));

    expect(await screen.findByText(/soulman dashboard/i)).toBeInTheDocument();
  });
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm --prefix web run test -- App.test.tsx`
Expected: FAIL — there's no "Obsidian" button on the dashboard yet.

- [ ] **Step 3: Wire `Dashboard.tsx`**

In `web/src/components/Dashboard.tsx`, add an `onOpenObsidian` prop and an "Obsidian" link next to "Sign out":

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
}: {
  initialStatus: ServiceStatus | null;
  onSignOut: () => void;
  onOpenObsidian: () => void;
}) {
  return (
    <div className="min-h-screen bg-gray-50 p-6">
      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Soulman Dashboard</h1>
        <div className="flex items-center gap-4">
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

- [ ] **Step 4: Wire `App.tsx`**

In `web/src/App.tsx`, import `ObsidianPage`, add `'obsidian'` to `ViewState`, add a branch for it, and pass `onOpenObsidian` to `Dashboard`:

```tsx
import { useEffect, useState } from 'react';
import { useAuth, getAccessToken } from './auth';
import { getStatus, ApiError, type ServiceStatus } from './api';
import { LoginScreen } from './components/LoginScreen';
import { RestrictedScreen } from './components/RestrictedScreen';
import { Dashboard } from './components/Dashboard';
import { ObsidianPage } from './components/ObsidianPage';

type ViewState = 'loading' | 'login' | 'restricted' | 'dashboard' | 'obsidian';

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
  if (view === 'obsidian') return <ObsidianPage onBack={() => setView('dashboard')} />;

  return <Dashboard initialStatus={status} onSignOut={signOut} onOpenObsidian={() => setView('obsidian')} />;
}

export default App;
```

- [ ] **Step 5: Run test to verify it passes**

Run: `npm --prefix web run test -- App.test.tsx`
Expected: PASS

- [ ] **Step 6: Run the full frontend test suite**

Run: `npm --prefix web run test`
Expected: PASS across all test files.

- [ ] **Step 7: Commit**

```bash
git -C . add web/src/App.tsx web/src/App.test.tsx web/src/components/Dashboard.tsx
git -C . commit -m "feat(web): wire ObsidianPage into App and Dashboard navigation"
```

---

## Final Verification

- [ ] **Step 1: Run the full backend test suite**

Run: `go -C web-svc build ./...` then `go -C web-svc test ./...`
Expected: build succeeds, all tests PASS.

- [ ] **Step 2: Run the full frontend test suite and lint**

Run: `npm --prefix web run test` then `npm --prefix web run lint`
Expected: all tests PASS, lint reports no errors.

- [ ] **Step 3: Manual end-to-end check against dev**

With dev's `web-svc` and `web` running (`localhost:5190`), sign in, click "Obsidian", browse into `soulman`, open a `.md` file and a `.txt` file, click the pen icon, edit and Save, confirm the rendered view updates, create a new file, rename a file, refresh the page and confirm the rename/create persisted. Update `web-svc/NOTES.md` with anything unexpected found here (per this repo's convention of recording real incidents/gotchas, not just the design).

- [ ] **Step 4: Update `CLAUDE.md`**

Add a line to the `web-svc` row's spec list in the root `CLAUDE.md` referencing `docs/superpowers/specs/2026-08-07-obsidian-file-viewer-design.md`, and a short clause to `web-svc`'s bullet describing the new `/api/obsidian/*` routes — per this repo's CLAUDE.md instruction to update docs when committing a feature branch.

- [ ] **Step 5: Final commit**

```bash
git -C . add CLAUDE.md web-svc/NOTES.md
git -C . commit -m "docs: document obsidian file viewer feature in CLAUDE.md and NOTES.md"
```
