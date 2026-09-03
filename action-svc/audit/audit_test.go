package audit_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"soulman/action-svc/audit"
)

type fakeNotifier struct {
	sent []string
	err  error
}

func (f *fakeNotifier) Send(message string) error {
	f.sent = append(f.sent, message)
	return f.err
}

func readEntries(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	entries := make([]map[string]any, len(lines))
	for i, line := range lines {
		var e map[string]any
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("unmarshal line %d (%q): %v", i, line, err)
		}
		entries[i] = e
	}
	return entries
}

func TestSend_Success_RecordsSentEntry(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "nested", "discord-audit.jsonl")
	log := audit.New(logPath)
	real := &fakeNotifier{}
	n := audit.Wrap(log, real, "daily-digest")

	if err := n.Send("today's report"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(real.sent) != 1 || real.sent[0] != "today's report" {
		t.Errorf("real.sent = %v, want [\"today's report\"]", real.sent)
	}

	entries := readEntries(t, logPath)
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e["reason"] != "daily-digest" {
		t.Errorf("reason = %v, want daily-digest", e["reason"])
	}
	if e["summary"] != "today's report" {
		t.Errorf("summary = %v, want %q", e["summary"], "today's report")
	}
	if e["status"] != "sent" {
		t.Errorf("status = %v, want sent", e["status"])
	}
	if _, hasErr := e["error"]; hasErr {
		t.Errorf("entry = %+v, want no error field on success", e)
	}
	if e["timestamp"] == nil || e["timestamp"] == "" {
		t.Error("timestamp must be set")
	}
}

func TestSend_Failure_RecordsFailedEntryWithError_AndPropagatesError(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "discord-audit.jsonl")
	log := audit.New(logPath)
	real := &fakeNotifier{err: errors.New("discord api down")}
	n := audit.Wrap(log, real, "important-batch")

	err := n.Send("2 important item(s): ...")
	if err == nil || err.Error() != "discord api down" {
		t.Fatalf("Send: got %v, want the real notifier's error to propagate unchanged", err)
	}

	entries := readEntries(t, logPath)
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e["status"] != "failed" {
		t.Errorf("status = %v, want failed", e["status"])
	}
	if e["error"] != "discord api down" {
		t.Errorf("error = %v, want %q", e["error"], "discord api down")
	}
	if e["reason"] != "important-batch" {
		t.Errorf("reason = %v, want important-batch", e["reason"])
	}
}

func TestSend_MultilineMessage_SummaryCollapsesWholeMessage(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "discord-audit.jsonl")
	log := audit.New(logPath)
	n := audit.Wrap(log, &fakeNotifier{}, "school-reminder")

	message := "**Soulman Reminder**\n📅 Tomorrow (2026-09-04):\n• Treyjudagurinn (from thuridur.ottarsdottir01@reykjavik.is)"
	if err := n.Send(message); err != nil {
		t.Fatalf("Send: %v", err)
	}

	entries := readEntries(t, logPath)
	want := "**Soulman Reminder** 📅 Tomorrow (2026-09-04): • Treyjudagurinn (from thuridur.ottarsdottir01@reykjavik.is)"
	if entries[0]["summary"] != want {
		t.Errorf("summary = %v, want %q (the whole message collapsed onto one line, not just the first line)", entries[0]["summary"], want)
	}
}

func TestSend_BatchHeaderMessage_SummaryIncludesTheActualItem(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "discord-audit.jsonl")
	log := audit.New(logPath)
	n := audit.Wrap(log, &fakeNotifier{}, "important-batch")

	// Mirrors notifybatch.formatBatch's real shape: a count header on its
	// own line, blank line, then the actual item — the exact shape that
	// motivated this fix (a first-line-only summary recorded only the
	// header, never what was actually sent).
	message := "1 important item(s):\n\n[C:\\Users\\Lenovo\\DigitalMe\\errors] unknown-file\nAudit log verification"
	if err := n.Send(message); err != nil {
		t.Fatalf("Send: %v", err)
	}

	entries := readEntries(t, logPath)
	summary, _ := entries[0]["summary"].(string)
	if !strings.Contains(summary, "Audit log verification") {
		t.Errorf("summary = %q, want it to include the actual item content, not just the count header", summary)
	}
}

func TestSend_LongMessage_SummaryTruncated(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "discord-audit.jsonl")
	log := audit.New(logPath)
	n := audit.Wrap(log, &fakeNotifier{}, "important-batch")

	longLine := strings.Repeat("x", 300)
	if err := n.Send(longLine); err != nil {
		t.Fatalf("Send: %v", err)
	}

	entries := readEntries(t, logPath)
	summary, _ := entries[0]["summary"].(string)
	runes := []rune(summary)
	if len(runes) != 151 { // 150 chars + the "…" marker
		t.Fatalf("summary length = %d, want 151 (150 + ellipsis)", len(runes))
	}
	if runes[150] != '…' {
		t.Errorf("summary = %q, want it to end with an ellipsis marker", summary)
	}
}

func TestSend_MultipleReasons_ShareSameLogFile(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "discord-audit.jsonl")
	log := audit.New(logPath)
	daily := audit.Wrap(log, &fakeNotifier{}, "daily-digest")
	batch := audit.Wrap(log, &fakeNotifier{}, "important-batch")
	school := audit.Wrap(log, &fakeNotifier{}, "school-reminder")

	if err := daily.Send("digest"); err != nil {
		t.Fatalf("daily Send: %v", err)
	}
	if err := batch.Send("batch"); err != nil {
		t.Fatalf("batch Send: %v", err)
	}
	if err := school.Send("reminder"); err != nil {
		t.Fatalf("school Send: %v", err)
	}

	entries := readEntries(t, logPath)
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	reasons := map[string]bool{}
	for _, e := range entries {
		reasons[e["reason"].(string)] = true
	}
	for _, want := range []string{"daily-digest", "important-batch", "school-reminder"} {
		if !reasons[want] {
			t.Errorf("missing entry with reason %q among %v", want, entries)
		}
	}
}
