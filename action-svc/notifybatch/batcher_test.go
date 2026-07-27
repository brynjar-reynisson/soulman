package notifybatch_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"soulman/action-svc/notifybatch"
)

type fakeNotifier struct {
	mu       sync.Mutex
	messages []string
	sendCh   chan string
}

func newFakeNotifier() *fakeNotifier {
	return &fakeNotifier{sendCh: make(chan string, 10)}
}

func (f *fakeNotifier) Send(message string) error {
	f.mu.Lock()
	f.messages = append(f.messages, message)
	f.mu.Unlock()
	f.sendCh <- message
	return nil
}

func (f *fakeNotifier) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.messages...)
}

func waitForSend(t *testing.T, f *fakeNotifier, timeout time.Duration) string {
	t.Helper()
	select {
	case msg := <-f.sendCh:
		return msg
	case <-time.After(timeout):
		t.Fatal("timed out waiting for a Send call")
		return ""
	}
}

func TestBatcher_SingleItem_FlushesAfterGracePeriod(t *testing.T) {
	notifier := newFakeNotifier()
	b := notifybatch.New(40*time.Millisecond, 2*time.Second, notifier)

	b.Add(notifybatch.Item{Kind: "gmail", Sender: "a@b.com", Subject: "hi", Reason: "r", BodyExcerpt: "excerpt", ThreadID: "t1"})

	msg := waitForSend(t, notifier, time.Second)
	if !strings.Contains(msg, "1 important item(s):") {
		t.Errorf("message = %q, want a 1-item header", msg)
	}
	if !strings.Contains(msg, "a@b.com") {
		t.Errorf("message = %q, want it to contain the sender", msg)
	}

	time.Sleep(100 * time.Millisecond)
	if len(notifier.sent()) != 1 {
		t.Errorf("Send called %d times, want exactly 1", len(notifier.sent()))
	}
}

func TestBatcher_MultipleItemsWithinGracePeriod_CombineIntoOneSend(t *testing.T) {
	notifier := newFakeNotifier()
	b := notifybatch.New(60*time.Millisecond, 2*time.Second, notifier)

	b.Add(notifybatch.Item{Kind: "gmail", Sender: "a@b.com", Subject: "one", Reason: "r1", BodyExcerpt: "e1", ThreadID: "t1"})
	time.Sleep(15 * time.Millisecond)
	b.Add(notifybatch.Item{Kind: "gmail", Sender: "c@d.com", Subject: "two", Reason: "r2", BodyExcerpt: "e2", ThreadID: "t2"})

	msg := waitForSend(t, notifier, time.Second)
	if !strings.Contains(msg, "2 important item(s):") {
		t.Errorf("message = %q, want a 2-item header", msg)
	}
	if !strings.Contains(msg, "one") || !strings.Contains(msg, "two") {
		t.Errorf("message = %q, want both subjects present", msg)
	}

	time.Sleep(150 * time.Millisecond)
	if len(notifier.sent()) != 1 {
		t.Errorf("Send called %d times, want exactly 1 combined send", len(notifier.sent()))
	}
}

func TestBatcher_SteadyTrickle_FlushesAtMaxWaitCap(t *testing.T) {
	notifier := newFakeNotifier()
	maxWait := 150 * time.Millisecond
	b := notifybatch.New(50*time.Millisecond, maxWait, notifier)

	start := time.Now()
	stop := time.After(300 * time.Millisecond)
	ticker := time.NewTicker(30 * time.Millisecond)
	defer ticker.Stop()

loop:
	for {
		select {
		case <-ticker.C:
			b.Add(notifybatch.Item{Kind: "gmail", Sender: "a@b.com", Subject: "trickle", Reason: "r", BodyExcerpt: "e", ThreadID: "t"})
		case <-stop:
			break loop
		}
	}

	msg := waitForSend(t, notifier, time.Second)
	elapsed := time.Since(start)
	if elapsed > 350*time.Millisecond {
		t.Errorf("first flush took %v, want it forced by the %v max-wait cap well before the 300ms trickle stopped", elapsed, maxWait)
	}
	if !strings.Contains(msg, "important item(s):") {
		t.Errorf("message = %q, want the batch header", msg)
	}
}

func TestBatcher_ReportItem_FlushesWithReportFormat(t *testing.T) {
	notifier := newFakeNotifier()
	b := notifybatch.New(40*time.Millisecond, 2*time.Second, notifier)

	b.Add(notifybatch.Item{Kind: "report", Summary: `Disk space C:\ critical: 97% used`, SourcePath: `system-monitor/disk_space/C:\`, BodyExcerpt: `Disk space C:\ critical: 97% used (threshold 95%)`})

	msg := waitForSend(t, notifier, time.Second)
	if !strings.Contains(msg, `[system-monitor/disk_space/C:\]`) {
		t.Errorf("message = %q, want the source_path in brackets", msg)
	}
	if !strings.Contains(msg, `Disk space C:\ critical: 97% used`) {
		t.Errorf("message = %q, want the summary", msg)
	}
}

func TestBatcher_MixedGmailAndReportItems_BothRenderInOneFlush(t *testing.T) {
	notifier := newFakeNotifier()
	b := notifybatch.New(60*time.Millisecond, 2*time.Second, notifier)

	b.Add(notifybatch.Item{Kind: "gmail", Sender: "boss@company.com", Subject: "Server down", Reason: "outage", BodyExcerpt: "excerpt", ThreadID: "t1"})
	time.Sleep(15 * time.Millisecond)
	b.Add(notifybatch.Item{Kind: "report", Summary: "nats consumer start failed", SourcePath: "log-error/action-svc", BodyExcerpt: "2026/07/27 ERROR nats consumer start failed error=..."})

	msg := waitForSend(t, notifier, time.Second)
	if !strings.Contains(msg, "2 important item(s):") {
		t.Errorf("message = %q, want a 2-item header", msg)
	}
	if !strings.Contains(msg, "boss@company.com") {
		t.Errorf("message = %q, want the gmail item rendered", msg)
	}
	if !strings.Contains(msg, "[log-error/action-svc]") {
		t.Errorf("message = %q, want the report item rendered", msg)
	}
}
