package reports_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"soulman/web-svc/reports"
)

func TestPathForDate_ImportantVsNotImportant(t *testing.T) {
	date := time.Date(2026, 7, 19, 0, 0, 0, 0, time.Local)

	important := reports.PathForDate(`C:\root`, date, true)
	notImportant := reports.PathForDate(`C:\root`, date, false)

	wantImportant := filepath.Join(`C:\root`, "reports", "daily-report-2026-07-19.txt")
	wantNotImportant := filepath.Join(`C:\root`, "reports", "daily-report-2026-07-19-fyi.txt")
	if important != wantImportant {
		t.Errorf("important path = %q, want %q", important, wantImportant)
	}
	if notImportant != wantNotImportant {
		t.Errorf("not-important path = %q, want %q", notImportant, wantNotImportant)
	}
}

func TestRead_ImportantFileOnly_ReturnsWrappedContentAndFound(t *testing.T) {
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
	if content != "## Important\n\n2026-07-19 09:00  [test]  hello\n" {
		t.Errorf("content = %q", content)
	}
}

func TestRead_BothFiles_CombinesWithHeadings(t *testing.T) {
	root := t.TempDir()
	date := time.Date(2026, 7, 19, 0, 0, 0, 0, time.Local)
	dir := filepath.Join(root, "reports")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "daily-report-2026-07-19.txt"), []byte("important entry"), 0o644)
	os.WriteFile(filepath.Join(dir, "daily-report-2026-07-19-fyi.txt"), []byte("fyi entry"), 0o644)

	content, found, err := reports.Read(root, date)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !found {
		t.Fatal("found = false, want true")
	}
	want := "## Important\n\nimportant entry\n\n## Not Important\n\nfyi entry\n"
	if content != want {
		t.Errorf("content = %q, want %q", content, want)
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
