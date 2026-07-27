package logmonitor

import (
	"encoding/json"
	"os"
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

func TestLoadCheckpoint_CorruptFile_StartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logmonitor-checkpoint.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	c := loadCheckpoint(path)
	if _, ok := c.offsetFor("memory-svc-startup-err.log"); ok {
		t.Error("offsetFor should report no entry after falling back to empty checkpoint on corrupt file")
	}
}

func TestCheckpoint_Mark_PersistsToDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logmonitor-checkpoint.json")
	c := loadCheckpoint(path)
	if err := c.mark("memory-svc-startup-err.log", 1024); err != nil {
		t.Fatalf("mark: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var data map[string]int64
	if err := json.Unmarshal(b, &data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if data["memory-svc-startup-err.log"] != 1024 {
		t.Errorf("persisted offset = %d, want 1024", data["memory-svc-startup-err.log"])
	}
}

func TestLoadCheckpoint_ReloadsFromDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logmonitor-checkpoint.json")
	c1 := loadCheckpoint(path)
	c1.mark("memory-svc-startup-err.log", 2048)

	c2 := loadCheckpoint(path)
	off, ok := c2.offsetFor("memory-svc-startup-err.log")
	if !ok || off != 2048 {
		t.Errorf("offsetFor after reload = (%d, %v), want (2048, true)", off, ok)
	}
}

func TestResolveStart_NoEntry_StartsAtCurrentSize(t *testing.T) {
	c := loadCheckpoint(filepath.Join(t.TempDir(), "logmonitor-checkpoint.json"))
	if got := c.resolveStart("new-file-startup-err.log", 5000); got != 5000 {
		t.Errorf("resolveStart = %d, want 5000 (first run starts at EOF)", got)
	}
}

func TestResolveStart_StoredOffsetWithinCurrentSize_ResumesFromOffset(t *testing.T) {
	c := loadCheckpoint(filepath.Join(t.TempDir(), "logmonitor-checkpoint.json"))
	c.mark("memory-svc-startup-err.log", 1000)
	if got := c.resolveStart("memory-svc-startup-err.log", 2000); got != 1000 {
		t.Errorf("resolveStart = %d, want 1000 (resume from stored offset)", got)
	}
}

func TestResolveStart_StoredOffsetBeyondCurrentSize_TruncationResetsToZero(t *testing.T) {
	c := loadCheckpoint(filepath.Join(t.TempDir(), "logmonitor-checkpoint.json"))
	c.mark("memory-svc-startup-err.log", 5000)
	if got := c.resolveStart("memory-svc-startup-err.log", 100); got != 0 {
		t.Errorf("resolveStart = %d, want 0 (file truncated below stored offset)", got)
	}
}

func TestResolveStart_StoredOffsetEqualsCurrentSize_NoNewContent(t *testing.T) {
	c := loadCheckpoint(filepath.Join(t.TempDir(), "logmonitor-checkpoint.json"))
	c.mark("memory-svc-startup-err.log", 3000)
	if got := c.resolveStart("memory-svc-startup-err.log", 3000); got != 3000 {
		t.Errorf("resolveStart = %d, want 3000 (no new content, offset unchanged)", got)
	}
}
