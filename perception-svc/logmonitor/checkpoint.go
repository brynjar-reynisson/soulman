package logmonitor

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
)

// checkpoint tracks the byte offset each tracked log file has been read up
// to, persisted as JSON so no ERROR line is silently skipped if
// perception-svc is briefly down while another service keeps writing —
// a materially different failure mode than watcher's own checkpoint (which
// exists so file *identity* survives restarts, not so no byte is missed).
type checkpoint struct {
	mu     sync.Mutex
	path   string
	offset map[string]int64 // filename -> byte offset already read
}

// loadCheckpoint reads the checkpoint file at path. A missing, unreadable,
// or corrupt file logs and starts empty — same accepted tradeoff as
// watcher.LoadCheckpoint (worst case: the affected file's first read starts
// at 0 instead of its last-known offset, but combined with the in-memory
// dedup map this just means each distinct historical error type in that
// file alerts once more, not a flood).
func loadCheckpoint(path string) *checkpoint {
	c := &checkpoint{path: path, offset: map[string]int64{}}

	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("logmonitor: checkpoint read failed, starting empty", "path", path, "error", err)
		}
		return c
	}

	var data map[string]int64
	if err := json.Unmarshal(b, &data); err != nil {
		slog.Warn("logmonitor: checkpoint parse failed, starting empty", "path", path, "error", err)
		return c
	}

	// json.Unmarshal of null succeeds and sets data to nil (documented Go behavior),
	// which would cause a panic on subsequent map assignment. Default to empty.
	if data == nil {
		data = map[string]int64{}
	}
	c.offset = data
	return c
}

// offsetFor returns the stored byte offset for filename, and whether an
// entry existed at all.
func (c *checkpoint) offsetFor(filename string) (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	off, ok := c.offset[filename]
	return off, ok
}

// resolveStart returns the byte offset a read should start from for
// filename, given the file's current size on disk. Three cases: no
// checkpoint entry yet (first run) starts at the current size (EOF) so old
// history is never replayed; a stored offset within the current size
// resumes from there; a stored offset beyond the current size means the
// file was truncated (e.g. manually cleared, as memory-svc-startup-err.log
// was on 2026-07-27) and resets to 0.
func (c *checkpoint) resolveStart(filename string, currentSize int64) int64 {
	off, ok := c.offsetFor(filename)
	if !ok {
		return currentSize
	}
	if off > currentSize {
		return 0
	}
	return off
}

// mark records filename's new byte offset and persists the checkpoint to
// disk. Called after every successful read (not every line), so a crash
// mid-batch re-reads from the last fully-processed offset rather than
// losing partial progress.
func (c *checkpoint) mark(filename string, offset int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.offset[filename] = offset
	return c.saveLocked()
}

func (c *checkpoint) saveLocked() error {
	b, err := json.MarshalIndent(c.offset, "", "  ")
	if err != nil {
		return fmt.Errorf("logmonitor: checkpoint marshal: %w", err)
	}
	if err := os.WriteFile(c.path, b, 0o644); err != nil {
		return fmt.Errorf("logmonitor: checkpoint write %s: %w", c.path, err)
	}
	return nil
}
