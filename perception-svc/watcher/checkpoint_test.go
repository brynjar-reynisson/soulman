package watcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCheckpoint_MissingFile_StartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoints.json")
	cp := LoadCheckpoint(path)

	if !cp.IsNew("C:\\errors", "any.txt", "sha256:whatever") {
		t.Error("IsNew should be true for a checkpoint with no entries")
	}
}

func TestLoadCheckpoint_CorruptFile_StartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoints.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cp := LoadCheckpoint(path)
	if !cp.IsNew("C:\\errors", "any.txt", "sha256:whatever") {
		t.Error("IsNew should be true after falling back to empty checkpoint on corrupt file")
	}
}

func TestCheckpoint_IsNew_UnseenFilename(t *testing.T) {
	cp := LoadCheckpoint(filepath.Join(t.TempDir(), "checkpoints.json"))
	if !cp.IsNew("folder", "file.txt", "sha256:abc") {
		t.Error("IsNew should be true for an unseen filename")
	}
}

func TestCheckpoint_Mark_ThenIsNew_SameHash_False(t *testing.T) {
	cp := LoadCheckpoint(filepath.Join(t.TempDir(), "checkpoints.json"))
	entry := CheckpointEntry{Hash: "sha256:abc", Mtime: "2026-07-17T00:00:00Z", PublishedAt: "2026-07-17T00:00:01Z"}

	if err := cp.Mark("folder", "file.txt", entry); err != nil {
		t.Fatalf("Mark: %v", err)
	}

	if cp.IsNew("folder", "file.txt", "sha256:abc") {
		t.Error("IsNew should be false for a filename already marked with the same hash")
	}
}

func TestCheckpoint_IsNew_ChangedHash_True(t *testing.T) {
	cp := LoadCheckpoint(filepath.Join(t.TempDir(), "checkpoints.json"))
	cp.Mark("folder", "file.txt", CheckpointEntry{Hash: "sha256:abc", Mtime: "t1", PublishedAt: "t2"})

	if !cp.IsNew("folder", "file.txt", "sha256:different") {
		t.Error("IsNew should be true when content hash has changed")
	}
}

func TestCheckpoint_Mark_PersistsToDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoints.json")
	cp := LoadCheckpoint(path)
	cp.Mark("folder", "file.txt", CheckpointEntry{Hash: "sha256:abc", Mtime: "t1", PublishedAt: "t2"})

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var data map[string]map[string]CheckpointEntry
	if err := json.Unmarshal(b, &data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	entry, ok := data["folder"]["file.txt"]
	if !ok {
		t.Fatal("persisted checkpoint missing folder/file.txt entry")
	}
	if entry.Hash != "sha256:abc" {
		t.Errorf("Hash = %q, want sha256:abc", entry.Hash)
	}
}

func TestLoadCheckpoint_ReloadsFromDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoints.json")
	cp1 := LoadCheckpoint(path)
	cp1.Mark("folder", "file.txt", CheckpointEntry{Hash: "sha256:abc", Mtime: "t1", PublishedAt: "t2"})

	cp2 := LoadCheckpoint(path)
	if cp2.IsNew("folder", "file.txt", "sha256:abc") {
		t.Error("reloaded checkpoint should retain previously marked entry")
	}
}
