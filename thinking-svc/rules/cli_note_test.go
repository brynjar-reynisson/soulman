package rules_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"soulman/common"
	"soulman/thinking-svc/rules"
)

func newCLINoteStimulus(text string, occurredAt time.Time) *common.Stimulus {
	return &common.Stimulus{
		StimulusID: "stim-cli-001",
		Channel:    "cli-note",
		ReceivedAt: time.Now().UTC(),
		OccurredAt: &occurredAt,
		Content: common.Content{
			RawText:     text,
			ContentType: "text",
			RawPayload:  json.RawMessage(`{}`),
		},
		Hints:    common.Hints{Priority: "normal"},
		Override: common.Override{Params: json.RawMessage(`{}`)},
	}
}

func TestCLINoteRule_Match_CLINoteChannel(t *testing.T) {
	s := newCLINoteStimulus("disk cleanup done", time.Now())
	if !rules.CLINoteRule.Match(s) {
		t.Error("expected match for cli-note channel")
	}
}

func TestCLINoteRule_Match_PlainCLIChannel_NoMatch(t *testing.T) {
	s := newCLINoteStimulus("disk cleanup done", time.Now())
	s.Channel = "cli"
	if rules.CLINoteRule.Match(s) {
		t.Error("expected no match for plain cli channel")
	}
}

func TestCLINoteRule_Handle_BuildsActionRequest(t *testing.T) {
	occurred := time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)
	s := newCLINoteStimulus("disk cleanup done", occurred)

	req, err := rules.CLINoteRule.Handle(context.Background(), s, &fakeSummarizer{})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if req.ActionHint != "append_daily_report_entry" {
		t.Errorf("ActionHint = %q, want append_daily_report_entry", req.ActionHint)
	}
	if req.Intent != "Log this note to today's daily report" {
		t.Errorf("Intent = %q, want the spec's intent text", req.Intent)
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
	if params.Summary != "disk cleanup done" {
		t.Errorf("Summary = %q, want verbatim text", params.Summary)
	}
	if params.RawContent != "disk cleanup done" {
		t.Errorf("RawContent = %q, want verbatim text", params.RawContent)
	}
	if params.SourcePath != "cli/note" {
		t.Errorf(`SourcePath = %q, want "cli/note"`, params.SourcePath)
	}
}

func TestMatch_FindsCLINoteRule(t *testing.T) {
	s := newCLINoteStimulus("disk cleanup done", time.Now())
	r := rules.Match(s)
	if r == nil {
		t.Fatal("Match = nil, want CLINoteRule for cli-note stimulus")
	}
	if r.Name != "cli-note" {
		t.Errorf("Name = %q, want cli-note", r.Name)
	}
}

func TestProcess_PlainCLIChannel_NoMatchYet(t *testing.T) {
	s := newCLINoteStimulus("remind me to check logs", time.Now())
	s.Channel = "cli"

	req, err := rules.Process(context.Background(), s, &fakeSummarizer{})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if req != nil {
		t.Errorf("Process = %v, want nil for plain cli channel (no reasoning rule yet)", req)
	}
}
