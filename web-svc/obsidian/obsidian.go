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
