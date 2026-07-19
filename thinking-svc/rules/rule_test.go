package rules_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"soulman/common"
	"soulman/thinking-svc/rules"
)

type fakeSummarizer struct {
	summary string
	err     error

	classifyImportant bool
	classifyReason    string
	classifyErr       error
}

func (f *fakeSummarizer) Summarize(_ context.Context, _ string) (string, error) {
	return f.summary, f.err
}

func (f *fakeSummarizer) ClassifyImportance(_ context.Context, _, _, _ string) (bool, string, error) {
	return f.classifyImportant, f.classifyReason, f.classifyErr
}

func newFolderWatcherStimulus(rawText string, occurredAt time.Time) *common.Stimulus {
	return &common.Stimulus{
		StimulusID: "stim-001",
		Channel:    "folder-watcher",
		ReceivedAt: time.Now().UTC(),
		OccurredAt: &occurredAt,
		Content: common.Content{
			RawText:     rawText,
			ContentType: "text",
			RawPayload:  json.RawMessage(`{}`),
		},
		ChannelMeta: common.ChannelMeta{
			ChannelSpecific: json.RawMessage(`{"watched_path":"C:\\Users\\Lenovo\\DigitalMe\\errors"}`),
		},
		Hints:    common.Hints{Priority: "high", Tags: []string{"error", "folder-watcher"}},
		Override: common.Override{Params: json.RawMessage(`{}`)},
	}
}

func TestMatch_NoRuleForUnknownChannel(t *testing.T) {
	s := newFolderWatcherStimulus("boom", time.Now())
	s.Channel = "webhook"

	if r := rules.Match(s); r != nil {
		t.Errorf("Match = %v, want nil for unmatched channel", r)
	}
}

func TestMatch_FindsErrorReportRule(t *testing.T) {
	s := newFolderWatcherStimulus("boom", time.Now())

	r := rules.Match(s)
	if r == nil {
		t.Fatal("Match = nil, want ErrorReportRule for folder-watcher stimulus")
	}
	if r.Name != "error-report" {
		t.Errorf("Name = %q, want error-report", r.Name)
	}
}

func TestProcess_NoMatch_ReturnsNilNil(t *testing.T) {
	s := newFolderWatcherStimulus("boom", time.Now())
	s.Channel = "webhook"

	req, err := rules.Process(context.Background(), s, &fakeSummarizer{})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if req != nil {
		t.Errorf("Process = %v, want nil for unmatched stimulus", req)
	}
}

func TestProcess_Match_ReturnsActionRequest(t *testing.T) {
	s := newFolderWatcherStimulus("boom", time.Now())

	req, err := rules.Process(context.Background(), s, &fakeSummarizer{summary: "boom summary"})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if req == nil {
		t.Fatal("Process = nil, want ActionRequest for folder-watcher stimulus")
	}
	if req.ActionHint != "append_daily_report_entry" {
		t.Errorf("ActionHint = %q, want append_daily_report_entry", req.ActionHint)
	}
}

func TestMatch_FindsGmailTriageRule(t *testing.T) {
	s := newGmailStimulus("a@b.com", "subject", "body", time.Now())
	r := rules.Match(s)
	if r == nil {
		t.Fatal("Match = nil, want GmailTriageRule for gmail stimulus")
	}
	if r.Name != "gmail-triage" {
		t.Errorf("Name = %q, want gmail-triage", r.Name)
	}
}
