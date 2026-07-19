package storage

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"soulman/common"
)

const DefaultMaxFileSize = 10 * 1024 * 1024

type stimulusRecord struct {
	Type string `json:"_type"`
	*common.Stimulus
}

type syncedRecord struct {
	Type       string `json:"_type"`
	StimulusID string `json:"stimulus_id"`
}

type FileLog struct {
	path    string
	maxSize int64
	mu      sync.Mutex
	f       *os.File
}

func NewFileLog(dir string, maxSize int64) (*FileLog, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("filelog: mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, "raw_inputs.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("filelog: open %s: %w", path, err)
	}
	return &FileLog{path: path, maxSize: maxSize, f: f}, nil
}

func (fl *FileLog) AppendStimulus(s *common.Stimulus) error {
	fl.mu.Lock()
	defer fl.mu.Unlock()

	b, err := json.Marshal(stimulusRecord{Type: "stimulus", Stimulus: s})
	if err != nil {
		return fmt.Errorf("filelog: marshal stimulus: %w", err)
	}
	if err := fl.writeLine(b); err != nil {
		return err
	}
	return fl.rotateIfNeeded()
}

func (fl *FileLog) AppendSynced(stimulusID string) error {
	fl.mu.Lock()
	defer fl.mu.Unlock()

	b, err := json.Marshal(syncedRecord{Type: "synced", StimulusID: stimulusID})
	if err != nil {
		return fmt.Errorf("filelog: marshal synced: %w", err)
	}
	return fl.writeLine(b)
}

func (fl *FileLog) writeLine(b []byte) error {
	b = append(b, '\n')
	if _, err := fl.f.Write(b); err != nil {
		return fmt.Errorf("filelog: write: %w", err)
	}
	return nil
}

// ScanPending returns Stimulus entries that have no matching synced record.
// Scans both raw_inputs.jsonl.1 (rotation backup) and raw_inputs.jsonl.
func (fl *FileLog) ScanPending() ([]*common.Stimulus, error) {
	fl.mu.Lock()
	defer fl.mu.Unlock()

	stimuli := map[string]*common.Stimulus{}
	synced := map[string]bool{}

	for _, path := range []string{fl.path + ".1", fl.path} {
		if err := scanFile(path, stimuli, synced); err != nil {
			return nil, err
		}
	}

	var pending []*common.Stimulus
	for id, s := range stimuli {
		if !synced[id] {
			pending = append(pending, s)
		}
	}
	return pending, nil
}

func scanFile(path string, stimuli map[string]*common.Stimulus, synced map[string]bool) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("filelog: scan open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		var rec struct {
			Type string `json:"_type"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		switch rec.Type {
		case "stimulus":
			var sr stimulusRecord
			if err := json.Unmarshal(line, &sr); err == nil && sr.Stimulus != nil {
				stimuli[sr.StimulusID] = sr.Stimulus
			}
		case "synced":
			var sr syncedRecord
			if err := json.Unmarshal(line, &sr); err == nil {
				synced[sr.StimulusID] = true
			}
		}
	}
	return scanner.Err()
}

func (fl *FileLog) rotateIfNeeded() error {
	info, err := fl.f.Stat()
	if err != nil || info.Size() < fl.maxSize {
		return nil
	}
	if err := fl.f.Close(); err != nil {
		log.Printf("filelog: close before rotate: %v", err)
	}
	if err := os.Rename(fl.path, fl.path+".1"); err != nil {
		return fmt.Errorf("filelog: rotate rename: %w", err)
	}
	fl.f = nil
	f, err := os.OpenFile(fl.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("filelog: reopen after rotate: %w", err)
	}
	fl.f = f
	return nil
}

func (fl *FileLog) Close() error {
	fl.mu.Lock()
	defer fl.mu.Unlock()
	if fl.f == nil {
		return nil
	}
	return fl.f.Close()
}
