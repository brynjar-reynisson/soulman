package rules_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"soulman/common"
	"soulman/thinking-svc/llm"
	"soulman/thinking-svc/rules"
)

func newSchoolStimulus(sender, subject, body string, occurredAt time.Time) *common.Stimulus {
	channelSpecific, _ := json.Marshal(map[string]string{"subject": subject})
	return &common.Stimulus{
		StimulusID: "school-1",
		Channel:    "gmail",
		ReceivedAt: time.Now().UTC(),
		OccurredAt: &occurredAt,
		Source:     common.Source{Identity: sender},
		Content:    common.Content{RawText: body, ContentType: "text"},
		ChannelMeta: common.ChannelMeta{
			ThreadID:        "thread-9",
			ChannelSpecific: channelSpecific,
		},
	}
}

func TestSchoolEmailRule_Match_ConfiguredDomain(t *testing.T) {
	rule := rules.NewSchoolEmailRule([]string{"@reykjavik.is"}, nil)
	s := newSchoolStimulus("teacher@reykjavik.is", "Reminder", "sweater day", time.Now())
	if !rule.Match(s) {
		t.Error("Match = false, want true for a configured domain")
	}
}

func TestSchoolEmailRule_Match_OtherDomain_NoMatch(t *testing.T) {
	rule := rules.NewSchoolEmailRule([]string{"@reykjavik.is"}, nil)
	s := newSchoolStimulus("someone@example.com", "Reminder", "sweater day", time.Now())
	if rule.Match(s) {
		t.Error("Match = true, want false for an unconfigured domain")
	}
}

func TestSchoolEmailRule_Match_NonGmailChannel_NoMatch(t *testing.T) {
	rule := rules.NewSchoolEmailRule([]string{"@reykjavik.is"}, nil)
	s := newSchoolStimulus("teacher@reykjavik.is", "Reminder", "sweater day", time.Now())
	s.Channel = "cli-note"
	if rule.Match(s) {
		t.Error("Match = true, want false for a non-gmail channel")
	}
}

func TestSchoolEmailRule_Match_CaseInsensitive(t *testing.T) {
	rule := rules.NewSchoolEmailRule([]string{"@reykjavik.is"}, nil)
	s := newSchoolStimulus("Teacher@Reykjavik.IS", "Reminder", "sweater day", time.Now())
	if !rule.Match(s) {
		t.Error("Match = false, want true for a case-different domain match")
	}
}

func TestSchoolEmailRule_Handle_EventsFound_HighUrgency(t *testing.T) {
	rule := rules.NewSchoolEmailRule([]string{"@reykjavik.is"}, nil)
	occurredAt := time.Date(2026, 9, 3, 8, 0, 0, 0, time.UTC)
	s := newSchoolStimulus("teacher@reykjavik.is", "Reminder", "Tomorrow is sweater day!", occurredAt)

	client := &fakeSummarizer{extractEvents: []llm.SchoolEvent{{Date: "2026-09-04", HasTime: false, Description: "Sweater day"}}}
	req, err := rule.Handle(context.Background(), s, client)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if req.ActionHint != "process_school_event" {
		t.Errorf("ActionHint = %q, want process_school_event", req.ActionHint)
	}
	if req.Urgency != "high" {
		t.Errorf("Urgency = %q, want high when events were found", req.Urgency)
	}

	var params struct {
		Sender      string `json:"sender"`
		Subject     string `json:"subject"`
		BodyExcerpt string `json:"body_excerpt"`
		ThreadID    string `json:"thread_id"`
		Events      []struct {
			Date        string `json:"date"`
			HasTime     bool   `json:"has_time"`
			Description string `json:"description"`
		} `json:"events"`
	}
	if err := json.Unmarshal(req.Parameters, &params); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if params.Sender != "teacher@reykjavik.is" || params.Subject != "Reminder" || params.ThreadID != "thread-9" {
		t.Errorf("params = %+v, want sender/subject/thread_id to match", params)
	}
	if len(params.Events) != 1 || params.Events[0].Date != "2026-09-04" || params.Events[0].Description != "Sweater day" {
		t.Errorf("params.Events = %+v, want 1 event 2026-09-04 Sweater day", params.Events)
	}
}

func TestSchoolEmailRule_Handle_ThreadsRelevantGradesToExtractor(t *testing.T) {
	rule := rules.NewSchoolEmailRule([]string{"@reykjavik.is"}, []string{"5", "8"})
	s := newSchoolStimulus("teacher@reykjavik.is", "Reminder", "sweater day", time.Now())

	client := &fakeSummarizer{}
	if _, err := rule.Handle(context.Background(), s, client); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(client.capturedGrades) != 2 || client.capturedGrades[0] != "5" || client.capturedGrades[1] != "8" {
		t.Errorf("capturedGrades = %v, want [5 8]", client.capturedGrades)
	}
}

func TestSchoolEmailRule_Handle_NoEvents_NormalUrgency(t *testing.T) {
	rule := rules.NewSchoolEmailRule([]string{"@reykjavik.is"}, nil)
	s := newSchoolStimulus("info@reykjavik.is", "Notice", "unrelated municipal notice", time.Now())

	client := &fakeSummarizer{extractEvents: nil}
	req, err := rule.Handle(context.Background(), s, client)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if req.Urgency != "normal" {
		t.Errorf("Urgency = %q, want normal when no events were found", req.Urgency)
	}

	var params struct {
		Events []struct{} `json:"events"`
	}
	json.Unmarshal(req.Parameters, &params)
	if len(params.Events) != 0 {
		t.Errorf("params.Events = %v, want empty", params.Events)
	}
}

func TestSchoolEmailRule_Handle_ExtractorError_NoteRecordedNoEvents(t *testing.T) {
	rule := rules.NewSchoolEmailRule([]string{"@reykjavik.is"}, nil)
	s := newSchoolStimulus("teacher@reykjavik.is", "Reminder", "sweater day", time.Now())

	client := &fakeSummarizer{extractNote: "extraction unavailable: deepseek status 500"}
	req, err := rule.Handle(context.Background(), s, client)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var params struct {
		Note   string     `json:"note"`
		Events []struct{} `json:"events"`
	}
	json.Unmarshal(req.Parameters, &params)
	if params.Note != "extraction unavailable: deepseek status 500" {
		t.Errorf("params.Note = %q, want the extractor's note", params.Note)
	}
	if len(params.Events) != 0 {
		t.Errorf("params.Events = %v, want empty on extractor failure", params.Events)
	}
}

func TestMatch_FindsSchoolEmailRule_WhenPrepended(t *testing.T) {
	orig := rules.Registry
	defer func() { rules.Registry = orig }()
	rules.Registry = append([]rules.Rule{rules.NewSchoolEmailRule([]string{"@reykjavik.is"}, nil)}, rules.Registry...)

	s := newSchoolStimulus("teacher@reykjavik.is", "Reminder", "sweater day", time.Now())
	r := rules.Match(s)
	if r == nil || r.Name != "school-email" {
		t.Errorf("Match found %v, want the school-email rule", r)
	}
}
