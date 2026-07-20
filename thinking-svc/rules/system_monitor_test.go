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

func newServiceHealthStimulus(rawText, name string, occurredAt time.Time) *common.Stimulus {
	specific, _ := json.Marshal(struct {
		CheckType string `json:"check_type"`
		Name      string `json:"name"`
	}{CheckType: "service_health", Name: name})

	return &common.Stimulus{
		StimulusID: "stim-sysmon-002",
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
		Hints:    common.Hints{Priority: "critical", Tags: []string{"system", "system-monitor", "service_health"}},
		Override: common.Override{Params: json.RawMessage(`{}`)},
	}
}

func TestSystemMonitorRule_Handle_BuildsActionRequest_ServiceHealthName(t *testing.T) {
	occurred := time.Date(2026, 7, 19, 10, 5, 0, 0, time.UTC)
	rawText := "Service down: agent-suite-backend unreachable (connection refused)"
	s := newServiceHealthStimulus(rawText, "agent-suite-backend", occurred)

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
	if params.SourcePath != "system-monitor/service_health/agent-suite-backend" {
		t.Errorf("SourcePath = %q, want %q", params.SourcePath, "system-monitor/service_health/agent-suite-backend")
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

func TestSystemMonitorRule_Handle_Important(t *testing.T) {
	cases := []struct {
		name     string
		severity string
		want     bool
	}{
		{"critical is important", "critical", true},
		{"ok is important (edge-triggered publish means ok is always a recovery)", "ok", true},
		{"warning is not important", "warning", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			specific, _ := json.Marshal(struct {
				CheckType string `json:"check_type"`
				Path      string `json:"path,omitempty"`
				Severity  string `json:"severity"`
			}{CheckType: "disk_space", Path: `C:\`, Severity: tc.severity})

			occurred := time.Now()
			s := &common.Stimulus{
				StimulusID: "stim-sysmon-importance",
				Channel:    "system-monitor",
				ReceivedAt: time.Now().UTC(),
				OccurredAt: &occurred,
				Content: common.Content{
					RawText:     "message",
					ContentType: "text",
					RawPayload:  json.RawMessage(`{}`),
				},
				ChannelMeta: common.ChannelMeta{ChannelSpecific: specific},
				Hints:       common.Hints{Priority: "normal", Tags: []string{"system", "system-monitor", "disk_space"}},
				Override:    common.Override{Params: json.RawMessage(`{}`)},
			}

			req, err := rules.SystemMonitorRule.Handle(context.Background(), s, &fakeSummarizer{})
			if err != nil {
				t.Fatalf("Handle: %v", err)
			}

			var params struct {
				Important bool `json:"important"`
			}
			if err := json.Unmarshal(req.Parameters, &params); err != nil {
				t.Fatalf("decode Parameters: %v", err)
			}
			if params.Important != tc.want {
				t.Errorf("severity=%q: important = %v, want %v", tc.severity, params.Important, tc.want)
			}
		})
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
