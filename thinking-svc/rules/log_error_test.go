package rules_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"soulman/common"
	"soulman/thinking-svc/rules"
)

func newLogErrorStimulus(rawText, service string, occurredAt time.Time) *common.Stimulus {
	specific, _ := json.Marshal(struct {
		Service string `json:"service"`
		Msg     string `json:"msg"`
	}{Service: service, Msg: rawText})

	return &common.Stimulus{
		StimulusID: "stim-logerror-001",
		Channel:    "log-error",
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
		Hints:    common.Hints{Priority: "critical", Tags: []string{"system", "log-error", service}},
		Override: common.Override{Params: json.RawMessage(`{}`)},
	}
}

func TestLogErrorRule_Match_LogErrorChannel(t *testing.T) {
	s := newLogErrorStimulus("2026/07/27 10:00:00 ERROR writer: DB insert failed", "memory-svc", time.Now())
	if !rules.LogErrorRule.Match(s) {
		t.Error("expected match for log-error channel")
	}
}

func TestLogErrorRule_Match_OtherChannel_NoMatch(t *testing.T) {
	s := newLogErrorStimulus("x", "memory-svc", time.Now())
	s.Channel = "system-monitor"
	if rules.LogErrorRule.Match(s) {
		t.Error("expected no match for system-monitor channel")
	}
}

func TestLogErrorRule_Handle_BuildsActionRequest(t *testing.T) {
	occurred := time.Date(2026, 7, 27, 10, 5, 0, 0, time.UTC)
	rawText := `2026/07/27 10:05:00 ERROR writer: DB insert failed stimulus_id=abc error="dial tcp: connect: connection refused"`
	s := newLogErrorStimulus(rawText, "memory-svc", occurred)

	req, err := rules.LogErrorRule.Handle(context.Background(), s, &fakeSummarizer{})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if req.ActionHint != "append_daily_report_entry" {
		t.Errorf("ActionHint = %q, want append_daily_report_entry", req.ActionHint)
	}
	if req.CorrelationID == "" {
		t.Error("CorrelationID must be generated")
	}

	var params struct {
		Summary    string     `json:"summary"`
		RawContent string     `json:"raw_content"`
		SourcePath string     `json:"source_path"`
		OccurredAt *time.Time `json:"occurred_at"`
		Important  bool       `json:"important"`
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
	if params.SourcePath != "log-error/memory-svc" {
		t.Errorf("SourcePath = %q, want log-error/memory-svc", params.SourcePath)
	}
	if !params.Important {
		t.Error("Important must always be true for log-error")
	}
}

func TestLogErrorRule_Handle_AlwaysImportant(t *testing.T) {
	cases := []string{"memory-svc", "perception-svc", "thinking-svc", "action-svc", "web-svc"}
	for _, svc := range cases {
		t.Run(svc, func(t *testing.T) {
			occurred := time.Now()
			s := newLogErrorStimulus("some error line", svc, occurred)

			req, err := rules.LogErrorRule.Handle(context.Background(), s, &fakeSummarizer{})
			if err != nil {
				t.Fatalf("Handle: %v", err)
			}
			var params struct {
				Important bool `json:"important"`
			}
			if err := json.Unmarshal(req.Parameters, &params); err != nil {
				t.Fatalf("decode Parameters: %v", err)
			}
			if !params.Important {
				t.Errorf("service=%q: important = %v, want true", svc, params.Important)
			}
		})
	}
}

func TestMatch_FindsLogErrorRule(t *testing.T) {
	s := newLogErrorStimulus("x", "memory-svc", time.Now())
	r := rules.Match(s)
	if r == nil {
		t.Fatal("Match = nil, want LogErrorRule for log-error stimulus")
	}
	if r.Name != "log-error" {
		t.Errorf("Name = %q, want log-error", r.Name)
	}
}
