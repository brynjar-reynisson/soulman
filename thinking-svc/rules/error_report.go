package rules

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"soulman/common"
	"soulman/thinking-svc/llm"
)

// errorReportParams mirrors error-report-action-design.md's Thinking Rule
// parameters shape. Marshaled into common.ActionRequest.Parameters as raw
// JSON so action-svc can unmarshal it directly into its own params struct
// without an intermediate map[string]any round trip.
type errorReportParams struct {
	Summary    string     `json:"summary"`
	RawContent string     `json:"raw_content"`
	SourcePath string     `json:"source_path"`
	OccurredAt *time.Time `json:"occurred_at"`
	Important  bool       `json:"important"`
}

// ErrorReportRule implements the single v1 rule from
// docs/superpowers/specs/2026-07-17-error-report-action-design.md: any
// stimulus from the folder-watcher channel becomes an
// append_daily_report_entry Action Request.
var ErrorReportRule = Rule{
	Name: "error-report",
	Match: func(s *common.Stimulus) bool {
		return s.Channel == "folder-watcher"
	},
	Handle: handleErrorReport,
}

// handleErrorReport builds the report entry mechanically — no LLM call.
// Error files speak for themselves (raw_content carries the full text into
// the report), so spending a DeepSeek call here would just burn credits for
// no signal. The client parameter stays unused here, threaded through
// only because Rule.Handle's signature is shared with other rules
// (e.g. GmailTriageRule) that need Classify/Summarize capabilities this rule doesn't.
func handleErrorReport(_ context.Context, s *common.Stimulus, _ llm.Client) (*common.ActionRequest, error) {
	filename := attachmentFilename(s)
	watched := watchedPath(s)

	summary := filename
	if s.Content.RawText == "" {
		// Binary-attachment case (perception-svc's design): no raw text to
		// log alongside the header.
		summary = fmt.Sprintf("%s (binary, see attachment)", filename)
	}

	sourcePath := watched + "/" + filename

	params, err := json.Marshal(errorReportParams{
		Summary:    summary,
		RawContent: s.Content.RawText,
		SourcePath: sourcePath,
		OccurredAt: occurredAtValue(s),
		Important:  true,
	})
	if err != nil {
		return nil, fmt.Errorf("rules: marshal error report parameters: %w", err)
	}

	req := &common.ActionRequest{
		CorrelationID:   uuid.NewString(),
		Intent:          "Log this error to today's daily report",
		ActionHint:      "append_daily_report_entry",
		Parameters:      params,
		RiskLevel:       "low",
		Urgency:         "normal",
		ExpectedOutcome: "one entry appended to today's report file",
		Fallback:        "if fs-agent fails, retry once; if it fails again, log to episodic memory with error:execution tag and give up silently — a missed report entry is not worth interrupting the human",
	}
	return req, nil
}

// attachmentFilename extracts a filename for the source_path parameter.
// perception-svc's design only populates content.attachments for binary
// files; inlined text content has no filename anywhere in the Stimulus
// schema. See this plan's Task 4 note for the assumption behind the
// "unknown-file" fallback.
func attachmentFilename(s *common.Stimulus) string {
	if len(s.Content.Attachments) > 0 && s.Content.Attachments[0].Filename != "" {
		return s.Content.Attachments[0].Filename
	}
	return "unknown-file"
}

// watchedPath extracts channel_metadata.channel_specific.watched_path, the
// only key perception-svc-design.md guarantees for folder-watcher stimuli.
func watchedPath(s *common.Stimulus) string {
	var meta struct {
		WatchedPath string `json:"watched_path"`
	}
	if len(s.ChannelMeta.ChannelSpecific) > 0 {
		_ = json.Unmarshal(s.ChannelMeta.ChannelSpecific, &meta)
	}
	return meta.WatchedPath
}

// occurredAtValue passes stimulus.occurred_at through verbatim; nil marshals
// to JSON null (folder-watcher stimuli always set it per perception-svc's
// design, so this should not occur in practice).
func occurredAtValue(s *common.Stimulus) *time.Time {
	return s.OccurredAt
}
