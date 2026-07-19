package rules

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"soulman/common"
	"soulman/thinking-svc/llm"
)

// gmailTriageParams mirrors gmail-triage-action-design.md's Thinking Rule
// parameters shape. Marshaled into common.ActionRequest.Parameters as raw
// JSON so action-svc can unmarshal it directly into its own params struct.
type gmailTriageParams struct {
	Sender      string     `json:"sender"`
	Subject     string     `json:"subject"`
	BodyExcerpt string     `json:"body_excerpt"`
	Reason      string     `json:"reason"`
	Important   bool       `json:"important"`
	ThreadID    string     `json:"thread_id"`
	OccurredAt  *time.Time `json:"occurred_at"`
}

// classifyBodyTruncateLen bounds cost/latency on the classification call,
// mirroring error_report's precedent of truncating summarizer input; the
// full body is never sent to action-svc anyway; only the shorter excerpt
// below travels through.
const classifyBodyTruncateLen = 4000

// excerptLen is the length of the body excerpt carried in the Action
// Request for both the report entry and the eventual Discord message.
const excerptLen = 200

// GmailTriageRule implements
// docs/superpowers/specs/2026-07-18-gmail-triage-action-design.md: every
// gmail-channel stimulus becomes a triage_gmail_email Action Request. The
// report-entry half always happens in action-svc's dispatch handler; the
// Discord-notify half is conditional on the "important" verdict decided
// here.
var GmailTriageRule = Rule{
	Name: "gmail-triage",
	Match: func(s *common.Stimulus) bool {
		return s.Channel == "gmail"
	},
	Handle: handleGmailTriage,
}

func handleGmailTriage(ctx context.Context, s *common.Stimulus, client llm.Client) (*common.ActionRequest, error) {
	sender := s.Source.Identity
	subject := gmailSubject(s)
	body := s.Content.RawText
	threadID := s.ChannelMeta.ThreadID

	important, reason, err := client.ClassifyImportance(ctx, sender, subject, truncate(body, classifyBodyTruncateLen))
	if err != nil {
		// Belt-and-suspenders: production *DeepSeekClient never returns a
		// non-nil error (it fails closed internally — see deepseek.go), but
		// a future or fake Classifier implementation might; treat that the
		// same fail-closed way.
		important = false
		reason = fmt.Sprintf("classification unavailable: %v", err)
	}

	params, err := json.Marshal(gmailTriageParams{
		Sender:      sender,
		Subject:     subject,
		BodyExcerpt: truncate(body, excerptLen),
		Reason:      reason,
		Important:   important,
		ThreadID:    threadID,
		OccurredAt:  s.OccurredAt,
	})
	if err != nil {
		return nil, fmt.Errorf("rules: marshal gmail triage parameters: %w", err)
	}

	intent := "Log this email to today's daily report"
	urgency := "normal"
	if important {
		intent = "Notify me about this important email"
		urgency = "high"
	}

	req := &common.ActionRequest{
		CorrelationID:   uuid.NewString(),
		Intent:          intent,
		ActionHint:      "triage_gmail_email",
		Parameters:      params,
		RiskLevel:       "low",
		Urgency:         urgency,
		ExpectedOutcome: "one report entry appended, plus an immediate (debounced) Discord notification if judged important",
		Fallback:        "if report append fails, retry once; if it fails again, log to episodic memory with error:execution tag and give up silently. If the Discord notification fails, no retry is attempted — a missed immediate ping is not worth blocking on since the report entry is the permanent record.",
	}
	return req, nil
}

// gmailSubject extracts channel_metadata.channel_specific.subject, the
// field gmail-channel-design.md guarantees for gmail stimuli.
func gmailSubject(s *common.Stimulus) string {
	var meta struct {
		Subject string `json:"subject"`
	}
	if len(s.ChannelMeta.ChannelSpecific) > 0 {
		_ = json.Unmarshal(s.ChannelMeta.ChannelSpecific, &meta)
	}
	return meta.Subject
}

// truncate returns s cut to at most n runes, appending "…" when truncation
// actually occurred. Operates on runes (not bytes) so multi-byte UTF-8
// characters in a sender name or body are never split mid-character.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
