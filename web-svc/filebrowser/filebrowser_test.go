package filebrowser_test

import (
	"encoding/json"
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
