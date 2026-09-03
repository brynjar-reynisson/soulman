package rules

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"soulman/common"
	"soulman/thinking-svc/llm"
)

// schoolEventParam mirrors action-svc/dispatch's SchoolEventParam — kept as
// a separate type (not shared across modules) per this repo's "small
// independent duplication over cross-module imports" precedent.
type schoolEventParam struct {
	Date        string `json:"date"`
	HasTime     bool   `json:"has_time"`
	Time        string `json:"time"`
	Description string `json:"description"`
}

type schoolEmailParams struct {
	Sender      string             `json:"sender"`
	Subject     string             `json:"subject"`
	BodyExcerpt string             `json:"body_excerpt"`
	Note        string             `json:"note"`
	ThreadID    string             `json:"thread_id"`
	OccurredAt  *time.Time         `json:"occurred_at"`
	Events      []schoolEventParam `json:"events"`
}

// schoolExtractTruncateLen mirrors gmail_triage.go's classifyBodyTruncateLen.
const schoolExtractTruncateLen = 4000

// NewSchoolEmailRule builds a Rule matching gmail stimuli whose sender
// address ends in one of senderDomains (case-insensitive). Constructed
// (not a package var like GmailTriageRule) because the domain allowlist is
// config-driven — see
// docs/superpowers/specs/2026-09-03-school-email-events-design.md.
func NewSchoolEmailRule(senderDomains []string) Rule {
	domains := make([]string, len(senderDomains))
	for i, d := range senderDomains {
		domains[i] = strings.ToLower(d)
	}
	return Rule{
		Name: "school-email",
		Match: func(s *common.Stimulus) bool {
			if s.Channel != "gmail" {
				return false
			}
			sender := strings.ToLower(s.Source.Identity)
			for _, d := range domains {
				if d != "" && strings.HasSuffix(sender, d) {
					return true
				}
			}
			return false
		},
		Handle: handleSchoolEmail,
	}
}

func handleSchoolEmail(ctx context.Context, s *common.Stimulus, client llm.Client) (*common.ActionRequest, error) {
	sender := s.Source.Identity
	subject := gmailSubject(s)
	body := s.Content.RawText
	threadID := s.ChannelMeta.ThreadID

	referenceDate := time.Now()
	if s.OccurredAt != nil {
		referenceDate = *s.OccurredAt
	}

	events, note, err := client.ExtractSchoolEvents(ctx, sender, subject, truncate(body, schoolExtractTruncateLen), referenceDate)
	if err != nil {
		events = nil
		note = fmt.Sprintf("extraction unavailable: %v", err)
	}

	eventParams := make([]schoolEventParam, len(events))
	for i, e := range events {
		eventParams[i] = schoolEventParam{Date: e.Date, HasTime: e.HasTime, Time: e.Time, Description: e.Description}
	}

	params, err := json.Marshal(schoolEmailParams{
		Sender:      sender,
		Subject:     subject,
		BodyExcerpt: truncate(body, excerptLen),
		Note:        note,
		ThreadID:    threadID,
		OccurredAt:  s.OccurredAt,
		Events:      eventParams,
	})
	if err != nil {
		return nil, fmt.Errorf("rules: marshal school email parameters: %w", err)
	}

	intent := "Log this school email to today's daily report"
	urgency := "normal"
	if len(events) > 0 {
		intent = "Log this school email and schedule a parent reminder for its date(s)"
		urgency = "high"
	}

	return &common.ActionRequest{
		CorrelationID:   uuid.NewString(),
		Intent:          intent,
		ActionHint:      "process_school_event",
		Parameters:      params,
		RiskLevel:       "low",
		Urgency:         urgency,
		ExpectedOutcome: "one report entry appended; any future-dated event is queued for a reminder (Discord to the owner, calendar invite to the other parent) at the configured notify time the day before",
		Fallback:        "if report append fails, retry once, then give up silently (same as gmail triage). If an event fails to queue, it is dropped for this run.",
	}, nil
}
