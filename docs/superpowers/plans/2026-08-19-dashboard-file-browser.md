# Dashboard File Browser (Download/Upload) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the owner browse a curated set of filesystem roots (Documents, Downloads) at arbitrary depth from the Soulman dashboard, downloading any file and uploading into the current folder — reusing `web-svc`'s existing owner-only JWT auth.

**Architecture:** An incremental extension of `web-svc` (no new service): a new `web-svc/filebrowser` package (path-validated list/resolve/save, mirroring `web-svc/obsidian`'s and `web-svc/claudesession`'s existing validation shape but extended to arbitrary depth), four new owner-JWT-gated routes in `web-svc/httpserver`, one new config field (`web.file_browser_roots`), and three new React components (`FileBrowser`, `FileRootList`, `FilesPage`) wired into `App.tsx`/`Dashboard.tsx` the same way Obsidian and Claude already are.

**Tech Stack:** Go 1.25 (`web-svc`, chi router, stdlib `testing`), React + TypeScript + Vite (`web`, Vitest + Testing Library + `@testing-library/user-event`).

**Spec:** `docs/superpowers/specs/2026-08-19-file-browser-design.md`

## Global Constraints

- Scoped to a configured allow-list of roots (`web.file_browser_roots`) — never an arbitrary absolute path. Starting roots: Documents, Downloads.
- No delete, rename, or folder creation. Browse + download + upload only.
- No file-extension restriction anywhere in this feature — "any file" is the point (unlike `obsidian`'s `.txt`/`.md` gate).
- No in-browser preview or editing of file contents.
- Upload conflict resolution is a simple `overwrite` boolean — no versioning, no merge. Check-then-act is an accepted narrow TOCTOU race (single-owner tool), same acceptance `obsidian`'s rename/create already made.
- Upload body is capped at `maxUploadBytes = 200 << 20` (200MB), a package-level Go const in `files_handler.go` — deliberately not part of the config schema (YAGNI; bump in code if wrong).
- No pagination on directory listings in this iteration (YAGNI, same posture as `obsidian`'s no-size-cap call).
- Reuses `web-svc`'s existing owner-only-JWT auth (`s.verifier.Middleware`) — no new auth mechanism.

---

### Task 1: Config schema — `file_browser_roots`

**Files:**
- Modify: `common/sharedconfig/config.go` (add `FileBrowserRoot` type + `WebConfig.FileBrowserRoots` field, near `ClaudeProjectRoot`/`WebConfig` at lines 121-148)
- Modify: `web-svc/config/config.go` (add `Config.FileBrowserRoots` field, a required-non-empty check, and population in `Load()`'s return)
- Modify: `web-svc/config/config_test.go` (extend `validConfigJSON` fixture; add two tests mirroring `TestLoad_PopulatesClaudeProjectRoots`/`TestLoad_MissingClaudeProjectRoots_ReturnsError`)
- Modify: `config/dev.json` and `config/prod.json` (add `web.file_browser_roots`)

**Interfaces:**
- Produces: `sharedconfig.FileBrowserRoot{Label, Path string}`, `sharedconfig.WebConfig.FileBrowserRoots []FileBrowserRoot`, `config.Config.FileBrowserRoots []sharedconfig.FileBrowserRoot` — consumed by Task 10 (`main.go`) and, indirectly through `httpserver.Config`, by Task 6.

- [ ] **Step 1: Add `FileBrowserRoot` type and `WebConfig` field (no dedicated test — `sharedconfig` has none for `ClaudeProjectRoot` either; `web-svc/config` is where this gets validated and tested)**

In `common/sharedconfig/config.go`, after the `ClaudeProjectRoot` type (currently lines 121-129):

```go
// FileBrowserRoot is one curated root the file browser
// (web-svc/filebrowser) offers for browsing, download, and upload: a
// human-readable label (matched against a request's "root" field) and
// the filesystem path it corresponds to. A distinct type from
// ClaudeProjectRoot even though the shape is identical — they represent
// different concerns (small independent duplication over a shared type,
// consistent with this repo's existing preference — see
// web-svc/NOTES.md). See
// docs/superpowers/specs/2026-08-19-file-browser-design.md.
type FileBrowserRoot struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}
```

And add a field to `WebConfig` (currently lines 139-148), right after `ClaudeProjectRoots`:

```go
type WebConfig struct {
	OwnerEmail         string              `json:"owner_email"`
	CORSAllowedOrigin  string              `json:"cors_allowed_origin"`
	PerceptionSvcURL   string              `json:"perception_svc_url"`
	MemorySvcURL       string              `json:"memory_svc_url"`
	ThinkingSvcURL     string              `json:"thinking_svc_url"`
	ActionSvcURL       string              `json:"action_svc_url"`
	ObsidianRoot       string              `json:"obsidian_root"`
	ClaudeProjectRoots []ClaudeProjectRoot `json:"claude_project_roots"`
	FileBrowserRoots   []FileBrowserRoot   `json:"file_browser_roots"`
}
```

- [ ] **Step 2: Write failing tests in `web-svc/config/config_test.go`**

First, extend the `validConfigJSON` const (currently lines 20-33) so every existing test keeps passing once the new required-field check lands — add `file_browser_roots` alongside `claude_project_roots`:

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
    ],
    "file_browser_roots": [
      {"label": "Documents", "path": "C:\\Users\\Lenovo\\Documents"}
    ]
  }
}`
```

Then add these two tests at the end of the file:

```go
func TestLoad_PopulatesFileBrowserRoots(t *testing.T) {
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
	if len(cfg.FileBrowserRoots) != 1 {
		t.Fatalf("len(FileBrowserRoots) = %d, want 1", len(cfg.FileBrowserRoots))
	}
	if cfg.FileBrowserRoots[0].Label != "Documents" || cfg.FileBrowserRoots[0].Path != `C:\Users\Lenovo\Documents` {
		t.Errorf("FileBrowserRoots[0] = %+v", cfg.FileBrowserRoots[0])
	}
}

func TestLoad_MissingFileBrowserRoots_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	incomplete := `{"web": {"owner_email": "breynisson@gmail.com", "cors_allowed_origin": "http://localhost:5178", "perception_svc_url": "http://localhost:9011", "memory_svc_url": "http://localhost:9012", "thinking_svc_url": "http://localhost:9013", "action_svc_url": "http://localhost:9014", "obsidian_root": "C:\\Users\\Lenovo\\Documents\\obsidian", "claude_project_roots": [{"label": "Obsidian", "path": "C:\\Users\\Lenovo\\Documents\\obsidian"}], "file_browser_roots": []}}`
	path := writeConfigFile(t, dir, incomplete)
	os.Setenv("CONFIG_PATH", path)
	os.Setenv("SUPABASE_URL", "https://example.supabase.co")
	os.Setenv("SUPABASE_JWT_SECRET", "shh")
	defer os.Unsetenv("CONFIG_PATH")
	defer os.Unsetenv("SUPABASE_URL")
	defer os.Unsetenv("SUPABASE_JWT_SECRET")

	if _, err := config.Load(); err == nil {
		t.Fatal("Load() error = nil, want an error when web.file_browser_roots is empty")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./web-svc/config/... -run FileBrowserRoots -v` (from the vault root)
Expected: compile error — `cfg.FileBrowserRoots undefined (type *config.Config has no field or method FileBrowserRoots)`

- [ ] **Step 4: Implement — add the field, validation, and population in `web-svc/config/config.go`**

Add to the `Config` struct (currently lines 10-23), right after `ClaudeProjectRoots`:

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
}
```

Add a validation check right after the existing `ClaudeProjectRoots` check (currently lines 53-55):

```go
	if len(shared.Web.ClaudeProjectRoots) == 0 {
		return nil, fmt.Errorf("shared config %s has no web.claude_project_roots configured", configPath)
	}
	if len(shared.Web.FileBrowserRoots) == 0 {
		return nil, fmt.Errorf("shared config %s has no web.file_browser_roots configured", configPath)
	}
```

And populate it in the returned `&Config{...}` literal (currently lines 69-82), right after `ClaudeProjectRoots`:

```go
	return &Config{
		HTTPPort:           env("HTTP_PORT", "9005"),
		SupabaseURL:        supabaseURL,
		SupabaseJWTSecret:  jwtSecret,
		OwnerEmail:         shared.Web.OwnerEmail,
		CORSAllowedOrigin:  shared.Web.CORSAllowedOrigin,
		PerceptionSvcURL:   shared.Web.PerceptionSvcURL,
		MemorySvcURL:       shared.Web.MemorySvcURL,
		ThinkingSvcURL:     shared.Web.ThinkingSvcURL,
		ActionSvcURL:       shared.Web.ActionSvcURL,
		SoulmanRoot:        env("SOULMAN_ROOT", `C:\Users\Lenovo\soulman-dev`),
		ObsidianRoot:       shared.Web.ObsidianRoot,
		ClaudeProjectRoots: shared.Web.ClaudeProjectRoots,
		FileBrowserRoots:   shared.Web.FileBrowserRoots,
	}, nil
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./web-svc/config/... -v` (from the vault root)
Expected: PASS — all tests in the package, including the two new ones and every pre-existing one (the `validConfigJSON` fixture update must not have broken anything else).

- [ ] **Step 6: Update real configs so the service can actually start**

In `config/dev.json`, inside `"web"`, right after `"claude_project_roots"`'s closing `]`:

```json
    "claude_project_roots": [
      { "label": "Obsidian", "path": "C:\\Users\\Lenovo\\Documents\\obsidian" },
      { "label": "IdeaProjects", "path": "C:\\Users\\Lenovo\\IdeaProjects" },
      { "label": "Misc Projects", "path": "C:\\Users\\Lenovo\\misc_projects" }
    ],
    "file_browser_roots": [
      { "label": "Documents", "path": "C:\\Users\\Lenovo\\Documents" },
      { "label": "Downloads", "path": "C:\\Users\\Lenovo\\Downloads" }
    ]
```

Apply the identical addition to `config/prod.json` (same absolute paths — this is "my machine," not a per-environment resource, same treatment as `obsidian_root`).

- [ ] **Step 7: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add common/sharedconfig/config.go web-svc/config/config.go web-svc/config/config_test.go config/dev.json config/prod.json
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "feat(web-svc): add web.file_browser_roots config"
```

---

### Task 2: `web-svc/filebrowser` — types, path validation, `ListRoots`

**Files:**
- Create: `web-svc/filebrowser/filebrowser.go`
- Test: `web-svc/filebrowser/filebrowser_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `Root{Label, Path string}`, `RootListing{Label, Path string; Exists bool}`, `FileInfo{Name string; Size int64}`, sentinels `ErrNotFound`/`ErrExists`/`ErrInvalidName`, `ListRoots(roots []Root) []RootListing`, the unexported `resolveDir(root Root, relPath string) (string, error)` — consumed by Tasks 3-5 (`List`/`ResolveFile`/`Save`) and, via the exported names, by Task 6 (`files_handler.go`).

- [ ] **Step 1: Write the failing test**

```go
// web-svc/filebrowser/filebrowser_test.go
package filebrowser_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"soulman/web-svc/filebrowser"
)

func TestListRoots_ReportsExistsPerRoot(t *testing.T) {
	dir := t.TempDir()
	roots := []filebrowser.Root{
		{Label: "Documents", Path: dir},
		{Label: "Missing", Path: filepath.Join(dir, "does-not-exist")},
	}

	listings := filebrowser.ListRoots(roots)

	if len(listings) != 2 {
		t.Fatalf("len(listings) = %d, want 2", len(listings))
	}
	if !listings[0].Exists {
		t.Errorf("listings[0].Exists = false, want true for %s", dir)
	}
	if listings[1].Exists {
		t.Errorf("listings[1].Exists = true, want false for a missing path")
	}
	if listings[0].Label != "Documents" || listings[0].Path != dir {
		t.Errorf("listings[0] = %+v", listings[0])
	}
}

func TestList_InvalidPathSegment_ReturnsErrInvalidName(t *testing.T) {
	root := filebrowser.Root{Label: "Documents", Path: t.TempDir()}
	for _, relPath := range []string{"..", "../etc", "a/../../etc", `a\b`, "a//b", "good/../../../windows"} {
		_, _, err := filebrowser.List(root, relPath)
		if !errors.Is(err, filebrowser.ErrInvalidName) {
			t.Errorf("List(%q) error = %v, want ErrInvalidName", relPath, err)
		}
	}
}

func TestList_ValidNestedPath_ReturnsErrNotFoundWhenMissing(t *testing.T) {
	root := filebrowser.Root{Label: "Documents", Path: t.TempDir()}
	_, _, err := filebrowser.List(root, "Taxes/2025")
	if !errors.Is(err, filebrowser.ErrNotFound) {
		t.Errorf("List() error = %v, want ErrNotFound", err)
	}
}

func TestList_NTFSColonSegment_ReturnsErrInvalidName(t *testing.T) {
	root := filebrowser.Root{Label: "Documents", Path: t.TempDir()}
	_, _, err := filebrowser.List(root, "12:30 notes")
	if !errors.Is(err, filebrowser.ErrInvalidName) {
		t.Errorf("List() error = %v, want ErrInvalidName for an NTFS-colon segment", err)
	}
}

var _ = os.Stat // keeps "os" imported for this step only; Task 3 adds real os-using tests
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./web-svc/filebrowser/... -v`
Expected: FAIL to compile — the package doesn't exist yet (`undefined: filebrowser.Root`, etc.)

- [ ] **Step 3: Write minimal implementation**

```go
// web-svc/filebrowser/filebrowser.go

// Package filebrowser provides validated browse/download/upload access to
// a curated set of filesystem roots (web.file_browser_roots), at arbitrary
// depth — unlike web-svc/obsidian's one-level-deep browsing. Every
// function validates its path arguments before touching the filesystem
// (see resolveDir) since this is reachable from the internet-tunneled prod
// dashboard (owner-JWT-gated, but path-traversal protection matters
// regardless). See
// docs/superpowers/specs/2026-08-19-file-browser-design.md.
package filebrowser

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var (
	ErrNotFound    = errors.New("filebrowser: not found")
	ErrExists      = errors.New("filebrowser: already exists")
	ErrInvalidName = errors.New("filebrowser: invalid name")
)

// Root identifies one curated filesystem root: a human-readable label
// (matched against a request's "root" field) and the filesystem path it
// corresponds to.
type Root struct {
	Label string
	Path  string
}

// RootListing is a Root plus its current filesystem existence. Unlike
// claudesession.RootListing this carries no folder listing — file-browser
// navigation is stateless per-request via List, not preloaded here.
type RootListing struct {
	Label  string
	Path   string
	Exists bool
}

// FileInfo describes one file directly inside a listed directory.
type FileInfo struct {
	Name string
	Size int64
}

// ListRoots reports each configured root's current existence. A
// temporarily missing root is reported as such (Exists: false), not
// omitted or treated as an error — mirrors claudesession.ListRoots.
func ListRoots(roots []Root) []RootListing {
	listings := make([]RootListing, len(roots))
	for i, root := range roots {
		info, err := os.Stat(root.Path)
		exists := err == nil && info.IsDir()
		listings[i] = RootListing{Label: root.Label, Path: root.Path, Exists: exists}
	}
	return listings
}

// validSegment rejects anything that isn't a single, plain path component
// — see web-svc/obsidian's identical guard for the full rationale (path
// traversal and NTFS alternate-data-stream protection).
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

// resolveDir validates relPath as a "/"-joined sequence of path segments
// (each individually checked via validSegment — rejects traversal and
// NTFS-colon tricks per segment, not just on the whole joined string),
// joins them onto root.Path one at a time, confirms the result is still
// contained within root.Path (defense in depth), and confirms it exists
// and is a directory. relPath == "" resolves to root.Path itself.
func resolveDir(root Root, relPath string) (string, error) {
	cleanRoot := filepath.Clean(root.Path)
	dir := cleanRoot
	if relPath != "" {
		for _, seg := range strings.Split(relPath, "/") {
			if !validSegment(seg) {
				return "", ErrInvalidName
			}
			dir = filepath.Join(dir, seg)
		}
		if !isWithin(cleanRoot, dir) {
			return "", ErrInvalidName
		}
	}
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("filebrowser: stat %s: %w", dir, err)
	}
	if !info.IsDir() {
		return "", ErrNotFound
	}
	return dir, nil
}

// List returns the subfolder names and files directly inside
// root.Path/relPath, sorted. relPath is "" for the root itself, or a
// "/"-joined relative path for a subfolder. Returns ErrNotFound if
// relPath doesn't resolve to an existing directory.
func List(root Root, relPath string) (folders []string, files []FileInfo, err error) {
	dir, err := resolveDir(root, relPath)
	if err != nil {
		return nil, nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("filebrowser: reading dir %s: %w", dir, err)
	}
	folders = []string{}
	files = []FileInfo{}
	for _, e := range entries {
		if e.IsDir() {
			folders = append(folders, e.Name())
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, FileInfo{Name: e.Name(), Size: info.Size()})
	}
	sort.Strings(folders)
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return folders, files, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./web-svc/filebrowser/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add web-svc/filebrowser/filebrowser.go web-svc/filebrowser/filebrowser_test.go
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "feat(web-svc): add filebrowser package with path validation and ListRoots"
```

---

### Task 3: `filebrowser.List` — full listing behavior

**Files:**
- Modify: `web-svc/filebrowser/filebrowser.go` (`List` already exists from Task 2 — this task's tests exercise its actual directory-walking behavior)
- Test: `web-svc/filebrowser/filebrowser_test.go` (append; remove the `var _ = os.Stat` placeholder from Task 2 once these tests use `os` for real)

**Interfaces:**
- Consumes: `Root`, `FileInfo`, `resolveDir` from Task 2.
- Produces: confirmed behavior of `List(root Root, relPath string) (folders []string, files []FileInfo, err error)` — consumed by Task 7 (`files_handler.go`'s list handler).

- [ ] **Step 1: Write the failing test**

Remove the `var _ = os.Stat` placeholder line from Task 2, then append:

```go
func TestList_ReturnsSortedFoldersAndFilesWithSizes(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "Zeta"))
	mustMkdir(t, filepath.Join(dir, "Alpha"))
	mustWriteFile(t, filepath.Join(dir, "b.txt"), []byte("hello"))
	mustWriteFile(t, filepath.Join(dir, "a.txt"), []byte("hi"))
	root := filebrowser.Root{Label: "Documents", Path: dir}

	folders, files, err := filebrowser.List(root, "")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(folders) != 2 || folders[0] != "Alpha" || folders[1] != "Zeta" {
		t.Errorf("folders = %v, want [Alpha Zeta]", folders)
	}
	if len(files) != 2 || files[0].Name != "a.txt" || files[1].Name != "b.txt" {
		t.Fatalf("files = %v, want a.txt then b.txt", files)
	}
	if files[0].Size != 2 || files[1].Size != 5 {
		t.Errorf("file sizes = %d, %d, want 2, 5", files[0].Size, files[1].Size)
	}
}

func TestList_NestedSubfolder(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "Taxes", "2025"))
	mustWriteFile(t, filepath.Join(dir, "Taxes", "2025", "return.pdf"), []byte("pdf-bytes"))
	root := filebrowser.Root{Label: "Documents", Path: dir}

	folders, files, err := filebrowser.List(root, "Taxes/2025")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(folders) != 0 {
		t.Errorf("folders = %v, want none", folders)
	}
	if len(files) != 1 || files[0].Name != "return.pdf" {
		t.Fatalf("files = %v, want [return.pdf]", files)
	}
}

func TestList_EmptyDir_ReturnsEmptySlicesNotNil(t *testing.T) {
	root := filebrowser.Root{Label: "Documents", Path: t.TempDir()}
	folders, files, err := filebrowser.List(root, "")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	foldersJSON, _ := json.Marshal(folders)
	filesJSON, _ := json.Marshal(files)
	if string(foldersJSON) != "[]" {
		t.Errorf("folders serializes as %s, want []", foldersJSON)
	}
	if string(filesJSON) != "[]" {
		t.Errorf("files serializes as %s, want []", filesJSON)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", path, err)
	}
}

func mustWriteFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}
```

Add `"encoding/json"` to the test file's import block.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./web-svc/filebrowser/... -run TestList_ -v`
Expected: this may PASS immediately if Task 2's `List` is already correct. If so, confirm the test suite is actually exercising the sort/size logic by temporarily commenting out `sort.Strings(folders)` in `filebrowser.go`, re-running to see `TestList_ReturnsSortedFoldersAndFilesWithSizes` FAIL, then restoring the line.

- [ ] **Step 3: Implementation**

`List` was already written in Task 2's Step 3; no changes should be needed here. If Step 2 revealed a genuine gap, fix `List` in `web-svc/filebrowser/filebrowser.go` to match the behavior above (sorted folders, sorted-by-name files with correct `Size`, empty (not nil) slices for an empty directory).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./web-svc/filebrowser/... -v`
Expected: PASS (all tests in the package)

- [ ] **Step 5: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add web-svc/filebrowser/filebrowser.go web-svc/filebrowser/filebrowser_test.go
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "test(web-svc): cover filebrowser.List's sorting and nested-path behavior"
```

---

### Task 4: `filebrowser.ResolveFile`

**Files:**
- Modify: `web-svc/filebrowser/filebrowser.go` (add `ResolveFile`)
- Test: `web-svc/filebrowser/filebrowser_test.go` (append)

**Interfaces:**
- Consumes: `Root`, `resolveDir`, `validSegment`, `isWithin`, `ErrNotFound`/`ErrInvalidName` from Task 2.
- Produces: `ResolveFile(root Root, relPath, filename string) (string, error)` — consumed by Task 8 (`files_handler.go`'s download handler).

- [ ] **Step 1: Write the failing test**

```go
func TestResolveFile_ExistingFile_ReturnsAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "Taxes"))
	mustWriteFile(t, filepath.Join(dir, "Taxes", "return.pdf"), []byte("pdf-bytes"))
	root := filebrowser.Root{Label: "Documents", Path: dir}

	path, err := filebrowser.ResolveFile(root, "Taxes", "return.pdf")
	if err != nil {
		t.Fatalf("ResolveFile() error = %v", err)
	}
	want := filepath.Join(dir, "Taxes", "return.pdf")
	if path != want {
		t.Errorf("ResolveFile() = %q, want %q", path, want)
	}
}

func TestResolveFile_MissingFile_ReturnsErrNotFound(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "Taxes"))
	root := filebrowser.Root{Label: "Documents", Path: dir}

	_, err := filebrowser.ResolveFile(root, "Taxes", "missing.pdf")
	if !errors.Is(err, filebrowser.ErrNotFound) {
		t.Errorf("ResolveFile() error = %v, want ErrNotFound", err)
	}
}

func TestResolveFile_InvalidFilenameSegment_ReturnsErrInvalidName(t *testing.T) {
	root := filebrowser.Root{Label: "Documents", Path: t.TempDir()}
	for _, name := range []string{"..", `a\b`, "a/b", ""} {
		_, err := filebrowser.ResolveFile(root, "", name)
		if !errors.Is(err, filebrowser.ErrInvalidName) {
			t.Errorf("ResolveFile(%q) error = %v, want ErrInvalidName", name, err)
		}
	}
}

func TestResolveFile_TargetIsDirectory_ReturnsErrNotFound(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "Taxes"))
	root := filebrowser.Root{Label: "Documents", Path: dir}

	_, err := filebrowser.ResolveFile(root, "", "Taxes")
	if !errors.Is(err, filebrowser.ErrNotFound) {
		t.Errorf("ResolveFile() error = %v, want ErrNotFound for a directory target", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./web-svc/filebrowser/... -run TestResolveFile_ -v`
Expected: FAIL to compile — `undefined: filebrowser.ResolveFile`

- [ ] **Step 3: Write minimal implementation**

Add to `web-svc/filebrowser/filebrowser.go`, after `List`:

```go
// ResolveFile validates relPath (a folder) and filename (a single path
// segment) and returns the file's absolute path for a caller to stream.
// Returns ErrNotFound if it doesn't exist or is a directory.
func ResolveFile(root Root, relPath, filename string) (string, error) {
	dir, err := resolveDir(root, relPath)
	if err != nil {
		return "", err
	}
	if !validSegment(filename) {
		return "", ErrInvalidName
	}
	path := filepath.Join(dir, filename)
	if !isWithin(dir, path) {
		return "", ErrInvalidName
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("filebrowser: stat %s: %w", path, err)
	}
	if info.IsDir() {
		return "", ErrNotFound
	}
	return path, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./web-svc/filebrowser/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add web-svc/filebrowser/filebrowser.go web-svc/filebrowser/filebrowser_test.go
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "feat(web-svc): add filebrowser.ResolveFile"
```

---

### Task 5: `filebrowser.Save`

**Files:**
- Modify: `web-svc/filebrowser/filebrowser.go` (add `Save`; add `"io"` to imports)
- Test: `web-svc/filebrowser/filebrowser_test.go` (append; add `"bytes"` to imports)

**Interfaces:**
- Consumes: `Root`, `resolveDir`, `validSegment`, `isWithin`, `ErrExists`/`ErrInvalidName`/`ErrNotFound` from Task 2.
- Produces: `Save(root Root, relPath, filename string, r io.Reader, overwrite bool) error` — consumed by Task 9 (`files_handler.go`'s upload handler).

- [ ] **Step 1: Write the failing test**

```go
func TestSave_NewFile_WritesContent(t *testing.T) {
	dir := t.TempDir()
	root := filebrowser.Root{Label: "Documents", Path: dir}

	err := filebrowser.Save(root, "", "note.txt", bytes.NewReader([]byte("hello")), false)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "note.txt"))
	if err != nil {
		t.Fatalf("reading saved file: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("content = %q, want hello", got)
	}
}

func TestSave_ExistingFileNoOverwrite_ReturnsErrExists(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "note.txt"), []byte("original"))
	root := filebrowser.Root{Label: "Documents", Path: dir}

	err := filebrowser.Save(root, "", "note.txt", bytes.NewReader([]byte("new")), false)
	if !errors.Is(err, filebrowser.ErrExists) {
		t.Errorf("Save() error = %v, want ErrExists", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "note.txt"))
	if string(got) != "original" {
		t.Errorf("content = %q, want unchanged (no write attempted)", got)
	}
}

func TestSave_ExistingFileWithOverwrite_ReplacesContent(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "note.txt"), []byte("original"))
	root := filebrowser.Root{Label: "Documents", Path: dir}

	err := filebrowser.Save(root, "", "note.txt", bytes.NewReader([]byte("replaced")), true)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "note.txt"))
	if string(got) != "replaced" {
		t.Errorf("content = %q, want replaced", got)
	}
}

func TestSave_TargetFolderMissing_ReturnsErrNotFound(t *testing.T) {
	root := filebrowser.Root{Label: "Documents", Path: t.TempDir()}
	err := filebrowser.Save(root, "DoesNotExist", "note.txt", bytes.NewReader([]byte("x")), false)
	if !errors.Is(err, filebrowser.ErrNotFound) {
		t.Errorf("Save() error = %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./web-svc/filebrowser/... -run TestSave_ -v`
Expected: FAIL to compile — `undefined: filebrowser.Save`

- [ ] **Step 3: Write minimal implementation**

Add `"io"` to `web-svc/filebrowser/filebrowser.go`'s import block, and add after `ResolveFile`:

```go
// Save writes r's contents as relPath/filename. relPath's folder must
// already exist (no folder creation) — returns ErrNotFound otherwise.
// Returns ErrExists if filename already exists and overwrite is false.
func Save(root Root, relPath, filename string, r io.Reader, overwrite bool) error {
	dir, err := resolveDir(root, relPath)
	if err != nil {
		return err
	}
	if !validSegment(filename) {
		return ErrInvalidName
	}
	path := filepath.Join(dir, filename)
	if !isWithin(dir, path) {
		return ErrInvalidName
	}
	if !overwrite {
		if _, err := os.Stat(path); err == nil {
			return ErrExists
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("filebrowser: creating %s: %w", path, err)
	}
	defer f.Close()
	if _, err := io.Copy(f, r); err != nil {
		return fmt.Errorf("filebrowser: writing %s: %w", path, err)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./web-svc/filebrowser/... -v`
Expected: PASS — full package

- [ ] **Step 5: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add web-svc/filebrowser/filebrowser.go web-svc/filebrowser/filebrowser_test.go
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "feat(web-svc): add filebrowser.Save"
```

---

### Task 6: `httpserver` wiring + `GET /api/files/roots`

**Files:**
- Modify: `web-svc/httpserver/server.go` (add `Config.FileBrowserRoots` field; import `soulman/web-svc/filebrowser`; add the `/api/files/roots` route inside the existing owner-JWT `r.Group`)
- Create: `web-svc/httpserver/files_handler.go` (`fileBrowserRootResponse`, `filesRoots` handler, `findFileBrowserRoot`, `writeFileBrowserError` — the shared plumbing every later file-browser handler uses)
- Test: `web-svc/httpserver/files_handler_test.go`

**Interfaces:**
- Consumes: `filebrowser.Root`, `filebrowser.ListRoots`, `filebrowser.ErrNotFound`/`ErrExists`/`ErrInvalidName` from Task 2; `writeJSON`/`writeJSONError` from `server.go` (existing).
- Produces: `Server.cfg.FileBrowserRoots []filebrowser.Root` field, `findFileBrowserRoot(roots []filebrowser.Root, label string) (filebrowser.Root, bool)`, `writeFileBrowserError(w http.ResponseWriter, err error)` — consumed by Tasks 7-9.

- [ ] **Step 1: Write the failing test**

```go
// web-svc/httpserver/files_handler_test.go
package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"soulman/web-svc/auth"
	"soulman/web-svc/filebrowser"
	"soulman/web-svc/httpserver"
)

func TestAPIFilesRoots_ReportsExistsPerRoot(t *testing.T) {
	dir := t.TempDir()
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		FileBrowserRoots: []filebrowser.Root{
			{Label: "Documents", Path: dir},
			{Label: "Missing", Path: dir + `\does-not-exist`},
		},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/files/roots", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Roots []struct {
			Label  string `json:"label"`
			Exists bool   `json:"exists"`
		} `json:"roots"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Roots) != 2 {
		t.Fatalf("len(roots) = %d, want 2", len(body.Roots))
	}
	if body.Roots[0].Label != "Documents" || !body.Roots[0].Exists {
		t.Errorf("roots[0] = %+v, want Documents/exists=true", body.Roots[0])
	}
	if body.Roots[1].Exists {
		t.Errorf("roots[1].Exists = true, want false for a missing path")
	}
}

func TestAPIFilesRoots_NoToken_Returns401(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178"}
	srv := httpserver.New("9005", cfg, auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail))
	req := httptest.NewRequest(http.MethodGet, "/api/files/roots", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./web-svc/httpserver/... -run TestAPIFilesRoots -v`
Expected: FAIL to compile — `httpserver.Config` has no field `FileBrowserRoots`.

- [ ] **Step 3: Write minimal implementation**

In `web-svc/httpserver/server.go`: add the import and the `Config` field (currently lines 21-30):

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
	"soulman/web-svc/filebrowser"
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
	FileBrowserRoots   []filebrowser.Root
}
```

And add the route inside `buildRouter`'s `r.Group` (currently lines 70-86), right after the Claude routes:

```go
		r.Get("/api/claude/roots", s.claudeRoots)
		r.Post("/api/claude/launch", s.claudeLaunch)
		r.Get("/api/files/roots", s.filesRoots)
```

Create `web-svc/httpserver/files_handler.go`:

```go
package httpserver

import (
	"errors"
	"log/slog"
	"net/http"

	"soulman/web-svc/filebrowser"
)

type fileBrowserRootResponse struct {
	Label  string `json:"label"`
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

func (s *Server) filesRoots(w http.ResponseWriter, r *http.Request) {
	listings := filebrowser.ListRoots(s.cfg.FileBrowserRoots)
	resp := make([]fileBrowserRootResponse, len(listings))
	for i, l := range listings {
		resp[i] = fileBrowserRootResponse{Label: l.Label, Path: l.Path, Exists: l.Exists}
	}
	writeJSON(w, http.StatusOK, map[string][]fileBrowserRootResponse{"roots": resp})
}

func findFileBrowserRoot(roots []filebrowser.Root, label string) (filebrowser.Root, bool) {
	for _, r := range roots {
		if r.Label == label {
			return r, true
		}
	}
	return filebrowser.Root{}, false
}

func writeFileBrowserError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, filebrowser.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "not found")
	case errors.Is(err, filebrowser.ErrExists):
		writeJSONError(w, http.StatusConflict, "already exists")
	case errors.Is(err, filebrowser.ErrInvalidName):
		writeJSONError(w, http.StatusBadRequest, "invalid name")
	default:
		slog.Error("file browser unexpected error", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./web-svc/httpserver/... -v`
Expected: PASS — full package (including all pre-existing tests, unaffected by the additive `Config` field)

- [ ] **Step 5: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add web-svc/httpserver/server.go web-svc/httpserver/files_handler.go web-svc/httpserver/files_handler_test.go
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "feat(web-svc): wire filebrowser roots into httpserver, add GET /api/files/roots"
```

---

### Task 7: `GET /api/files/list`

**Files:**
- Modify: `web-svc/httpserver/server.go` (add the route)
- Modify: `web-svc/httpserver/files_handler.go` (add `fileEntryResponse`, `filesList`)
- Modify: `web-svc/httpserver/files_handler_test.go` (append)

**Interfaces:**
- Consumes: `findFileBrowserRoot`, `writeFileBrowserError` from Task 6; `filebrowser.List`, `filebrowser.FileInfo` from Tasks 2-3.
- Produces: `filesList` handler at `GET /api/files/list?root=X&path=Y` — consumed only by the frontend (Task 11).

- [ ] **Step 1: Write the failing test**

Append to `web-svc/httpserver/files_handler_test.go`; add `"os"` and `"path/filepath"` to the import block:

```go
func TestAPIFilesList_ReturnsFoldersAndFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "Taxes"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		FileBrowserRoots:  []filebrowser.Root{{Label: "Documents", Path: dir}},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/files/list?root=Documents&path=", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Folders []string `json:"folders"`
		Files   []struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		} `json:"files"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Folders) != 1 || body.Folders[0] != "Taxes" {
		t.Errorf("folders = %v, want [Taxes]", body.Folders)
	}
	if len(body.Files) != 1 || body.Files[0].Name != "note.txt" || body.Files[0].Size != 5 {
		t.Errorf("files = %+v, want [{note.txt 5}]", body.Files)
	}
}

func TestAPIFilesList_UnknownRoot_Returns400(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178"}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/files/list?root=NoSuchRoot&path=", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPIFilesList_PathTraversal_Returns400(t *testing.T) {
	dir := t.TempDir()
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		FileBrowserRoots:  []filebrowser.Root{{Label: "Documents", Path: dir}},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/files/list?root=Documents&path=..%2F..%2Fwindows", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./web-svc/httpserver/... -run TestAPIFilesList -v`
Expected: FAIL — `404 page not found` (route doesn't exist yet)

- [ ] **Step 3: Write minimal implementation**

In `web-svc/httpserver/server.go`, add the route right after `/api/files/roots`:

```go
		r.Get("/api/files/roots", s.filesRoots)
		r.Get("/api/files/list", s.filesList)
```

In `web-svc/httpserver/files_handler.go`, append:

```go
type fileEntryResponse struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

func (s *Server) filesList(w http.ResponseWriter, r *http.Request) {
	root, ok := findFileBrowserRoot(s.cfg.FileBrowserRoots, r.URL.Query().Get("root"))
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "unknown root")
		return
	}
	folders, files, err := filebrowser.List(root, r.URL.Query().Get("path"))
	if err != nil {
		writeFileBrowserError(w, err)
		return
	}
	resp := make([]fileEntryResponse, len(files))
	for i, f := range files {
		resp[i] = fileEntryResponse{Name: f.Name, Size: f.Size}
	}
	writeJSON(w, http.StatusOK, map[string]any{"folders": folders, "files": resp})
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./web-svc/httpserver/... -v`
Expected: PASS — full package

- [ ] **Step 5: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add web-svc/httpserver/server.go web-svc/httpserver/files_handler.go web-svc/httpserver/files_handler_test.go
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "feat(web-svc): add GET /api/files/list"
```

---

### Task 8: `GET /api/files/download`

**Files:**
- Modify: `web-svc/httpserver/server.go` (add the route)
- Modify: `web-svc/httpserver/files_handler.go` (add `filesDownload`)
- Modify: `web-svc/httpserver/files_handler_test.go` (append)

**Interfaces:**
- Consumes: `findFileBrowserRoot`, `writeFileBrowserError` from Task 6; `filebrowser.ResolveFile` from Task 4.
- Produces: `filesDownload` handler at `GET /api/files/download?root=X&path=Y&file=Z` — consumed by the frontend (Task 12).

- [ ] **Step 1: Write the failing test**

Append to `web-svc/httpserver/files_handler_test.go`:

```go
func TestAPIFilesDownload_ServesFileBytes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		FileBrowserRoots:  []filebrowser.Root{{Label: "Documents", Path: dir}},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/files/download?root=Documents&path=&file=note.txt", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "hello world" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "hello world")
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="note.txt"` {
		t.Errorf("Content-Disposition = %q", got)
	}
}

func TestAPIFilesDownload_MissingFile_Returns404(t *testing.T) {
	dir := t.TempDir()
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		FileBrowserRoots:  []filebrowser.Root{{Label: "Documents", Path: dir}},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/files/download?root=Documents&path=&file=missing.txt", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./web-svc/httpserver/... -run TestAPIFilesDownload -v`
Expected: FAIL — `404 page not found` (route doesn't exist yet)

- [ ] **Step 3: Write minimal implementation**

In `web-svc/httpserver/server.go`, add the route after `/api/files/list`:

```go
		r.Get("/api/files/list", s.filesList)
		r.Get("/api/files/download", s.filesDownload)
```

In `web-svc/httpserver/files_handler.go`, append:

```go
func (s *Server) filesDownload(w http.ResponseWriter, r *http.Request) {
	root, ok := findFileBrowserRoot(s.cfg.FileBrowserRoots, r.URL.Query().Get("root"))
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "unknown root")
		return
	}
	filename := r.URL.Query().Get("file")
	absPath, err := filebrowser.ResolveFile(root, r.URL.Query().Get("path"), filename)
	if err != nil {
		writeFileBrowserError(w, err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	http.ServeFile(w, r, absPath)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./web-svc/httpserver/... -v`
Expected: PASS — full package

- [ ] **Step 5: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add web-svc/httpserver/server.go web-svc/httpserver/files_handler.go web-svc/httpserver/files_handler_test.go
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "feat(web-svc): add GET /api/files/download"
```

---

### Task 9: `POST /api/files/upload`

**Files:**
- Modify: `web-svc/httpserver/server.go` (add the route)
- Modify: `web-svc/httpserver/files_handler.go` (add `maxUploadBytes` const, `filesUpload`)
- Modify: `web-svc/httpserver/files_handler_test.go` (append)

**Interfaces:**
- Consumes: `findFileBrowserRoot`, `writeFileBrowserError` from Task 6; `filebrowser.Save` from Task 5.
- Produces: `filesUpload` handler at `POST /api/files/upload?root=X&path=Y&overwrite=bool` — consumed by the frontend (Task 13).

- [ ] **Step 1: Write the failing test**

Append to `web-svc/httpserver/files_handler_test.go`; add `"bytes"`, `"io"`, and `"mime/multipart"` to the import block:

```go
func newUploadRequest(t *testing.T, url, fieldName, filename string, content []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile(fieldName, filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("writing multipart content: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("closing multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, url, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestAPIFilesUpload_NewFile_Returns200AndWritesContent(t *testing.T) {
	dir := t.TempDir()
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		FileBrowserRoots:  []filebrowser.Root{{Label: "Documents", Path: dir}},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := newUploadRequest(t, "/api/files/upload?root=Documents&path=&overwrite=false", "file", "note.txt", []byte("hello"))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, "note.txt"))
	if err != nil {
		t.Fatalf("reading uploaded file: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("content = %q, want hello", got)
	}
}

func TestAPIFilesUpload_ExistingFileNoOverwrite_Returns409(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("original"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		FileBrowserRoots:  []filebrowser.Root{{Label: "Documents", Path: dir}},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := newUploadRequest(t, "/api/files/upload?root=Documents&path=&overwrite=false", "file", "note.txt", []byte("new"))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPIFilesUpload_OversizedBody_Returns413(t *testing.T) {
	dir := t.TempDir()
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		FileBrowserRoots:  []filebrowser.Root{{Label: "Documents", Path: dir}},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	// Streams a body one byte over the 200MB cap via io.Pipe, so the test
	// doesn't have to hold the whole oversized payload in memory at once.
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		part, err := mw.CreateFormFile("file", "huge.bin")
		if err == nil {
			_, _ = io.Copy(part, io.LimitReader(zeroReader{}, 200<<20+1))
		}
		mw.Close()
		pw.Close()
	}()

	req := httptest.NewRequest(http.MethodPost, "/api/files/upload?root=Documents&path=&overwrite=false", pr)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413, body=%s", rec.Code, rec.Body.String())
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

func TestAPIFilesUpload_UnknownRoot_Returns400(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178"}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := newUploadRequest(t, "/api/files/upload?root=NoSuchRoot&path=&overwrite=false", "file", "note.txt", []byte("hi"))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./web-svc/httpserver/... -run TestAPIFilesUpload -v`
Expected: FAIL — `404 page not found` (route doesn't exist yet)

- [ ] **Step 3: Write minimal implementation**

In `web-svc/httpserver/server.go`, add the route after `/api/files/download`:

```go
		r.Get("/api/files/download", s.filesDownload)
		r.Post("/api/files/upload", s.filesUpload)
```

In `web-svc/httpserver/files_handler.go`, append:

```go
// maxUploadBytes caps a single upload's request body. Hardcoded rather
// than added to the config schema — bump here if 200MB turns out wrong
// (YAGNI, same posture as obsidian's no-size-cap call, just starting
// from *some* cap instead of none given this is binary/large-file
// upload rather than markdown notes).
const maxUploadBytes = 200 << 20 // 200MB

func (s *Server) filesUpload(w http.ResponseWriter, r *http.Request) {
	root, ok := findFileBrowserRoot(s.cfg.FileBrowserRoots, r.URL.Query().Get("root"))
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "unknown root")
		return
	}
	overwrite := r.URL.Query().Get("overwrite") == "true"

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "upload too large")
			return
		}
		writeJSONError(w, http.StatusBadRequest, "invalid multipart body")
		return
	}
	defer r.MultipartForm.RemoveAll()

	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "missing file field")
		return
	}
	defer file.Close()

	if err := filebrowser.Save(root, r.URL.Query().Get("path"), header.Filename, file, overwrite); err != nil {
		writeFileBrowserError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./web-svc/httpserver/... -v`
Expected: PASS — full package (note: the 413 test streams ~200MB and may take a few seconds; that's expected)

- [ ] **Step 5: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add web-svc/httpserver/server.go web-svc/httpserver/files_handler.go web-svc/httpserver/files_handler_test.go
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "feat(web-svc): add POST /api/files/upload with a 200MB cap"
```

---

### Task 10: `main.go` wiring

**Files:**
- Modify: `web-svc/main.go`

**Interfaces:**
- Consumes: `config.Config.FileBrowserRoots` (Task 1), `httpserver.Config.FileBrowserRoots` (Task 6), `filebrowser.Root` (Task 2).
- Produces: a fully wired backend — nothing further consumes this; it's the integration point.

- [ ] **Step 1: There is no new unit test for `main.go`** (no `main_test.go` exists in this codebase for any of the four services or web-svc — this task's verification is a full build + the existing test suite, matching that convention).

- [ ] **Step 2: Implement**

In `web-svc/main.go`, add the import and build the slice, mirroring `claudeRoots` immediately above it (currently lines 24-38):

```go
import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"soulman/web-svc/auth"
	"soulman/web-svc/claudesession"
	"soulman/web-svc/config"
	"soulman/web-svc/filebrowser"
	"soulman/web-svc/httpserver"
)
```

```go
	claudeRoots := make([]claudesession.Root, len(cfg.ClaudeProjectRoots))
	for i, r := range cfg.ClaudeProjectRoots {
		claudeRoots[i] = claudesession.Root{Label: r.Label, Path: r.Path}
	}

	fileBrowserRoots := make([]filebrowser.Root, len(cfg.FileBrowserRoots))
	for i, r := range cfg.FileBrowserRoots {
		fileBrowserRoots[i] = filebrowser.Root{Label: r.Label, Path: r.Path}
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
		FileBrowserRoots:   fileBrowserRoots,
	}, verifier)
```

- [ ] **Step 3: Verify**

Run: `go build ./...` (from `web-svc/`, or `go build ./web-svc/...` from the vault root)
Expected: builds cleanly with no errors.

Run: `go test ./web-svc/... ./common/...` (from the vault root)
Expected: PASS — every package, including everything from Tasks 1-9.

- [ ] **Step 4: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add web-svc/main.go
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "feat(web-svc): wire file_browser_roots into main.go"
```

---

### Task 11: `api.ts` — `getFileBrowserRoots` + `listFiles`

**Files:**
- Modify: `web/src/api.ts` (append types + functions, after the Claude section)
- Modify: `web/src/api.test.ts` (append tests)

**Interfaces:**
- Consumes: `getJSON` (existing).
- Produces: `FileBrowserRootListing`, `FileBrowserRoots`, `FileEntry`, `FileListing` types; `getFileBrowserRoots(token): Promise<FileBrowserRoots>`; `listFiles(token, root, path): Promise<FileListing>` — consumed by Tasks 14-15.

- [ ] **Step 1: Write the failing test**

Append to `web/src/api.test.ts` (matching the file's existing `beforeEach(() => { vi.stubGlobal('fetch', vi.fn()); })` convention already in place for every other endpoint); add `getFileBrowserRoots, listFiles` to the file's existing import from `./api`:

```ts
describe('getFileBrowserRoots', () => {
  it('fetches /api/files/roots with the auth header', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ roots: [{ label: 'Documents', path: 'C:\\Users\\Lenovo\\Documents', exists: true }] }),
    });

    const result = await getFileBrowserRoots('tok-abc');

    expect(result.roots[0].label).toBe('Documents');
    const [url, options] = mockFetch.mock.calls[0];
    expect(url).toBe('/api/files/roots');
    expect(options.headers).toEqual({ Authorization: 'Bearer tok-abc' });
  });
});

describe('listFiles', () => {
  it('fetches /api/files/list with encoded root and path query params', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ folders: ['Taxes'], files: [{ name: 'note.txt', size: 42 }] }),
    });

    const result = await listFiles('tok-abc', 'Documents', 'Taxes/2025');

    expect(result.folders).toEqual(['Taxes']);
    expect(result.files[0]).toEqual({ name: 'note.txt', size: 42 });
    const [url] = mockFetch.mock.calls[0];
    expect(url).toBe('/api/files/list?root=Documents&path=Taxes%2F2025');
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/api.test.ts`
Expected: FAIL — `getFileBrowserRoots is not a function` / `listFiles is not a function`

- [ ] **Step 3: Write minimal implementation**

Append to `web/src/api.ts`, after the Claude section:

```ts
export interface FileBrowserRootListing {
  label: string;
  path: string;
  exists: boolean;
}

export interface FileBrowserRoots {
  roots: FileBrowserRootListing[];
}

export interface FileEntry {
  name: string;
  size: number;
}

export interface FileListing {
  folders: string[];
  files: FileEntry[];
}

export const getFileBrowserRoots = (token: string | null): Promise<FileBrowserRoots> =>
  getJSON('/api/files/roots', token);

export const listFiles = (
  token: string | null,
  root: string,
  path: string,
): Promise<FileListing> =>
  getJSON(`/api/files/list?root=${encodeURIComponent(root)}&path=${encodeURIComponent(path)}`, token);
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/api.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add web/src/api.ts web/src/api.test.ts
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "feat(web): add getFileBrowserRoots and listFiles to api.ts"
```

---

### Task 12: `api.ts` — `downloadFile`

**Files:**
- Modify: `web/src/api.ts` (append)
- Modify: `web/src/api.test.ts` (append)

**Interfaces:**
- Consumes: `ApiError` (existing).
- Produces: `downloadFile(token, root, path, file): Promise<void>` — consumed by Task 14.

- [ ] **Step 1: Write the failing test**

Append to `web/src/api.test.ts`; add `downloadFile` to the file's existing import from `./api`:

```ts
describe('downloadFile', () => {
  it('fetches the file and triggers a browser download via a synthetic anchor', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    const blob = new Blob(['hello'], { type: 'text/plain' });
    mockFetch.mockResolvedValue({ ok: true, status: 200, blob: async () => blob });

    const createObjectURL = vi.fn().mockReturnValue('blob:mock-url');
    const revokeObjectURL = vi.fn();
    vi.stubGlobal('URL', { ...URL, createObjectURL, revokeObjectURL });
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});

    await downloadFile('tok-abc', 'Documents', 'Taxes', '2025-return.pdf');

    const [url] = mockFetch.mock.calls[0];
    expect(url).toBe('/api/files/download?root=Documents&path=Taxes&file=2025-return.pdf');
    expect(createObjectURL).toHaveBeenCalledWith(blob);
    expect(clickSpy).toHaveBeenCalled();
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:mock-url');

    clickSpy.mockRestore();
  });

  it('throws ApiError when the response is not ok', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: false, status: 404 });

    await expect(downloadFile('tok-abc', 'Documents', '', 'missing.pdf')).rejects.toThrow(ApiError);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/api.test.ts`
Expected: FAIL — `downloadFile is not a function`

- [ ] **Step 3: Write minimal implementation**

Append to `web/src/api.ts`:

```ts
export async function downloadFile(
  token: string | null,
  root: string,
  path: string,
  file: string,
): Promise<void> {
  const response = await fetch(
    `/api/files/download?root=${encodeURIComponent(root)}&path=${encodeURIComponent(path)}&file=${encodeURIComponent(file)}`,
    { headers: token ? { Authorization: `Bearer ${token}` } : {} },
  );
  if (!response.ok) {
    throw new ApiError(response.status, `download failed (${response.status})`);
  }
  const blob = await response.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = file;
  a.click();
  URL.revokeObjectURL(url);
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/api.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add web/src/api.ts web/src/api.test.ts
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "feat(web): add downloadFile to api.ts"
```

---

### Task 13: `api.ts` — `uploadFile`

**Files:**
- Modify: `web/src/api.ts` (append)
- Modify: `web/src/api.test.ts` (append)

**Interfaces:**
- Consumes: `ApiError` (existing).
- Produces: `uploadFile(token, root, path, file, overwrite): Promise<void>` — consumed by Task 14.

- [ ] **Step 1: Write the failing test**

Append to `web/src/api.test.ts`; add `uploadFile` to the file's existing import from `./api`:

```ts
describe('uploadFile', () => {
  it('POSTs a FormData body with the overwrite flag in the URL', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: true, status: 200 });
    const file = new File(['hello'], 'note.txt', { type: 'text/plain' });

    await uploadFile('tok-abc', 'Documents', 'Taxes', file, true);

    const [url, options] = mockFetch.mock.calls[0];
    expect(url).toBe('/api/files/upload?root=Documents&path=Taxes&overwrite=true');
    expect(options.method).toBe('POST');
    expect(options.body).toBeInstanceOf(FormData);
    expect(options.headers).toEqual({ Authorization: 'Bearer tok-abc' });
  });

  it('throws ApiError with the response status on failure', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: false, status: 409 });
    const file = new File(['hello'], 'note.txt');

    await expect(uploadFile('tok-abc', 'Documents', '', file, false)).rejects.toMatchObject({ status: 409 });
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/api.test.ts`
Expected: FAIL — `uploadFile is not a function`

- [ ] **Step 3: Write minimal implementation**

Append to `web/src/api.ts`:

```ts
export async function uploadFile(
  token: string | null,
  root: string,
  path: string,
  file: File,
  overwrite: boolean,
): Promise<void> {
  const formData = new FormData();
  formData.append('file', file);
  const response = await fetch(
    `/api/files/upload?root=${encodeURIComponent(root)}&path=${encodeURIComponent(path)}&overwrite=${overwrite}`,
    { method: 'POST', headers: token ? { Authorization: `Bearer ${token}` } : {}, body: formData },
  );
  if (!response.ok) {
    throw new ApiError(response.status, `upload failed (${response.status})`);
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/api.test.ts`
Expected: PASS — full file

- [ ] **Step 5: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add web/src/api.ts web/src/api.test.ts
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "feat(web): add uploadFile to api.ts"
```

---

### Task 14: `FileBrowser.tsx`

**Files:**
- Create: `web/src/components/FileBrowser.tsx`
- Test: `web/src/components/FileBrowser.test.tsx`

**Interfaces:**
- Consumes: `getAccessToken` (`../auth`, existing); `listFiles`, `downloadFile`, `uploadFile`, `ApiError`, `type FileListing` (`../api`, Tasks 11-13); `getParam`, `setParams` (`../urlState`, existing).
- Produces: `FileBrowser({ root: string })` — consumed by Task 15.

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/components/FileBrowser.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ApiError } from '../api';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockListFiles = vi.fn();
const mockDownloadFile = vi.fn();
const mockUploadFile = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    listFiles: (...args: unknown[]) => mockListFiles(...args),
    downloadFile: (...args: unknown[]) => mockDownloadFile(...args),
    uploadFile: (...args: unknown[]) => mockUploadFile(...args),
  };
});

beforeEach(() => {
  vi.clearAllMocks();
  window.history.replaceState(null, '', '/');
});

describe('FileBrowser', () => {
  it('lists folders and files, and drills into a subfolder via breadcrumb', async () => {
    mockListFiles
      .mockResolvedValueOnce({ folders: ['Taxes'], files: [{ name: 'note.txt', size: 42 }] })
      .mockResolvedValueOnce({ folders: [], files: [{ name: '2025-return.pdf', size: 1024 }] });
    const { FileBrowser } = await import('./FileBrowser');
    render(<FileBrowser root="Documents" />);

    await userEvent.click(await screen.findByText('Taxes'));

    expect(await screen.findByText('2025-return.pdf')).toBeInTheDocument();
    expect(mockListFiles).toHaveBeenLastCalledWith('tok-abc', 'Documents', 'Taxes');
    expect(screen.getByText('Documents')).toBeInTheDocument();
  });

  it('downloads a file when its Download button is clicked', async () => {
    mockListFiles.mockResolvedValue({ folders: [], files: [{ name: 'note.txt', size: 42 }] });
    mockDownloadFile.mockResolvedValue(undefined);
    const { FileBrowser } = await import('./FileBrowser');
    render(<FileBrowser root="Documents" />);

    await userEvent.click(await screen.findByText('Download'));

    expect(mockDownloadFile).toHaveBeenCalledWith('tok-abc', 'Documents', '', 'note.txt');
  });

  it('shows a replace confirmation on a 409 and retries with overwrite=true', async () => {
    mockListFiles.mockResolvedValue({ folders: [], files: [] });
    mockUploadFile.mockRejectedValueOnce(new ApiError(409, 'upload failed (409)'));
    mockUploadFile.mockResolvedValueOnce(undefined);
    const { FileBrowser } = await import('./FileBrowser');
    render(<FileBrowser root="Documents" />);

    const file = new File(['hello'], 'note.txt', { type: 'text/plain' });
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    await userEvent.upload(input, file);
    await userEvent.click(await screen.findByText('Upload'));

    expect(await screen.findByText(/already exists/i)).toBeInTheDocument();

    await userEvent.click(screen.getByText('replace?'));

    expect(mockUploadFile).toHaveBeenLastCalledWith('tok-abc', 'Documents', '', file, true);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/components/FileBrowser.test.tsx`
Expected: FAIL — `Failed to resolve import "./FileBrowser"` (the component doesn't exist yet)

- [ ] **Step 3: Write minimal implementation**

```tsx
// web/src/components/FileBrowser.tsx
import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { listFiles, downloadFile, uploadFile, ApiError, type FileListing } from '../api';
import { getParam, setParams } from '../urlState';

export function FileBrowser({ root }: { root: string }) {
  const [currentPath, setCurrentPath] = useState<string>(() => getParam('filePath') ?? '');
  const [listing, setListing] = useState<FileListing | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [pendingFile, setPendingFile] = useState<File | null>(null);
  const [conflictFile, setConflictFile] = useState<string | null>(null);
  const [refreshKey, setRefreshKey] = useState(0);

  useEffect(() => {
    let active = true;
    (async () => {
      const token = await getAccessToken();
      try {
        const data = await listFiles(token, root, currentPath);
        if (active) {
          setListing(data);
          setError(null);
        }
      } catch {
        if (active) setError('Unable to load folder');
      }
    })();
    return () => {
      active = false;
    };
  }, [root, currentPath, refreshKey]);

  function navigateTo(path: string) {
    setCurrentPath(path);
    setParams({ fileRoot: root, filePath: path || null });
  }

  const crumbs = currentPath === '' ? [] : currentPath.split('/');

  async function handleDownload(name: string) {
    const token = await getAccessToken();
    try {
      await downloadFile(token, root, currentPath, name);
    } catch {
      setError('Download failed — the file may have moved');
      setRefreshKey((k) => k + 1);
    }
  }

  async function handleUpload(file: File, overwrite: boolean) {
    const token = await getAccessToken();
    try {
      await uploadFile(token, root, currentPath, file, overwrite);
      setConflictFile(null);
      setPendingFile(null);
      setError(null);
      setRefreshKey((k) => k + 1);
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        setConflictFile(file.name);
      } else if (err instanceof ApiError && err.status === 413) {
        setError('Upload exceeds the 200MB limit');
      } else {
        setError('Upload failed');
      }
    }
  }

  return (
    <div>
      <nav className="mb-2 text-sm text-gray-500">
        <button onClick={() => navigateTo('')} className="underline">
          {root}
        </button>
        {crumbs.map((seg, i) => (
          <span key={i}>
            {' / '}
            <button onClick={() => navigateTo(crumbs.slice(0, i + 1).join('/'))} className="underline">
              {seg}
            </button>
          </span>
        ))}
      </nav>
      {error && <p className="text-sm text-red-600">{error}</p>}
      {!listing && !error && <p className="text-sm text-gray-500">Loading...</p>}
      {listing && (
        <ul className="space-y-1">
          {listing.folders.map((folder) => (
            <li key={folder}>
              <button
                onClick={() => navigateTo(currentPath ? `${currentPath}/${folder}` : folder)}
                className="text-sm underline"
              >
                {folder}
              </button>
            </li>
          ))}
          {listing.files.map((file) => (
            <li key={file.name} className="flex items-center gap-2">
              <span className="text-sm">{file.name}</span>
              <span className="text-xs text-gray-400">{formatSize(file.size)}</span>
              <button onClick={() => handleDownload(file.name)} className="text-sm underline">
                Download
              </button>
            </li>
          ))}
        </ul>
      )}
      <div className="mt-4">
        <input type="file" onChange={(e) => setPendingFile(e.target.files?.[0] ?? null)} />
        <button
          disabled={!pendingFile}
          onClick={() => pendingFile && handleUpload(pendingFile, false)}
          className="ml-2 text-sm underline"
        >
          Upload
        </button>
        {conflictFile && (
          <div className="mt-2 text-sm text-red-600">
            &quot;{conflictFile}&quot; already exists —{' '}
            <button onClick={() => pendingFile && handleUpload(pendingFile, true)} className="underline">
              replace?
            </button>
          </div>
        )}
      </div>
    </div>
  );
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/components/FileBrowser.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add web/src/components/FileBrowser.tsx web/src/components/FileBrowser.test.tsx
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "feat(web): add FileBrowser component (breadcrumbs, download, upload)"
```

---

### Task 15: `FileRootList.tsx`

**Files:**
- Create: `web/src/components/FileRootList.tsx`
- Test: `web/src/components/FileRootList.test.tsx`

**Interfaces:**
- Consumes: `getAccessToken` (`../auth`); `getFileBrowserRoots`, `type FileBrowserRootListing` (`../api`, Task 11); `getParam`, `setParams` (`../urlState`); `FileBrowser` (`./FileBrowser`, Task 14).
- Produces: `FileRootList()` — consumed by Task 16.

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/components/FileRootList.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockGetFileBrowserRoots = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return { ...actual, getFileBrowserRoots: (...args: unknown[]) => mockGetFileBrowserRoots(...args) };
});

vi.mock('./FileBrowser', () => ({
  FileBrowser: ({ root }: { root: string }) => <div data-testid="file-browser">{root}</div>,
}));

beforeEach(() => {
  vi.clearAllMocks();
  window.history.replaceState(null, '', '/');
});

describe('FileRootList', () => {
  it('lists existing roots and renders FileBrowser when one is selected', async () => {
    mockGetFileBrowserRoots.mockResolvedValue({
      roots: [
        { label: 'Documents', path: 'C:\\Users\\Lenovo\\Documents', exists: true },
        { label: 'Downloads', path: 'C:\\Users\\Lenovo\\Downloads', exists: true },
      ],
    });
    const { FileRootList } = await import('./FileRootList');
    render(<FileRootList />);

    await userEvent.click(await screen.findByText('Documents'));

    expect(await screen.findByTestId('file-browser')).toHaveTextContent('Documents');
  });

  it('shows a missing root as unavailable and does not let it be selected', async () => {
    mockGetFileBrowserRoots.mockResolvedValue({
      roots: [{ label: 'Downloads', path: 'C:\\Users\\Lenovo\\Downloads', exists: false }],
    });
    const { FileRootList } = await import('./FileRootList');
    render(<FileRootList />);

    expect(await screen.findByText(/downloads.*not found/i)).toBeInTheDocument();
    expect(screen.queryByTestId('file-browser')).not.toBeInTheDocument();
  });

  it('shows an error banner when roots fail to load', async () => {
    mockGetFileBrowserRoots.mockRejectedValue(new Error('network error'));
    const { FileRootList } = await import('./FileRootList');
    render(<FileRootList />);

    expect(await screen.findByText(/roots unavailable/i)).toBeInTheDocument();
  });

  it('restores the previously selected root from the URL on mount', async () => {
    window.history.replaceState(null, '', '/?fileRoot=Documents');
    mockGetFileBrowserRoots.mockResolvedValue({
      roots: [{ label: 'Documents', path: 'C:\\Users\\Lenovo\\Documents', exists: true }],
    });
    const { FileRootList } = await import('./FileRootList');
    render(<FileRootList />);

    expect(await screen.findByTestId('file-browser')).toHaveTextContent('Documents');
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/components/FileRootList.test.tsx`
Expected: FAIL — `Failed to resolve import "./FileRootList"`

- [ ] **Step 3: Write minimal implementation**

```tsx
// web/src/components/FileRootList.tsx
import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { getFileBrowserRoots, type FileBrowserRootListing } from '../api';
import { FileBrowser } from './FileBrowser';
import { getParam, setParams } from '../urlState';

export function FileRootList() {
  const [roots, setRoots] = useState<FileBrowserRootListing[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selectedRoot, setSelectedRoot] = useState<string | null>(() => getParam('fileRoot'));

  useEffect(() => {
    let active = true;
    (async () => {
      const token = await getAccessToken();
      try {
        const data = await getFileBrowserRoots(token);
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
                <button
                  onClick={() => {
                    const next = selectedRoot === root.label ? null : root.label;
                    setSelectedRoot(next);
                    setParams({ fileRoot: next, filePath: null });
                  }}
                  className="text-sm font-medium underline"
                >
                  {root.label}
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
      {selectedRoot && (
        <div className="mt-4">
          <FileBrowser root={selectedRoot} />
        </div>
      )}
    </div>
  );
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/components/FileRootList.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add web/src/components/FileRootList.tsx web/src/components/FileRootList.test.tsx
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "feat(web): add FileRootList component"
```

---

### Task 16: `FilesPage.tsx` + `App.tsx` + `Dashboard.tsx` wiring

**Files:**
- Create: `web/src/components/FilesPage.tsx`
- Modify: `web/src/App.tsx` (`ViewState`, `viewFromPageParam`, the `view === 'files'` branch, `Dashboard`'s `onOpenFiles` prop)
- Modify: `web/src/components/Dashboard.tsx` (`onOpenFiles` prop, "Files" header button)
- Modify: `web/src/App.test.tsx` (append the page-switch round-trip test)

**Interfaces:**
- Consumes: `FileRootList` (Task 15).
- Produces: the "Files" page is reachable end-to-end from the dashboard.

- [ ] **Step 1: Write the failing test**

Append to `web/src/App.test.tsx` (mirroring the file's existing Claude round-trip test, using whatever `mockUseAuth`/`mockGetStatus` mocking scaffolding it already has for the `useAuth`/`getStatus` mocks):

```tsx
it('switches to the files page and back via the header link', async () => {
  mockUseAuth.mockReturnValue({ user: { email: 'breynisson@gmail.com' }, loading: false, signIn: vi.fn(), signOut: vi.fn() });
  mockGetStatus.mockResolvedValue({ 'memory-svc': 'up' });
  const { default: App } = await import('./App');
  render(<App />);
  await screen.findByText(/soulman dashboard/i);

  await userEvent.click(screen.getByRole('button', { name: /files/i }));

  expect(await screen.findByRole('heading', { name: /files/i })).toBeInTheDocument();

  await userEvent.click(screen.getByText(/soulman/i));

  expect(await screen.findByText(/soulman dashboard/i)).toBeInTheDocument();
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/App.test.tsx`
Expected: FAIL — no button named "Files" exists yet

- [ ] **Step 3: Write minimal implementation**

Create `web/src/components/FilesPage.tsx`:

```tsx
import { FileRootList } from './FileRootList';

export function FilesPage({ onBack }: { onBack: () => void }) {
  return (
    <div className="min-h-screen bg-gray-50 p-6">
      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Files</h1>
        <button onClick={onBack} className="text-sm text-gray-500 underline">
          ← Soulman
        </button>
      </div>
      <FileRootList />
    </div>
  );
}
```

In `web/src/components/Dashboard.tsx`, add the `onOpenFiles` prop and a "Files" button between "Obsidian" and "Sign out":

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
}: {
  initialStatus: ServiceStatus | null;
  onSignOut: () => void;
  onOpenObsidian: () => void;
  onOpenClaude: () => void;
  onOpenFiles: () => void;
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

In `web/src/App.tsx`:

```tsx
import { useEffect, useState } from 'react';
import { useAuth, getAccessToken } from './auth';
import { getStatus, ApiError, type ServiceStatus } from './api';
import { LoginScreen } from './components/LoginScreen';
import { RestrictedScreen } from './components/RestrictedScreen';
import { Dashboard } from './components/Dashboard';
import { ObsidianPage } from './components/ObsidianPage';
import { ClaudePage } from './components/ClaudePage';
import { FilesPage } from './components/FilesPage';
import { getParam, setParams } from './urlState';

type ViewState = 'loading' | 'login' | 'restricted' | 'dashboard' | 'obsidian' | 'claude' | 'files';

function viewFromPageParam(): ViewState {
  const page = getParam('page');
  if (page === 'obsidian') return 'obsidian';
  if (page === 'claude') return 'claude';
  if (page === 'files') return 'files';
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
  if (view === 'files') {
    return (
      <FilesPage
        onBack={() => {
          setParams({ page: null, fileRoot: null, filePath: null });
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
      onOpenFiles={() => {
        setParams({ page: 'files' });
        setView('files');
      }}
    />
  );
}

export default App;
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/App.test.tsx`
Expected: PASS

Then run the full frontend and backend suites to confirm no regressions:

Run: `cd web && npx vitest run`
Expected: PASS — every test file

Run: `go test ./web-svc/... ./common/...` (from the vault root)
Expected: PASS — every package

- [ ] **Step 5: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add web/src/components/FilesPage.tsx web/src/components/Dashboard.tsx web/src/App.tsx web/src/App.test.tsx
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "feat(web): wire the Files page into App.tsx and Dashboard.tsx"
```

---

## Manual Verification (after all tasks land)

Per the spec's Testing section — exercise the full flow against dev (`localhost:5190`) before treating this as done:
1. Rebuild and restart `web-svc` and `web` in `soulman-dev` (via the `deploy-soulman-services` skill — never a hand-rolled robocopy/copy against `soulman-dev`).
2. Open the dashboard, click "Files", browse into a nested subfolder, download a real file and confirm its bytes match.
3. Upload a file, upload the same name again and confirm the overwrite prompt, then confirm the retry succeeds.
4. Refresh the page mid-browse and confirm the URL (`?page=files&fileRoot=...&filePath=...`) restores position.
