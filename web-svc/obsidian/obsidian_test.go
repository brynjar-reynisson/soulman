package obsidian_test

import (
	"encoding/json"
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

func TestListFiles_EmptyFolder_SerializesAsEmptyArrayNotNull(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "vault"), 0o755)

	files, err := obsidian.ListFiles(root, "vault")
	if err != nil {
		t.Fatalf("ListFiles() error = %v", err)
	}
	if files == nil {
		t.Fatalf("ListFiles() returned nil slice, want non-nil empty slice")
	}
	if len(files) != 0 {
		t.Fatalf("ListFiles() = %v, want empty", files)
	}

	b, err := json.Marshal(files)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if string(b) != "[]" {
		t.Errorf("json.Marshal(ListFiles(...)) = %s, want []  (a nil slice would marshal to null, leaving the frontend stuck on \"Loading...\" forever)", b)
	}
}

func TestListFolders_EmptyRoot_SerializesAsEmptyArrayNotNull(t *testing.T) {
	root := t.TempDir()

	folders, err := obsidian.ListFolders(root)
	if err != nil {
		t.Fatalf("ListFolders() error = %v", err)
	}
	if folders == nil {
		t.Fatalf("ListFolders() returned nil slice, want non-nil empty slice")
	}

	b, err := json.Marshal(folders)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if string(b) != "[]" {
		t.Errorf("json.Marshal(ListFolders(...)) = %s, want []", b)
	}
}

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

func TestCreateFile_NameContainsColon_ReturnsErrInvalidName(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "vault")
	os.Mkdir(folder, 0o755)

	// On Windows/NTFS a colon in a filename addresses an alternate data
	// stream instead of creating a new base file — "2026-08-07 12:30
	// notes.md" would silently write into a hidden stream of a "12" file
	// rather than creating the note the user asked for. This must be
	// rejected up front rather than accepted and lost.
	for _, file := range []string{"2026-08-07 12:30 notes.md", "C:drive.md", "a:b.md"} {
		err := obsidian.CreateFile(root, "vault", file, "content")
		if !errors.Is(err, obsidian.ErrInvalidName) {
			t.Errorf("CreateFile(%q) error = %v, want ErrInvalidName", file, err)
		}
	}

	entries, err := os.ReadDir(folder)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no files to have been created, found %v", entries)
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
