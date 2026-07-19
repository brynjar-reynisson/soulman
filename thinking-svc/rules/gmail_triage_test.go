package rules_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"soulman/common"
	"soulman/thinking-svc/rules"
)

var errClassifyBoom = errors.New("boom")

func newGmailStimulus(sender, subject, body string, occurredAt time.Time) *common.Stimulus {
	channelSpecific, _ := json.Marshal(map[string]any{"subject": subject, "label_ids": []string{"UNREAD"}})
	return &common.Stimulus{
		StimulusID: "stim-gmail-001",
		Channel:    "gmail",
		ReceivedAt: time.Now().UTC(),
		OccurredAt: &occurredAt,
		Source:     common.Source{Identity: sender},
		Content: common.Content{
			RawText:     body,
			ContentType: "text",
			RawPayload:  json.RawMessage(`{}`),
		},
		ChannelMeta: common.ChannelMeta{
			MessageID:       "msg-001",
			ThreadID:        "thread-001",
			ChannelSpecific: channelSpecific,
		},
		Hints:    common.Hints{Priority: "normal", Tags: []string{"email", "gmail"}},
		Override: common.Override{Params: json.RawMessage(`{}`)},
	}
}

func TestGmailTriageRule_Match_GmailChannel(t *testing.T) {
	s := newGmailStimulus("a@b.com", "subject", "body", time.Now())
	if !rules.GmailTriageRule.Match(s) {
		t.Error("expected match for gmail channel")
	}
}

func TestGmailTriageRule_Match_OtherChannel(t *testing.T) {
	s := newGmailStimulus("a@b.com", "subject", "body", time.Now())
	s.Channel = "folder-watcher"
	if rules.GmailTriageRule.Match(s) {
		t.Error("expected no match for non-gmail channel")
	}
}

func TestGmailTriageRule_Handle_Important_BuildsHighUrgencyRequest(t *testing.T) {
	occurred := time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)
	s := newGmailStimulus("boss@company.com", "Urgent: server down", "Production is down, please call me.", occurred)

	client := &fakeSummarizer{classifyImportant: true, classifyReason: "production outage"}
	req, err := rules.GmailTriageRule.Handle(context.Background(), s, client)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if req.ActionHint != "triage_gmail_email" {
		t.Errorf("ActionHint = %q, want triage_gmail_email", req.ActionHint)
	}
	if req.Urgency != "high" {
		t.Errorf("Urgency = %q, want high", req.Urgency)
	}
	if req.Intent != "Notify me about this important email" {
		t.Errorf("Intent = %q, want the important-path intent text", req.Intent)
	}
	if req.RiskLevel != "low" {
		t.Errorf("RiskLevel = %q, want low", req.RiskLevel)
	}
	if req.CorrelationID == "" {
		t.Error("CorrelationID must be generated")
	}

	var params map[string]any
	if err := json.Unmarshal(req.Parameters, &params); err != nil {
		t.Fatalf("decode Parameters: %v", err)
	}
	if params["important"] != true {
		t.Errorf("important = %v, want true", params["important"])
	}
	if params["reason"] != "production outage" {
		t.Errorf("reason = %v, want %q", params["reason"], "production outage")
	}
	if params["sender"] != "boss@company.com" {
		t.Errorf("sender = %v, want boss@company.com", params["sender"])
	}
	if params["subject"] != "Urgent: server down" {
		t.Errorf("subject = %v, want %q", params["subject"], "Urgent: server down")
	}
	if params["thread_id"] != "thread-001" {
		t.Errorf("thread_id = %v, want thread-001", params["thread_id"])
	}
}

func TestGmailTriageRule_Handle_NotImportant_BuildsNormalUrgencyRequest(t *testing.T) {
	s := newGmailStimulus("newsletter@example.com", "Weekly digest", "Here's what happened this week...", time.Now())

	client := &fakeSummarizer{classifyImportant: false, classifyReason: "routine newsletter"}
	req, err := rules.GmailTriageRule.Handle(context.Background(), s, client)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if req.Urgency != "normal" {
		t.Errorf("Urgency = %q, want normal", req.Urgency)
	}
	if req.Intent != "Log this email to today's daily report" {
		t.Errorf("Intent = %q, want the not-important-path intent text", req.Intent)
	}

	var params map[string]any
	if err := json.Unmarshal(req.Parameters, &params); err != nil {
		t.Fatalf("decode Parameters: %v", err)
	}
	if params["important"] != false {
		t.Errorf("important = %v, want false", params["important"])
	}
}

func TestGmailTriageRule_Handle_ClassifierError_FailsClosed(t *testing.T) {
	s := newGmailStimulus("a@b.com", "subject", "body", time.Now())

	client := &fakeSummarizer{classifyErr: errClassifyBoom}
	req, err := rules.GmailTriageRule.Handle(context.Background(), s, client)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var params map[string]any
	if err := json.Unmarshal(req.Parameters, &params); err != nil {
		t.Fatalf("decode Parameters: %v", err)
	}
	if params["important"] != false {
		t.Errorf("important = %v, want false when classifier errors", params["important"])
	}
	reason, _ := params["reason"].(string)
	if reason == "" {
		t.Error("expected a non-empty reason describing the classifier failure")
	}
}

func TestGmailTriageRule_Handle_BodyExcerptTruncatedTo200Chars(t *testing.T) {
	longBody := make([]rune, 500)
	for i := range longBody {
		longBody[i] = 'x'
	}
	s := newGmailStimulus("a@b.com", "subject", string(longBody), time.Now())

	client := &fakeSummarizer{classifyImportant: false, classifyReason: "not important"}
	req, err := rules.GmailTriageRule.Handle(context.Background(), s, client)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var params map[string]any
	if err := json.Unmarshal(req.Parameters, &params); err != nil {
		t.Fatalf("decode Parameters: %v", err)
	}
	excerpt, _ := params["body_excerpt"].(string)
	if len([]rune(excerpt)) != 201 { // 200 chars + the truncation ellipsis
		t.Errorf("excerpt rune length = %d, want 201 (200 + ellipsis)", len([]rune(excerpt)))
	}
}
