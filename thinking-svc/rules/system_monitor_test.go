package rules_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"soulman/common"
	"soulman/thinking-svc/rules"
)

func newSystemMonitorStimulus(rawText, checkType, path string, occurredAt time.Time) *common.Stimulus {
	specific, _ := json.Marshal(struct {
		CheckType string `json:"check_type"`
		Path      string `json:"path,omitempty"`
	}{CheckType: checkType, Path: path})

	return &common.Stimulus{
		StimulusID: "stim-sysmon-001",
		Channel:    "system-monitor",
		ReceivedAt: time.Now().UTC(),
		OccurredAt: &occurredAt,
		Content: common.Content{
			RawText:     rawText,
			ContentType: "text",
			RawPayload:  json.RawMessage(`{}`),
		},
		ChannelMeta: common.ChannelMeta{
			ChannelSpecific: specific,
		},
		Hints:    common.Hints{Priority: "critical", Tags: []string{"system", "system-monitor", checkType}},
		Override: common.Override{Params: json.RawMessage(`{}`)},
	}
}

func TestSystemMonitorRule_Match_SystemMonitorChannel(t *testing.T) {
	s := newSystemMonitorStimulus(`Disk space C:\ critical: 97% used (threshold 95%)`, "disk_space", `C:\`, time.Now())
	if !rules.SystemMonitorRule.Match(s) {
		t.Error("expected match for system-monitor channel")
	}
}

func TestSystemMonitorRule_Match_OtherChannel_NoMatch(t *testing.T) {
	s := newSystemMonitorStimulus("x", "disk_space", `C:\`, time.Now())
	s.Channel = "folder-watcher"
	if rules.SystemMonitorRule.Match(s) {
		t.Error("expected no match for folder-watcher channel")
	}
}

func TestSystemMonitorRule_Handle_BuildsActionRequest_WithPath(t *testing.T) {
	occurred := time.Date(2026, 7, 18, 15, 42, 0, 0, time.UTC)
	rawText := `Disk space C:\ critical: 97% used (threshold 95%)`
	s := newSystemMonitorStimulus(rawText, "disk_space", `C:\`, occurred)

	req, err := rules.SystemMonitorRule.Handle(context.Background(), s, &fakeSummarizer{})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if req.ActionHint != "append_daily_report_entry" {
		t.Errorf("ActionHint = %q, want append_daily_report_entry", req.ActionHint)
	}
	if req.RiskLevel != "low" {
		t.Errorf("RiskLevel = %q, want low", req.RiskLevel)
	}
	if req.CorrelationID == "" {
		t.Error("CorrelationID must be generated")
	}

	var params struct {
		Summary    string     `json:"summary"`
		RawContent string     `json:"raw_content"`
		SourcePath string     `json:"source_path"`
		OccurredAt *time.Time `json:"occurred_at"`
	}
	if err := json.Unmarshal(req.Parameters, &params); err != nil {
		t.Fatalf("decode Parameters: %v", err)
	}
	if params.Summary != rawText {
		t.Errorf("Summary = %q, want %q", params.Summary, rawText)
	}
	if params.RawContent != rawText {
		t.Errorf("RawContent = %q, want %q", params.RawContent, rawText)
	}
	if params.SourcePath != `system-monitor/disk_space/C:\` {
		t.Errorf(`SourcePath = %q, want "system-monitor/disk_space/C:\"`, params.SourcePath)
	}
}

func TestSystemMonitorRule_Handle_BuildsActionRequest_NoPath(t *testing.T) {
	occurred := time.Date(2026, 7, 18, 15, 42, 0, 0, time.UTC)
	s := newSystemMonitorStimulus("Memory usage warning: 87% used (threshold 85%)", "memory", "", occurred)

	req, err := rules.SystemMonitorRule.Handle(context.Background(), s, &fakeSummarizer{})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var params struct {
		SourcePath string `json:"source_path"`
	}
	if err := json.Unmarshal(req.Parameters, &params); err != nil {
		t.Fatalf("decode Parameters: %v", err)
	}
	if params.SourcePath != "system-monitor/memory" {
		t.Errorf(`SourcePath = %q, want "system-monitor/memory"`, params.SourcePath)
	}
}

func TestMatch_FindsSystemMonitorRule(t *testing.T) {
	s := newSystemMonitorStimulus("x", "cpu", "", time.Now())
	r := rules.Match(s)
	if r == nil {
		t.Fatal("Match = nil, want SystemMonitorRule for system-monitor stimulus")
	}
	if r.Name != "system-monitor" {
		t.Errorf("Name = %q, want system-monitor", r.Name)
	}
}
