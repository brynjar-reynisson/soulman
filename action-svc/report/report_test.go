package report_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"soulman/action-svc/report"
)

func TestAppend_CreatesReportsDirAndFile(t *testing.T) {
	root := t.TempDir()
	occurred := time.Date(2026, 7, 17, 14, 32, 0, 0, time.Local)

	path, err := report.Append(root, report.Entry{
		Summary:    "DigitalMe sync failed: connection timeout to remote host.",
		RawContent: "full stack trace",
		SourcePath: `C:\Users\Lenovo\DigitalMe\errors\err1.txt`,
		OccurredAt: occurred,
		Important:  true,
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	want := filepath.Join(root, "reports", "daily-report-2026-07-17.txt")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	got := string(b)

	if !strings.Contains(got, "2026-07-17 14:32") {
		t.Errorf("entry missing timestamp: %q", got)
	}
	if !strings.Contains(got, `[C:\Users\Lenovo\DigitalMe\errors]`) {
		t.Errorf("entry missing bracketed source dir: %q", got)
	}
	if !strings.Contains(got, "DigitalMe sync failed") {
		t.Errorf("entry missing summary: %q", got)
	}
	if !strings.Contains(got, "full stack trace") {
		t.Errorf("entry missing raw content: %q", got)
	}
}

func TestAppend_UsesOccurredAtDate_NotToday(t *testing.T) {
	root := t.TempDir()
	occurred := time.Date(2020, 1, 1, 9, 0, 0, 0, time.Local)

	path, err := report.Append(root, report.Entry{Summary: "s", OccurredAt: occurred, Important: true})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if filepath.Base(path) != "daily-report-2020-01-01.txt" {
		t.Errorf("filename = %q, want daily-report-2020-01-01.txt", filepath.Base(path))
	}
}

func TestAppend_SecondEntry_PrecededByExactlyOneBlankLine(t *testing.T) {
	root := t.TempDir()
	day := time.Date(2026, 7, 17, 8, 0, 0, 0, time.Local)

	if _, err := report.Append(root, report.Entry{Summary: "first", OccurredAt: day, Important: true}); err != nil {
		t.Fatalf("first append: %v", err)
	}
	day2 := time.Date(2026, 7, 17, 9, 0, 0, 0, time.Local)
	if _, err := report.Append(root, report.Entry{Summary: "second", OccurredAt: day2, Important: true}); err != nil {
		t.Fatalf("second append: %v", err)
	}

	b, err := os.ReadFile(report.PathForDate(root, day, true))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	lines := strings.Split(string(b), "\n")
	idx := -1
	for i, l := range lines {
		if strings.Contains(l, "second") {
			idx = i
			break
		}
	}
	if idx < 2 {
		t.Fatalf("could not locate second entry with a preceding blank line in:\n%s", string(b))
	}
	if lines[idx-1] != "" {
		t.Errorf("line before second entry = %q, want blank", lines[idx-1])
	}
	if lines[idx-2] == "" {
		t.Errorf("expected exactly one blank line, found two")
	}
}

func TestAppend_SecondEntry_PrecededByExactlyOneBlankLine_WhenRawContentEndsInNewline(t *testing.T) {
	root := t.TempDir()
	day := time.Date(2026, 7, 17, 8, 0, 0, 0, time.Local)

	if _, err := report.Append(root, report.Entry{
		Summary:    "first",
		RawContent: "trailing newline in raw content\n",
		SourcePath: `C:\errors\err1.txt`,
		OccurredAt: day,
		Important:  true,
	}); err != nil {
		t.Fatalf("first append: %v", err)
	}
	day2 := time.Date(2026, 7, 17, 9, 0, 0, 0, time.Local)
	if _, err := report.Append(root, report.Entry{Summary: "second", OccurredAt: day2, Important: true}); err != nil {
		t.Fatalf("second append: %v", err)
	}

	b, err := os.ReadFile(report.PathForDate(root, day, true))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(b)

	idx := strings.Index(got, "2026-07-17 09:00")
	if idx < 0 {
		t.Fatalf("could not locate second entry's header in:\n%s", got)
	}
	// Exactly one blank line means exactly two consecutive '\n' characters
	// immediately before the second entry's header — not three (which would
	// be two blank lines).
	if !strings.HasSuffix(got[:idx], "\n\n") {
		t.Fatalf("expected second entry preceded by exactly \\n\\n, got tail: %q", got[:idx])
	}
	if strings.HasSuffix(got[:idx], "\n\n\n") {
		t.Errorf("expected exactly one blank line before second entry, found two:\n%s", got)
	}
}

func TestAppend_SecondEntry_PrecededByExactlyOneBlankLine_WhenRawContentEndsInThreeNewlines(t *testing.T) {
	root := t.TempDir()
	day := time.Date(2026, 7, 17, 8, 0, 0, 0, time.Local)

	if _, err := report.Append(root, report.Entry{
		Summary:    "first",
		RawContent: "trailing newlines in raw content\n\n\n",
		SourcePath: `C:\errors\err1.txt`,
		OccurredAt: day,
		Important:  true,
	}); err != nil {
		t.Fatalf("first append: %v", err)
	}
	day2 := time.Date(2026, 7, 17, 9, 0, 0, 0, time.Local)
	if _, err := report.Append(root, report.Entry{Summary: "second", OccurredAt: day2, Important: true}); err != nil {
		t.Fatalf("second append: %v", err)
	}

	b, err := os.ReadFile(report.PathForDate(root, day, true))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(b)

	idx := strings.Index(got, "2026-07-17 09:00")
	if idx < 0 {
		t.Fatalf("could not locate second entry's header in:\n%s", got)
	}
	// Exactly one blank line means exactly two consecutive '\n' characters
	// immediately before the second entry's header — not three or more
	// (which would mean two or more blank lines).
	if !strings.HasSuffix(got[:idx], "\n\n") {
		t.Fatalf("expected second entry preceded by exactly \\n\\n, got tail: %q", got[:idx])
	}
	if strings.HasSuffix(got[:idx], "\n\n\n") {
		t.Errorf("expected exactly one blank line before second entry, found two or more:\n%s", got)
	}
}

func TestAppend_EmptyRawContent_NoTrailingBodyLine(t *testing.T) {
	root := t.TempDir()
	day := time.Date(2026, 7, 17, 8, 0, 0, 0, time.Local)

	path, err := report.Append(root, report.Entry{
		Summary:    "err1.bin (binary, see attachment)",
		RawContent: "",
		SourcePath: `C:\errors\err1.bin`,
		OccurredAt: day,
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	b, _ := os.ReadFile(path)
	if strings.Count(string(b), "\n") != 0 {
		t.Errorf("expected a single line with no raw content, got: %q", string(b))
	}
}

func TestPathForDate_NormalizesToLocal(t *testing.T) {
	root := t.TempDir()

	// Same instant, expressed in two fixed zones 24 hours apart, so the
	// UTC-side representation is guaranteed to have crossed a calendar-day
	// boundary relative to whatever zone .Local() resolves to on this
	// machine. Rather than reasoning about which specific calendar day that
	// lands on (which would depend on the host's real timezone), we assert
	// the property PathForDate must have: it normalizes its input via
	// .Local() before formatting, so any two time.Time values representing
	// the same instant must yield the same path, and the result must match
	// computing date.Local().Format(...) inline.
	instant := time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)
	zonePlus12 := instant.In(time.FixedZone("UTC+12", 12*60*60))
	zoneMinus12 := instant.In(time.FixedZone("UTC-12", -12*60*60))

	gotPlus := report.PathForDate(root, zonePlus12, true)
	gotMinus := report.PathForDate(root, zoneMinus12, true)

	wantFilename := fmt.Sprintf("daily-report-%s.txt", instant.Local().Format("2006-01-02"))
	want := filepath.Join(root, "reports", wantFilename)

	if gotPlus != want {
		t.Errorf("PathForDate(+12 zone) = %q, want %q", gotPlus, want)
	}
	if gotMinus != want {
		t.Errorf("PathForDate(-12 zone) = %q, want %q", gotMinus, want)
	}
	if gotPlus != report.PathForDate(root, zonePlus12.Local(), true) {
		t.Errorf("PathForDate(t) = %q, want same as PathForDate(t.Local()) = %q", gotPlus, report.PathForDate(root, zonePlus12.Local(), true))
	}
}

func TestRead_MissingFile_ReturnsEmptyNoError(t *testing.T) {
	root := t.TempDir()
	content, err := report.Read(root, time.Date(2026, 7, 17, 0, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if content != "" {
		t.Errorf("content = %q, want empty for missing file", content)
	}
}

func TestRead_ReturnsWrittenContent(t *testing.T) {
	root := t.TempDir()
	day := time.Date(2026, 7, 17, 0, 0, 0, 0, time.Local)
	if _, err := report.Append(root, report.Entry{Summary: "hello", OccurredAt: day}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	content, err := report.Read(root, day)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(content, "hello") {
		t.Errorf("content = %q, want it to contain %q", content, "hello")
	}
}

func TestAppend_CreatesSoulmanRootIfMissing(t *testing.T) {
	// SOULMAN_ROOT may not exist on this machine — Append must create the
	// whole path, including root itself, not just reports/.
	root := filepath.Join(t.TempDir(), "does-not-exist-yet", "soulman-dev")
	day := time.Date(2026, 7, 17, 0, 0, 0, 0, time.Local)

	if _, err := report.Append(root, report.Entry{Summary: "s", OccurredAt: day}); err != nil {
		t.Fatalf("Append should create missing root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "reports")); err != nil {
		t.Errorf("reports dir was not created: %v", err)
	}
}

func TestPathForDate_ImportantVsNotImportant(t *testing.T) {
	root := t.TempDir()
	day := time.Date(2026, 7, 20, 0, 0, 0, 0, time.Local)

	important := report.PathForDate(root, day, true)
	notImportant := report.PathForDate(root, day, false)

	if filepath.Base(important) != "daily-report-2026-07-20.txt" {
		t.Errorf("important path = %q, want daily-report-2026-07-20.txt", filepath.Base(important))
	}
	if filepath.Base(notImportant) != "daily-report-2026-07-20-fyi.txt" {
		t.Errorf("not-important path = %q, want daily-report-2026-07-20-fyi.txt", filepath.Base(notImportant))
	}
}

func TestAppend_ImportantEntry_WritesToImportantFile(t *testing.T) {
	root := t.TempDir()
	day := time.Date(2026, 7, 20, 8, 0, 0, 0, time.Local)

	path, err := report.Append(root, report.Entry{Summary: "s", OccurredAt: day, Important: true})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if path != report.PathForDate(root, day, true) {
		t.Errorf("path = %q, want the important-file path", path)
	}
}

func TestAppend_NotImportantEntry_WritesToFYIFile(t *testing.T) {
	root := t.TempDir()
	day := time.Date(2026, 7, 20, 8, 0, 0, 0, time.Local)

	path, err := report.Append(root, report.Entry{Summary: "s", OccurredAt: day, Important: false})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if path != report.PathForDate(root, day, false) {
		t.Errorf("path = %q, want the not-important-file path", path)
	}
}

func TestRead_CombinesImportantAndNotImportant_WithHeadings(t *testing.T) {
	root := t.TempDir()
	day := time.Date(2026, 7, 20, 8, 0, 0, 0, time.Local)

	if _, err := report.Append(root, report.Entry{Summary: "important thing", OccurredAt: day, Important: true}); err != nil {
		t.Fatalf("append important: %v", err)
	}
	if _, err := report.Append(root, report.Entry{Summary: "fyi thing", OccurredAt: day, Important: false}); err != nil {
		t.Fatalf("append fyi: %v", err)
	}

	content, err := report.Read(root, day)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if !strings.Contains(content, "## Important") {
		t.Errorf("content missing '## Important' heading:\n%s", content)
	}
	if !strings.Contains(content, "## Not Important") {
		t.Errorf("content missing '## Not Important' heading:\n%s", content)
	}
	if !strings.Contains(content, "important thing") {
		t.Errorf("content missing important entry:\n%s", content)
	}
	if !strings.Contains(content, "fyi thing") {
		t.Errorf("content missing fyi entry:\n%s", content)
	}
	importantIdx := strings.Index(content, "## Important")
	notImportantIdx := strings.Index(content, "## Not Important")
	if importantIdx < 0 || notImportantIdx < 0 || importantIdx > notImportantIdx {
		t.Errorf("expected '## Important' to appear before '## Not Important', got:\n%s", content)
	}
}

func TestRead_OnlyImportantEntries_OmitsNotImportantHeading(t *testing.T) {
	root := t.TempDir()
	day := time.Date(2026, 7, 20, 8, 0, 0, 0, time.Local)

	if _, err := report.Append(root, report.Entry{Summary: "important thing", OccurredAt: day, Important: true}); err != nil {
		t.Fatalf("append: %v", err)
	}

	content, err := report.Read(root, day)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if strings.Contains(content, "## Not Important") {
		t.Errorf("content should not have '## Not Important' heading when that file is empty/missing:\n%s", content)
	}
}
