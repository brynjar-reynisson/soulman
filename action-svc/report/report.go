package report

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Entry is one daily-report entry, matching the format from
// error-report-action-design.md: a header line (timestamp, source
// directory, one-line summary) optionally followed by verbatim raw content.
type Entry struct {
	Summary    string
	RawContent string
	SourcePath string
	OccurredAt time.Time
}

// PathForDate returns the report file path for the given date, using the
// YYYY-MM-DD of the *occurred* date (not "today") per the error-report
// spec. Shared by the dispatch handler (writes) and the scheduler (reads)
// so the two can never disagree on the filename convention.
func PathForDate(root string, date time.Time) string {
	filename := fmt.Sprintf("daily-report-%s.txt", date.Local().Format("2006-01-02"))
	return filepath.Join(root, "reports", filename)
}

// Append writes one entry to the report file for entry.OccurredAt's date,
// creating $root/reports/ (and the file) if either is missing. If the file
// already has content, the new entry is preceded by exactly one blank line.
//
// Append is not safe for concurrent use: it performs a read-then-write
// sequence (peeking the file's trailing newlines via separatorFor, possibly
// truncating, then appending) with no internal locking. Safe today because
// the sole caller (dispatch's NATS message handler) processes messages one
// at a time in a single goroutine; a future concurrent caller must add its
// own synchronization.
func Append(root string, e Entry) (string, error) {
	dir := filepath.Join(root, "reports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("report: mkdir %s: %w", dir, err)
	}

	path := PathForDate(root, e.OccurredAt)

	sep, err := separatorFor(path)
	if err != nil {
		return "", err
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("report: open %s: %w", path, err)
	}
	defer f.Close()

	text := sep + formatEntry(e)

	if _, err := f.WriteString(text); err != nil {
		return "", fmt.Errorf("report: write %s: %w", path, err)
	}
	return path, nil
}

// separatorFor returns the newline prefix that must be written before a new
// entry's text so that the file ends up with exactly one blank line between
// the previous entry's content and the new entry's header. It does this by
// counting how many trailing '\n' bytes the file already has (0, 1, 2, or
// more) rather than assuming a preceding entry's RawContent never itself
// ends in a newline — stack traces and file tails often do.
//
// The count is exact, not capped at a fixed-size tail read: report files are
// small daily text logs, so reading the whole file to scan backward from the
// end is simple and has no realistic size concern.
//
// Appending can only add newlines, never remove them, so when the file
// already ends in more than two newlines (RawContent that itself ends in a
// blank line, e.g. "\n\n\n"), no number of *added* newlines can bring the
// count back down to exactly two — the excess must be trimmed from the file
// first. In that case the file is truncated to end in exactly two trailing
// newlines and no additional separator is needed.
func separatorFor(path string) (string, error) {
	info, statErr := os.Stat(path)
	if statErr != nil || info.Size() == 0 {
		return "", nil
	}

	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("report: read %s: %w", path, err)
	}

	trailing := 0
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] != '\n' {
			break
		}
		trailing++
	}

	if trailing > 2 {
		if err := os.Truncate(path, info.Size()-int64(trailing-2)); err != nil {
			return "", fmt.Errorf("report: truncate %s: %w", path, err)
		}
		trailing = 2
	}

	needed := 2 - trailing
	if needed < 0 {
		needed = 0
	}
	return strings.Repeat("\n", needed), nil
}

func formatEntry(e Entry) string {
	header := fmt.Sprintf("%s  [%s]  %s",
		e.OccurredAt.Local().Format("2006-01-02 15:04"), filepath.Dir(e.SourcePath), e.Summary)
	if e.RawContent == "" {
		return header
	}
	return header + "\n" + e.RawContent
}

// Read returns the full contents of the report file for the given date, or
// "" (with no error) if the file doesn't exist yet.
func Read(root string, date time.Time) (string, error) {
	path := PathForDate(root, date)
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("report: read %s: %w", path, err)
	}
	return string(b), nil
}
