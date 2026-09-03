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

// reportExcerptLen mirrors thinking-svc/rules/gmail_triage.go's excerptLen —
// the same 200-character excerpt convention already used for Gmail's
// BodyExcerpt. A report entry's RawContent is not bounded the way Gmail's
// body is (folder-watcher's ErrorReportRule, which always sets
// Important: true, can carry an entire multi-KB error file), so it must be
// truncated before it reaches notifybatch.Item — otherwise notify/discord.go's
// splitMessage keeps an over-limit paragraph whole rather than truncating it,
// DiscordNotifier.Send aborts on the first failing chunk (dropping the rest
// of the flush too), and the error is swallowed silently by
// notifybatch.Batcher.Flush.
const reportExcerptLen = 200

// truncateExcerpt returns s cut to at most n runes, appending "…" when
// truncation actually occurred. Operates on runes (not bytes) so multi-byte
// UTF-8 characters are never split mid-character. Duplicated from (rather
// than importing) thinking-svc/rules/gmail_triage.go's truncate: thinking-svc
// and action-svc are separate Go modules.
func truncateExcerpt(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
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
	case "process_school_event":
		d.dispatchSchoolEvent(req)
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
			BodyExcerpt: truncateExcerpt(p.RawContent, reportExcerptLen),
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
