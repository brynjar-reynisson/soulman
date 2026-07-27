package logmonitor

import (
	"path/filepath"
	"testing"
)

func TestLoadCheckpoint_MissingFile_StartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logmonitor-checkpoint.json")
	c := loadCheckpoint(path)
	if _, ok := c.offsetFor("memory-svc-startup-err.log"); ok {
		t.Error("offsetFor should report no entry for a checkpoint with no data")
	}
}
