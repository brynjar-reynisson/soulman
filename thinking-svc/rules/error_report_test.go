package rules_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"soulman/common"
	"soulman/thinking-svc/rules"
)

// decodeParams unmarshals an ActionRequest's raw Parameters into a map for
// assertions, mirroring how action-svc would unmarshal it into its own
// typed params struct.
func decodeParams(t *testing.T, req *common.ActionRequest) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(req.Parameters, &m); err != nil {
		t.Fatalf("decode Parameters: %v", err)
	}
	return m
}

func TestErrorReportRule_Match_FolderWatcher(t *testing.T) {
	s := newFolderWatcherStimulus("boom", time.Now())
	if !rules.ErrorReportRule.Match(s) {
		t.Error("expected match for folder-watcher channel")
	}
}

func TestErrorReportRule_Match_OtherChannel(t *testing.T) {
	s := newFolderWatcherStimulus("boom", time.Now())
	s.Channel = "webhook"
	if rules.ErrorReportRule.Match(s) {
		t.Error("expected no match for non folder-watcher channel")
	}
}

func TestErrorReportRule_Handle_BuildsActionRequest(t *testing.T) {
	occurred := time.Date(2026, 7, 17, 14, 32, 0, 0, time.UTC)
	s := newFolderWatcherStimulus("connection timeout to remote host", occurred)

	req, err := rules.ErrorReportRule.Handle(context.Background(), s, &fakeSummarizer{})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if req.ActionHint != "append_daily_report_entry" {
		t.Errorf("ActionHint = %q, want append_daily_report_entry", req.ActionHint)
	}
	if req.Intent != "Log this error to today's daily report" {
		t.Errorf("Intent = %q, want the spec's intent text", req.Intent)
	}
	if req.RiskLevel != "low" {
		t.Errorf("RiskLevel = %q, want low", req.RiskLevel)
	}
	if req.Urgency != "normal" {
		t.Errorf("Urgency = %q, want normal", req.Urgency)
	}
	if req.ExpectedOutcome != "one entry appended to today's report file" {
		t.Errorf("ExpectedOutcome = %q, want the spec's text", req.ExpectedOutcome)
	}
	if req.CorrelationID == "" {
		t.Error("CorrelationID must be generated")
	}
	params := decodeParams(t, req)
	if params["summary"] != "unknown-file" {
		t.Errorf("summary = %v, want the filename (no summarizer call)", params["summary"])
	}
	if params["raw_content"] != "connection timeout to remote host" {
		t.Errorf("raw_content = %v, want verbatim raw text", params["raw_content"])
	}
	wantPath := "C:\\Users\\Lenovo\\DigitalMe\\errors/unknown-file"
	if params["source_path"] != wantPath {
		t.Errorf("source_path = %v, want %v", params["source_path"], wantPath)
	}
	if params["important"] != true {
		t.Errorf("important = %v, want true", params["important"])
	}
}

func TestErrorReportRule_Handle_EmptyRawText_BinaryFallback(t *testing.T) {
	s := newFolderWatcherStimulus("", time.Now())
	s.Content.Attachments = []common.Attachment{{Filename: "screenshot.png", MIMEType: "image/png"}}

	summarizer := &fakeSummarizer{summary: "should not be called"}

	req, err := rules.ErrorReportRule.Handle(context.Background(), s, summarizer)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	params := decodeParams(t, req)
	summary, _ := params["summary"].(string)
	if summary != "screenshot.png (binary, see attachment)" {
		t.Errorf("summary = %q, want binary fallback with attachment filename", summary)
	}
	if params["raw_content"] != "" {
		t.Errorf("raw_content = %v, want empty for binary case", params["raw_content"])
	}
}
