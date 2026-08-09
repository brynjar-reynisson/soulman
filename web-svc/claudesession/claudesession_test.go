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
