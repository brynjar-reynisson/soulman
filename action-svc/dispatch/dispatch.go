package dispatch

import (
	"encoding/json"
	"log/slog"
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
		slog.Error("dispatch: unparseable request, dropping", "error", err)
		return
	}

	switch req.ActionHint {
	case "append_daily_report_entry":
		d.dispatchAppendDailyReportEntry(req)
	case "triage_gmail_email":
		d.dispatchGmailTriage(req)
	default:
		slog.Error("dispatch: unknown action_hint, dropping", "action_hint", req.ActionHint, "correlation_id", req.CorrelationID)
	}
}

func (d *Dispatcher) dispatchAppendDailyReportEntry(req common.ActionRequest) {
	var p ReportEntryParams
	// Best-effort: if this fails, AppendReportEntry's own unmarshal below
	// fails identically and status becomes "failed"; p stays zero-value, so
	// no batcher.Add fires for an unparseable request.
	_ = json.Unmarshal(req.Parameters, &p)

	_, err := AppendReportEntry(d.root, req.Parameters)
	if err != nil {
		slog.Warn("dispatch: append_daily_report_entry failed, retrying once", "correlation_id", req.CorrelationID, "error", err)
		_, err = AppendReportEntry(d.root, req.Parameters)
	}

	status := "success"
	if err != nil {
		status = "failed"
		slog.Error("dispatch: append_daily_report_entry failed after retry, giving up", "correlation_id", req.CorrelationID, "error", err)
	}

	if p.Important && d.batcher != nil {
		d.batcher.Add(notifybatch.Item{
			Kind:        "report",
			Summary:     p.Summary,
			SourcePath:  p.SourcePath,
			BodyExcerpt: p.RawContent,
		})
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
		slog.Error("dispatch: outcome publish failed", "correlation_id", req.CorrelationID, "error", pubErr)
	}
}
