package gmailwatcher

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"google.golang.org/api/gmail/v1"

	"soulman/common"
)

// fakeClient is shared between the test goroutine and the poll-loop
// goroutine once Start is called (since Start's first poll now runs
// entirely in the background, per the async-startup fix) — mu guards
// listCalls against concurrent access from both.
type fakeClient struct {
	mu            sync.Mutex
	listIDs       []string
	listErr       error
	messages      map[string]*gmail.Message
	getErr        error
	ensureLabelID string
	ensureErr     error
	addedLabels   map[string]string
	addLabelErr   error
	listCalls     int
}

func (f *fakeClient) ListMatching(ctx context.Context, query string) ([]string, error) {
	f.mu.Lock()
	f.listCalls++
	f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.listIDs, nil
}

func (f *fakeClient) getListCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listCalls
}

func (f *fakeClient) GetMessage(ctx context.Context, id string) (*gmail.Message, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	msg, ok := f.messages[id]
	if !ok {
		return nil, errors.New("fakeClient: no fixture for message " + id)
	}
	return msg, nil
}

func (f *fakeClient) EnsureLabel(ctx context.Context, name string) (string, error) {
	if f.ensureErr != nil {
		return "", f.ensureErr
	}
	return f.ensureLabelID, nil
}

func (f *fakeClient) AddLabel(ctx context.Context, id, labelID string) error {
	if f.addLabelErr != nil {
		return f.addLabelErr
	}
	if f.addedLabels == nil {
		f.addedLabels = map[string]string{}
	}
	f.addedLabels[id] = labelID
	return nil
}

// fakePublisher is shared between the test goroutine and (since Start's
// first poll now runs in a background goroutine, per the async-startup
// fix) the poll-loop goroutine — mu guards published against concurrent
// access from both.
type fakePublisher struct {
	mu            sync.Mutex
	published     []*common.Stimulus
	publishErrFor map[string]error
}

func (f *fakePublisher) Publish(ctx context.Context, s *common.Stimulus) error {
	if err, ok := f.publishErrFor[s.ChannelMeta.MessageID]; ok {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published = append(f.published, s)
	return nil
}

func (f *fakePublisher) publishedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.published)
}

func validMessage(id string) *gmail.Message {
	return &gmail.Message{
		Id:           id,
		ThreadId:     "thread-" + id,
		InternalDate: 1700000000000,
		Payload: &gmail.MessagePart{
			MimeType: "text/plain",
			Headers: []*gmail.MessagePartHeader{
				{Name: "From", Value: "sender@example.com"},
				{Name: "Subject", Value: "test"},
			},
			Body: &gmail.MessagePartBody{Data: encodeBody("body for " + id)},
		},
	}
}

func TestPoll_PublishesEachMatchAndLabelsIt(t *testing.T) {
	client := &fakeClient{
		listIDs:       []string{"m1", "m2"},
		messages:      map[string]*gmail.Message{"m1": validMessage("m1"), "m2": validMessage("m2")},
		ensureLabelID: "Label_1",
	}
	pub := &fakePublisher{}
	w := newWatcher(client, pub, "in:inbox is:unread", "soulman/seen", time.Second)

	w.poll(context.Background())

	if len(pub.published) != 2 {
		t.Fatalf("published = %d messages, want 2", len(pub.published))
	}
	if client.addedLabels["m1"] != "Label_1" || client.addedLabels["m2"] != "Label_1" {
		t.Errorf("addedLabels = %v, want both m1 and m2 labeled Label_1", client.addedLabels)
	}
}

func TestPoll_PublishFailure_SkipsLabelAndWillRetryNextPoll(t *testing.T) {
	client := &fakeClient{
		listIDs:       []string{"m1"},
		messages:      map[string]*gmail.Message{"m1": validMessage("m1")},
		ensureLabelID: "Label_1",
	}
	pub := &fakePublisher{publishErrFor: map[string]error{"m1": errors.New("nats down")}}
	w := newWatcher(client, pub, "in:inbox is:unread", "soulman/seen", time.Second)

	w.poll(context.Background())

	if len(pub.published) != 0 {
		t.Errorf("published = %d messages, want 0 (publish failed)", len(pub.published))
	}
	if _, labeled := client.addedLabels["m1"]; labeled {
		t.Error("m1 was labeled despite a failed publish — should be left unlabeled so it's retried next poll")
	}
}

func TestPoll_LabelFailure_MessageStillCountsAsPublished(t *testing.T) {
	client := &fakeClient{
		listIDs:       []string{"m1"},
		messages:      map[string]*gmail.Message{"m1": validMessage("m1")},
		ensureLabelID: "Label_1",
		addLabelErr:   errors.New("modify failed"),
	}
	pub := &fakePublisher{}
	w := newWatcher(client, pub, "in:inbox is:unread", "soulman/seen", time.Second)

	w.poll(context.Background())

	if len(pub.published) != 1 {
		t.Fatalf("published = %d messages, want 1 (label failure shouldn't erase an already-successful publish)", len(pub.published))
	}
}

func TestPoll_ListError_SkipsCycleWithoutPanicking(t *testing.T) {
	client := &fakeClient{listErr: errors.New("list failed"), ensureLabelID: "Label_1"}
	pub := &fakePublisher{}
	w := newWatcher(client, pub, "in:inbox is:unread", "soulman/seen", time.Second)

	w.poll(context.Background())

	if len(pub.published) != 0 {
		t.Errorf("published = %d messages, want 0", len(pub.published))
	}
}

func TestPoll_GetMessageError_SkipsThatMessageOnly(t *testing.T) {
	client := &fakeClient{
		listIDs:       []string{"m1", "m2"},
		messages:      map[string]*gmail.Message{"m2": validMessage("m2")},
		ensureLabelID: "Label_1",
	}
	pub := &fakePublisher{}
	w := newWatcher(client, pub, "in:inbox is:unread", "soulman/seen", time.Second)

	w.poll(context.Background())

	if len(pub.published) != 1 {
		t.Fatalf("published = %d messages, want 1 (only m2 should succeed)", len(pub.published))
	}
	if pub.published[0].ChannelMeta.MessageID != "m2" {
		t.Errorf("published message = %s, want m2", pub.published[0].ChannelMeta.MessageID)
	}
}

func TestPoll_SeenLabelResolutionFails_SkipsPollEntirely(t *testing.T) {
	client := &fakeClient{ensureErr: errors.New("labels.list failed")}
	pub := &fakePublisher{}
	w := newWatcher(client, pub, "in:inbox is:unread", "soulman/seen", time.Second)

	w.poll(context.Background())

	if w.seenLabelID != "" {
		t.Errorf("seenLabelID = %q, want empty (resolution failed)", w.seenLabelID)
	}
	if len(pub.published) != 0 {
		t.Errorf("published = %d messages, want 0", len(pub.published))
	}
}

// TestStart_ResolvesSeenLabelAndRunsImmediatePoll verifies the immediate
// poll still happens right away on Start — but since Start no longer
// blocks the caller (the poll now runs entirely inside the background
// goroutine, so a slow/large first poll can never delay perception-svc's
// own startup), this test polls with a bounded retry rather than asserting
// synchronously the instant Start() returns.
func TestStart_ResolvesSeenLabelAndRunsImmediatePoll(t *testing.T) {
	client := &fakeClient{
		listIDs:       []string{"m1"},
		messages:      map[string]*gmail.Message{"m1": validMessage("m1")},
		ensureLabelID: "Label_1",
	}
	pub := &fakePublisher{}
	w := newWatcher(client, pub, "in:inbox is:unread", "soulman/seen", time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for pub.publishedCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	if got := pub.publishedCount(); got != 1 {
		t.Fatalf("published = %d messages after Start, want 1 from the immediate poll", got)
	}
	if w.seenLabelID != "Label_1" {
		t.Errorf("seenLabelID = %q, want Label_1", w.seenLabelID)
	}
}

// TestClose_StopsPollLoop verifies Close() stops further polling. Since
// Start no longer blocks until the immediate poll completes (the
// async-startup fix), the very first poll can legitimately still be
// in-flight or freshly dispatched at the moment Close() is called — the
// guarantee this test checks is that no *further* polls happen afterward,
// not that zero polls ever happen.
func TestClose_StopsPollLoop(t *testing.T) {
	client := &fakeClient{
		listIDs:       []string{},
		ensureLabelID: "Label_1",
	}
	pub := &fakePublisher{}
	w := newWatcher(client, pub, "in:inbox is:unread", "soulman/seen", 5*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	// Wait for at least the immediate poll to have run before measuring
	// the "stopped" guarantee against a stable baseline.
	deadline := time.Now().Add(2 * time.Second)
	for client.getListCalls() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	callsAfterStart := client.getListCalls()
	if callsAfterStart == 0 {
		t.Fatal("expected at least one poll to have run before Close")
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	time.Sleep(100 * time.Millisecond) // several tick intervals, if the loop weren't stopped

	if got := client.getListCalls(); got != callsAfterStart {
		t.Errorf("listCalls after Close = %d, want unchanged from %d (poll loop should have stopped)", got, callsAfterStart)
	}
}
