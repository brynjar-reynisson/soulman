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
