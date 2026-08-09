package claudesession

import (
	"errors"
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
