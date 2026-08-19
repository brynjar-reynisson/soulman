package filebrowser_test

import (
	"bytes"
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
