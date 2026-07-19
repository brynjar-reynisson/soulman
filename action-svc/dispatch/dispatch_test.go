package dispatch_test

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"soulman/action-svc/dispatch"
	"soulman/common"
)

type fakePublisher struct {
	mu      sync.Mutex
	records []common.OutcomeRecord
}

func (f *fakePublisher) PublishOutcome(rec common.OutcomeRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = append(f.records, rec)
	return nil
}

func (f *fakePublisher) last() (common.OutcomeRecord, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.records) == 0 {
		return common.OutcomeRecord{}, false
	}
	return f.records[len(f.records)-1], true
}

func TestHandle_UnknownActionType_DroppedWithoutPublish(t *testing.T) {
	pub := &fakePublisher{}
	d := dispatch.New(t.TempDir(), pub, nil, nil)

	req := common.ActionRequest{CorrelationID: "t1", ActionHint: "does_not_exist"}
	b, _ := json.Marshal(req)
	d.Handle(b)

	if _, ok := pub.last(); ok {
		t.Error("unknown action_hint should not publish an outcome record")
	}
}

func TestHandle_AppendSuccess_PublishesSuccessOutcome(t *testing.T) {
	orig := dispatch.AppendReportEntry
	dispatch.AppendReportEntry = func(root string, params json.RawMessage) (string, error) {
		return "fake/path.txt", nil
	}
	defer func() { dispatch.AppendReportEntry = orig }()

	pub := &fakePublisher{}
	d := dispatch.New(t.TempDir(), pub, nil, nil)

	req := common.ActionRequest{CorrelationID: "t2", ActionHint: "append_daily_report_entry", Parameters: json.RawMessage(`{}`)}
	b, _ := json.Marshal(req)
	d.Handle(b)

	rec, ok := pub.last()
	if !ok {
		t.Fatal("expected an outcome record to be published")
	}
	if rec.Status != "success" || rec.TaskID != "t2" || rec.ActionType != "append_daily_report_entry" {
		t.Errorf("outcome = %+v, want success/t2/append_daily_report_entry", rec)
	}
	if rec.Summary != "Daily report entry appended" || rec.Decision != "append_daily_report_entry" {
		t.Errorf("outcome = %+v, want summary=%q decision=%q", rec, "Daily report entry appended", "append_daily_report_entry")
	}
	if len(rec.Tags) != 1 || rec.Tags[0] != "report" {
		t.Errorf("Tags = %v, want [report]", rec.Tags)
	}
}

func TestHandle_AppendFailsTwice_RetriesOnceThenPublishesFailedOutcome(t *testing.T) {
	calls := 0
	orig := dispatch.AppendReportEntry
	dispatch.AppendReportEntry = func(root string, params json.RawMessage) (string, error) {
		calls++
		return "", errors.New("boom")
	}
	defer func() { dispatch.AppendReportEntry = orig }()

	pub := &fakePublisher{}
	d := dispatch.New(t.TempDir(), pub, nil, nil)

	req := common.ActionRequest{CorrelationID: "t3", ActionHint: "append_daily_report_entry", Parameters: json.RawMessage(`{}`)}
	b, _ := json.Marshal(req)
	d.Handle(b)

	if calls != 2 {
		t.Errorf("AppendReportEntry called %d times, want 2 (one retry)", calls)
	}
	rec, ok := pub.last()
	if !ok {
		t.Fatal("expected an outcome record to be published")
	}
	if rec.Status != "failed" {
		t.Errorf("status = %q, want failed", rec.Status)
	}
}

func TestHandle_BadJSON_DoesNotPanicOrPublish(t *testing.T) {
	pub := &fakePublisher{}
	d := dispatch.New(t.TempDir(), pub, nil, nil)
	d.Handle([]byte("not json"))
	if _, ok := pub.last(); ok {
		t.Error("bad JSON should not publish an outcome record")
	}
}

func TestAppendReportEntry_RealImplementation_WritesReportFile(t *testing.T) {
	root := t.TempDir()
	params, _ := json.Marshal(map[string]string{
		"summary":     "test error",
		"raw_content": "stack trace here",
		"source_path": `C:\errors\file.txt`,
		"occurred_at": "2026-07-17T14:32:00-06:00",
	})

	path, err := dispatch.AppendReportEntry(root, params)
	if err != nil {
		t.Fatalf("AppendReportEntry: %v", err)
	}
	if path == "" {
		t.Error("expected non-empty report path")
	}
}
