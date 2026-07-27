package rules

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"soulman/common"
	"soulman/thinking-svc/llm"
)

// LogErrorRule implements the single rule from
// docs/superpowers/specs/2026-07-27-log-error-perception-design.md: any
// stimulus from the log-error channel becomes an append_daily_report_entry
// Action Request, always important — no LLM call, since logmonitor already
// built a complete raw_text and Error-level itself is the importance
// signal (unlike system-monitor, there is no non-important tier here at
// all: only Error-level lines ever reach this channel).
var LogErrorRule = Rule{
	Name: "log-error",
	Match: func(s *common.Stimulus) bool {
		return s.Channel == "log-error"
	},
	Handle: handleLogError,
}

func handleLogError(_ context.Context, s *common.Stimulus, _ llm.Client) (*common.ActionRequest, error) {
	service := logErrorService(s)
	params, err := json.Marshal(errorReportParams{
		// Summary is prefixed with the service name because SourcePath's
		// "log-error" directory (report.formatEntry renders
		// filepath.Dir(SourcePath), which collapses "log-error/<service>"
		// down to just "log-error") drops it, and the raw slog line itself
		// is prefixed with the *package* (e.g. "writer:", "checkpoint:"),
		// not the *service* — without this, the daily report and the
		// real-time Discord message would have no way to tell which of the
		// five services actually failed. RawContent is deliberately left
		// unprefixed so it still reads as the verbatim log line.
		Summary:    fmt.Sprintf("%s: %s", service, s.Content.RawText),
		RawContent: s.Content.RawText,
		SourcePath: "log-error/" + service,
		OccurredAt: s.OccurredAt,
		Important:  true,
	})
	if err != nil {
		return nil, fmt.Errorf("rules: marshal log error parameters: %w", err)
	}

	req := &common.ActionRequest{
		CorrelationID:   uuid.NewString(),
		Intent:          "Log this error-level log line to today's daily report",
		ActionHint:      "append_daily_report_entry",
		Parameters:      params,
		RiskLevel:       "low",
		Urgency:         "normal",
		ExpectedOutcome: "one entry appended to today's report file",
		Fallback:        "if fs-agent fails, retry once; if it fails again, log to episodic memory with error:execution tag and give up silently — a missed report entry is not worth interrupting the human",
	}
	return req, nil
}

// logErrorService extracts channel_metadata.channel_specific.service —
// parallels systemMonitorSourcePath/watchedPath's same channel_specific
// extraction pattern. Shared by handleLogError (for the Summary prefix) and
// the SourcePath it builds, so the two never disagree on which service a
// stimulus came from.
func logErrorService(s *common.Stimulus) string {
	var meta struct {
		Service string `json:"service"`
	}
	if len(s.ChannelMeta.ChannelSpecific) > 0 {
		_ = json.Unmarshal(s.ChannelMeta.ChannelSpecific, &meta)
	}
	return meta.Service
}
