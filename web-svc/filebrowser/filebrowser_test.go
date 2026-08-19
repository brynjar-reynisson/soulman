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
