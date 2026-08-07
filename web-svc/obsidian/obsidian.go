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
