package dispatch

import (
	"encoding/json"
	"fmt"
	"time"

	"soulman/action-svc/report"
)

type ReportEntryParams struct {
	Summary    string `json:"summary"`
	RawContent string `json:"raw_content"`
	SourcePath string `json:"source_path"`
	OccurredAt string `json:"occurred_at"`
}

// AppendReportEntry implements the append_daily_report_entry action. It is a
// package-level var (not a plain function) so tests can inject a failing
// stand-in to deterministically exercise Dispatcher's retry-then-give-up
// behaviour without needing to force a real filesystem failure.
var AppendReportEntry = func(root string, params json.RawMessage) (string, error) {
	var p ReportEntryParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("dispatch: unmarshal params: %w", err)
	}
	occurredAt, err := time.Parse(time.RFC3339, p.OccurredAt)
	if err != nil {
		return "", fmt.Errorf("dispatch: parse occurred_at %q: %w", p.OccurredAt, err)
	}
	entry := report.Entry{
		Summary:    p.Summary,
		RawContent: p.RawContent,
		SourcePath: p.SourcePath,
		OccurredAt: occurredAt.Local(),
	}
	path, err := report.Append(root, entry)
	if err != nil {
		return "", fmt.Errorf("dispatch: append report entry: %w", err)
	}
	return path, nil
}
