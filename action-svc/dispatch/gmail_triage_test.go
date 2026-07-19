package dispatch_test

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"soulman/action-svc/dispatch"
	"soulman/action-svc/feign"
	"soulman/action-svc/notifybatch"
	"soulman/common"
)

type fakeBatcher struct {
	mu    sync.Mutex
	items []notifybatch.Item
}

func (f *fakeBatcher) Add(item notifybatch.Item) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items = append(f.items, item)
}

func (f *fakeBatcher) added() []notifybatch.Item {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]notifybatch.Item(nil), f.items...)
}

func gmailTriageParamsJSON(t *testing.T, sender, subject, reason string, important bool) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"sender":       sender,
		"subject":      subject,
		"body_excerpt": "excerpt text",
		"reason":       reason,
		"important":    important,
		"thread_id":    "thread-1",
		"occurred_at":  "2026-07-18T09:00:00-06:00",
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return b
}

func TestDispatch_GmailTriage_Important_AddsToBatcher(t *testing.T) {
	pub := &fakePublisher{}
	batcher := &fakeBatcher{}
	d := dispatch.New(t.TempDir(), pub, batcher, nil)

	req := common.ActionRequest{
		CorrelationID: "g1",
		ActionHint:    "triage_gmail_email",
		Parameters:    gmailTriageParamsJSON(t, "boss@company.com", "Server down", "outage", true),
	}
	b, _ := json.Marshal(req)
	d.Handle(b)

	items := batcher.added()
	if len(items) != 1 {
		t.Fatalf("batcher.Add called %d times, want 1", len(items))
	}
	if items[0].Sender != "boss@company.com" || items[0].Subject != "Server down" {
		t.Errorf("batched item = %+v, want sender/subject to match", items[0])
	}

	rec, ok := pub.last()
	if !ok || rec.Status != "success" || rec.ActionType != "triage_gmail_email" {
		t.Errorf("outcome = %+v, ok=%v, want status=success actionType=triage_gmail_email", rec, ok)
	}
	if rec.Decision != "notified via Discord" {
		t.Errorf("Decision = %q, want %q", rec.Decision, "notified via Discord")
	}
	if rec.Summary != "Server down — important" {
		t.Errorf("Summary = %q, want %q", rec.Summary, "Server down — important")
	}
	if len(rec.Tags) != 2 || rec.Tags[0] != "gmail" || rec.Tags[1] != "triage" {
		t.Errorf("Tags = %v, want [gmail triage]", rec.Tags)
	}
}

func TestDispatch_GmailTriage_NotImportant_SkipsBatcher(t *testing.T) {
	pub := &fakePublisher{}
	batcher := &fakeBatcher{}
	d := dispatch.New(t.TempDir(), pub, batcher, nil)

	req := common.ActionRequest{
		CorrelationID: "g2",
		ActionHint:    "triage_gmail_email",
		Parameters:    gmailTriageParamsJSON(t, "newsletter@example.com", "Weekly digest", "routine", false),
	}
	b, _ := json.Marshal(req)
	d.Handle(b)

	if items := batcher.added(); len(items) != 0 {
		t.Errorf("batcher.Add called %d times, want 0 for a not-important email", len(items))
	}

	rec, ok := pub.last()
	if !ok || rec.Decision != "logged only" {
		t.Errorf("outcome = %+v, ok=%v, want Decision=%q", rec, ok, "logged only")
	}
}

func TestDispatch_GmailTriage_AlwaysWritesReportEntry_RegardlessOfImportance(t *testing.T) {
	orig := dispatch.AppendGmailReportEntry
	calls := 0
	dispatch.AppendGmailReportEntry = func(root string, params json.RawMessage) (string, error) {
		calls++
		return "fake/path.txt", nil
	}
	defer func() { dispatch.AppendGmailReportEntry = orig }()

	pub := &fakePublisher{}
	batcher := &fakeBatcher{}
	d := dispatch.New(t.TempDir(), pub, batcher, nil)

	req := common.ActionRequest{
		CorrelationID: "g3",
		ActionHint:    "triage_gmail_email",
		Parameters:    gmailTriageParamsJSON(t, "newsletter@example.com", "Weekly digest", "routine", false),
	}
	b, _ := json.Marshal(req)
	d.Handle(b)

	if calls != 1 {
		t.Errorf("AppendGmailReportEntry called %d times for a not-important email, want 1", calls)
	}
}

func TestDispatch_GmailTriage_ReportAppendFailsTwice_RetriesOnceThenPublishesFailedOutcome(t *testing.T) {
	orig := dispatch.AppendGmailReportEntry
	calls := 0
	dispatch.AppendGmailReportEntry = func(root string, params json.RawMessage) (string, error) {
		calls++
		return "", errors.New("boom")
	}
	defer func() { dispatch.AppendGmailReportEntry = orig }()

	pub := &fakePublisher{}
	d := dispatch.New(t.TempDir(), pub, &fakeBatcher{}, nil)

	req := common.ActionRequest{
		CorrelationID: "g4",
		ActionHint:    "triage_gmail_email",
		Parameters:    gmailTriageParamsJSON(t, "a@b.com", "s", "r", false),
	}
	b, _ := json.Marshal(req)
	d.Handle(b)

	if calls != 2 {
		t.Errorf("AppendGmailReportEntry called %d times, want 2 (one retry)", calls)
	}
	rec, ok := pub.last()
	if !ok || rec.Status != "failed" {
		t.Errorf("outcome = %+v, ok=%v, want status=failed", rec, ok)
	}
}

func TestAppendGmailReportEntry_RealImplementation_WritesReportFile(t *testing.T) {
	root := t.TempDir()
	params := gmailTriageParamsJSON(t, "a@b.com", "subject", "reason text", true)

	path, err := dispatch.AppendGmailReportEntry(root, params)
	if err != nil {
		t.Fatalf("AppendGmailReportEntry: %v", err)
	}
	if path == "" {
		t.Error("expected non-empty report path")
	}
}

func TestDispatch_GmailTriage_FeignMode_Important_DecisionSaysFeigned(t *testing.T) {
	pub := &fakePublisher{}
	batcher := &fakeBatcher{}
	gate := feign.New(true, filepath.Join(t.TempDir(), "feigned-actions.jsonl"))
	d := dispatch.New(t.TempDir(), pub, batcher, gate)

	req := common.ActionRequest{
		CorrelationID: "g5",
		ActionHint:    "triage_gmail_email",
		Parameters:    gmailTriageParamsJSON(t, "boss@company.com", "Server down", "outage", true),
	}
	b, _ := json.Marshal(req)
	d.Handle(b)

	// Deciding to notify (queuing on the batcher) is unaffected by feign
	// mode — only the eventual real Notifier.Send (inside the batcher's
	// flush, via whatever Notifier main.go wired in) is what actually gets
	// intercepted, tested separately in the feign package.
	if items := batcher.added(); len(items) != 1 {
		t.Errorf("batcher.Add called %d times, want 1 (feign mode doesn't change the decision to notify)", len(items))
	}

	rec, ok := pub.last()
	if !ok || rec.Decision != "feigned notify via Discord" {
		t.Errorf("outcome = %+v, ok=%v, want Decision=%q", rec, ok, "feigned notify via Discord")
	}
}
