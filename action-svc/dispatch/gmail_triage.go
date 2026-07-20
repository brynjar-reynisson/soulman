package dispatch

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"soulman/action-svc/notifybatch"
	"soulman/action-svc/report"
	"soulman/common"
)

// GmailTriageParams mirrors thinking-svc's gmailTriageParams — the
// Parameters shape triage_gmail_email Action Requests carry.
type GmailTriageParams struct {
	Sender      string `json:"sender"`
	Subject     string `json:"subject"`
	BodyExcerpt string `json:"body_excerpt"`
	Reason      string `json:"reason"`
	Important   bool   `json:"important"`
	ThreadID    string `json:"thread_id"`
	OccurredAt  string `json:"occurred_at"`
}

// AppendGmailReportEntry implements the always-log half of
// triage_gmail_email. A package-level var (mirroring AppendReportEntry) so
// tests can inject a failing stand-in to deterministically exercise
// Dispatcher's retry-then-give-up behaviour without needing to force a
// real filesystem failure.
var AppendGmailReportEntry = func(root string, params json.RawMessage) (string, error) {
	var p GmailTriageParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("dispatch: unmarshal gmail triage params: %w", err)
	}
	occurredAt, err := time.Parse(time.RFC3339, p.OccurredAt)
	if err != nil {
		return "", fmt.Errorf("dispatch: parse occurred_at %q: %w", p.OccurredAt, err)
	}

	verdict := "not important"
	if p.Important {
		verdict = "important"
	}

	entry := report.Entry{
		Summary:    fmt.Sprintf("%s — deemed %s", p.Subject, verdict),
		RawContent: fmt.Sprintf("Reason: %s\n\n%s", p.Reason, p.BodyExcerpt),
		// report.Append's formatEntry derives the bracketed report context
		// via filepath.Dir(SourcePath) — there's no real file path for an
		// email, so the sender is synthesized as a fake "directory" by
		// appending the thread ID as a placeholder "filename" segment
		// (never itself displayed). This is the same "/" trick
		// error_report.go already relies on for folder-watcher's
		// watched_path + filename.
		SourcePath: p.Sender + "/" + p.ThreadID,
		OccurredAt: occurredAt.Local(),
		Important:  p.Important,
	}
	path, err := report.Append(root, entry)
	if err != nil {
		return "", fmt.Errorf("dispatch: append gmail report entry: %w", err)
	}
	return path, nil
}

func (d *Dispatcher) dispatchGmailTriage(req common.ActionRequest) {
	var p GmailTriageParams
	if err := json.Unmarshal(req.Parameters, &p); err != nil {
		log.Printf("dispatch: triage_gmail_email unparseable params, dropping (correlation_id=%s): %v", req.CorrelationID, err)
		return
	}

	_, err := AppendGmailReportEntry(d.root, req.Parameters)
	if err != nil {
		log.Printf("dispatch: triage_gmail_email report append failed for task %s, retrying once: %v", req.CorrelationID, err)
		_, err = AppendGmailReportEntry(d.root, req.Parameters)
	}

	status := "success"
	if err != nil {
		status = "failed"
		log.Printf("dispatch: triage_gmail_email report append failed for task %s after retry, giving up: %v", req.CorrelationID, err)
	}

	if p.Important && d.batcher != nil {
		d.batcher.Add(notifybatch.Item{
			Sender:      p.Sender,
			Subject:     p.Subject,
			Reason:      p.Reason,
			BodyExcerpt: p.BodyExcerpt,
			ThreadID:    p.ThreadID,
		})
	}

	if d.publisher == nil {
		return
	}

	verdict := "not important"
	decision := "logged only"
	if p.Important {
		verdict = "important"
		decision = "notified via Discord"
		if d.gate.Enabled() {
			decision = "feigned notify via Discord"
		}
	}
	occurredAt, parseErr := time.Parse(time.RFC3339, p.OccurredAt)
	if parseErr != nil {
		occurredAt = time.Now()
	}

	rec := common.OutcomeRecord{
		ActionType: req.ActionHint,
		Status:     status,
		TaskID:     req.CorrelationID,
		OccurredAt: occurredAt,
		Summary:    fmt.Sprintf("%s — %s", p.Subject, verdict),
		Decision:   decision,
		Tags:       []string{"gmail", "triage"},
	}
	if pubErr := d.publisher.PublishOutcome(rec); pubErr != nil {
		log.Printf("dispatch: outcome publish failed for task %s: %v", req.CorrelationID, pubErr)
	}
}
