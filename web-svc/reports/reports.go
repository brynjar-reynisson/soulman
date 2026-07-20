// Package reports reads action-svc's daily report files
// ($SOULMAN_ROOT/reports/daily-report-YYYY-MM-DD.txt for important entries,
// daily-report-YYYY-MM-DD-fyi.txt for not-important ones). This duplicates
// action-svc/report's PathForDate/Read logic rather than adding a
// cross-module Go dependency, consistent with this codebase's convention
// of keeping each service an independent module.
package reports

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PathForDate returns the report file path for the given date and
// importance. The important filename is unchanged from before this
// feature (daily-report-<date>.txt).
func PathForDate(root string, date time.Time, important bool) string {
	suffix := ""
	if !important {
		suffix = "-fyi"
	}
	filename := fmt.Sprintf("daily-report-%s%s.txt", date.Format("2006-01-02"), suffix)
	return filepath.Join(root, "reports", filename)
}

// Read returns the combined view for the given date: important entries
// under a "## Important" heading, not-important entries under a
// "## Not Important" heading. found is true if either file exists; false
// (with a nil error) only if neither does — that's an expected,
// non-error condition, not a failure.
func Read(root string, date time.Time) (content string, found bool, err error) {
	important, importantFound, err := readOne(PathForDate(root, date, true))
	if err != nil {
		return "", false, err
	}
	notImportant, notImportantFound, err := readOne(PathForDate(root, date, false))
	if err != nil {
		return "", false, err
	}
	return combine(important, notImportant), importantFound || notImportantFound, nil
}

func readOne(path string) (content string, found bool, err error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("reports: read %s: %w", path, err)
	}
	return string(b), true, nil
}

func combine(important, notImportant string) string {
	important = strings.TrimSpace(important)
	notImportant = strings.TrimSpace(notImportant)

	var b strings.Builder
	if important != "" {
		b.WriteString("## Important\n\n")
		b.WriteString(important)
		b.WriteString("\n")
	}
	if notImportant != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("## Not Important\n\n")
		b.WriteString(notImportant)
		b.WriteString("\n")
	}
	return b.String()
}
