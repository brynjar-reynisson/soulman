package rules

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"soulman/common"
	"soulman/thinking-svc/llm"
)

// CLINoteRule implements docs/superpowers/specs/2026-07-18-soulman-cli-design.md's
// mechanical rule: any stimulus from the cli-note channel becomes an
// append_daily_report_entry Action Request, the same shape ErrorReportRule
// produces for folder-watcher — but built directly from the CLI-typed text,
// with no filename/watched-path extraction since there is no source file.
var CLINoteRule = Rule{
	Name: "cli-note",
	Match: func(s *common.Stimulus) bool {
		return s.Channel == "cli-note"
	},
	Handle: handleCLINote,
}

// handleCLINote builds the report entry mechanically — no LLM call. A short
// human-typed note doesn't need summarization, same reasoning as
// handleErrorReport for folder-watcher stimuli.
func handleCLINote(_ context.Context, s *common.Stimulus, _ llm.Client) (*common.ActionRequest, error) {
	params, err := json.Marshal(errorReportParams{
		Summary:    s.Content.RawText,
		RawContent: s.Content.RawText,
		SourcePath: "cli/note",
		OccurredAt: s.OccurredAt,
	})
	if err != nil {
		return nil, fmt.Errorf("rules: marshal cli note parameters: %w", err)
	}

	req := &common.ActionRequest{
		CorrelationID:   uuid.NewString(),
		Intent:          "Log this note to today's daily report",
		ActionHint:      "append_daily_report_entry",
		Parameters:      params,
		RiskLevel:       "low",
		Urgency:         "normal",
		ExpectedOutcome: "one entry appended to today's report file",
		Fallback:        "if fs-agent fails, retry once; if it fails again, log to episodic memory with error:execution tag and give up silently — a missed report entry is not worth interrupting the human",
	}
	return req, nil
}
