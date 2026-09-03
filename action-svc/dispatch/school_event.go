package dispatch

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"soulman/action-svc/report"
	"soulman/action-svc/schoolevents"
	"soulman/common"
)

// SchoolEventParam mirrors thinking-svc's schoolEventParam — the shape one
// extracted event takes inside process_school_event's Parameters.
type SchoolEventParam struct {
	Date         string `json:"date"`
	HasTime      bool   `json:"has_time"`
	Time         string `json:"time"`
	Description  string `json:"description"`
	ContactEmail string `json:"contact_email"`
}

// SchoolEmailParams mirrors thinking-svc's schoolEmailParams — the
// Parameters shape process_school_event Action Requests carry.
type SchoolEmailParams struct {
	Sender      string             `json:"sender"`
	Subject     string             `json:"subject"`
	BodyExcerpt string             `json:"body_excerpt"`
	Note        string             `json:"note"`
	ThreadID    string             `json:"thread_id"`
	OccurredAt  string             `json:"occurred_at"`
	Events      []SchoolEventParam `json:"events"`
}

func (d *Dispatcher) dispatchSchoolEvent(req common.ActionRequest) {
	var p SchoolEmailParams
	if err := json.Unmarshal(req.Parameters, &p); err != nil {
		slog.Error("dispatch: process_school_event unparseable params, dropping", "correlation_id", req.CorrelationID, "error", err)
		return
	}

	occurredAt, parseErr := time.Parse(time.RFC3339, p.OccurredAt)
	if parseErr != nil {
		occurredAt = time.Now()
	}

	important := len(p.Events) > 0
	entry := report.Entry{
		Summary:    fmt.Sprintf("%s — %d school event(s) found", p.Subject, len(p.Events)),
		RawContent: fmt.Sprintf("Note: %s\n\n%s", p.Note, p.BodyExcerpt),
		SourcePath: p.Sender + "/" + p.ThreadID,
		OccurredAt: occurredAt.Local(),
		Important:  important,
	}
	_, err := report.Append(d.root, entry)
	if err != nil {
		slog.Warn("dispatch: process_school_event report append failed, retrying once", "correlation_id", req.CorrelationID, "error", err)
		_, err = report.Append(d.root, entry)
	}
	status := "success"
	if err != nil {
		status = "failed"
		slog.Error("dispatch: process_school_event report append failed after retry, giving up", "correlation_id", req.CorrelationID, "error", err)
	}

	now := time.Now()
	queued := 0
	for i, ev := range p.Events {
		date, parseErr := time.ParseInLocation("2006-01-02", ev.Date, now.Location())
		if parseErr != nil {
			slog.Warn("dispatch: process_school_event unparseable event date, dropping", "date", ev.Date, "correlation_id", req.CorrelationID)
			continue
		}
		if date.Before(startOfDay(now)) {
			continue // already happened — silently dropped, no retry
		}

		id := schoolevents.ID(p.ThreadID, i, ev.Date)
		saveErr := schoolevents.Save(d.root, schoolevents.Event{
			ID: id, Date: ev.Date, HasTime: ev.HasTime, Time: ev.Time, Description: ev.Description,
			Sender: p.Sender, Subject: p.Subject, ContactEmail: ev.ContactEmail,
			DiscordStatus: "pending", CalendarStatus: "pending", CreatedAt: now,
		})
		if saveErr != nil {
			slog.Error("dispatch: process_school_event failed to queue event", "id", id, "error", saveErr)
			continue
		}
		queued++
	}

	if d.publisher == nil {
		return
	}

	decision := "logged only"
	if queued > 0 {
		decision = fmt.Sprintf("%d event(s) queued", queued)
	}

	rec := common.OutcomeRecord{
		ActionType: req.ActionHint,
		Status:     status,
		TaskID:     req.CorrelationID,
		OccurredAt: occurredAt,
		Summary:    entry.Summary,
		Decision:   decision,
		Tags:       []string{"gmail", "school"},
	}
	if pubErr := d.publisher.PublishOutcome(rec); pubErr != nil {
		slog.Error("dispatch: outcome publish failed", "correlation_id", req.CorrelationID, "error", pubErr)
	}
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}
