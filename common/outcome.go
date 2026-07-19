package common

import "time"

// OutcomeRecord is the payload published to soulman.memory.write — the
// record of an action-svc dispatch outcome that memory-svc's episodic
// memory consumer turns into an episodes row. This is the single canonical
// wire format for Action -> Memory, mirroring how ActionRequest is the
// canonical wire format for Thinking -> Action.
//
// Type is a forward-compat discriminator; today it's always "action_log"
// (set internally by natsclient.Publisher.PublishOutcome, not by callers).
// TaskID may be empty (e.g. the daily-report cron has no per-message
// correlation ID) — memory-svc dedups on the JetStream message's stream
// sequence number instead, not TaskID.
type OutcomeRecord struct {
	Type       string    `json:"type"`
	ActionType string    `json:"action_type"`
	Status     string    `json:"status"`
	TaskID     string    `json:"task_id"`
	OccurredAt time.Time `json:"occurred_at"`
	Summary    string    `json:"summary"`
	Decision   string    `json:"decision"`
	Tags       []string  `json:"tags"`
}
