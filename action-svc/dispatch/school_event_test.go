package dispatch_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"soulman/action-svc/dispatch"
	"soulman/action-svc/schoolevents"
	"soulman/common"
)

func schoolEventParamsJSON(t *testing.T, sender, subject string, occurredAt string, events []map[string]interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]interface{}{
		"sender":       sender,
		"subject":      subject,
		"body_excerpt": "excerpt text",
		"note":         "",
		"thread_id":    "thread-9",
		"occurred_at":  occurredAt,
		"events":       events,
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return b
}

func TestDispatch_SchoolEvent_ContactEmail_PersistedToStore(t *testing.T) {
	root := t.TempDir()
	pub := &fakePublisher{}
	d := dispatch.New(root, pub, nil, nil)

	future := time.Now().AddDate(0, 0, 3).Format("2006-01-02")
	req := common.ActionRequest{
		CorrelationID: "s5",
		ActionHint:    "process_school_event",
		Parameters: schoolEventParamsJSON(t, "noreply@mentor.is", "Reminder", time.Now().Format(time.RFC3339),
			[]map[string]interface{}{{"date": future, "has_time": false, "time": "", "description": "Sweater day", "contact_email": "alma@reykjavik.is"}}),
	}
	b, _ := json.Marshal(req)
	d.Handle(b)

	id := schoolevents.ID("thread-9", 0, future)
	data, err := os.ReadFile(filepath.Join(root, "logs", "school-events", id+".json"))
	if err != nil {
		t.Fatalf("read queued event file: %v", err)
	}
	var e schoolevents.Event
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if e.ContactEmail != "alma@reykjavik.is" {
		t.Errorf("ContactEmail = %q, want alma@reykjavik.is", e.ContactEmail)
	}
}

func TestDispatch_SchoolEvent_FutureDate_QueuesEvent(t *testing.T) {
	root := t.TempDir()
	pub := &fakePublisher{}
	d := dispatch.New(root, pub, nil, nil)

	future := time.Now().AddDate(0, 0, 3).Format("2006-01-02")
	req := common.ActionRequest{
		CorrelationID: "s1",
		ActionHint:    "process_school_event",
		Parameters: schoolEventParamsJSON(t, "teacher@reykjavik.is", "Reminder", time.Now().Format(time.RFC3339),
			[]map[string]interface{}{{"date": future, "has_time": false, "time": "", "description": "Sweater day"}}),
	}
	b, _ := json.Marshal(req)
	d.Handle(b)

	id := schoolevents.ID("thread-9", 0, future)
	entries, err := os.ReadDir(filepath.Join(root, "logs", "school-events"))
	if err != nil {
		t.Fatalf("read school-events dir: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Name() == id+".json" {
			found = true
		}
	}
	if !found {
		t.Errorf("no pending event file found for id %s among %v", id, entries)
	}
}

func TestDispatch_SchoolEvent_PastDate_DroppedSilently(t *testing.T) {
	root := t.TempDir()
	pub := &fakePublisher{}
	d := dispatch.New(root, pub, nil, nil)

	past := time.Now().AddDate(0, 0, -3).Format("2006-01-02")
	req := common.ActionRequest{
		CorrelationID: "s2",
		ActionHint:    "process_school_event",
		Parameters: schoolEventParamsJSON(t, "teacher@reykjavik.is", "Reminder", time.Now().Format(time.RFC3339),
			[]map[string]interface{}{{"date": past, "has_time": false, "time": "", "description": "Already happened"}}),
	}
	b, _ := json.Marshal(req)
	d.Handle(b)

	entries, err := os.ReadDir(filepath.Join(root, "logs", "school-events"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read school-events dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("school-events dir = %v, want empty for a past-dated event", entries)
	}
}

func TestDispatch_SchoolEvent_NoEvents_ReportOnlyNotImportant(t *testing.T) {
	root := t.TempDir()
	pub := &fakePublisher{}
	d := dispatch.New(root, pub, nil, nil)

	req := common.ActionRequest{
		CorrelationID: "s3",
		ActionHint:    "process_school_event",
		Parameters:    schoolEventParamsJSON(t, "info@reykjavik.is", "Notice", time.Now().Format(time.RFC3339), nil),
	}
	b, _ := json.Marshal(req)
	d.Handle(b)

	rec, ok := pub.last()
	if !ok || rec.Decision != "logged only" {
		t.Errorf("outcome = %+v, ok=%v, want Decision=logged only", rec, ok)
	}
}

func TestDispatch_SchoolEvent_EventsFound_OutcomeReflectsCount(t *testing.T) {
	root := t.TempDir()
	pub := &fakePublisher{}
	d := dispatch.New(root, pub, nil, nil)

	future := time.Now().AddDate(0, 0, 3).Format("2006-01-02")
	req := common.ActionRequest{
		CorrelationID: "s4",
		ActionHint:    "process_school_event",
		Parameters: schoolEventParamsJSON(t, "teacher@reykjavik.is", "Reminder", time.Now().Format(time.RFC3339),
			[]map[string]interface{}{{"date": future, "has_time": false, "time": "", "description": "Sweater day"}}),
	}
	b, _ := json.Marshal(req)
	d.Handle(b)

	rec, ok := pub.last()
	if !ok || rec.Decision != "1 event(s) queued" {
		t.Errorf("outcome = %+v, ok=%v, want Decision=\"1 event(s) queued\"", rec, ok)
	}
}
