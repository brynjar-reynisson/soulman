package scheduler_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"soulman/action-svc/feign"
	"soulman/action-svc/report"
	"soulman/action-svc/scheduler"
	"soulman/common"
)

type fakeNotifier struct {
	mu       sync.Mutex
	messages []string
	failN    int // number of Send calls to fail before succeeding
	calls    int // total number of Send calls
}

func (f *fakeNotifier) Send(message string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.failN > 0 {
		f.failN--
		return errors.New("simulated send failure")
	}
	f.messages = append(f.messages, message)
	return nil
}

func (f *fakeNotifier) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.messages...)
}

func (f *fakeNotifier) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

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

func fixedNow() time.Time { return time.Date(2026, 7, 17, 10, 0, 0, 0, time.Local) }

func TestRunOnce_MissingReport_SkipsSend(t *testing.T) {
	root := t.TempDir()
	notifier := &fakeNotifier{}
	pub := &fakePublisher{}
	s := scheduler.New(root, "10:00", notifier, pub, nil)
	s.Now = fixedNow

	s.RunOnce()

	if len(notifier.sent()) != 0 {
		t.Error("expected no send for missing report")
	}
}

func TestRunOnce_WhitespaceOnlyReport_SkipsSend(t *testing.T) {
	root := t.TempDir()
	yesterday := time.Date(2026, 7, 16, 0, 0, 0, 0, time.Local)
	path := report.PathForDate(root, yesterday, true)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("setup mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("   \n\n  "), 0o644); err != nil {
		t.Fatalf("setup write: %v", err)
	}

	notifier := &fakeNotifier{}
	pub := &fakePublisher{}
	s := scheduler.New(root, "10:00", notifier, pub, nil)
	s.Now = fixedNow

	s.RunOnce()

	if len(notifier.sent()) != 0 {
		t.Error("expected no send for whitespace-only report")
	}
}

func TestRunOnce_NonEmptyReport_SendsContentAndPublishesSuccess(t *testing.T) {
	root := t.TempDir()
	yesterday := time.Date(2026, 7, 16, 0, 0, 0, 0, time.Local)
	if _, err := report.Append(root, report.Entry{
		OccurredAt: yesterday,
		Summary:    "test error",
		RawContent: "trace",
		SourcePath: `C:\errors\a.txt`,
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	notifier := &fakeNotifier{}
	pub := &fakePublisher{}
	s := scheduler.New(root, "10:00", notifier, pub, nil)
	s.Now = fixedNow

	s.RunOnce()

	sent := notifier.sent()
	if len(sent) != 1 {
		t.Fatalf("expected exactly one send, got %d", len(sent))
	}
	if !strings.Contains(sent[0], "test error") {
		t.Errorf("sent message = %q, want it to contain %q", sent[0], "test error")
	}

	rec, ok := pub.last()
	if !ok || rec.Status != "success" || rec.ActionType != "daily_report_delivery" {
		t.Errorf("outcome = %+v, ok=%v, want status=success actionType=daily_report_delivery", rec, ok)
	}
	if rec.Summary != "Daily report delivered" || rec.Decision != "daily_report_delivery" {
		t.Errorf("outcome = %+v, want summary=%q decision=%q", rec, "Daily report delivered", "daily_report_delivery")
	}
	if len(rec.Tags) != 2 || rec.Tags[0] != "report" || rec.Tags[1] != "cron" {
		t.Errorf("Tags = %v, want [report cron]", rec.Tags)
	}
}

func TestRunOnce_ReportNeverModifiedOrDeleted(t *testing.T) {
	root := t.TempDir()
	yesterday := time.Date(2026, 7, 16, 0, 0, 0, 0, time.Local)
	path, err := report.Append(root, report.Entry{OccurredAt: yesterday, Summary: "s"})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	before, _ := os.ReadFile(path)

	notifier := &fakeNotifier{}
	s := scheduler.New(root, "10:00", notifier, &fakePublisher{}, nil)
	s.Now = fixedNow
	s.RunOnce()

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("report file missing after RunOnce: %v", err)
	}
	if string(before) != string(after) {
		t.Error("report file was modified by RunOnce")
	}
}

func TestRunOnce_SendFailsAllThreeAttempts_PublishesFailedOutcome(t *testing.T) {
	root := t.TempDir()
	yesterday := time.Date(2026, 7, 16, 0, 0, 0, 0, time.Local)
	if _, err := report.Append(root, report.Entry{OccurredAt: yesterday, Summary: "s"}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	notifier := &fakeNotifier{failN: 3}
	pub := &fakePublisher{}
	s := scheduler.New(root, "10:00", notifier, pub, nil)
	s.Now = fixedNow
	s.BackoffBase = time.Millisecond // keep the test fast

	s.RunOnce()

	if notifier.callCount() != 3 {
		t.Errorf("expected 3 Send calls, got %d", notifier.callCount())
	}

	rec, ok := pub.last()
	if !ok || rec.Status != "failed" {
		t.Errorf("outcome = %+v, ok=%v, want status=failed", rec, ok)
	}
}

func TestRunOnce_SendFailsTwiceThenSucceeds_PublishesSuccessOutcome(t *testing.T) {
	root := t.TempDir()
	yesterday := time.Date(2026, 7, 16, 0, 0, 0, 0, time.Local)
	if _, err := report.Append(root, report.Entry{OccurredAt: yesterday, Summary: "s"}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	notifier := &fakeNotifier{failN: 2}
	pub := &fakePublisher{}
	s := scheduler.New(root, "10:00", notifier, pub, nil)
	s.Now = fixedNow
	s.BackoffBase = time.Millisecond

	s.RunOnce()

	if len(notifier.sent()) != 1 {
		t.Errorf("expected the eventual retry to succeed and send once, got %d sends", len(notifier.sent()))
	}
	rec, ok := pub.last()
	if !ok || rec.Status != "success" {
		t.Errorf("outcome = %+v, ok=%v, want status=success", rec, ok)
	}
}

func TestRunOnce_FeignMode_SummarySaysFeigned(t *testing.T) {
	root := t.TempDir()
	yesterday := time.Date(2026, 7, 16, 0, 0, 0, 0, time.Local)
	if _, err := report.Append(root, report.Entry{OccurredAt: yesterday, Summary: "test error"}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	notifier := &fakeNotifier{}
	pub := &fakePublisher{}
	gate := feign.New(true, filepath.Join(t.TempDir(), "feigned-actions.jsonl"))
	s := scheduler.New(root, "10:00", notifier, pub, gate)
	s.Now = fixedNow

	s.RunOnce()

	// Scheduler always calls the Notifier it was given — the actual
	// interception happens one layer down, inside whatever Notifier
	// main.go wired in (tested in the feign package). Here we're only
	// verifying scheduler's own outcome-record phrasing.
	if len(notifier.sent()) != 1 {
		t.Errorf("expected exactly one Send call regardless of feign mode, got %d", len(notifier.sent()))
	}

	rec, ok := pub.last()
	if !ok || rec.Status != "success" {
		t.Errorf("outcome = %+v, ok=%v, want status=success", rec, ok)
	}
	if rec.Summary != "Daily report delivery feigned" {
		t.Errorf("Summary = %q, want %q", rec.Summary, "Daily report delivery feigned")
	}
}
