package reports_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"soulman/web-svc/reports"
)

func TestPathForDate_UsesDailyReportNamingConvention(t *testing.T) {
	date := time.Date(2026, 7, 19, 0, 0, 0, 0, time.Local)
	got := reports.PathForDate(`C:\root`, date)
	want := filepath.Join(`C:\root`, "reports", "daily-report-2026-07-19.txt")
	if got != want {
		t.Errorf("PathForDate = %q, want %q", got, want)
	}
}

func TestRead_ExistingFile_ReturnsContentAndFound(t *testing.T) {
	root := t.TempDir()
	date := time.Date(2026, 7, 19, 0, 0, 0, 0, time.Local)
	dir := filepath.Join(root, "reports")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "daily-report-2026-07-19.txt"), []byte("2026-07-19 09:00  [test]  hello"), 0o644)

	content, found, err := reports.Read(root, date)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !found {
		t.Fatal("found = false, want true")
	}
	if content != "2026-07-19 09:00  [test]  hello" {
		t.Errorf("content = %q", content)
	}
}

func TestRead_MissingFile_ReturnsNotFoundNoError(t *testing.T) {
	root := t.TempDir()
	date := time.Date(2026, 7, 19, 0, 0, 0, 0, time.Local)

	content, found, err := reports.Read(root, date)
	if err != nil {
		t.Fatalf("Read() error = %v, want nil", err)
	}
	if found {
		t.Fatal("found = true, want false")
	}
	if content != "" {
		t.Errorf("content = %q, want empty", content)
	}
}
