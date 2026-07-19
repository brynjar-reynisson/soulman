// Package reports reads action-svc's daily report files
// ($SOULMAN_ROOT/reports/daily-report-YYYY-MM-DD.txt). This duplicates
// action-svc/report's PathForDate/Read logic rather than adding a
// cross-module Go dependency, consistent with this codebase's convention
// of keeping each service an independent module.
package reports

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func PathForDate(root string, date time.Time) string {
	filename := fmt.Sprintf("daily-report-%s.txt", date.Format("2006-01-02"))
	return filepath.Join(root, "reports", filename)
}

// Read returns the report file's content for the given date. found is
// false (with a nil error) if the file doesn't exist yet — that's an
// expected, non-error condition, not a failure.
func Read(root string, date time.Time) (content string, found bool, err error) {
	path := PathForDate(root, date)
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("reports: read %s: %w", path, err)
	}
	return string(b), true, nil
}
