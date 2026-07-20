package rules

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"soulman/common"
	"soulman/thinking-svc/llm"
)

// SystemMonitorRule implements the System Monitor design's mechanical rule
// (docs/superpowers/specs/2026-07-18-system-monitor-channel-design.md): any
// stimulus with channel == "system-monitor" becomes an
// append_daily_report_entry Action Request, the same shape
// ErrorReportRule/CLINoteRule already produce — no LLM call, since
// perception-svc's sysmonitor package already builds a complete,
// human-readable message in raw_text.
var SystemMonitorRule = Rule{
	Name: "system-monitor",
	Match: func(s *common.Stimulus) bool {
		return s.Channel == "system-monitor"
	},
	Handle: handleSystemMonitor,
}

func handleSystemMonitor(_ context.Context, s *common.Stimulus, _ llm.Client) (*common.ActionRequest, error) {
	params, err := json.Marshal(errorReportParams{
		Summary:    s.Content.RawText,
		RawContent: s.Content.RawText,
		SourcePath: systemMonitorSourcePath(s),
		OccurredAt: s.OccurredAt,
	})
	if err != nil {
		return nil, fmt.Errorf("rules: marshal system monitor parameters: %w", err)
	}

	req := &common.ActionRequest{
		CorrelationID:   uuid.NewString(),
		Intent:          "Log this system monitor alert to today's daily report",
		ActionHint:      "append_daily_report_entry",
		Parameters:      params,
		RiskLevel:       "low",
		Urgency:         "normal",
		ExpectedOutcome: "one entry appended to today's report file",
		Fallback:        "if fs-agent fails, retry once; if it fails again, log to episodic memory with error:execution tag and give up silently — a missed report entry is not worth interrupting the human",
	}
	return req, nil
}

// systemMonitorSourcePath builds "system-monitor/<check_type>",
// "system-monitor/<check_type>/<path>", or "system-monitor/<check_type>/<name>"
// from channel_metadata.channel_specific — path is disk_space's identifier,
// name is service_health's. Parallels error_report.go's watchedPath()
// extraction helper.
func systemMonitorSourcePath(s *common.Stimulus) string {
	var meta struct {
		CheckType string `json:"check_type"`
		Path      string `json:"path"`
		Name      string `json:"name"`
	}
	if len(s.ChannelMeta.ChannelSpecific) > 0 {
		_ = json.Unmarshal(s.ChannelMeta.ChannelSpecific, &meta)
	}
	id := meta.Path
	if id == "" {
		id = meta.Name
	}
	if id == "" {
		return "system-monitor/" + meta.CheckType
	}
	return "system-monitor/" + meta.CheckType + "/" + id
}
