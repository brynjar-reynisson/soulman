package watcher

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
)

// CheckpointEntry records what we know about the last time a file was
// published: its content hash (for change detection), its mtime at
// publish time, and when we published it.
type CheckpointEntry struct {
	Hash        string `json:"hash"`
	Mtime       string `json:"mtime"`
	PublishedAt string `json:"published_at"`
}

// Checkpoint tracks which files have already been published, keyed by
// folder path then filename. Persisted as JSON to a local file after every
// successful Mark.
type Checkpoint struct {
	mu   sync.Mutex
	path string
	data map[string]map[string]CheckpointEntry
}

// LoadCheckpoint reads the checkpoint file at path. If the file doesn't
// exist, or is unreadable/corrupt, it logs and starts with an empty
// checkpoint — this may re-publish everything currently present once,
// which is an accepted tradeoff per the perception-svc design spec.
func LoadCheckpoint(path string) *Checkpoint {
	c := &Checkpoint{path: path, data: map[string]map[string]CheckpointEntry{}}

	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("checkpoint: read %s failed, starting empty: %v", path, err)
		}
		return c
	}

	var data map[string]map[string]CheckpointEntry
	if err := json.Unmarshal(b, &data); err != nil {
		log.Printf("checkpoint: parse %s failed, starting empty: %v", path, err)
		return c
	}

	c.data = data
	return c
}

// IsNew reports whether filename in folder is absent from the checkpoint,
// or present but with a different content hash (file replaced under the
// same name).
func (c *Checkpoint) IsNew(folder, filename, hash string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	folderEntries, ok := c.data[folder]
	if !ok {
		return true
	}
	entry, ok := folderEntries[filename]
	if !ok {
		return true
	}
	return entry.Hash != hash
}

// Mark records filename as published and persists the checkpoint to disk.
// Call only after a successful publish — a crash between publish and Mark
// results in a harmless duplicate stimulus on restart (accepted per spec).
func (c *Checkpoint) Mark(folder, filename string, entry CheckpointEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.data[folder] == nil {
		c.data[folder] = map[string]CheckpointEntry{}
	}
	c.data[folder][filename] = entry

	return c.saveLocked()
}

func (c *Checkpoint) saveLocked() error {
	b, err := json.MarshalIndent(c.data, "", "  ")
	if err != nil {
		return fmt.Errorf("checkpoint: marshal: %w", err)
	}
	if err := os.WriteFile(c.path, b, 0o644); err != nil {
		return fmt.Errorf("checkpoint: write %s: %w", c.path, err)
	}
	return nil
}
