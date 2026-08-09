// Package claudesession launches Claude Code "--remote-control" sessions
// rooted in a curated set of project folders (web.claude_project_roots).
// Fire-and-forget: once the process starts, this package no longer
// tracks it — session lifecycle is owned entirely by Claude Code's own
// remote-control registration (claude.ai/code). See
// docs/superpowers/specs/2026-08-09-claude-remote-sessions-design.md.
package claudesession

import (
	"errors"
	"os"
	"sort"
)

var (
	ErrNotFound     = errors.New("claudesession: not found")
	ErrInvalidName  = errors.New("claudesession: invalid name")
	ErrLaunchFailed = errors.New("claudesession: launch failed")
)

// Root identifies one curated project-folder root: a human-readable
// label (matched against a launch request's "root" field) and the
// filesystem path it corresponds to.
type Root struct {
	Label string
	Path  string
}

// RootListing is a Root plus its current filesystem state.
type RootListing struct {
	Label   string
	Path    string
	Exists  bool
	Folders []string
}

// ListRoots reports the current state of each configured root. A root
// whose Path doesn't exist (or isn't a directory) is reported with
// Exists: false and a nil Folders slice, rather than being omitted or
// treated as an error — a temporarily missing root should not take
// down the whole roots listing.
func ListRoots(roots []Root) []RootListing {
	listings := make([]RootListing, len(roots))
	for i, root := range roots {
		info, err := os.Stat(root.Path)
		if err != nil || !info.IsDir() {
			listings[i] = RootListing{Label: root.Label, Path: root.Path, Exists: false}
			continue
		}
		listings[i] = RootListing{Label: root.Label, Path: root.Path, Exists: true, Folders: listFolders(root.Path)}
	}
	return listings
}

func listFolders(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return []string{}
	}
	folders := []string{}
	for _, e := range entries {
		if e.IsDir() {
			folders = append(folders, e.Name())
		}
	}
	sort.Strings(folders)
	return folders
}
