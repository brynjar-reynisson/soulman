package dispatch

import (
	"encoding/json"
	"log"
	"time"

	"soulman/action-svc/feign"
	"soulman/action-svc/notifybatch"
	"soulman/common"
)

// Publisher is satisfied by *natsclient.Publisher. Defined here (not in
// natsclient) so this package doesn't need to import natsclient.
type Publisher interface {
	PublishOutcome(rec common.OutcomeRecord) error
}

// Batcher is satisfied by *notifybatch.Batcher. Defined here (not in
// notifybatch) so tests can inject a fake that records Add calls without
// waiting on real timers — flush timing itself is already covered by
// notifybatch's own tests.
type Batcher interface {
	Add(item notifybatch.Item)
}

type Dispatcher struct {
	root      string
	publisher Publisher
	batcher   Batcher
	gate      *feign.Gate
}

func New(root string, publisher Publisher, batcher Batcher, gate *feign.Gate) *Dispatcher {
	return &Dispatcher{root: root, publisher: publisher, batcher: batcher, gate: gate}
}

// Handle is the NATS message handler for soulman.thinking.request. It never
// returns an error — all failures are logged and/or published as outcome
// records, per the "a missed report entry isn't worth interrupting the
// human" decision in the error-report-action spec.
func (d *Dispatcher) Handle(msg []byte) {
	var req common.ActionRequest
	if err := json.Unmarshal(msg, &req); err != nil {
		log.Printf("dispatch: unparseable request, dropping: %v", err)
		return
	}

	switch req.ActionHint {
	case "append_daily_report_entry":
		d.dispatchAppendDailyReportEntry(req)
	case "triage_gmail_email":
		d.dispatchGmailTriage(req)
	default:
		log.Printf("dispatch: unknown action_hint %q, dropping (correlation_id=%s)", req.ActionHint, req.CorrelationID)
	}
}

func (d *Dispatcher) dispatchAppendDailyReportEntry(req common.ActionRequest) {
	_, err := AppendReportEntry(d.root, req.Parameters)
	if err != nil {
		log.Printf("dispatch: append_daily_report_entry failed for task %s, retrying once: %v", req.CorrelationID, err)
		_, err = AppendReportEntry(d.root, req.Parameters)
	}

	status := "success"
	if err != nil {
		status = "failed"
		log.Printf("dispatch: append_daily_report_entry failed for task %s after retry, giving up: %v", req.CorrelationID, err)
	}

	if d.publisher == nil {
		return
	}

	rec := common.OutcomeRecord{
		ActionType: req.ActionHint,
		Status:     status,
		TaskID:     req.CorrelationID,
		OccurredAt: time.Now(),
		Summary:    "Daily report entry appended",
		Decision:   "append_daily_report_entry",
		Tags:       []string{"report"},
	}
	if pubErr := d.publisher.PublishOutcome(rec); pubErr != nil {
		log.Printf("dispatch: outcome publish failed for task %s: %v", req.CorrelationID, pubErr)
	}
}
