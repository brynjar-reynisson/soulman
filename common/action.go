package common

import "encoding/json"

// ActionRequest is the payload published to soulman.thinking.request — the
// INVOKE_ACTION handoff shape from Thinking module.md's DECIDE step. This is
// the single canonical wire format for Thinking -> Action; see
// docs/superpowers/specs/2026-07-17-error-report-action-design.md's Handoff
// section for the correction history (an earlier version of that spec had
// two incompatible shapes for this message, which caused action-svc to
// silently drop every request until this type was unified here).
//
// Parameters is raw JSON rather than a typed map so it can be passed through
// unmodified from the publisher (thinking-svc, which marshals per-rule
// parameters into it) to the consumer (action-svc, which unmarshals it into
// its own per-action-type params struct) without an intermediate
// map[string]any round trip.
type ActionRequest struct {
	CorrelationID   string          `json:"correlation_id"`
	Intent          string          `json:"intent"`
	ActionHint      string          `json:"action_hint"`
	Parameters      json.RawMessage `json:"parameters"`
	RiskLevel       string          `json:"risk_level"`
	Urgency         string          `json:"urgency"`
	ExpectedOutcome string          `json:"expected_outcome"`
	Fallback        string          `json:"fallback"`
}
