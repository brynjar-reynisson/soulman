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
