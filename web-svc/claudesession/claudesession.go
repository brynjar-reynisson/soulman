// Package claudesession launches Claude Code "--remote-control" sessions
// rooted in a curated set of project folders (web.claude_project_roots).
// Fire-and-forget: once the process starts, this package no longer
// tracks it — session lifecycle is owned entirely by Claude Code's own
// remote-control registration (claude.ai/code). See
// docs/superpowers/specs/2026-08-09-claude-remote-sessions-design.md.
package claudesession

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
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

// validSegment rejects anything that isn't a single, plain path
// component — see web-svc/obsidian's identical guard for the full
// rationale (path traversal and NTFS alternate-data-stream protection).
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

// resolveDir validates folder as a single path segment directly under
// root.Path, confirms the joined path stays within root.Path, and
// confirms it exists and is a directory.
func resolveDir(root Root, folder string) (string, error) {
	if !validSegment(folder) {
		return "", ErrInvalidName
	}
	cleanRoot := filepath.Clean(root.Path)
	dir := filepath.Join(cleanRoot, folder)
	if !isWithin(cleanRoot, dir) {
		return "", ErrInvalidName
	}
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("claudesession: stat %s: %w", dir, err)
	}
	if !info.IsDir() {
		return "", ErrNotFound
	}
	return dir, nil
}

// Launch starts `claude --remote-control --bg --name sessionName` detached,
// with its working directory set to root.Path/folder. --bg is required:
// without it, `claude --remote-control` runs as a normal interactive
// foreground session expecting a real terminal, but Launch gives the
// child no stdin/stdout/stderr (all three go to the null device, since
// this is fire-and-forget) — without a terminal to attach to, the plain
// command exits immediately and silently, with nothing left running and
// no error anywhere to explain why (see web-svc/NOTES.md's "Claude
// remote-session launcher" section for the incident this fixed). Launch
// does not wait for the process, capture its output, or track it after
// Start() succeeds. sessionName is passed as a literal exec.Command
// argument (never through a shell), so it carries no injection risk
// regardless of its contents.
func Launch(root Root, folder, sessionName string) error {
	if sessionName == "" {
		return ErrInvalidName
	}
	dir, err := resolveDir(root, folder)
	if err != nil {
		return err
	}
	cmd := exec.Command("claude", "--remote-control", "--bg", "--name", sessionName)
	cmd.Dir = dir
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%w: %v", ErrLaunchFailed, err)
	}
	cmd.Process.Release()
	return nil
}
